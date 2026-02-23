package vectorindex

import (
	"container/heap"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"sort"
	"sync"
)

// HNSWConfig holds configuration for the HNSW index.
type HNSWConfig struct {
	M              int // Max connections per layer (default: 16)
	EfConstruction int // Size of dynamic candidate list during construction (default: 200)
	EfSearch       int // Size of dynamic candidate list during search (default: 50)
	Dimensions     int // Vector dimensionality
}

// DefaultHNSWConfig returns sensible defaults for embedding search.
func DefaultHNSWConfig(dimensions int) HNSWConfig {
	return HNSWConfig{
		M:              16,
		EfConstruction: 200,
		EfSearch:       50,
		Dimensions:     dimensions,
	}
}

type hnswNode struct {
	id     string
	vector []float32
	layers [][]int // connections per layer, indexed by neighbor's internal ID
}

// HNSW implements the Hierarchical Navigable Small World graph for
// approximate nearest neighbor search.
type HNSW struct {
	mu     sync.RWMutex
	cfg    HNSWConfig
	nodes  []*hnswNode
	idToIx map[string]int // external ID → internal index
	maxLvl int
	entryIx int // internal index of entry point
	rng    *rand.Rand
}

// NewHNSW creates a new HNSW index.
func NewHNSW(cfg HNSWConfig) *HNSW {
	return &HNSW{
		cfg:    cfg,
		nodes:  make([]*hnswNode, 0),
		idToIx: make(map[string]int),
		entryIx: -1,
		rng:    rand.New(rand.NewSource(42)),
	}
}

func (h *HNSW) randomLevel() int {
	ml := 1.0 / math.Log(float64(h.cfg.M))
	lvl := int(-math.Log(h.rng.Float64()) * ml)
	if lvl < 0 {
		lvl = 0
	}
	return lvl
}

// Add inserts a vector with the given ID.
func (h *HNSW) Add(id string, vector []float32) error {
	if len(vector) != h.cfg.Dimensions {
		return fmt.Errorf("expected %d dimensions, got %d", h.cfg.Dimensions, len(vector))
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.idToIx[id]; exists {
		// Update in-place
		ix := h.idToIx[id]
		h.nodes[ix].vector = vector
		return nil
	}

	lvl := h.randomLevel()
	ix := len(h.nodes)
	node := &hnswNode{
		id:     id,
		vector: vector,
		layers: make([][]int, lvl+1),
	}
	for i := range node.layers {
		node.layers[i] = make([]int, 0)
	}
	h.nodes = append(h.nodes, node)
	h.idToIx[id] = ix

	if h.entryIx == -1 {
		h.entryIx = ix
		h.maxLvl = lvl
		return nil
	}

	ep := h.entryIx

	// Traverse from top layer down to lvl+1
	for l := h.maxLvl; l > lvl; l-- {
		ep = h.searchLayer(vector, ep, 1, l)[0].ix
	}

	// Insert at each layer from lvl down to 0
	maxConn := h.cfg.M
	for l := min(lvl, h.maxLvl); l >= 0; l-- {
		candidates := h.searchLayer(vector, ep, h.cfg.EfConstruction, l)
		neighbors := h.selectNeighbors(candidates, maxConn)

		node.layers[l] = make([]int, len(neighbors))
		for i, n := range neighbors {
			node.layers[l][i] = n.ix
		}

		// Add bidirectional connections
		for _, n := range neighbors {
			neighbor := h.nodes[n.ix]
			if l < len(neighbor.layers) {
				neighbor.layers[l] = append(neighbor.layers[l], ix)
				if len(neighbor.layers[l]) > maxConn*2 {
					neighbor.layers[l] = h.pruneConnections(neighbor.vector, neighbor.layers[l], maxConn, l)
				}
			}
		}

		if len(candidates) > 0 {
			ep = candidates[0].ix
		}
	}

	if lvl > h.maxLvl {
		h.maxLvl = lvl
		h.entryIx = ix
	}

	return nil
}

// Remove deletes a vector by ID.
func (h *HNSW) Remove(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	ix, exists := h.idToIx[id]
	if !exists {
		return nil // Already gone
	}

	// Remove all connections to this node
	node := h.nodes[ix]
	for l := range node.layers {
		for _, neighborIx := range node.layers[l] {
			if neighborIx < len(h.nodes) {
				neighbor := h.nodes[neighborIx]
				if l < len(neighbor.layers) {
					filtered := make([]int, 0, len(neighbor.layers[l]))
					for _, nix := range neighbor.layers[l] {
						if nix != ix {
							filtered = append(filtered, nix)
						}
					}
					neighbor.layers[l] = filtered
				}
			}
		}
	}

	// Mark as deleted (nil vector)
	h.nodes[ix].vector = nil
	h.nodes[ix].layers = nil
	delete(h.idToIx, id)

	// Update entry point if needed
	if ix == h.entryIx {
		h.entryIx = -1
		for i, n := range h.nodes {
			if n.vector != nil {
				h.entryIx = i
				break
			}
		}
	}

	return nil
}

// Search finds the k nearest neighbors to the query vector.
func (h *HNSW) Search(query []float32, k int) ([]SearchResult, error) {
	if len(query) != h.cfg.Dimensions {
		return nil, fmt.Errorf("expected %d dimensions, got %d", h.cfg.Dimensions, len(query))
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.entryIx == -1 || len(h.nodes) == 0 {
		return nil, nil
	}

	ep := h.entryIx

	// Traverse from top layer down to layer 1
	for l := h.maxLvl; l > 0; l-- {
		results := h.searchLayer(query, ep, 1, l)
		if len(results) > 0 {
			ep = results[0].ix
		}
	}

	// Search layer 0 with ef = max(efSearch, k)
	ef := h.cfg.EfSearch
	if k > ef {
		ef = k
	}
	candidates := h.searchLayer(query, ep, ef, 0)

	if len(candidates) > k {
		candidates = candidates[:k]
	}

	results := make([]SearchResult, 0, len(candidates))
	for _, c := range candidates {
		if h.nodes[c.ix].vector != nil {
			results = append(results, SearchResult{
				ID:       h.nodes[c.ix].id,
				Distance: c.dist,
			})
		}
	}
	return results, nil
}

// Len returns the number of active vectors in the index.
func (h *HNSW) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.idToIx)
}

type candidate struct {
	ix   int
	dist float32
}

func (h *HNSW) searchLayer(query []float32, entryIx int, ef int, layer int) []candidate {
	if entryIx < 0 || entryIx >= len(h.nodes) || h.nodes[entryIx].vector == nil {
		return nil
	}

	visited := make(map[int]bool)
	visited[entryIx] = true

	dist := CosineDistance(query, h.nodes[entryIx].vector)
	candidates := &candidateHeap{{ix: entryIx, dist: dist}}
	heap.Init(candidates)

	results := &candidateMaxHeap{{ix: entryIx, dist: dist}}
	heap.Init(results)

	for candidates.Len() > 0 {
		c := heap.Pop(candidates).(candidate)

		// Get worst result so far
		worst := (*results)[0]
		if c.dist > worst.dist && results.Len() >= ef {
			break
		}

		if layer >= len(h.nodes[c.ix].layers) {
			continue
		}

		for _, neighborIx := range h.nodes[c.ix].layers[layer] {
			if visited[neighborIx] {
				continue
			}
			visited[neighborIx] = true

			if neighborIx >= len(h.nodes) || h.nodes[neighborIx].vector == nil {
				continue
			}

			d := CosineDistance(query, h.nodes[neighborIx].vector)
			if results.Len() < ef || d < (*results)[0].dist {
				heap.Push(candidates, candidate{ix: neighborIx, dist: d})
				heap.Push(results, candidate{ix: neighborIx, dist: d})
				if results.Len() > ef {
					heap.Pop(results)
				}
			}
		}
	}

	// Extract results sorted by distance (closest first)
	sorted := make([]candidate, results.Len())
	for i := results.Len() - 1; i >= 0; i-- {
		sorted[i] = heap.Pop(results).(candidate)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].dist < sorted[j].dist
	})
	return sorted
}

func (h *HNSW) selectNeighbors(candidates []candidate, maxConn int) []candidate {
	if len(candidates) <= maxConn {
		return candidates
	}
	return candidates[:maxConn]
}

func (h *HNSW) pruneConnections(nodeVec []float32, connections []int, maxConn int, layer int) []int {
	if len(connections) <= maxConn {
		return connections
	}
	type connDist struct {
		ix   int
		dist float32
	}
	dists := make([]connDist, 0, len(connections))
	for _, cix := range connections {
		if cix < len(h.nodes) && h.nodes[cix].vector != nil {
			dists = append(dists, connDist{ix: cix, dist: CosineDistance(nodeVec, h.nodes[cix].vector)})
		}
	}
	sort.Slice(dists, func(i, j int) bool {
		return dists[i].dist < dists[j].dist
	})
	if len(dists) > maxConn {
		dists = dists[:maxConn]
	}
	result := make([]int, len(dists))
	for i, d := range dists {
		result[i] = d.ix
	}
	return result
}

// Save persists the index to a binary file.
func (h *HNSW) Save(path string) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create index file: %w", err)
	}
	defer f.Close()

	// Header: magic + config
	if err := binary.Write(f, binary.LittleEndian, uint32(0x484E5357)); err != nil { // "HNSW"
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, int32(h.cfg.Dimensions)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, int32(h.cfg.M)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, int32(h.maxLvl)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, int32(h.entryIx)); err != nil {
		return err
	}

	// Number of active nodes
	activeNodes := make([]*hnswNode, 0, len(h.idToIx))
	for _, n := range h.nodes {
		if n.vector != nil {
			activeNodes = append(activeNodes, n)
		}
	}
	if err := binary.Write(f, binary.LittleEndian, int32(len(activeNodes))); err != nil {
		return err
	}

	for _, n := range activeNodes {
		// ID length + ID
		idBytes := []byte(n.id)
		if err := binary.Write(f, binary.LittleEndian, int32(len(idBytes))); err != nil {
			return err
		}
		if _, err := f.Write(idBytes); err != nil {
			return err
		}
		// Vector
		if err := binary.Write(f, binary.LittleEndian, n.vector); err != nil {
			return err
		}
		// Layers
		if err := binary.Write(f, binary.LittleEndian, int32(len(n.layers))); err != nil {
			return err
		}
		for _, layer := range n.layers {
			if err := binary.Write(f, binary.LittleEndian, int32(len(layer))); err != nil {
				return err
			}
			for _, conn := range layer {
				if err := binary.Write(f, binary.LittleEndian, int32(conn)); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// Load restores the index from a binary file.
func (h *HNSW) Load(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open index file: %w", err)
	}
	defer f.Close()

	// Header
	var magic uint32
	if err := binary.Read(f, binary.LittleEndian, &magic); err != nil {
		return err
	}
	if magic != 0x484E5357 {
		return fmt.Errorf("invalid HNSW file magic: %x", magic)
	}

	var dims, m, maxLvl, entryIx int32
	if err := binary.Read(f, binary.LittleEndian, &dims); err != nil {
		return err
	}
	if err := binary.Read(f, binary.LittleEndian, &m); err != nil {
		return err
	}
	if err := binary.Read(f, binary.LittleEndian, &maxLvl); err != nil {
		return err
	}
	if err := binary.Read(f, binary.LittleEndian, &entryIx); err != nil {
		return err
	}

	h.cfg.Dimensions = int(dims)
	h.cfg.M = int(m)
	h.maxLvl = int(maxLvl)
	h.entryIx = int(entryIx)

	var nodeCount int32
	if err := binary.Read(f, binary.LittleEndian, &nodeCount); err != nil {
		return err
	}

	h.nodes = make([]*hnswNode, 0, nodeCount)
	h.idToIx = make(map[string]int, nodeCount)

	for i := int32(0); i < nodeCount; i++ {
		var idLen int32
		if err := binary.Read(f, binary.LittleEndian, &idLen); err != nil {
			return err
		}
		idBytes := make([]byte, idLen)
		if _, err := io.ReadFull(f, idBytes); err != nil {
			return err
		}
		id := string(idBytes)

		vector := make([]float32, dims)
		if err := binary.Read(f, binary.LittleEndian, &vector); err != nil {
			return err
		}

		var layerCount int32
		if err := binary.Read(f, binary.LittleEndian, &layerCount); err != nil {
			return err
		}

		layers := make([][]int, layerCount)
		for l := int32(0); l < layerCount; l++ {
			var connCount int32
			if err := binary.Read(f, binary.LittleEndian, &connCount); err != nil {
				return err
			}
			conns := make([]int, connCount)
			for c := int32(0); c < connCount; c++ {
				var conn int32
				if err := binary.Read(f, binary.LittleEndian, &conn); err != nil {
					return err
				}
				conns[c] = int(conn)
			}
			layers[l] = conns
		}

		ix := len(h.nodes)
		h.nodes = append(h.nodes, &hnswNode{
			id:     id,
			vector: vector,
			layers: layers,
		})
		h.idToIx[id] = ix
	}

	return nil
}

// Min-heap for candidates (closest first)
type candidateHeap []candidate

func (h candidateHeap) Len() int            { return len(h) }
func (h candidateHeap) Less(i, j int) bool   { return h[i].dist < h[j].dist }
func (h candidateHeap) Swap(i, j int)        { h[i], h[j] = h[j], h[i] }
func (h *candidateHeap) Push(x any)          { *h = append(*h, x.(candidate)) }
func (h *candidateHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// Max-heap for results (worst first, so we can evict)
type candidateMaxHeap []candidate

func (h candidateMaxHeap) Len() int            { return len(h) }
func (h candidateMaxHeap) Less(i, j int) bool   { return h[i].dist > h[j].dist }
func (h candidateMaxHeap) Swap(i, j int)        { h[i], h[j] = h[j], h[i] }
func (h *candidateMaxHeap) Push(x any)          { *h = append(*h, x.(candidate)) }
func (h *candidateMaxHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

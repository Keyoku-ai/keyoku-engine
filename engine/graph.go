package engine

import (
	"context"
	"fmt"

	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// GraphEngine provides graph traversal and query capabilities.
type GraphEngine struct {
	store  storage.Store
	config GraphConfig
}

// GraphConfig holds configuration for the graph engine.
type GraphConfig struct {
	MaxTraversalDepth       int
	MaxResults              int
	MinRelationshipStrength float64
}

// DefaultGraphConfig returns default graph configuration.
func DefaultGraphConfig() GraphConfig {
	return GraphConfig{
		MaxTraversalDepth:       5,
		MaxResults:              100,
		MinRelationshipStrength: 0.3,
	}
}

// NewGraphEngine creates a new graph engine.
func NewGraphEngine(store storage.Store, config GraphConfig) *GraphEngine {
	if config.MaxTraversalDepth <= 0 {
		config.MaxTraversalDepth = 5
	}
	if config.MaxResults <= 0 {
		config.MaxResults = 100
	}
	if config.MinRelationshipStrength <= 0 {
		config.MinRelationshipStrength = 0.3
	}
	return &GraphEngine{store: store, config: config}
}

// GraphNode represents a node in the traversal result.
type GraphNode struct {
	Entity        *storage.Entity
	Depth         int
	PathFromRoot  []string
	Relationships []*GraphEdge
}

// GraphEdge represents an edge (relationship) in the graph.
type GraphEdge struct {
	Relationship *storage.Relationship
	TargetEntity *storage.Entity
	Direction    string // "outgoing" or "incoming"
}

// TraversalResult contains the result of a graph traversal.
type TraversalResult struct {
	RootEntity *storage.Entity
	Nodes      map[string]*GraphNode
	Edges      []*GraphEdge
	TotalNodes int
	TotalEdges int
}

// GraphQuery represents a query for the knowledge graph.
type GraphQuery struct {
	StartEntityID     string
	StartEntityName   string
	RelationshipTypes []string
	EntityTypes       []storage.EntityType
	MaxDepth          int
	Direction         string // "outgoing", "incoming", or "both"
	IncludeMemories   bool
	TeamID            string // When set, include team-scoped relationships in traversal
}

// TraverseFrom performs a breadth-first traversal from an entity.
func (g *GraphEngine) TraverseFrom(ctx context.Context, ownerEntityID string, query GraphQuery) (*TraversalResult, error) {
	maxDepth := query.MaxDepth
	if maxDepth <= 0 {
		maxDepth = g.config.MaxTraversalDepth
	}

	// Find start entity
	var startEntity *storage.Entity
	var err error

	if query.StartEntityID != "" {
		startEntity, err = g.store.GetEntity(ctx, query.StartEntityID)
	} else if query.StartEntityName != "" {
		startEntity, err = g.store.FindEntityByAlias(ctx, ownerEntityID, query.StartEntityName)
	} else {
		return nil, fmt.Errorf("must specify StartEntityID or StartEntityName")
	}

	if err != nil {
		return nil, fmt.Errorf("failed to find start entity: %w", err)
	}
	if startEntity == nil {
		return nil, fmt.Errorf("start entity not found")
	}

	result := &TraversalResult{
		RootEntity: startEntity,
		Nodes:      make(map[string]*GraphNode),
		Edges:      make([]*GraphEdge, 0),
	}

	rootNode := &GraphNode{
		Entity:        startEntity,
		Depth:         0,
		PathFromRoot:  []string{startEntity.ID},
		Relationships: make([]*GraphEdge, 0),
	}
	result.Nodes[startEntity.ID] = rootNode

	// BFS traversal
	queue := []*GraphNode{rootNode}
	visited := map[string]bool{startEntity.ID: true}

	for len(queue) > 0 && len(result.Nodes) < g.config.MaxResults {
		current := queue[0]
		queue = queue[1:]

		if current.Depth >= maxDepth {
			continue
		}

		edges, err := g.getEdges(ctx, ownerEntityID, current.Entity.ID, query)
		if err != nil {
			continue
		}

		for _, edge := range edges {
			result.Edges = append(result.Edges, edge)
			current.Relationships = append(current.Relationships, edge)
			result.TotalEdges++

			if !visited[edge.TargetEntity.ID] {
				visited[edge.TargetEntity.ID] = true

				newPath := make([]string, len(current.PathFromRoot)+1)
				copy(newPath, current.PathFromRoot)
				newPath[len(current.PathFromRoot)] = edge.TargetEntity.ID

				newNode := &GraphNode{
					Entity:        edge.TargetEntity,
					Depth:         current.Depth + 1,
					PathFromRoot:  newPath,
					Relationships: make([]*GraphEdge, 0),
				}

				result.Nodes[edge.TargetEntity.ID] = newNode
				result.TotalNodes++

				if newNode.Depth < maxDepth {
					queue = append(queue, newNode)
				}
			}
		}
	}

	result.TotalNodes = len(result.Nodes)
	return result, nil
}

// FindPath finds the shortest path between two entities.
func (g *GraphEngine) FindPath(ctx context.Context, ownerEntityID, fromEntityID, toEntityID string) ([]string, error) {
	if fromEntityID == toEntityID {
		return []string{fromEntityID}, nil
	}

	visited := map[string]bool{fromEntityID: true}
	parent := map[string]string{}
	queue := []string{fromEntityID}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		edges, err := g.getEdges(ctx, ownerEntityID, current, GraphQuery{Direction: "both"})
		if err != nil {
			continue
		}

		for _, edge := range edges {
			targetID := edge.TargetEntity.ID
			if targetID == toEntityID {
				path := []string{toEntityID}
				curr := current
				for curr != fromEntityID {
					path = append([]string{curr}, path...)
					curr = parent[curr]
				}
				path = append([]string{fromEntityID}, path...)
				return path, nil
			}

			if !visited[targetID] {
				visited[targetID] = true
				parent[targetID] = current
				queue = append(queue, targetID)
			}
		}
	}

	return nil, fmt.Errorf("no path found between entities")
}

// GetEntityNeighbors returns all entities directly connected to an entity.
func (g *GraphEngine) GetEntityNeighbors(ctx context.Context, ownerEntityID, entityID string) ([]*GraphEdge, error) {
	return g.getEdges(ctx, ownerEntityID, entityID, GraphQuery{Direction: "both"})
}

// EntityContext contains all context for an entity.
type EntityContext struct {
	Entity        *storage.Entity
	Relationships []*GraphEdge
	Memories      []*storage.Memory
}

// GetEntityContext returns all relationships for an entity.
func (g *GraphEngine) GetEntityContext(ctx context.Context, ownerEntityID, entityID string) (*EntityContext, error) {
	entity, err := g.store.GetEntity(ctx, entityID)
	if err != nil {
		return nil, err
	}
	if entity == nil {
		return nil, fmt.Errorf("entity not found")
	}

	edges, err := g.getEdges(ctx, ownerEntityID, entityID, GraphQuery{Direction: "both"})
	if err != nil {
		return nil, err
	}

	return &EntityContext{
		Entity:        entity,
		Relationships: edges,
	}, nil
}

// FindRelatedEntities finds entities related to a given entity by relationship type.
func (g *GraphEngine) FindRelatedEntities(ctx context.Context, ownerEntityID, entityID string, relTypes []string) ([]*storage.Entity, error) {
	edges, err := g.getEdges(ctx, ownerEntityID, entityID, GraphQuery{
		RelationshipTypes: relTypes,
		Direction:         "both",
	})
	if err != nil {
		return nil, err
	}

	entities := make([]*storage.Entity, 0, len(edges))
	seen := make(map[string]bool)
	for _, edge := range edges {
		if !seen[edge.TargetEntity.ID] {
			seen[edge.TargetEntity.ID] = true
			entities = append(entities, edge.TargetEntity)
		}
	}
	return entities, nil
}

// getEdges retrieves relationships for an entity from storage and converts to edges.
func (g *GraphEngine) getEdges(ctx context.Context, ownerEntityID, entityID string, query GraphQuery) ([]*GraphEdge, error) {
	direction := query.Direction
	if direction == "" {
		direction = "both"
	}

	rels, err := g.store.GetEntityRelationships(ctx, ownerEntityID, entityID, direction)
	if err != nil {
		return nil, err
	}

	var edges []*GraphEdge
	for _, rel := range rels {
		// Filter by team scope: when TeamID is set, include relationships
		// that belong to the team or have no team (backward compat)
		if query.TeamID != "" && rel.TeamID != "" && rel.TeamID != query.TeamID {
			continue
		}

		// Filter by relationship type if specified
		if len(query.RelationshipTypes) > 0 {
			matched := false
			for _, t := range query.RelationshipTypes {
				if rel.RelationshipType == t {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		// Filter by minimum strength
		if rel.Strength < g.config.MinRelationshipStrength {
			continue
		}

		// Determine the target entity (the other side)
		var targetID string
		var dir string
		if rel.SourceEntityID == entityID {
			targetID = rel.TargetEntityID
			dir = "outgoing"
		} else {
			targetID = rel.SourceEntityID
			dir = "incoming"
		}

		targetEntity, err := g.store.GetEntity(ctx, targetID)
		if err != nil || targetEntity == nil {
			continue
		}

		// Filter by entity type if specified
		if len(query.EntityTypes) > 0 {
			matched := false
			for _, t := range query.EntityTypes {
				if targetEntity.Type == t {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		edges = append(edges, &GraphEdge{
			Relationship: rel,
			TargetEntity: targetEntity,
			Direction:    dir,
		})
	}

	return edges, nil
}

// ExplainConnection uses LLM to explain how two entities are connected.
// Finds the shortest path, collects entity names and relationships, then asks LLM to summarize.
func (g *GraphEngine) ExplainConnection(ctx context.Context, ownerEntityID, fromEntityID, toEntityID string, provider llm.Provider) (string, error) {
	path, err := g.FindPath(ctx, ownerEntityID, fromEntityID, toEntityID)
	if err != nil {
		return "", fmt.Errorf("no connection found: %w", err)
	}

	var entities []string
	var relationships []string

	for i, entityID := range path {
		entity, err := g.store.GetEntity(ctx, entityID)
		if err != nil || entity == nil {
			entities = append(entities, entityID)
			continue
		}
		entities = append(entities, fmt.Sprintf("%s (%s)", entity.CanonicalName, entity.Type))

		if i < len(path)-1 {
			nextID := path[i+1]
			rels, _ := g.store.GetEntityRelationships(ctx, ownerEntityID, entityID, "both")
			for _, rel := range rels {
				if (rel.SourceEntityID == entityID && rel.TargetEntityID == nextID) ||
					(rel.TargetEntityID == entityID && rel.SourceEntityID == nextID) {
					nextEntity, _ := g.store.GetEntity(ctx, nextID)
					nextName := nextID
					if nextEntity != nil {
						nextName = nextEntity.CanonicalName
					}
					relationships = append(relationships, fmt.Sprintf("%s -[%s]-> %s", entity.CanonicalName, rel.RelationshipType, nextName))
					break
				}
			}
		}
	}

	resp, err := provider.SummarizeGraph(ctx, llm.GraphSummaryRequest{
		Entities:      entities,
		Relationships: relationships,
		Question:      fmt.Sprintf("Explain how %s and %s are connected.", entities[0], entities[len(entities)-1]),
	})
	if err != nil {
		return "", fmt.Errorf("LLM graph summary failed: %w", err)
	}

	return resp.Summary, nil
}

// SummarizeEntityContext uses LLM to summarize all relationships for an entity.
func (g *GraphEngine) SummarizeEntityContext(ctx context.Context, ownerEntityID, entityID string, provider llm.Provider) (string, error) {
	entityCtx, err := g.GetEntityContext(ctx, ownerEntityID, entityID)
	if err != nil {
		return "", err
	}

	var entities []string
	var relationships []string

	entities = append(entities, fmt.Sprintf("%s (%s)", entityCtx.Entity.CanonicalName, entityCtx.Entity.Type))

	for _, edge := range entityCtx.Relationships {
		entities = append(entities, fmt.Sprintf("%s (%s)", edge.TargetEntity.CanonicalName, edge.TargetEntity.Type))
		if edge.Direction == "outgoing" {
			relationships = append(relationships, fmt.Sprintf("%s -[%s]-> %s",
				entityCtx.Entity.CanonicalName, edge.Relationship.RelationshipType, edge.TargetEntity.CanonicalName))
		} else {
			relationships = append(relationships, fmt.Sprintf("%s -[%s]-> %s",
				edge.TargetEntity.CanonicalName, edge.Relationship.RelationshipType, entityCtx.Entity.CanonicalName))
		}
	}

	resp, err := provider.SummarizeGraph(ctx, llm.GraphSummaryRequest{
		Entities:      entities,
		Relationships: relationships,
		Question:      fmt.Sprintf("Summarize everything we know about %s based on their relationships.", entityCtx.Entity.CanonicalName),
	})
	if err != nil {
		return "", fmt.Errorf("LLM graph summary failed: %w", err)
	}

	return resp.Summary, nil
}

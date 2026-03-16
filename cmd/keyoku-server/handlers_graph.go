// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/keyoku-ai/keyoku-engine/engine"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// --- JSON response types ---

type entityJSON struct {
	ID              string            `json:"id"`
	OwnerEntityID   string            `json:"owner_entity_id"`
	AgentID         string            `json:"agent_id,omitempty"`
	TeamID          string            `json:"team_id,omitempty"`
	CanonicalName   string            `json:"canonical_name"`
	Type            string            `json:"type"`
	Description     string            `json:"description"`
	Aliases         []string          `json:"aliases,omitempty"`
	Attributes      map[string]any    `json:"attributes,omitempty"`
	MentionCount    int               `json:"mention_count"`
	LastMentionedAt *time.Time        `json:"last_mentioned_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type relationshipJSON struct {
	ID               string         `json:"id"`
	OwnerEntityID    string         `json:"owner_entity_id"`
	AgentID          string         `json:"agent_id,omitempty"`
	TeamID           string         `json:"team_id,omitempty"`
	SourceEntityID   string         `json:"source_entity_id"`
	TargetEntityID   string         `json:"target_entity_id"`
	RelationshipType string         `json:"relationship_type"`
	Description      string         `json:"description"`
	Strength         float64        `json:"strength"`
	Confidence       float64        `json:"confidence"`
	IsBidirectional  bool           `json:"is_bidirectional"`
	EvidenceCount    int            `json:"evidence_count"`
	Attributes       map[string]any `json:"attributes,omitempty"`
	FirstSeenAt      time.Time      `json:"first_seen_at"`
	LastSeenAt       time.Time      `json:"last_seen_at"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type graphEdgeJSON struct {
	Relationship relationshipJSON `json:"relationship"`
	TargetEntity entityJSON       `json:"target_entity"`
	Direction    string           `json:"direction"`
}

type graphNodeJSON struct {
	Entity        entityJSON    `json:"entity"`
	Depth         int           `json:"depth"`
	PathFromRoot  []string      `json:"path_from_root"`
	Relationships []graphEdgeJSON `json:"relationships"`
}

type traversalResponse struct {
	RootEntity entityJSON               `json:"root_entity"`
	Nodes      map[string]graphNodeJSON `json:"nodes"`
	Edges      []graphEdgeJSON          `json:"edges"`
	TotalNodes int                      `json:"total_nodes"`
	TotalEdges int                      `json:"total_edges"`
}

type pathResponse struct {
	Path []string `json:"path"`
}

// --- Conversion helpers ---

func toEntityJSON(e *storage.Entity) entityJSON {
	return entityJSON{
		ID:              e.ID,
		OwnerEntityID:   e.OwnerEntityID,
		AgentID:         e.AgentID,
		TeamID:          e.TeamID,
		CanonicalName:   e.CanonicalName,
		Type:            string(e.Type),
		Description:     e.Description,
		Aliases:         e.Aliases,
		Attributes:      e.Attributes,
		MentionCount:    e.MentionCount,
		LastMentionedAt: e.LastMentionedAt,
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
	}
}

func toRelationshipJSON(r *storage.Relationship) relationshipJSON {
	return relationshipJSON{
		ID:               r.ID,
		OwnerEntityID:    r.OwnerEntityID,
		AgentID:          r.AgentID,
		TeamID:           r.TeamID,
		SourceEntityID:   r.SourceEntityID,
		TargetEntityID:   r.TargetEntityID,
		RelationshipType: r.RelationshipType,
		Description:      r.Description,
		Strength:         r.Strength,
		Confidence:       r.Confidence,
		IsBidirectional:  r.IsBidirectional,
		EvidenceCount:    r.EvidenceCount,
		Attributes:       r.Attributes,
		FirstSeenAt:      r.FirstSeenAt,
		LastSeenAt:       r.LastSeenAt,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}

func toGraphEdgeJSON(edge *engine.GraphEdge) graphEdgeJSON {
	return graphEdgeJSON{
		Relationship: toRelationshipJSON(edge.Relationship),
		TargetEntity: toEntityJSON(edge.TargetEntity),
		Direction:    edge.Direction,
	}
}

// --- Handlers ---

// HandleListGraphEntities lists entities in the knowledge graph for a given owner.
// GET /api/v1/graph/entities?owner_entity_id=X&limit=100
func (h *Handlers) HandleListGraphEntities(w http.ResponseWriter, r *http.Request) {
	ownerEntityID := r.URL.Query().Get("owner_entity_id")
	if ownerEntityID == "" {
		writeError(w, http.StatusBadRequest, "owner_entity_id is required")
		return
	}

	limit := clampLimit(r.URL.Query().Get("limit"), 100)

	entities, err := h.k.Store().QueryEntities(r.Context(), storage.EntityQuery{
		OwnerEntityID: ownerEntityID,
		Limit:         limit,
	})
	if err != nil {
		writeInternalError(w, err)
		return
	}

	result := make([]entityJSON, 0, len(entities))
	for _, e := range entities {
		result = append(result, toEntityJSON(e))
	}

	writeJSON(w, http.StatusOK, result)
}

// HandleListRelationships lists relationships in the knowledge graph with optional filters.
// GET /api/v1/graph/relationships?owner_entity_id=X&entity_id=Y&relationship_type=Z&min_strength=0.5&limit=100
func (h *Handlers) HandleListRelationships(w http.ResponseWriter, r *http.Request) {
	ownerEntityID := r.URL.Query().Get("owner_entity_id")
	if ownerEntityID == "" {
		writeError(w, http.StatusBadRequest, "owner_entity_id is required")
		return
	}

	query := storage.RelationshipQuery{
		OwnerEntityID: ownerEntityID,
		EntityID:      r.URL.Query().Get("entity_id"),
		Limit:         clampLimit(r.URL.Query().Get("limit"), 100),
	}

	if relType := r.URL.Query().Get("relationship_type"); relType != "" {
		query.RelationshipTypes = []string{relType}
	}

	if minStr := r.URL.Query().Get("min_strength"); minStr != "" {
		if v, err := strconv.ParseFloat(minStr, 64); err == nil {
			query.MinStrength = v
		}
	}

	relationships, err := h.k.Store().QueryRelationships(r.Context(), query)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	result := make([]relationshipJSON, 0, len(relationships))
	for _, rel := range relationships {
		result = append(result, toRelationshipJSON(rel))
	}

	writeJSON(w, http.StatusOK, result)
}

// HandleGraphTraverse performs a breadth-first graph traversal from a start entity.
// POST /api/v1/graph/traverse
func (h *Handlers) HandleGraphTraverse(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OwnerEntityID string `json:"owner_entity_id"`
		StartEntityID string `json:"start_entity_id"`
		MaxDepth      int    `json:"max_depth"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.OwnerEntityID == "" {
		writeError(w, http.StatusBadRequest, "owner_entity_id is required")
		return
	}
	if req.StartEntityID == "" {
		writeError(w, http.StatusBadRequest, "start_entity_id is required")
		return
	}

	result, err := h.k.Graph().TraverseFrom(r.Context(), req.OwnerEntityID, engine.GraphQuery{
		StartEntityID: req.StartEntityID,
		MaxDepth:      req.MaxDepth,
		Direction:     "both",
	})
	if err != nil {
		writeInternalError(w, err)
		return
	}

	// Convert to JSON response
	nodes := make(map[string]graphNodeJSON, len(result.Nodes))
	for id, node := range result.Nodes {
		rels := make([]graphEdgeJSON, 0, len(node.Relationships))
		for _, edge := range node.Relationships {
			rels = append(rels, toGraphEdgeJSON(edge))
		}
		nodes[id] = graphNodeJSON{
			Entity:        toEntityJSON(node.Entity),
			Depth:         node.Depth,
			PathFromRoot:  node.PathFromRoot,
			Relationships: rels,
		}
	}

	edges := make([]graphEdgeJSON, 0, len(result.Edges))
	for _, edge := range result.Edges {
		edges = append(edges, toGraphEdgeJSON(edge))
	}

	writeJSON(w, http.StatusOK, traversalResponse{
		RootEntity: toEntityJSON(result.RootEntity),
		Nodes:      nodes,
		Edges:      edges,
		TotalNodes: result.TotalNodes,
		TotalEdges: result.TotalEdges,
	})
}

// HandleGraphPath finds the shortest path between two entities.
// POST /api/v1/graph/path
func (h *Handlers) HandleGraphPath(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OwnerEntityID string `json:"owner_entity_id"`
		FromEntityID  string `json:"from_entity_id"`
		ToEntityID    string `json:"to_entity_id"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.OwnerEntityID == "" {
		writeError(w, http.StatusBadRequest, "owner_entity_id is required")
		return
	}
	if req.FromEntityID == "" {
		writeError(w, http.StatusBadRequest, "from_entity_id is required")
		return
	}
	if req.ToEntityID == "" {
		writeError(w, http.StatusBadRequest, "to_entity_id is required")
		return
	}

	path, err := h.k.Graph().FindPath(r.Context(), req.OwnerEntityID, req.FromEntityID, req.ToEntityID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, pathResponse{
		Path: path,
	})
}

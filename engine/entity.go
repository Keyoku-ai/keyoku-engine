// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package engine

import (
	"context"
	"regexp"
	"strings"

	"github.com/keyoku-ai/keyoku-engine/embedder"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// EntityResolver handles entity extraction and resolution.
type EntityResolver struct {
	store    storage.Store
	embedder embedder.Embedder
	config   EntityConfig
}

// EntityConfig holds configuration for entity resolution.
type EntityConfig struct {
	SemanticMatchThreshold float64
	EnableFuzzyMatching    bool
	MaxAliases             int
}

// DefaultEntityConfig returns default entity configuration.
func DefaultEntityConfig() EntityConfig {
	return EntityConfig{
		SemanticMatchThreshold: 0.85,
		EnableFuzzyMatching:    true,
		MaxAliases:             10,
	}
}

// NewEntityResolver creates a new entity resolver.
func NewEntityResolver(store storage.Store, emb embedder.Embedder, config EntityConfig) *EntityResolver {
	if config.SemanticMatchThreshold <= 0 {
		config.SemanticMatchThreshold = 0.85
	}
	if config.MaxAliases <= 0 {
		config.MaxAliases = 10
	}
	return &EntityResolver{
		store:    store,
		embedder: emb,
		config:   config,
	}
}

// ExtractedEntity represents an entity extracted from text.
type ExtractedEntity struct {
	Name       string
	Type       storage.EntityType
	Context    string
	Confidence float64
}

// EntityResolution represents the result of resolving an entity.
type EntityResolution struct {
	ExtractedName  string
	ResolvedEntity *storage.Entity
	IsNew          bool
	Confidence     float64
	MatchType      string // "exact", "alias", "semantic", "new"
}

// ExtractEntities extracts entities from memory content using pattern matching.
func (r *EntityResolver) ExtractEntities(ctx context.Context, content string) ([]ExtractedEntity, error) {
	var entities []ExtractedEntity

	// 1. Extract proper nouns (capitalized words not at sentence start)
	properNouns := extractProperNouns(content)
	for _, noun := range properNouns {
		entities = append(entities, ExtractedEntity{
			Name:       noun,
			Type:       inferEntityType(noun, content),
			Context:    getEntityContext(content, noun),
			Confidence: 0.7,
		})
	}

	// 2. Extract quoted names
	quotedNames := extractQuotedStrings(content)
	for _, name := range quotedNames {
		entities = append(entities, ExtractedEntity{
			Name:       name,
			Type:       inferEntityType(name, content),
			Context:    getEntityContext(content, name),
			Confidence: 0.8,
		})
	}

	// 3. Extract relationship-indicated entities
	relEntities := extractRelationshipEntities(content)
	entities = append(entities, relEntities...)

	return deduplicateEntities(entities), nil
}

// ResolveEntity finds an existing entity or determines it's new.
func (r *EntityResolver) ResolveEntity(ctx context.Context, ownerEntityID string, extracted ExtractedEntity) (*EntityResolution, error) {
	// 1. Try exact name match
	existing, err := r.store.GetEntityByName(ctx, ownerEntityID, extracted.Name, extracted.Type)
	if err == nil && existing != nil {
		return &EntityResolution{
			ExtractedName:  extracted.Name,
			ResolvedEntity: existing,
			IsNew:          false,
			Confidence:     1.0,
			MatchType:      "exact",
		}, nil
	}

	// 2. Try alias match
	existing, err = r.store.FindEntityByAlias(ctx, ownerEntityID, extracted.Name)
	if err == nil && existing != nil {
		return &EntityResolution{
			ExtractedName:  extracted.Name,
			ResolvedEntity: existing,
			IsNew:          false,
			Confidence:     0.95,
			MatchType:      "alias",
		}, nil
	}

	// 3. Try semantic/fuzzy matching via embedding similarity
	if r.config.EnableFuzzyMatching && r.embedder != nil {
		emb, err := r.embedder.Embed(ctx, extracted.Name)
		if err == nil && len(emb) > 0 {
			similar, err := r.store.FindSimilarEntities(ctx, emb, ownerEntityID, 1, r.config.SemanticMatchThreshold)
			if err == nil && len(similar) > 0 {
				return &EntityResolution{
					ExtractedName:  extracted.Name,
					ResolvedEntity: similar[0],
					IsNew:          false,
					Confidence:     r.config.SemanticMatchThreshold,
					MatchType:      "semantic",
				}, nil
			}
		}
	}

	// 4. No match — it's a new entity
	return &EntityResolution{
		ExtractedName: extracted.Name,
		IsNew:         true,
		Confidence:    extracted.Confidence,
		MatchType:     "new",
	}, nil
}

// MergeEntities merges a duplicate entity into a primary entity.
func (r *EntityResolver) MergeEntities(ctx context.Context, primary, duplicate *storage.Entity) error {
	// Add duplicate's canonical name as alias
	if !contains(primary.Aliases, duplicate.CanonicalName) {
		r.store.AddEntityAlias(ctx, primary.ID, duplicate.CanonicalName)
	}

	// Add duplicate's aliases
	for _, alias := range duplicate.Aliases {
		if !contains(primary.Aliases, alias) && len(primary.Aliases) < r.config.MaxAliases {
			r.store.AddEntityAlias(ctx, primary.ID, alias)
		}
	}

	// Transfer mentions
	mentions, err := r.store.GetEntityMentions(ctx, duplicate.ID, 1000)
	if err == nil {
		for _, mention := range mentions {
			r.store.CreateEntityMention(ctx, &storage.EntityMention{
				EntityID:       primary.ID,
				MemoryID:       mention.MemoryID,
				MentionText:    mention.MentionText,
				Confidence:     mention.Confidence,
				ContextSnippet: mention.ContextSnippet,
			})
		}
	}

	// Update mention count
	for i := 0; i < duplicate.MentionCount; i++ {
		r.store.UpdateEntityMentionCount(ctx, primary.ID)
	}

	// Delete the duplicate
	return r.store.DeleteEntity(ctx, duplicate.ID)
}

// --- helper functions ---

func extractProperNouns(content string) []string {
	var nouns []string
	words := strings.Fields(content)

	for i, word := range words {
		if i > 0 && !strings.HasSuffix(words[i-1], ".") {
			if len(word) > 1 && word[0] >= 'A' && word[0] <= 'Z' {
				clean := strings.Trim(word, ".,!?;:'\"")
				if len(clean) > 1 {
					nouns = append(nouns, clean)
				}
			}
		}
	}

	// Also look for consecutive capitalized words (full names)
	re := regexp.MustCompile(`[A-Z][a-z]+(?:\s+[A-Z][a-z]+)+`)
	matches := re.FindAllString(content, -1)
	nouns = append(nouns, matches...)

	return nouns
}

func extractQuotedStrings(content string) []string {
	var quoted []string
	re := regexp.MustCompile(`"([^"]+)"`)
	matches := re.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		if len(match) > 1 {
			quoted = append(quoted, match[1])
		}
	}
	return quoted
}

func extractRelationshipEntities(content string) []ExtractedEntity {
	var entities []ExtractedEntity

	patterns := []struct {
		pattern    string
		entityType storage.EntityType
	}{
		{`(?:my|the)\s+(?:friend|colleague|coworker|boss|manager)\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)*)`, storage.EntityTypePerson},
		{`(?:works?\s+(?:at|for))\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)*)`, storage.EntityTypeOrganization},
		{`(?:lives?\s+in|from)\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)*)`, storage.EntityTypeLocation},
		{`(?:married\s+to|dating)\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)*)`, storage.EntityTypePerson},
	}

	for _, p := range patterns {
		re := regexp.MustCompile(p.pattern)
		matches := re.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) > 1 {
				entities = append(entities, ExtractedEntity{
					Name:       match[1],
					Type:       p.entityType,
					Context:    getEntityContext(content, match[1]),
					Confidence: 0.85,
				})
			}
		}
	}

	return entities
}

func inferEntityType(name string, context string) storage.EntityType {
	contextLower := strings.ToLower(context)

	personIndicators := []string{
		"friend", "colleague", "coworker", "boss", "manager", "wife", "husband",
		"brother", "sister", "mother", "father", "son", "daughter", "partner",
		"he ", "she ", "his ", "her ", "they ",
	}
	for _, ind := range personIndicators {
		if strings.Contains(contextLower, ind) {
			return storage.EntityTypePerson
		}
	}

	orgIndicators := []string{
		"company", "corporation", "inc", "llc", "ltd", "works at", "works for",
		"employed by", "organization", "team", "department",
	}
	for _, ind := range orgIndicators {
		if strings.Contains(contextLower, ind) {
			return storage.EntityTypeOrganization
		}
	}

	locIndicators := []string{
		"city", "country", "state", "lives in", "from", "located", "moved to",
		"visiting", "trip to", "travel to",
	}
	for _, ind := range locIndicators {
		if strings.Contains(contextLower, ind) {
			return storage.EntityTypeLocation
		}
	}

	productIndicators := []string{
		"bought", "purchased", "using", "uses", "product", "app", "software",
		"device", "phone", "computer", "car",
	}
	for _, ind := range productIndicators {
		if strings.Contains(contextLower, ind) {
			return storage.EntityTypeProduct
		}
	}

	return storage.EntityTypeOther
}

func getEntityContext(content string, entity string) string {
	idx := strings.Index(strings.ToLower(content), strings.ToLower(entity))
	if idx == -1 {
		return ""
	}
	start := idx - 50
	if start < 0 {
		start = 0
	}
	end := idx + len(entity) + 50
	if end > len(content) {
		end = len(content)
	}
	return content[start:end]
}

func deduplicateEntities(entities []ExtractedEntity) []ExtractedEntity {
	seen := make(map[string]bool)
	var result []ExtractedEntity
	for _, e := range entities {
		key := strings.ToLower(e.Name) + ":" + string(e.Type)
		if !seen[key] {
			seen[key] = true
			result = append(result, e)
		}
	}
	return result
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

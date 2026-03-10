package engine

import (
	"context"
	"regexp"
	"strings"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// RelationshipDetector detects and extracts relationships between entities.
type RelationshipDetector struct {
	store  storage.Store
	config RelationshipConfig
}

// RelationshipConfig holds configuration for relationship detection.
type RelationshipConfig struct {
	MinConfidence               float64
	EnableBidirectionalInference bool
}

// DefaultRelationshipConfig returns default relationship configuration.
func DefaultRelationshipConfig() RelationshipConfig {
	return RelationshipConfig{
		MinConfidence:               0.6,
		EnableBidirectionalInference: true,
	}
}

// NewRelationshipDetector creates a new relationship detector.
func NewRelationshipDetector(store storage.Store, config RelationshipConfig) *RelationshipDetector {
	if config.MinConfidence <= 0 {
		config.MinConfidence = 0.6
	}
	return &RelationshipDetector{store: store, config: config}
}

// DetectedRelationship represents a relationship found in text.
type DetectedRelationship struct {
	SourceEntity     string
	TargetEntity     string
	RelationshipType string
	Description      string
	IsBidirectional  bool
	Confidence       float64
	Evidence         string
}

// RelationshipPattern defines a pattern for detecting relationships.
type RelationshipPattern struct {
	Pattern         *regexp.Regexp
	Type            string
	IsBidirectional bool
	SourceGroup     int
	TargetGroup     int
	Confidence      float64
}

// Common relationship patterns.
var relationshipPatterns = []RelationshipPattern{
	// Family
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+is\s+(?:my|the)\s+(wife|husband|spouse)`), "married_to", true, 1, 0, 0.9},
	{regexp.MustCompile(`(?:my|the)\s+(wife|husband|spouse)\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)`), "married_to", true, 0, 2, 0.9},
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+is\s+(?:my|the)\s+(mother|father|parent)`), "parent_of", false, 1, 0, 0.9},
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+is\s+(?:my|the)\s+(son|daughter|child)`), "child_of", false, 1, 0, 0.9},
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+is\s+(?:my|the)\s+(brother|sister|sibling)`), "sibling_of", true, 1, 0, 0.9},

	// Work
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+works?\s+(?:at|for)\s+([A-Z][a-zA-Z]+(?:\s+[A-Z][a-zA-Z]+)*)`), "works_at", false, 1, 2, 0.85},
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+is\s+(?:my|the|a)\s+(boss|manager|supervisor)`), "manages", false, 1, 0, 0.8},
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+is\s+(?:my|a)\s+(colleague|coworker|teammate)`), "colleague_of", true, 1, 0, 0.8},

	// Social
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+is\s+(?:my|a)\s+(friend|best friend)`), "friend_of", true, 1, 0, 0.85},
	{regexp.MustCompile(`(?:my|a)\s+friend\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)`), "friend_of", true, 0, 1, 0.85},
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+(?:and I|and me)\s+(?:are|were)\s+friends`), "friend_of", true, 1, 0, 0.85},

	// Location
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+lives?\s+in\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)`), "lives_in", false, 1, 2, 0.8},
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+is\s+from\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)`), "from", false, 1, 2, 0.8},
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+(?:is located|is based)\s+in\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)`), "located_in", false, 1, 2, 0.85},

	// Ownership/usage
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+(?:uses|owns|has)\s+(?:a|an|the)\s+([A-Z][a-zA-Z]+(?:\s+[A-Z][a-zA-Z]+)?)`), "uses", false, 1, 2, 0.7},

	// Dating/romantic
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+is\s+dating\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)`), "dating", true, 1, 2, 0.85},
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+(?:and)\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+are\s+(?:dating|together|a couple)`), "dating", true, 1, 2, 0.85},

	// Membership
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+is\s+(?:a member of|part of)\s+([A-Z][a-zA-Z]+(?:\s+[A-Z][a-zA-Z]+)?)`), "member_of", false, 1, 2, 0.8},
	{regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+(?:joined|belongs to)\s+([A-Z][a-zA-Z]+(?:\s+[A-Z][a-zA-Z]+)?)`), "member_of", false, 1, 2, 0.8},
}

// DetectRelationships extracts relationships from memory content.
func (d *RelationshipDetector) DetectRelationships(ctx context.Context, content string, entities []ExtractedEntity) ([]DetectedRelationship, error) {
	var relationships []DetectedRelationship

	// Pattern-based detection
	for _, pattern := range relationshipPatterns {
		matches := pattern.Pattern.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) <= pattern.SourceGroup || len(match) <= pattern.TargetGroup {
				continue
			}

			var source, target string
			if pattern.SourceGroup == 0 {
				source = "user"
			} else {
				source = match[pattern.SourceGroup]
			}
			if pattern.TargetGroup == 0 {
				target = "user"
			} else {
				target = match[pattern.TargetGroup]
			}

			if source != "" && target != "" && source != target {
				relationships = append(relationships, DetectedRelationship{
					SourceEntity:     source,
					TargetEntity:     target,
					RelationshipType: pattern.Type,
					IsBidirectional:  pattern.IsBidirectional,
					Confidence:       pattern.Confidence,
					Evidence:         match[0],
				})
			}
		}
	}

	// Co-occurrence based detection
	if len(entities) >= 2 {
		cooccurrences := detectCooccurrences(content, entities)
		relationships = append(relationships, cooccurrences...)
	}

	// Verb-based detection
	verbRels := detectVerbRelationships(content)
	relationships = append(relationships, verbRels...)

	return deduplicateRelationships(relationships), nil
}

// GetRelationshipStrength calculates the strength of a relationship.
func (d *RelationshipDetector) GetRelationshipStrength(evidenceCount int, recency float64, confidence float64) float64 {
	countWeight := 0.4
	recencyWeight := 0.3
	confidenceWeight := 0.3
	countScore := 1.0 - (1.0 / float64(1+evidenceCount))
	return (countScore * countWeight) + (recency * recencyWeight) + (confidence * confidenceWeight)
}

// --- helpers ---

func detectCooccurrences(content string, entities []ExtractedEntity) []DetectedRelationship {
	var relationships []DetectedRelationship
	contentLower := strings.ToLower(content)

	for i := 0; i < len(entities); i++ {
		for j := i + 1; j < len(entities); j++ {
			e1 := entities[i]
			e2 := entities[j]

			pos1 := strings.Index(contentLower, strings.ToLower(e1.Name))
			pos2 := strings.Index(contentLower, strings.ToLower(e2.Name))
			if pos1 == -1 || pos2 == -1 {
				continue
			}

			distance := pos2 - pos1 - len(e1.Name)
			if distance < 0 {
				distance = -distance
			}

			if distance < 100 {
				var start, end int
				if pos1 < pos2 {
					start = pos1 + len(e1.Name)
					end = pos2
				} else {
					start = pos2 + len(e2.Name)
					end = pos1
				}
				if start >= end || start < 0 || end > len(content) {
					continue
				}

				between := content[start:end]
				relType := inferRelationshipFromContext(between)
				if relType != "" {
					relationships = append(relationships, DetectedRelationship{
						SourceEntity:     e1.Name,
						TargetEntity:     e2.Name,
						RelationshipType: relType,
						IsBidirectional:  false,
						Confidence:       0.5,
						Evidence:         between,
					})
				}
			}
		}
	}

	return relationships
}

func detectVerbRelationships(content string) []DetectedRelationship {
	var relationships []DetectedRelationship

	verbPatterns := []struct {
		pattern string
		relType string
		bidir   bool
	}{
		{"met", "knows", true},
		{"know", "knows", true},
		{"introduced", "knows", true},
		{"hired", "employed_by", false},
		{"taught", "taught_by", false},
		{"mentored", "mentored_by", false},
		{"recommended", "recommended_by", false},
		{"invited", "invited_by", false},
	}

	for _, vp := range verbPatterns {
		re := regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)\s+` + vp.pattern + `\s+([A-Z][a-z]+(?:\s+[A-Z][a-z]+)?)`)
		matches := re.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) >= 3 {
				relationships = append(relationships, DetectedRelationship{
					SourceEntity:     match[1],
					TargetEntity:     match[2],
					RelationshipType: vp.relType,
					IsBidirectional:  vp.bidir,
					Confidence:       0.75,
					Evidence:         match[0],
				})
			}
		}
	}

	return relationships
}

func inferRelationshipFromContext(between string) string {
	betweenLower := strings.ToLower(strings.TrimSpace(between))

	if strings.Contains(betweenLower, "and") {
		if strings.Contains(betweenLower, "work") {
			return "works_with"
		}
		return "associated_with"
	}
	if strings.Contains(betweenLower, "with") {
		return "associated_with"
	}
	if strings.Contains(betweenLower, "at") {
		return "located_at"
	}
	if strings.Contains(betweenLower, "from") {
		return "from"
	}
	return ""
}

func deduplicateRelationships(relationships []DetectedRelationship) []DetectedRelationship {
	seen := make(map[string]DetectedRelationship)

	for _, r := range relationships {
		var key string
		if r.IsBidirectional && r.TargetEntity < r.SourceEntity {
			key = r.TargetEntity + ":" + r.SourceEntity + ":" + r.RelationshipType
		} else {
			key = r.SourceEntity + ":" + r.TargetEntity + ":" + r.RelationshipType
		}

		existing, exists := seen[key]
		if !exists || r.Confidence > existing.Confidence {
			seen[key] = r
		}
	}

	result := make([]DetectedRelationship, 0, len(seen))
	for _, r := range seen {
		result = append(result, r)
	}
	return result
}

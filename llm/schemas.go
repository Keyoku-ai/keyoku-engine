// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package llm

import "google.golang.org/genai"

// -----------------------------------------------------------------------
// Canonical JSON-Schema definitions shared by every LLM provider.
//
// Each schema is defined once as map[string]interface{} (standard JSON Schema).
// Provider-specific adapters convert these to the native format:
//   - OpenAI:    adds "additionalProperties":false recursively (required for strict mode)
//   - Gemini:    converts to *genai.Schema structs
//   - Anthropic: adds "minimum"/"maximum" on number fields, optional "description"
// -----------------------------------------------------------------------

// --- Shared enum values ---------------------------------------------------

var MemoryTypeEnums = []string{"IDENTITY", "PREFERENCE", "RELATIONSHIP", "EVENT", "ACTIVITY", "PLAN", "CONTEXT", "EPHEMERAL"}
var EntityTypeEnums = []string{"PERSON", "ORGANIZATION", "LOCATION", "PRODUCT"}
var ConflictTypeEnums = []string{"contradiction", "update", "temporal", "partial", "none"}
var ResolutionEnums = []string{"use_new", "keep_existing", "merge", "keep_both"}
var UrgencyEnums = []string{"immediate", "soon", "can_wait"}
var HeartbeatUrgencyEnums = []string{"none", "low", "medium", "high", "critical"}
var AutonomyEnums = []string{"observe", "suggest", "act"}

// --- Sub-schema building blocks -------------------------------------------

// MemoryItemSchema is the schema for a single extracted memory.
var MemoryItemSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"content":            map[string]interface{}{"type": "string"},
		"type":               map[string]interface{}{"type": "string", "enum": MemoryTypeEnums},
		"importance":         map[string]interface{}{"type": "number"},
		"confidence":         map[string]interface{}{"type": "number"},
		"sentiment":          map[string]interface{}{"type": "number"},
		"importance_factors": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		"confidence_factors": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		"hedging_detected":   map[string]interface{}{"type": "boolean"},
	},
}

// EntityItemSchema is the schema for a single extracted entity.
var EntityItemSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"canonical_name": map[string]interface{}{"type": "string"},
		"type":           map[string]interface{}{"type": "string", "enum": EntityTypeEnums},
		"aliases":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		"context":        map[string]interface{}{"type": "string"},
	},
}

// RelationshipItemSchema is the schema for a single extracted relationship.
var RelationshipItemSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"source":     map[string]interface{}{"type": "string"},
		"relation":   map[string]interface{}{"type": "string"},
		"target":     map[string]interface{}{"type": "string"},
		"confidence": map[string]interface{}{"type": "number"},
	},
	"required": []string{"source", "relation", "target", "confidence"},
}

// UpdateItemSchema is the schema for a suggested memory update.
var UpdateItemSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"query":       map[string]interface{}{"type": "string"},
		"new_content": map[string]interface{}{"type": "string"},
		"reason":      map[string]interface{}{"type": "string"},
	},
	"required": []string{"query", "new_content", "reason"},
}

// DeleteItemSchema is the schema for a suggested memory deletion.
var DeleteItemSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"query":  map[string]interface{}{"type": "string"},
		"reason": map[string]interface{}{"type": "string"},
	},
	"required": []string{"query", "reason"},
}

// ResolveItemSchema is the schema for a suggested memory resolution.
var ResolveItemSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"query":  map[string]interface{}{"type": "string"},
		"reason": map[string]interface{}{"type": "string"},
	},
	"required": []string{"query", "reason"},
}

// SkippedItemSchema is the schema for skipped content.
var SkippedItemSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"text":   map[string]interface{}{"type": "string"},
		"reason": map[string]interface{}{"type": "string"},
	},
	"required": []string{"text", "reason"},
}

// RankingItemSchema is the schema for a single re-ranked result.
var RankingItemSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"id":    map[string]interface{}{"type": "string"},
		"score": map[string]interface{}{"type": "number"},
	},
	"required": []string{"id", "score"},
}

// --- Top-level schemas ---------------------------------------------------

// ExtractionSchema is the full memory extraction schema (memories + entities + relationships + updates + deletes + resolves + skipped).
func ExtractionSchema() map[string]interface{} {
	memItem := cloneSchema(MemoryItemSchema)
	entItem := cloneSchema(EntityItemSchema)
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"memories":      map[string]interface{}{"type": "array", "items": memItem},
			"entities":      map[string]interface{}{"type": "array", "items": entItem},
			"relationships": map[string]interface{}{"type": "array", "items": cloneSchema(RelationshipItemSchema)},
			"updates":       map[string]interface{}{"type": "array", "items": cloneSchema(UpdateItemSchema)},
			"deletes":       map[string]interface{}{"type": "array", "items": cloneSchema(DeleteItemSchema)},
			"resolves":      map[string]interface{}{"type": "array", "items": cloneSchema(ResolveItemSchema)},
			"skipped":       map[string]interface{}{"type": "array", "items": cloneSchema(SkippedItemSchema)},
		},
	}
}

// CoreExtractionSchema is the extraction schema without graph data (no entities/relationships).
// Used by lite models that split extraction into two calls.
func CoreExtractionSchema() map[string]interface{} {
	memItem := cloneSchema(MemoryItemSchema)
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"memories": map[string]interface{}{"type": "array", "items": memItem},
			"updates":  map[string]interface{}{"type": "array", "items": cloneSchema(UpdateItemSchema)},
			"deletes":  map[string]interface{}{"type": "array", "items": cloneSchema(DeleteItemSchema)},
			"resolves": map[string]interface{}{"type": "array", "items": cloneSchema(ResolveItemSchema)},
			"skipped":  map[string]interface{}{"type": "array", "items": cloneSchema(SkippedItemSchema)},
		},
	}
}

// GraphExtractionSchema is the schema for entity + relationship extraction only.
func GraphExtractionSchema() map[string]interface{} {
	entItem := cloneSchema(EntityItemSchema)
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"entities":      map[string]interface{}{"type": "array", "items": entItem},
			"relationships": map[string]interface{}{"type": "array", "items": cloneSchema(RelationshipItemSchema)},
		},
	}
}

// ConsolidationSchema is the schema for memory consolidation responses.
func ConsolidationSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content":    map[string]interface{}{"type": "string"},
			"confidence": map[string]interface{}{"type": "number"},
			"reasoning":  map[string]interface{}{"type": "string"},
		},
		"required": []string{"content", "confidence", "reasoning"},
	}
}

// CustomExtractionResponseSchema wraps a user-provided schema with confidence and reasoning.
func CustomExtractionResponseSchema(userSchema map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"extracted_data": userSchema,
			"confidence":     map[string]interface{}{"type": "number"},
			"reasoning":      map[string]interface{}{"type": "string"},
		},
		"required": []string{"extracted_data", "confidence", "reasoning"},
	}
}

// StateExtractionResponseSchema is the schema for state extraction responses.
func StateExtractionResponseSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"extracted_state":  map[string]interface{}{"type": "object"},
			"changed_fields":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"confidence":       map[string]interface{}{"type": "number"},
			"reasoning":        map[string]interface{}{"type": "string"},
			"suggested_action": map[string]interface{}{"type": "string"},
			"validation_error": map[string]interface{}{"type": "string"},
		},
		"required": []string{"extracted_state", "changed_fields", "confidence", "reasoning"},
	}
}

// ConflictCheckSchema is the schema for conflict detection responses.
func ConflictCheckSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"contradicts":   map[string]interface{}{"type": "boolean"},
			"conflict_type": map[string]interface{}{"type": "string", "enum": ConflictTypeEnums},
			"confidence":    map[string]interface{}{"type": "number"},
			"explanation":   map[string]interface{}{"type": "string"},
			"resolution":    map[string]interface{}{"type": "string", "enum": ResolutionEnums},
		},
		"required": []string{"contradicts", "conflict_type", "confidence", "explanation", "resolution"},
	}
}

// ImportanceReEvalSchema is the schema for importance re-evaluation responses.
func ImportanceReEvalSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"new_importance": map[string]interface{}{"type": "number"},
			"reason":         map[string]interface{}{"type": "string"},
			"should_update":  map[string]interface{}{"type": "boolean"},
		},
		"required": []string{"new_importance", "reason", "should_update"},
	}
}

// ActionPrioritySchema is the schema for action prioritization responses.
func ActionPrioritySchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"priority_action": map[string]interface{}{"type": "string"},
			"action_items":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"reasoning":       map[string]interface{}{"type": "string"},
			"urgency":         map[string]interface{}{"type": "string", "enum": UrgencyEnums},
		},
		"required": []string{"priority_action", "action_items", "reasoning", "urgency"},
	}
}

// HeartbeatAnalysisSchema is the schema for heartbeat analysis responses.
func HeartbeatAnalysisSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"should_act":          map[string]interface{}{"type": "boolean"},
			"action_brief":        map[string]interface{}{"type": "string"},
			"recommended_actions": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"urgency":             map[string]interface{}{"type": "string", "enum": HeartbeatUrgencyEnums},
			"reasoning":           map[string]interface{}{"type": "string"},
			"autonomy":            map[string]interface{}{"type": "string", "enum": AutonomyEnums},
			"user_facing":         map[string]interface{}{"type": "string"},
		},
		"required": []string{"should_act", "action_brief", "recommended_actions", "urgency", "reasoning", "autonomy", "user_facing"},
	}
}

// GraphSummarySchema is the schema for graph summary responses.
func GraphSummarySchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"summary":    map[string]interface{}{"type": "string"},
			"confidence": map[string]interface{}{"type": "number"},
		},
		"required": []string{"summary", "confidence"},
	}
}

// RerankSchema is the schema for re-ranking responses.
func RerankSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"rankings": map[string]interface{}{
				"type":  "array",
				"items": cloneSchema(RankingItemSchema),
			},
		},
		"required": []string{"rankings"},
	}
}

// -----------------------------------------------------------------------
// Provider-specific schema adapters
// -----------------------------------------------------------------------

// ForOpenAI decorates a canonical schema for OpenAI strict mode:
//   - adds "additionalProperties": false to every object
//   - ensures all properties are listed in "required"
func ForOpenAI(schema map[string]interface{}) map[string]interface{} {
	return addOpenAIStrict(cloneSchema(schema))
}

// ForOpenAIExtraction returns the full extraction schema with OpenAI-specific required fields.
// OpenAI requires all 8 memory item fields in "required" for strict mode.
func ForOpenAIExtraction() map[string]interface{} {
	s := ExtractionSchema()
	// OpenAI needs all memory fields required
	setRequired(s, "memories", []string{"content", "type", "importance", "confidence", "sentiment", "importance_factors", "confidence_factors", "hedging_detected"})
	// OpenAI needs all entity fields required
	setRequired(s, "entities", []string{"canonical_name", "type", "aliases", "context"})
	// Top-level required
	s["required"] = []string{"memories", "entities", "relationships", "updates", "deletes", "resolves", "skipped"}
	return ForOpenAI(s)
}

// ForAnthropicExtraction returns the extraction schema with Anthropic-specific decorations.
// Anthropic adds min/max constraints on numeric fields and requires fewer memory fields.
func ForAnthropicExtraction() map[string]interface{} {
	s := ExtractionSchema()
	// Anthropic requires fewer memory fields
	setRequired(s, "memories", []string{"content", "type", "importance", "confidence", "sentiment"})
	// Anthropic requires fewer entity fields
	setRequired(s, "entities", []string{"canonical_name", "type"})
	// Pre-set sentiment range to -1..1 before addAnthropicConstraints applies 0..1 defaults
	setSentimentRange(s)
	return addAnthropicConstraints(s)
}

// ForAnthropicProps extracts just the properties from a schema, suitable for ToolInputSchemaParam.Properties.
func ForAnthropicProps(schema map[string]interface{}) map[string]interface{} {
	s := addAnthropicConstraints(cloneSchema(schema))
	if props, ok := s["properties"].(map[string]interface{}); ok {
		return props
	}
	return s
}

// ForGemini converts a canonical schema to a Gemini *genai.Schema struct.
func ForGemini(schema map[string]interface{}) *genai.Schema {
	return mapToGenaiSchema(schema)
}

// ForGeminiExtraction returns the full extraction schema as a Gemini *genai.Schema.
// Gemini requires fewer memory fields (5 instead of 8).
func ForGeminiExtraction() *genai.Schema {
	s := ExtractionSchema()
	setRequired(s, "memories", []string{"content", "type", "importance", "confidence", "sentiment"})
	setRequired(s, "entities", []string{"canonical_name", "type"})
	return mapToGenaiSchema(s)
}

// ForGeminiCoreExtraction returns the core extraction schema (no graph) as a Gemini *genai.Schema.
func ForGeminiCoreExtraction() *genai.Schema {
	s := CoreExtractionSchema()
	setRequired(s, "memories", []string{"content", "type", "importance", "confidence", "sentiment"})
	return mapToGenaiSchema(s)
}

// ForGeminiGraphExtraction returns the graph extraction schema as a Gemini *genai.Schema.
func ForGeminiGraphExtraction() *genai.Schema {
	s := GraphExtractionSchema()
	setRequired(s, "entities", []string{"canonical_name", "type"})
	return mapToGenaiSchema(s)
}

// -----------------------------------------------------------------------
// Internal helpers
// -----------------------------------------------------------------------

// cloneSchema deep-copies a map[string]interface{} so mutations don't affect the original.
func cloneSchema(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		switch val := v.(type) {
		case map[string]interface{}:
			out[k] = cloneSchema(val)
		case []string:
			cp := make([]string, len(val))
			copy(cp, val)
			out[k] = cp
		case []interface{}:
			cp := make([]interface{}, len(val))
			copy(cp, val)
			out[k] = cp
		default:
			out[k] = v
		}
	}
	return out
}

// setSentimentRange pre-sets "minimum":-1, "maximum":1 on the sentiment field
// inside the memories array items, so that addAnthropicConstraints (which defaults
// numbers to 0..1) does not override it.
func setSentimentRange(schema map[string]interface{}) {
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return
	}
	memories, ok := props["memories"].(map[string]interface{})
	if !ok {
		return
	}
	items, ok := memories["items"].(map[string]interface{})
	if !ok {
		return
	}
	itemProps, ok := items["properties"].(map[string]interface{})
	if !ok {
		return
	}
	if sentiment, ok := itemProps["sentiment"].(map[string]interface{}); ok {
		sentiment["minimum"] = -1
		sentiment["maximum"] = 1
	}
}

// setRequired sets the "required" field on the items of a named array property.
func setRequired(schema map[string]interface{}, arrayPropName string, required []string) {
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return
	}
	arrProp, ok := props[arrayPropName].(map[string]interface{})
	if !ok {
		return
	}
	items, ok := arrProp["items"].(map[string]interface{})
	if !ok {
		return
	}
	items["required"] = required
}

// addOpenAIStrict recursively adds "additionalProperties": false to every object schema
// and ensures all properties are listed as required.
func addOpenAIStrict(schema map[string]interface{}) map[string]interface{} {
	typ, _ := schema["type"].(string)
	if typ == "object" {
		schema["additionalProperties"] = false
		if props, ok := schema["properties"].(map[string]interface{}); ok {
			// If no required field set, make all properties required
			if _, hasReq := schema["required"]; !hasReq {
				keys := make([]string, 0, len(props))
				for k := range props {
					keys = append(keys, k)
				}
				schema["required"] = keys
			}
			for _, v := range props {
				if sub, ok := v.(map[string]interface{}); ok {
					addOpenAIStrict(sub)
				}
			}
		}
	}
	if typ == "array" {
		if items, ok := schema["items"].(map[string]interface{}); ok {
			addOpenAIStrict(items)
		}
	}
	return schema
}

// addAnthropicConstraints adds "minimum"/"maximum" to number fields and recurses.
func addAnthropicConstraints(schema map[string]interface{}) map[string]interface{} {
	typ, _ := schema["type"].(string)
	if typ == "number" {
		// Anthropic uses min/max on numeric fields for validation hints
		if _, ok := schema["minimum"]; !ok {
			schema["minimum"] = 0
			schema["maximum"] = 1
		}
	}
	if typ == "object" {
		if props, ok := schema["properties"].(map[string]interface{}); ok {
			for _, v := range props {
				if sub, ok := v.(map[string]interface{}); ok {
					addAnthropicConstraints(sub)
				}
			}
		}
	}
	if typ == "array" {
		if items, ok := schema["items"].(map[string]interface{}); ok {
			addAnthropicConstraints(items)
		}
	}
	return schema
}

// mapToGenaiSchema converts a map[string]interface{} JSON Schema to a *genai.Schema.
func mapToGenaiSchema(m map[string]interface{}) *genai.Schema {
	if m == nil {
		return nil
	}
	s := &genai.Schema{}

	if typ, ok := m["type"].(string); ok {
		switch typ {
		case "string":
			s.Type = genai.TypeString
		case "number":
			s.Type = genai.TypeNumber
		case "boolean":
			s.Type = genai.TypeBoolean
		case "array":
			s.Type = genai.TypeArray
		case "object":
			s.Type = genai.TypeObject
		}
	}

	if desc, ok := m["description"].(string); ok {
		s.Description = desc
	}

	if enumVals, ok := m["enum"].([]string); ok {
		s.Enum = enumVals
	}

	if items, ok := m["items"].(map[string]interface{}); ok {
		s.Items = mapToGenaiSchema(items)
	}

	if props, ok := m["properties"].(map[string]interface{}); ok {
		s.Properties = make(map[string]*genai.Schema, len(props))
		for k, v := range props {
			if sub, ok := v.(map[string]interface{}); ok {
				s.Properties[k] = mapToGenaiSchema(sub)
			}
		}
	}

	if req, ok := m["required"].([]string); ok {
		s.Required = req
	}

	return s
}

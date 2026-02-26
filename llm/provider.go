package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Provider is the interface that all LLM providers must implement.
type Provider interface {
	ExtractMemories(ctx context.Context, req ExtractionRequest) (*ExtractionResponse, error)
	ConsolidateMemories(ctx context.Context, req ConsolidationRequest) (*ConsolidationResponse, error)
	ExtractWithSchema(ctx context.Context, req CustomExtractionRequest) (*CustomExtractionResponse, error)
	ExtractState(ctx context.Context, req StateExtractionRequest) (*StateExtractionResponse, error)
	DetectConflict(ctx context.Context, req ConflictCheckRequest) (*ConflictCheckResponse, error)
	ReEvaluateImportance(ctx context.Context, req ImportanceReEvalRequest) (*ImportanceReEvalResponse, error)
	PrioritizeActions(ctx context.Context, req ActionPriorityRequest) (*ActionPriorityResponse, error)
	SummarizeGraph(ctx context.Context, req GraphSummaryRequest) (*GraphSummaryResponse, error)
	Name() string
	Model() string
}

// ProviderConfig holds configuration for creating a provider.
type ProviderConfig struct {
	Provider string
	APIKey   string
	Model    string
}

// NewProvider creates a new LLM provider based on configuration.
func NewProvider(cfg ProviderConfig) (Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("API key is required for provider %s", cfg.Provider)
	}

	switch cfg.Provider {
	case "google":
		return NewGeminiProvider(cfg.APIKey, cfg.Model)
	case "openai":
		return NewOpenAIProvider(cfg.APIKey, cfg.Model)
	case "anthropic":
		return NewAnthropicProvider(cfg.APIKey, cfg.Model)
	default:
		return nil, fmt.Errorf("unknown provider: %s (valid: google, openai, anthropic)", cfg.Provider)
	}
}

// Extraction prompt (shared across all providers).
const extractionPrompt = `You are a memory extraction system. Your job is to analyze input text and decide what is worth remembering.

## RECENT CONVERSATION CONTEXT
%s

## EXISTING MEMORIES (that might need updating)
%s

## MEMORY TYPES (with stability in days - determines how fast they decay)
- IDENTITY (365): Core facts - name, job, location, age
- PREFERENCE (180): Likes, dislikes, opinions
- RELATIONSHIP (180): Connections to other people/entities
- EVENT (60): Time-bound occurrences - meetings, incidents
- ACTIVITY (45): Ongoing actions - learning, working on
- PLAN (30): Future intentions - trips, goals
- CONTEXT (7): Session-specific - current mood, immediate situation
- EPHEMERAL (1): Very short-term - just mentioned

## IMPORTANCE SCORING (0.0-1.0)
Consider these factors when scoring importance:
- Explicit emphasis ("This is really important")
- Identity-level information (name, role, core attributes)
- Safety/health related (allergies, conditions, warnings)
- Emotional weight (strong feelings)
- Rarity (unusual, unexpected information)
- Specificity (precise details vs vague statements)
- Hedging language reduces importance ("maybe", "I think", "possibly")

Base scores by type: IDENTITY=0.8, PREFERENCE=0.6, RELATIONSHIP=0.7, EVENT=0.5, ACTIVITY=0.5, PLAN=0.5, CONTEXT=0.3, EPHEMERAL=0.2

## CONFIDENCE SCORING (0.0-1.0)
- HIGH: Direct first-person statement, unambiguous, explicit
- LOWER: Inferred from context, third-party ("friend said"), ambiguous, hedged

## SENTIMENT SCORING (-1.0 to 1.0)
- POSITIVE (0.3 to 1.0): joy, excitement, satisfaction, love, gratitude, enthusiasm
- NEGATIVE (-1.0 to -0.3): frustration, anger, sadness, disappointment, fear, anxiety
- NEUTRAL (-0.3 to 0.3): factual statements, descriptions without emotion
Examples: "I love my new job" → 0.8, "I'm frustrated with the commute" → -0.6, "I work at Google" → 0.0

## SCHEDULE DETECTION
If the input expresses a recurring or time-based intent, add a "tags" array with a cron tag:
- "every morning at 8" → tags: ["cron:daily:08:00"]
- "check this every Monday at 9am" → tags: ["cron:weekly:mon:09:00"]
- "remind me every weekday at 8:30" → tags: ["cron:weekdays:08:30"]
- "on the 1st of every month at 9am" → tags: ["cron:monthly:1:09:00"]
- "every 4 hours" → tags: ["cron:every:4h"]
- "every day" (no specific time) → tags: ["cron:daily"]
- "every hour" → tags: ["cron:hourly"]
Only add schedule tags when the user clearly expresses recurring intent. Do NOT tag one-time events.

## ENTITY EXTRACTION
Extract named entities mentioned in the input:
- PERSON: People's names (use their FULL name as the canonical form)
- ORGANIZATION: Companies, teams, departments, institutions
- LOCATION: Cities, countries, places, addresses
- PRODUCT: Apps, devices, services, brands

Rules for entity extraction:
- Use the COMPLETE canonical name, never fragments (e.g., "Sarah Chen" not "Sarah" + "Chen")
- If only a first name is mentioned, use just the first name
- Normalize variations to a single canonical form
- Include aliases if multiple forms are used (e.g., "Bob" and "Robert")

## RELATIONSHIP EXTRACTION
Extract relationships between entities:
- works_at, employed_by: Person works at Organization
- manages, reports_to: Management relationships
- friend_of, knows: Social connections
- lives_in, from: Location relationships
- uses, owns: Product/possession relationships
- married_to, related_to: Family relationships

## YOUR TASK
1. Read the current input and context carefully
2. Decide what is memory-worthy (YOU are the judge)
3. Segment into atomic units (one fact per memory)
4. Classify, score importance, score confidence
5. Check if this UPDATES or CONTRADICTS existing memories
   - If user says "actually" or "changed my mind" → suggest UPDATE/DELETE
   - Reference the context to understand corrections
6. Normalize content to third person ("User works at..." not "I work at...")
7. Extract entities and relationships mentioned in the input

## INPUT (Current message to process)
%s

## OUTPUT (JSON)
Return JSON matching this exact schema:
{
  "memories": [
    {
      "content": "string - the memory text in third person",
      "type": "string - one of: IDENTITY, PREFERENCE, RELATIONSHIP, EVENT, ACTIVITY, PLAN, CONTEXT, EPHEMERAL",
      "importance": 0.0-1.0,
      "confidence": 0.0-1.0,
      "sentiment": -1.0 to 1.0,
      "importance_factors": ["array", "of", "strings", "explaining", "importance"],
      "confidence_factors": ["array", "of", "strings", "explaining", "confidence"],
      "hedging_detected": true/false,
      "tags": ["optional", "array", "e.g.", "cron:daily:08:00"]
    }
  ],
  "entities": [
    {
      "canonical_name": "string - the full/proper name of the entity",
      "type": "string - one of: PERSON, ORGANIZATION, LOCATION, PRODUCT",
      "aliases": ["array", "of", "alternative", "names"],
      "context": "string - brief description or role"
    }
  ],
  "relationships": [
    {
      "source": "string - source entity canonical name",
      "relation": "string - relationship type (works_at, manages, friend_of, lives_in, uses, etc.)",
      "target": "string - target entity canonical name",
      "confidence": 0.0-1.0
    }
  ],
  "updates": [{"query": "string", "new_content": "string", "reason": "string"}],
  "deletes": [{"query": "string", "reason": "string"}],
  "skipped": [{"text": "string", "reason": "string"}]
}

CRITICAL: importance_factors and confidence_factors MUST be arrays of strings, NOT a single string.
CRITICAL: Use COMPLETE canonical names for entities, never fragments. "Sarah Chen" is ONE entity, not three.
Return ONLY valid JSON. No markdown, no explanation.`

const consolidationPrompt = `You are a memory consolidation system. Your job is to merge multiple similar memories into a single, coherent memory that preserves all important information.

## MEMORIES TO CONSOLIDATE
%s

%s
## YOUR TASK
1. Analyze all the provided memories
2. Identify the core information they share
3. Identify any unique details in each memory
4. Use entity/relationship context (if provided) to preserve proper names and connections
5. Prioritize information from higher-importance memories
6. Consider sentiment trends — if memories shifted from positive to negative (or vice versa), reflect the most recent sentiment
7. Create a single consolidated memory that:
   - Preserves ALL important facts
   - Eliminates redundancy
   - Maintains clarity and readability
   - Uses third person ("User..." not "I...")
   - Is concise but complete

## OUTPUT (JSON)
Return JSON with:
- "content": The consolidated memory text (string)
- "confidence": How confident you are in this consolidation, 0.0-1.0 (number)
- "reasoning": Brief explanation of how you merged these memories (string)

Return ONLY valid JSON. No markdown, no explanation.`

const customExtractionPrompt = `You are a data extraction system. Your job is to extract structured information from text according to a specific schema.

## SCHEMA NAME
%s

## SCHEMA DEFINITION
%s

## CONVERSATION CONTEXT
%s

## INPUT TEXT TO ANALYZE
%s

## YOUR TASK
1. Read the input text carefully
2. Extract information that matches the schema fields
3. For each field in the schema:
   - If information is present, extract it accurately
   - If information is not present or unclear, use null
   - Follow any field type constraints (string, number, boolean, array, enum)
4. Be conservative - only extract what is clearly stated or strongly implied
5. Do NOT make up or hallucinate information

## OUTPUT (JSON)
Return a JSON object with:
- "extracted_data": An object matching the schema structure with extracted values
- "confidence": Your overall confidence in the extraction (0.0-1.0)
- "reasoning": Brief explanation of what you found and any fields you couldn't extract

Return ONLY valid JSON. No markdown, no explanation outside the JSON.`

const stateExtractionPrompt = `You are a state extraction system for AI agent workflows. Your job is to analyze agent interactions and extract/update workflow state.

## SCHEMA NAME
%s

## STATE SCHEMA DEFINITION
%s

## CURRENT STATE
%s

## VALID STATE TRANSITIONS
%s

## AGENT CONTEXT
Agent: %s

## CONVERSATION CONTEXT
%s

## CURRENT INTERACTION TO ANALYZE
%s

## YOUR TASK
1. Analyze the interaction to determine if any state fields should change
2. Extract new values for fields according to the schema structure
3. If transition rules are defined, validate that the state change is allowed
4. Be conservative - only update fields where there's clear evidence
5. Do NOT make up or hallucinate state changes
6. Note which fields actually changed from the current state

## OUTPUT (JSON)
Return a JSON object with:
- "extracted_state": An object with the complete state (including unchanged fields from current state)
- "changed_fields": Array of field names that actually changed
- "confidence": Your confidence in the extraction (0.0-1.0)
- "reasoning": Brief explanation of what triggered the state changes
- "suggested_action": Optional suggestion for what should happen next
- "validation_error": Non-empty string if a transition rule would be violated

Return ONLY valid JSON. No markdown, no explanation outside the JSON.`

const conflictCheckPrompt = `Do these two memories contradict or conflict?

Memory A (existing): "%s"
Memory B (new): "%s"
Type: %s
Context: %s

Analyze whether memory B contradicts, updates, or conflicts with memory A.

## OUTPUT (JSON)
Return JSON with:
- "contradicts": true/false - whether there is a meaningful conflict
- "conflict_type": one of "contradiction" (direct opposite), "update" (newer info replaces), "temporal" (time-based change), "partial" (partially conflicts), "none"
- "confidence": 0.0-1.0 how confident you are
- "explanation": brief explanation of the conflict (or lack thereof)
- "resolution": one of "use_new" (replace with B), "keep_existing" (keep A), "merge" (combine both), "keep_both" (no conflict)

Return ONLY valid JSON. No markdown, no explanation.`

const importanceReEvalPrompt = `Given new information, should this existing memory's importance change?

New information: "%s"
Existing memory: "%s" (importance: %.2f, type: %s)
Related context: %s

Consider:
- Does the new info make the existing memory MORE relevant (e.g., confirms, adds context)?
- Does the new info make it LESS relevant (e.g., supersedes, contradicts)?
- Only update if the change is significant (>0.1 difference)

## OUTPUT (JSON)
Return JSON with:
- "new_importance": 0.0-1.0 the new importance score
- "reason": brief explanation
- "should_update": true/false - only true if the change is meaningful

Return ONLY valid JSON. No markdown, no explanation.`

const actionPriorityPrompt = `You are an AI agent's executive function. Given pending items from a heartbeat check, determine what the agent should prioritize.

Current agent context: %s
Entity: %s

Pending items:
%s

Analyze the items and determine:
1. The single most important action to take right now
2. All action items ordered by priority
3. The urgency level

## OUTPUT (JSON)
Return JSON with:
- "priority_action": string - the single most important thing to do right now
- "action_items": array of strings - all items ordered by priority (most important first)
- "reasoning": string - brief explanation of why this ordering
- "urgency": one of "immediate" (act now), "soon" (within the hour), "can_wait" (no rush)

Return ONLY valid JSON. No markdown, no explanation.`

// FormatActionPriorityPrompt formats the heartbeat prioritization prompt.
func FormatActionPriorityPrompt(req ActionPriorityRequest) string {
	agentCtx := req.AgentContext
	if agentCtx == "" {
		agentCtx = "(no agent context provided)"
	}
	entityCtx := req.EntityContext
	if entityCtx == "" {
		entityCtx = "(no entity context provided)"
	}
	return fmt.Sprintf(actionPriorityPrompt, agentCtx, entityCtx, req.Summary)
}

const graphSummaryPrompt = `You are a knowledge graph reasoning system. Analyze the following entity relationships and provide a natural language summary.

## ENTITIES
%s

## RELATIONSHIPS
%s

## QUESTION
%s

Provide a clear, concise summary explaining how these entities are connected and what the relationships mean.

## OUTPUT (JSON)
Return JSON with:
- "summary": string - natural language explanation of the connections
- "confidence": 0.0-1.0 - how confident you are in this summary

Return ONLY valid JSON. No markdown, no explanation.`

// FormatGraphSummaryPrompt formats the graph reasoning prompt.
func FormatGraphSummaryPrompt(req GraphSummaryRequest) string {
	entitiesStr := "(none)"
	if len(req.Entities) > 0 {
		entitiesStr = ""
		for _, e := range req.Entities {
			entitiesStr += fmt.Sprintf("- %s\n", e)
		}
	}
	relsStr := "(none)"
	if len(req.Relationships) > 0 {
		relsStr = ""
		for _, r := range req.Relationships {
			relsStr += fmt.Sprintf("- %s\n", r)
		}
	}
	question := req.Question
	if question == "" {
		question = "Summarize the connections between these entities."
	}
	return fmt.Sprintf(graphSummaryPrompt, entitiesStr, relsStr, question)
}

// FormatConflictCheckPrompt formats the conflict detection prompt.
func FormatConflictCheckPrompt(req ConflictCheckRequest) string {
	ctx := req.Context
	if ctx == "" {
		ctx = "(no additional context)"
	}
	return fmt.Sprintf(conflictCheckPrompt, req.ExistingContent, req.NewContent, req.MemoryType, ctx)
}

// FormatImportanceReEvalPrompt formats the importance re-evaluation prompt.
func FormatImportanceReEvalPrompt(req ImportanceReEvalRequest) string {
	relatedStr := "(none)"
	if len(req.RelatedMemories) > 0 {
		relatedStr = ""
		for _, m := range req.RelatedMemories {
			relatedStr += fmt.Sprintf("- %s\n", m)
		}
	}
	return fmt.Sprintf(importanceReEvalPrompt, req.NewContent, req.ExistingContent, req.CurrentImportance, req.CurrentType, relatedStr)
}

// FormatPrompt formats the extraction prompt with the given context.
func FormatPrompt(req ExtractionRequest) string {
	contextStr := "(No recent context)"
	if len(req.ConversationCtx) > 0 {
		contextStr = ""
		for i, msg := range req.ConversationCtx {
			contextStr += fmt.Sprintf("Turn %d: %s\n", i+1, msg)
		}
	}

	memoriesStr := "(No existing memories)"
	if len(req.ExistingMemories) > 0 {
		memoriesStr = ""
		for _, mem := range req.ExistingMemories {
			memoriesStr += fmt.Sprintf("- %s\n", mem)
		}
	}

	return fmt.Sprintf(extractionPrompt, contextStr, memoriesStr, req.Content)
}

// FormatConsolidationPrompt formats the consolidation prompt with memories and context.
func FormatConsolidationPrompt(req ConsolidationRequest) string {
	memoriesStr := ""
	for i, mem := range req.Memories {
		line := fmt.Sprintf("%d. %s", i+1, mem)
		// Append importance and sentiment inline if available
		if i < len(req.ImportanceScores) || i < len(req.SentimentValues) {
			annotations := []string{}
			if i < len(req.ImportanceScores) {
				annotations = append(annotations, fmt.Sprintf("importance=%.2f", req.ImportanceScores[i]))
			}
			if i < len(req.SentimentValues) {
				annotations = append(annotations, fmt.Sprintf("sentiment=%.2f", req.SentimentValues[i]))
			}
			line += fmt.Sprintf(" [%s]", strings.Join(annotations, ", "))
		}
		memoriesStr += line + "\n"
	}

	// Build optional context section
	contextStr := ""
	hasContext := len(req.EntityContext) > 0 || len(req.RelationshipContext) > 0 || len(req.ImportanceFactors) > 0

	if hasContext {
		contextStr += "## CONTEXT\n"
		if len(req.EntityContext) > 0 {
			contextStr += "Entities mentioned: " + strings.Join(req.EntityContext, ", ") + "\n"
		}
		if len(req.RelationshipContext) > 0 {
			contextStr += "Relationships: " + strings.Join(req.RelationshipContext, ", ") + "\n"
		}
		if len(req.ImportanceFactors) > 0 {
			contextStr += "Key importance factors: " + strings.Join(req.ImportanceFactors, ", ") + "\n"
		}
		contextStr += "\n"
	}

	return fmt.Sprintf(consolidationPrompt, memoriesStr, contextStr)
}

// FormatCustomExtractionPrompt formats the prompt for custom schema extraction.
func FormatCustomExtractionPrompt(req CustomExtractionRequest) string {
	schemaJSON, _ := json.MarshalIndent(req.Schema, "", "  ")
	contextStr := "(No context provided)"
	if len(req.ConversationCtx) > 0 {
		contextStr = ""
		for i, msg := range req.ConversationCtx {
			contextStr += fmt.Sprintf("Turn %d: %s\n", i+1, msg)
		}
	}
	return fmt.Sprintf(customExtractionPrompt, req.SchemaName, string(schemaJSON), contextStr, req.Content)
}

// FormatStateExtractionPrompt formats the prompt for state extraction.
func FormatStateExtractionPrompt(req StateExtractionRequest) string {
	schemaJSON, _ := json.MarshalIndent(req.Schema, "", "  ")
	currentStateStr := "(No existing state - this is a new state)"
	if req.CurrentState != nil && len(req.CurrentState) > 0 {
		stateJSON, _ := json.MarshalIndent(req.CurrentState, "", "  ")
		currentStateStr = string(stateJSON)
	}
	transitionRulesStr := "(No transition rules defined - any state change is valid)"
	if req.TransitionRules != nil && len(req.TransitionRules) > 0 {
		rulesJSON, _ := json.MarshalIndent(req.TransitionRules, "", "  ")
		transitionRulesStr = string(rulesJSON)
	}
	agentID := req.AgentID
	if agentID == "" {
		agentID = "default"
	}
	contextStr := "(No conversation context provided)"
	if len(req.ConversationCtx) > 0 {
		contextStr = ""
		for i, msg := range req.ConversationCtx {
			contextStr += fmt.Sprintf("Turn %d: %s\n", i+1, msg)
		}
	}
	return fmt.Sprintf(stateExtractionPrompt, req.SchemaName, string(schemaJSON), currentStateStr, transitionRulesStr, agentID, contextStr, req.Content)
}

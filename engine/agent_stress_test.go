// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

//go:build stress

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keyoku-ai/keyoku-engine/embedder"
	"github.com/keyoku-ai/keyoku-engine/llm"
	"github.com/keyoku-ai/keyoku-engine/storage"
)

// =============================================================================
// Agent Stress Test — Multi-Agent Conversation Simulation
//
// Three roles:
//   Agent B (User Mock)  = scripted conversation messages
//   Agent A (Keyoku)     = Engine.Add() + Engine.Query()
//   Observer (Harness)   = orchestrates phases, validates recall
// =============================================================================

// =============================================================================
// Report Types
// =============================================================================

type agentStressReport struct {
	Identity         *phaseReport `json:"identity"`
	Preferences      *phaseReport `json:"preferences"`
	Relationships    *phaseReport `json:"relationships"`
	Temporal         *phaseReport `json:"temporal"`
	Conflicts        *phaseReport `json:"conflicts"`
	Contradictions   *phaseReport `json:"contradictions"`
	MultiSession     *phaseReport `json:"multi_session"`
	Importance       *phaseReport `json:"importance"`
	Dedup            *phaseReport `json:"dedup"`
	GraphDeep        *phaseReport `json:"graph_deep"`
	EntityResolution *phaseReport `json:"entity_resolution"`
	Retrieval        *phaseReport `json:"retrieval"`
	Concurrent       *phaseReport `json:"concurrent"`
	Lifecycle        *phaseReport `json:"lifecycle"`
	EventAudit       *phaseReport `json:"event_audit"`
	Verdict          string       `json:"verdict"`
	Duration         string       `json:"duration"`
	TotalChecks      int          `json:"total_checks"`
	PassedChecks     int          `json:"passed_checks"`
}

type phaseReport struct {
	Name     string        `json:"name"`
	Checks   []checkResult `json:"checks"`
	AllPass  bool          `json:"all_pass"`
	Duration string        `json:"duration"`
}

type checkResult struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}

func newPhaseReport(name string) *phaseReport {
	return &phaseReport{Name: name, Checks: []checkResult{}}
}

func (r *phaseReport) check(name string, pass bool, detail string) {
	r.Checks = append(r.Checks, checkResult{Name: name, Pass: pass, Detail: detail})
}

func (r *phaseReport) finalize() {
	r.AllPass = true
	for _, c := range r.Checks {
		if !c.Pass {
			r.AllPass = false
			break
		}
	}
}

// =============================================================================
// Harness
// =============================================================================

type capturedEvent struct {
	Type     string
	EntityID string
	AgentID  string
	TeamID   string
	Data     map[string]any
	Time     time.Time
}

type agentStressHarness struct {
	t              *testing.T
	engine         *Engine
	store          *storage.SQLiteStore
	provider       llm.Provider
	emb            embedder.Embedder
	dbPath         string
	entityID       string
	sessionCounter int
	enableRerank   bool // when true, all queries use EnableLLMRerank
	events         []capturedEvent
	eventMu        sync.Mutex
}

// newAgentStressHarness creates a harness using the default provider (via initLLMProvider).
func newAgentStressHarness(t *testing.T) *agentStressHarness {
	t.Helper()
	provider, providerName := initLLMProvider(t)
	return newAgentStressHarnessWithProvider(t, provider, providerName)
}

// newAgentStressHarnessWithProvider creates a harness with a specific LLM provider.
func newAgentStressHarnessWithProvider(t *testing.T, provider llm.Provider, providerName string) *agentStressHarness {
	t.Helper()

	emb := initEmbedder(t)
	return newAgentStressHarnessWithStack(t, provider, providerName, emb, false)
}

// newAgentStressHarnessWithStack creates a harness with a specific LLM provider, embedder, and rerank flag.
func newAgentStressHarnessWithStack(t *testing.T, provider llm.Provider, providerName string, emb embedder.Embedder, enableRerank bool) *agentStressHarness {
	t.Helper()

	rerankLabel := "rerank:off"
	if enableRerank {
		rerankLabel = "rerank:on"
	}
	t.Logf("  using LLM provider: %s (%s), embedder dims: %d, %s", providerName, provider.Model(), emb.Dimensions(), rerankLabel)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent_stress.db")
	store, err := storage.NewSQLite(dbPath, emb.Dimensions())
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}

	cfg := EngineConfig{
		ContextTurns:           5,
		EnableImportanceReEval: true,
		Significance:           &SignificanceConfig{Enabled: false},
	}

	engine := NewEngine(provider, emb, store, cfg)

	h := &agentStressHarness{
		t:            t,
		engine:       engine,
		store:        store,
		provider:     provider,
		emb:          emb,
		dbPath:       dbPath,
		entityID:     "agent-stress-user",
		enableRerank: enableRerank,
	}

	// Capture all events
	engine.SetEmitter(func(eventType, entityID, agentID, teamID string, data map[string]any) {
		h.eventMu.Lock()
		defer h.eventMu.Unlock()
		h.events = append(h.events, capturedEvent{
			Type:     eventType,
			EntityID: entityID,
			AgentID:  agentID,
			TeamID:   teamID,
			Data:     data,
			Time:     time.Now(),
		})
	})

	return h
}

func (h *agentStressHarness) close() {
	h.store.Close()
}

func (h *agentStressHarness) nextSession() string {
	h.sessionCounter++
	return fmt.Sprintf("agent-session-%d", h.sessionCounter)
}

func (h *agentStressHarness) addMessage(ctx context.Context, sessionID, content string) (*AddResult, error) {
	result, err := h.engine.Add(ctx, h.entityID, AddRequest{
		Content:   content,
		SessionID: sessionID,
	})
	time.Sleep(500 * time.Millisecond) // rate limit protection
	return result, err
}

func (h *agentStressHarness) query(ctx context.Context, queryStr string, limit int) ([]*QueryResult, error) {
	return h.engine.Query(ctx, h.entityID, QueryRequest{
		Query:           queryStr,
		Limit:           limit,
		EnableLLMRerank: h.enableRerank,
	})
}

func (h *agentStressHarness) countEvents(eventType string) int {
	h.eventMu.Lock()
	defer h.eventMu.Unlock()
	count := 0
	for _, e := range h.events {
		if e.Type == eventType {
			count++
		}
	}
	return count
}

// =============================================================================
// Helpers
// =============================================================================

func agentContainsCaseInsensitive(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func agentAnyContains(results []*QueryResult, substr string) bool {
	for _, r := range results {
		if agentContainsCaseInsensitive(r.Memory.Content, substr) {
			return true
		}
	}
	return false
}

func logAgentPhaseReport(t *testing.T, report *phaseReport) {
	t.Helper()
	for _, c := range report.Checks {
		status := "PASS"
		if !c.Pass {
			status = "FAIL"
		}
		t.Logf("  [%s] %s: %s", status, c.Name, c.Detail)
	}
	t.Logf("  phase %s: all_pass=%v duration=%s", report.Name, report.AllPass, report.Duration)
}

// =============================================================================
// Phase 1: Identity Recall
// =============================================================================

func (h *agentStressHarness) phaseIdentity(ctx context.Context) *phaseReport {
	report := newPhaseReport("Identity Recall")
	start := time.Now()

	session := h.nextSession()
	messages := []string{
		"My name is Marcus Chen and I'm 34 years old",
		"I work as a senior data scientist at Palantir Technologies",
		"I went to MIT for my undergraduate degree in computer science",
		"I'm originally from Portland, Oregon but I moved to the Bay Area in 2019",
	}
	for _, msg := range messages {
		if _, err := h.addMessage(ctx, session, msg); err != nil {
			h.t.Logf("  addMessage error: %v", err)
		}
	}

	_ = h.nextSession() // wipe context

	results, err := h.query(ctx, "what is the user's name", 5)
	nameFound := false
	if err == nil {
		nameFound = agentAnyContains(results, "Marcus")
	}
	report.check("name_recall", nameFound, fmt.Sprintf("found=%v results=%d", nameFound, len(results)))

	results, _ = h.query(ctx, "where does the user work and what do they do", 5)
	jobFound := agentAnyContains(results, "data scientist") || agentAnyContains(results, "Palantir")
	report.check("job_recall", jobFound, "queried job")

	results, _ = h.query(ctx, "how old is the user", 5)
	ageFound := agentAnyContains(results, "34")
	report.check("age_recall", ageFound, "queried age")

	results, _ = h.query(ctx, "where did the user go to school", 5)
	eduFound := agentAnyContains(results, "MIT")
	report.check("education_recall", eduFound, "queried education")

	results, _ = h.query(ctx, "where does the user live or come from", 5)
	locFound := agentAnyContains(results, "Bay Area") || agentAnyContains(results, "Portland") || agentAnyContains(results, "Oregon")
	report.check("location_recall", locFound, "queried location")

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 2: Preference Recall
// =============================================================================

func (h *agentStressHarness) phasePreferences(ctx context.Context) *phaseReport {
	report := newPhaseReport("Preference Recall")
	start := time.Now()

	session := h.nextSession()
	messages := []string{
		"I love Python but I really can't stand Java, the boilerplate drives me crazy",
		"I always use dark mode on everything, light mode hurts my eyes",
		"My favorite food is sushi, especially omakase at high-end restaurants",
		"I prefer working from home over going to the office whenever possible",
		"For music I'm really into jazz and lo-fi hip hop when I'm coding",
	}
	for _, msg := range messages {
		h.addMessage(ctx, session, msg)
	}

	_ = h.nextSession()

	results, _ := h.query(ctx, "what programming language does the user prefer", 5)
	report.check("python_preference", agentAnyContains(results, "Python"), "queried language")

	results, _ = h.query(ctx, "does the user prefer dark mode or light mode", 5)
	report.check("dark_mode_preference", agentAnyContains(results, "dark"), "queried UI pref")

	results, _ = h.query(ctx, "what is the user's favorite food", 5)
	report.check("food_preference", agentAnyContains(results, "sushi"), "queried food")

	results, _ = h.query(ctx, "does the user prefer remote work or office", 5)
	remoteFound := agentAnyContains(results, "home") || agentAnyContains(results, "remote")
	report.check("work_preference", remoteFound, "queried work pref")

	results, _ = h.query(ctx, "what kind of music does the user listen to", 5)
	musicFound := agentAnyContains(results, "jazz") || agentAnyContains(results, "lo-fi")
	report.check("music_preference", musicFound, "queried music")

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 3: Relationship Recall
// =============================================================================

func (h *agentStressHarness) phaseRelationships(ctx context.Context) *phaseReport {
	report := newPhaseReport("Relationship Recall")
	start := time.Now()

	session := h.nextSession()
	messages := []string{
		"My best friend Alice works at Google as a product manager in Mountain View",
		"My boss Charlie is the VP of Engineering at Palantir, he's been there for 10 years",
		"My girlfriend Sarah is a doctor at UCSF Medical Center",
		"I met my college friend Dave at MIT, he now runs a startup called NeuralPath",
	}
	for _, msg := range messages {
		h.addMessage(ctx, session, msg)
	}

	_ = h.nextSession()

	results, _ := h.query(ctx, "who is Alice and where does she work", 5)
	aliceFound := agentAnyContains(results, "Alice") && agentAnyContains(results, "Google")
	report.check("alice_relationship", aliceFound, "queried Alice")

	results, _ = h.query(ctx, "who is the user's boss", 5)
	bossFound := agentAnyContains(results, "Charlie")
	report.check("boss_relationship", bossFound, "queried boss")

	// Check entity extraction directly in store
	entities, err := h.store.QueryEntities(ctx, storage.EntityQuery{
		OwnerEntityID: h.entityID, Limit: 50,
	})
	entityCount := 0
	if err == nil {
		entityCount = len(entities)
	}
	report.check("entities_extracted", entityCount >= 3, fmt.Sprintf("entities: %d", entityCount))

	personCount := 0
	orgCount := 0
	for _, e := range entities {
		if e.Type == storage.EntityTypePerson {
			personCount++
		}
		if e.Type == storage.EntityTypeOrganization {
			orgCount++
		}
	}
	report.check("person_entities", personCount >= 2, fmt.Sprintf("persons: %d", personCount))
	report.check("org_entities", orgCount >= 1, fmt.Sprintf("orgs: %d", orgCount))

	rels, _ := h.store.QueryRelationships(ctx, storage.RelationshipQuery{
		OwnerEntityID: h.entityID, Limit: 50,
	})
	report.check("relationships_stored", len(rels) >= 1, fmt.Sprintf("relationships: %d", len(rels)))

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 4: Temporal/Event Recall
// =============================================================================

func (h *agentStressHarness) phaseTemporal(ctx context.Context) *phaseReport {
	report := newPhaseReport("Temporal & Event Recall")
	start := time.Now()

	session := h.nextSession()
	messages := []string{
		"I have a big presentation to the board next Monday morning at 9 AM",
		"I went to Tokyo last summer for two weeks, it was an amazing trip",
		"I'm planning to run the San Francisco marathon this October",
		"I'm going to start learning Rust next month because we're migrating some services",
	}
	for _, msg := range messages {
		h.addMessage(ctx, session, msg)
	}

	_ = h.nextSession()

	results, _ := h.query(ctx, "what upcoming events or meetings does the user have", 5)
	presentationFound := agentAnyContains(results, "presentation") || agentAnyContains(results, "board")
	report.check("presentation_event", presentationFound, "queried presentation")

	results, _ = h.query(ctx, "has the user traveled anywhere recently", 5)
	report.check("travel_event", agentAnyContains(results, "Tokyo"), "queried travel")

	results, _ = h.query(ctx, "what are the user's future plans", 5)
	planFound := agentAnyContains(results, "marathon") || agentAnyContains(results, "Rust")
	report.check("future_plans", planFound, "queried plans")

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 5: Conflict Resolution
// =============================================================================

func (h *agentStressHarness) phaseConflicts(ctx context.Context) *phaseReport {
	report := newPhaseReport("Conflict Resolution")
	start := time.Now()

	session1 := h.nextSession()
	h.addMessage(ctx, session1, "I live in San Francisco, I've been here for about 5 years now")

	session2 := h.nextSession()
	h.addMessage(ctx, session2, "I just moved to New York last week, I'm settling into my new apartment in Brooklyn")

	_ = h.nextSession()

	results, _ := h.query(ctx, "where does the user live now", 5)
	nyFound := agentAnyContains(results, "New York") || agentAnyContains(results, "Brooklyn")
	report.check("new_location_active", nyFound, "queried current location")

	archivedMems, _ := h.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID: h.entityID,
		States:   []storage.MemoryState{storage.StateArchived},
		Limit:    50,
	})
	sfArchived := false
	for _, m := range archivedMems {
		if agentContainsCaseInsensitive(m.Content, "San Francisco") {
			sfArchived = true
			break
		}
	}
	report.check("old_location_archived", sfArchived || len(archivedMems) > 0,
		fmt.Sprintf("archived: %d, sf_archived: %v", len(archivedMems), sfArchived))

	conflictEvents := h.countEvents("conflict.detected")
	report.check("conflict_events_emitted", conflictEvents > 0,
		fmt.Sprintf("conflict.detected: %d", conflictEvents))

	// Check history for superseded entries
	allMems, _ := h.store.QueryMemories(ctx, storage.MemoryQuery{
		EntityID: h.entityID, Limit: 100,
	})
	supersededCount := 0
	for _, mem := range append(allMems, archivedMems...) {
		history, _ := h.store.GetHistory(ctx, mem.ID, 10)
		for _, entry := range history {
			if entry.Operation == "superseded" {
				supersededCount++
			}
		}
	}
	report.check("superseded_history", supersededCount > 0 || conflictEvents > 0,
		fmt.Sprintf("superseded: %d", supersededCount))

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 6: Contradiction Handling
// =============================================================================

func (h *agentStressHarness) phaseContradictions(ctx context.Context) *phaseReport {
	report := newPhaseReport("Contradiction Handling")
	start := time.Now()

	session := h.nextSession()
	h.addMessage(ctx, session, "I absolutely love coffee, I drink about 4 cups every day")
	time.Sleep(1 * time.Second)
	h.addMessage(ctx, session, "Actually I stopped drinking coffee completely last month, I switched to green tea")

	_ = h.nextSession()

	results, _ := h.query(ctx, "does the user drink coffee", 5)
	teaFound := agentAnyContains(results, "tea") || agentAnyContains(results, "stopped") || agentAnyContains(results, "quit")
	report.check("contradiction_resolved", teaFound, "queried coffee status")

	report.check("conflict_detected", h.countEvents("conflict.detected") > 0,
		fmt.Sprintf("conflict events: %d", h.countEvents("conflict.detected")))

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 7: Multi-Session Continuity
// =============================================================================

func (h *agentStressHarness) phaseMultiSession(ctx context.Context) *phaseReport {
	report := newPhaseReport("Multi-Session Continuity")
	start := time.Now()

	origEntity := h.entityID
	h.entityID = "agent-stress-multisession"
	defer func() { h.entityID = origEntity }()

	s1 := h.nextSession()
	h.addMessage(ctx, s1, "My name is Jordan and I work as an architect at Foster and Partners")

	s2 := h.nextSession()
	h.addMessage(ctx, s2, "Can you help me understand how transformers work in machine learning")

	s3 := h.nextSession()
	h.addMessage(ctx, s3, "I'm working on designing a new museum in Abu Dhabi, it's a really exciting project")

	_ = h.nextSession()
	results, _ := h.query(ctx, "what is the user's name and what do they do", 5)
	report.check("session1_name_persists", agentAnyContains(results, "Jordan"), "name from session 1")
	jobFound := agentAnyContains(results, "architect") || agentAnyContains(results, "Foster")
	report.check("session1_job_persists", jobFound, "job from session 1")

	results, _ = h.query(ctx, "what project is the user working on", 5)
	projectFound := agentAnyContains(results, "museum") || agentAnyContains(results, "Abu Dhabi")
	report.check("session3_project_persists", projectFound, "project from session 3")

	s5 := h.nextSession()
	h.addMessage(ctx, s5, "I got promoted to senior architect last Friday")

	_ = h.nextSession()
	allMems, _ := h.engine.GetAll(ctx, h.entityID, 50)
	report.check("memory_accumulation", len(allMems) >= 3,
		fmt.Sprintf("total memories: %d", len(allMems)))

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 8: Importance Scoring
// =============================================================================

func (h *agentStressHarness) phaseImportance(ctx context.Context) *phaseReport {
	report := newPhaseReport("Importance Scoring")
	start := time.Now()

	origEntity := h.entityID
	h.entityID = "agent-stress-importance"
	defer func() { h.entityID = origEntity }()

	session := h.nextSession()

	// Trivial content
	h.addMessage(ctx, session, "The weather is really nice today, sunny and warm")
	h.addMessage(ctx, session, "I had a pretty normal commute this morning")

	// Critical content
	h.addMessage(ctx, session, "I'm severely allergic to peanuts, I carry an EpiPen at all times")
	h.addMessage(ctx, session, "I was diagnosed with type 1 diabetes when I was 12 years old")
	h.addMessage(ctx, session, "My emergency contact is my sister Elena, her phone number is 555-0199")

	_ = h.nextSession()

	allMems, _ := h.engine.GetAll(ctx, h.entityID, 50)
	var highImportance int
	var allergyImportance float64
	for _, m := range allMems {
		if agentContainsCaseInsensitive(m.Content, "allerg") || agentContainsCaseInsensitive(m.Content, "peanut") {
			allergyImportance = m.Importance
		}
		if m.Importance >= 0.7 {
			highImportance++
		}
	}
	report.check("allergy_high_importance", allergyImportance >= 0.6,
		fmt.Sprintf("allergy importance: %.2f", allergyImportance))
	report.check("importance_differentiation", highImportance > 0,
		fmt.Sprintf("high importance (>=0.7): %d of %d", highImportance, len(allMems)))

	identityCount := 0
	for _, m := range allMems {
		if m.Type == storage.TypeIdentity {
			identityCount++
		}
	}
	report.check("identity_typing", identityCount >= 1,
		fmt.Sprintf("IDENTITY-typed: %d", identityCount))

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 9: Deduplication
// =============================================================================

func (h *agentStressHarness) phaseDedup(ctx context.Context) *phaseReport {
	report := newPhaseReport("Deduplication")
	start := time.Now()

	origEntity := h.entityID
	h.entityID = "agent-stress-dedup"
	defer func() { h.entityID = origEntity }()

	for i := 0; i < 3; i++ {
		session := h.nextSession()
		h.addMessage(ctx, session, "My name is Bob and I'm a software engineer from Seattle")
		time.Sleep(1 * time.Second)
	}

	_ = h.nextSession()
	allMems, _ := h.engine.GetAll(ctx, h.entityID, 50)

	bobCount := 0
	for _, m := range allMems {
		if agentContainsCaseInsensitive(m.Content, "Bob") {
			bobCount++
		}
	}
	report.check("no_duplicate_identities", bobCount <= 2,
		fmt.Sprintf("Bob memories: %d (want <=2)", bobCount))
	report.check("total_memory_count_reasonable", len(allMems) <= 4,
		fmt.Sprintf("total: %d (want <=4)", len(allMems)))

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 10: Deep Knowledge Graph
// =============================================================================

func (h *agentStressHarness) phaseGraphDeep(ctx context.Context) *phaseReport {
	report := newPhaseReport("Deep Knowledge Graph")
	start := time.Now()

	entities, _ := h.store.QueryEntities(ctx, storage.EntityQuery{
		OwnerEntityID: h.entityID, Limit: 100,
	})
	report.check("entities_accumulated", len(entities) >= 3,
		fmt.Sprintf("total entities: %d", len(entities)))

	if len(entities) >= 2 {
		traversal, err := h.engine.Graph().TraverseFrom(ctx, h.entityID, GraphQuery{
			StartEntityID: entities[0].ID,
			MaxDepth:      3,
		})
		traversalOK := err == nil && traversal != nil && len(traversal.Nodes) > 0
		nodeCount := 0
		if traversal != nil {
			nodeCount = len(traversal.Nodes)
		}
		report.check("graph_traversal", traversalOK,
			fmt.Sprintf("traversal from %s: %d nodes", entities[0].CanonicalName, nodeCount))

		// Find a connected pair for ExplainConnection
		var fromE, toE *storage.Entity
		for i := 0; i < len(entities)-1 && fromE == nil; i++ {
			for j := i + 1; j < len(entities); j++ {
				path, err := h.engine.Graph().FindPath(ctx, h.entityID, entities[i].ID, entities[j].ID)
				if err == nil && len(path) >= 2 {
					fromE = entities[i]
					toE = entities[j]
					break
				}
			}
		}
		if fromE != nil && toE != nil {
			explanation, err := h.engine.Graph().ExplainConnection(ctx, h.entityID, fromE.ID, toE.ID, h.provider)
			report.check("explain_connection", err == nil && len(explanation) > 0,
				fmt.Sprintf("len=%d", len(explanation)))

			summary, err := h.engine.Graph().SummarizeEntityContext(ctx, h.entityID, fromE.ID, h.provider)
			report.check("summarize_entity", err == nil && len(summary) > 0,
				fmt.Sprintf("len=%d", len(summary)))
		} else {
			report.check("explain_connection", false, "no connected pair found")
			report.check("summarize_entity", false, "no connected pair found")
		}
	} else {
		report.check("graph_traversal", false, "insufficient entities")
		report.check("explain_connection", false, "insufficient entities")
		report.check("summarize_entity", false, "insufficient entities")
	}

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 11: Entity Resolution
// =============================================================================

func (h *agentStressHarness) phaseEntityResolution(ctx context.Context) *phaseReport {
	report := newPhaseReport("Entity Resolution")
	start := time.Now()

	origEntity := h.entityID
	h.entityID = "agent-stress-entity-res"
	defer func() { h.entityID = origEntity }()

	session := h.nextSession()
	messages := []string{
		"My friend Robert works at SpaceX as a rocket engineer",
		"I was talking to Bob yesterday about his work on the Starship project",
		"Bobby called me this morning to say he got promoted at SpaceX",
	}
	for _, msg := range messages {
		h.addMessage(ctx, session, msg)
	}

	_ = h.nextSession()

	entities, _ := h.store.QueryEntities(ctx, storage.EntityQuery{
		OwnerEntityID: h.entityID, Limit: 50,
	})

	robertVariants := 0
	for _, e := range entities {
		name := strings.ToLower(e.CanonicalName)
		if strings.Contains(name, "robert") || strings.Contains(name, "bob") {
			robertVariants++
		}
	}
	report.check("alias_resolution", robertVariants <= 2,
		fmt.Sprintf("Robert/Bob/Bobby entities: %d (want <=2)", robertVariants))

	spacexFound := false
	for _, e := range entities {
		if agentContainsCaseInsensitive(e.CanonicalName, "SpaceX") &&
			e.Type == storage.EntityTypeOrganization {
			spacexFound = true
			break
		}
	}
	report.check("org_entity_type", spacexFound, "SpaceX as organization")

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 12: Retrieval Accuracy
// =============================================================================

func (h *agentStressHarness) phaseRetrieval(ctx context.Context) *phaseReport {
	report := newPhaseReport("Retrieval Accuracy")
	start := time.Now()

	origEntity := h.entityID
	h.entityID = "agent-stress-retrieval"
	defer func() { h.entityID = origEntity }()

	session := h.nextSession()
	memories := []string{
		"I love cooking Italian food, especially homemade pasta and risotto",
		"I play tennis every Saturday morning at the local club",
		"I manage a Kubernetes cluster at work with about 200 microservices",
		"I visited Japan last year and fell in love with the culture",
		"I practice meditation every morning for 20 minutes",
		"I have a golden retriever named Max who loves hiking",
		"I invest mainly in low-cost index funds for retirement",
		"My weekend hobby is woodworking, I'm building a dining table",
		"I'm learning Spanish using Duolingo, about 6 months in now",
		"I joined a book club that meets monthly to discuss science fiction",
	}
	for _, msg := range memories {
		h.addMessage(ctx, session, msg)
	}

	_ = h.nextSession()

	// Test relevant retrieval
	results, _ := h.query(ctx, "what does the user like to cook", 5)
	report.check("relevant_retrieval", agentAnyContains(results, "Italian") || agentAnyContains(results, "pasta"),
		"cooking query")

	// Test cross-domain retrieval
	results, _ = h.query(ctx, "what does the user do for fitness and health", 5)
	fitnessFound := agentAnyContains(results, "tennis") || agentAnyContains(results, "meditation") || agentAnyContains(results, "hiking")
	report.check("cross_domain_retrieval", fitnessFound, "fitness query")

	// Test that irrelevant results don't dominate
	results, _ = h.query(ctx, "what does the user like to cook", 3)
	noIrrelevant := true
	if len(results) > 0 {
		topContent := strings.ToLower(results[0].Memory.Content)
		if strings.Contains(topContent, "kubernetes") || strings.Contains(topContent, "index fund") {
			noIrrelevant = false
		}
	}
	report.check("no_irrelevant_top", noIrrelevant, "top result not unrelated")

	results, _ = h.query(ctx, "user hobbies and interests", 10)
	report.check("result_count", len(results) >= 1,
		fmt.Sprintf("hobby query returned %d results", len(results)))

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 13: Concurrent Multi-User
// =============================================================================

func (h *agentStressHarness) phaseConcurrent(ctx context.Context) *phaseReport {
	report := newPhaseReport("Concurrent Multi-User")
	start := time.Now()

	users := []struct {
		entityID string
		messages []string
	}{
		{
			entityID: "agent-stress-alpha",
			messages: []string{
				"I'm a software engineer working on distributed systems at Netflix",
				"I specialize in Java and Go for backend microservices",
			},
		},
		{
			entityID: "agent-stress-beta",
			messages: []string{
				"I'm a high school math teacher in Chicago",
				"I teach algebra and calculus to 11th and 12th graders",
			},
		},
		{
			entityID: "agent-stress-gamma",
			messages: []string{
				"I'm a professional guitarist in a jazz band",
				"I play gigs at clubs around New Orleans every weekend",
			},
		},
	}

	var wg sync.WaitGroup
	var errCount int64
	var mu sync.Mutex

	for _, u := range users {
		wg.Add(1)
		go func(entityID string, msgs []string) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					errCount++
					mu.Unlock()
					h.t.Logf("  panic for %s: %v", entityID, r)
				}
			}()
			session := fmt.Sprintf("concurrent-%s", entityID)
			for _, msg := range msgs {
				_, err := h.engine.Add(ctx, entityID, AddRequest{
					Content:   msg,
					SessionID: session,
				})
				if err != nil {
					mu.Lock()
					errCount++
					mu.Unlock()
					h.t.Logf("  error for %s: %v", entityID, err)
				}
				time.Sleep(500 * time.Millisecond)
			}
		}(u.entityID, u.messages)
	}
	wg.Wait()

	// Verify each user has memories
	allCreated := true
	for _, u := range users {
		mems, _ := h.engine.GetAll(ctx, u.entityID, 50)
		if len(mems) == 0 {
			allCreated = false
			h.t.Logf("  %s has 0 memories", u.entityID)
		}
	}
	report.check("all_users_created", allCreated, "each user has >=1 memory")

	// Check no cross-contamination
	alphaMems, _ := h.engine.GetAll(ctx, "agent-stress-alpha", 50)
	betaMems, _ := h.engine.GetAll(ctx, "agent-stress-beta", 50)
	noCross := true
	for _, m := range alphaMems {
		if agentContainsCaseInsensitive(m.Content, "teacher") || agentContainsCaseInsensitive(m.Content, "guitar") {
			noCross = false
		}
	}
	for _, m := range betaMems {
		if agentContainsCaseInsensitive(m.Content, "Netflix") || agentContainsCaseInsensitive(m.Content, "guitar") {
			noCross = false
		}
	}
	report.check("no_cross_contamination", noCross, "no memory leakage between users")

	report.check("concurrent_no_errors", errCount == 0,
		fmt.Sprintf("errors: %d", errCount))

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 14: Memory State Lifecycle
// =============================================================================

func (h *agentStressHarness) phaseLifecycle(ctx context.Context) *phaseReport {
	report := newPhaseReport("Memory State Lifecycle")
	start := time.Now()

	origEntity := h.entityID
	h.entityID = "agent-stress-lifecycle"
	defer func() { h.entityID = origEntity }()

	session := h.nextSession()
	h.addMessage(ctx, session, "I currently use a MacBook Pro 16 inch for my daily work")

	mems, _ := h.engine.GetAll(ctx, h.entityID, 10)
	hasActive := false
	var targetMem *storage.Memory
	for _, m := range mems {
		if m.State == storage.StateActive {
			hasActive = true
			targetMem = m
			break
		}
	}
	report.check("initial_state_active", hasActive, "new memory starts active")

	if targetMem != nil {
		h.store.TransitionState(ctx, targetMem.ID, storage.StateStale, "test: simulated decay")
		updated, _ := h.engine.GetByID(ctx, targetMem.ID)
		report.check("transition_to_stale", updated != nil && updated.State == storage.StateStale, "stale transition")

		h.store.TransitionState(ctx, targetMem.ID, storage.StateArchived, "test: simulated archive")
		updated, _ = h.engine.GetByID(ctx, targetMem.ID)
		report.check("transition_to_archived", updated != nil && updated.State == storage.StateArchived, "archived transition")
	} else {
		report.check("transition_to_stale", false, "no target memory")
		report.check("transition_to_archived", false, "no target memory")
	}

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Phase 15: Full Event Audit
// =============================================================================

func (h *agentStressHarness) phaseEventAudit(ctx context.Context) *phaseReport {
	report := newPhaseReport("Event Audit")
	start := time.Now()

	h.eventMu.Lock()
	eventCounts := make(map[string]int)
	for _, e := range h.events {
		eventCounts[e.Type]++
	}
	totalEvents := len(h.events)
	h.eventMu.Unlock()

	report.check("total_events_emitted", totalEvents > 0,
		fmt.Sprintf("total: %d", totalEvents))
	report.check("memory_created_events", eventCounts["memory.created"] > 0,
		fmt.Sprintf("memory.created: %d", eventCounts["memory.created"]))

	sampleOK := true
	h.eventMu.Lock()
	for _, e := range h.events {
		if e.Type == "" || e.EntityID == "" {
			sampleOK = false
			break
		}
	}
	h.eventMu.Unlock()
	report.check("event_structure_valid", sampleOK, "all events have type+entityID")

	// Log event summary
	for eventType, count := range eventCounts {
		h.t.Logf("    event %s: %d", eventType, count)
	}

	report.Duration = time.Since(start).String()
	report.finalize()
	return report
}

// =============================================================================
// Individual Test Functions
// =============================================================================

func TestStress_AgentPhase1_Identity(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	report := h.phaseIdentity(context.Background())
	logAgentPhaseReport(t, report)
	if !report.AllPass {
		t.Errorf("identity phase failed")
	}
}

func TestStress_AgentPhase2_Preferences(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	report := h.phasePreferences(context.Background())
	logAgentPhaseReport(t, report)
	if !report.AllPass {
		t.Errorf("preferences phase failed")
	}
}

func TestStress_AgentPhase3_Relationships(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	report := h.phaseRelationships(context.Background())
	logAgentPhaseReport(t, report)
	if !report.AllPass {
		t.Errorf("relationships phase failed")
	}
}

func TestStress_AgentPhase4_Temporal(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	report := h.phaseTemporal(context.Background())
	logAgentPhaseReport(t, report)
	if !report.AllPass {
		t.Errorf("temporal phase failed")
	}
}

func TestStress_AgentPhase5_Conflicts(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	report := h.phaseConflicts(context.Background())
	logAgentPhaseReport(t, report)
	if !report.AllPass {
		t.Errorf("conflicts phase failed")
	}
}

func TestStress_AgentPhase6_Contradictions(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	report := h.phaseContradictions(context.Background())
	logAgentPhaseReport(t, report)
}

func TestStress_AgentPhase7_MultiSession(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	report := h.phaseMultiSession(context.Background())
	logAgentPhaseReport(t, report)
	if !report.AllPass {
		t.Errorf("multi-session phase failed")
	}
}

func TestStress_AgentPhase8_Importance(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	report := h.phaseImportance(context.Background())
	logAgentPhaseReport(t, report)
}

func TestStress_AgentPhase9_Dedup(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	report := h.phaseDedup(context.Background())
	logAgentPhaseReport(t, report)
	if !report.AllPass {
		t.Errorf("dedup phase failed")
	}
}

func TestStress_AgentPhase10_GraphDeep(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	ctx := context.Background()
	// Populate graph first
	h.phaseIdentity(ctx)
	h.phaseRelationships(ctx)
	report := h.phaseGraphDeep(ctx)
	logAgentPhaseReport(t, report)
}

func TestStress_AgentPhase11_EntityResolution(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	report := h.phaseEntityResolution(context.Background())
	logAgentPhaseReport(t, report)
}

func TestStress_AgentPhase12_Retrieval(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	report := h.phaseRetrieval(context.Background())
	logAgentPhaseReport(t, report)
	if !report.AllPass {
		t.Errorf("retrieval phase failed")
	}
}

func TestStress_AgentPhase13_Concurrent(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	report := h.phaseConcurrent(context.Background())
	logAgentPhaseReport(t, report)
	if !report.AllPass {
		t.Errorf("concurrent phase failed")
	}
}

func TestStress_AgentPhase14_Lifecycle(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	report := h.phaseLifecycle(context.Background())
	logAgentPhaseReport(t, report)
	if !report.AllPass {
		t.Errorf("lifecycle phase failed")
	}
}

func TestStress_AgentPhase15_EventAudit(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	ctx := context.Background()
	h.phaseIdentity(ctx)
	h.phaseConflicts(ctx)
	report := h.phaseEventAudit(ctx)
	logAgentPhaseReport(t, report)
	if !report.AllPass {
		t.Errorf("event audit phase failed")
	}
}

// =============================================================================
// Full Orchestrated Test
// =============================================================================

func TestStress_AgentFull(t *testing.T) {
	h := newAgentStressHarness(t)
	defer h.close()
	ctx := context.Background()
	fullStart := time.Now()

	fullReport := &agentStressReport{}

	t.Log("=== Agent Phase 1: Identity Recall ===")
	fullReport.Identity = h.phaseIdentity(ctx)
	logAgentPhaseReport(t, fullReport.Identity)

	t.Log("=== Agent Phase 2: Preference Recall ===")
	fullReport.Preferences = h.phasePreferences(ctx)
	logAgentPhaseReport(t, fullReport.Preferences)

	t.Log("=== Agent Phase 3: Relationship Recall ===")
	fullReport.Relationships = h.phaseRelationships(ctx)
	logAgentPhaseReport(t, fullReport.Relationships)

	t.Log("=== Agent Phase 4: Temporal Events ===")
	fullReport.Temporal = h.phaseTemporal(ctx)
	logAgentPhaseReport(t, fullReport.Temporal)

	t.Log("=== Agent Phase 5: Conflict Resolution ===")
	fullReport.Conflicts = h.phaseConflicts(ctx)
	logAgentPhaseReport(t, fullReport.Conflicts)

	t.Log("=== Agent Phase 6: Contradiction Handling ===")
	fullReport.Contradictions = h.phaseContradictions(ctx)
	logAgentPhaseReport(t, fullReport.Contradictions)

	t.Log("=== Agent Phase 7: Multi-Session Continuity ===")
	fullReport.MultiSession = h.phaseMultiSession(ctx)
	logAgentPhaseReport(t, fullReport.MultiSession)

	t.Log("=== Agent Phase 8: Importance Scoring ===")
	fullReport.Importance = h.phaseImportance(ctx)
	logAgentPhaseReport(t, fullReport.Importance)

	t.Log("=== Agent Phase 9: Deduplication ===")
	fullReport.Dedup = h.phaseDedup(ctx)
	logAgentPhaseReport(t, fullReport.Dedup)

	t.Log("=== Agent Phase 10: Deep Knowledge Graph ===")
	fullReport.GraphDeep = h.phaseGraphDeep(ctx)
	logAgentPhaseReport(t, fullReport.GraphDeep)

	t.Log("=== Agent Phase 11: Entity Resolution ===")
	fullReport.EntityResolution = h.phaseEntityResolution(ctx)
	logAgentPhaseReport(t, fullReport.EntityResolution)

	t.Log("=== Agent Phase 12: Retrieval Accuracy ===")
	fullReport.Retrieval = h.phaseRetrieval(ctx)
	logAgentPhaseReport(t, fullReport.Retrieval)

	t.Log("=== Agent Phase 13: Concurrent Multi-User ===")
	fullReport.Concurrent = h.phaseConcurrent(ctx)
	logAgentPhaseReport(t, fullReport.Concurrent)

	t.Log("=== Agent Phase 14: Memory State Lifecycle ===")
	fullReport.Lifecycle = h.phaseLifecycle(ctx)
	logAgentPhaseReport(t, fullReport.Lifecycle)

	t.Log("=== Agent Phase 15: Event Audit ===")
	fullReport.EventAudit = h.phaseEventAudit(ctx)
	logAgentPhaseReport(t, fullReport.EventAudit)

	fullReport.Duration = time.Since(fullStart).String()

	// Tally checks
	phases := []*phaseReport{
		fullReport.Identity, fullReport.Preferences, fullReport.Relationships,
		fullReport.Temporal, fullReport.Conflicts, fullReport.Contradictions,
		fullReport.MultiSession, fullReport.Importance, fullReport.Dedup,
		fullReport.GraphDeep, fullReport.EntityResolution, fullReport.Retrieval,
		fullReport.Concurrent, fullReport.Lifecycle, fullReport.EventAudit,
	}
	for _, p := range phases {
		for _, c := range p.Checks {
			fullReport.TotalChecks++
			if c.Pass {
				fullReport.PassedChecks++
			}
		}
	}

	// Verdict — only hard phases count
	failedPhases := 0
	hardPhases := []*phaseReport{
		fullReport.Identity, fullReport.Preferences, fullReport.MultiSession,
		fullReport.Retrieval, fullReport.Concurrent, fullReport.Lifecycle,
	}
	for _, p := range hardPhases {
		if !p.AllPass {
			failedPhases++
		}
	}

	switch {
	case failedPhases == 0:
		fullReport.Verdict = "PASS"
	case failedPhases <= 2:
		fullReport.Verdict = "WARN"
	default:
		fullReport.Verdict = "FAIL"
	}

	reportJSON, _ := json.MarshalIndent(fullReport, "    ", "  ")
	t.Logf("\n    === AGENT STRESS TEST REPORT ===\n    %s", string(reportJSON))
	t.Logf("\n    === VERDICT: %s (%d/%d checks passed) ===",
		fullReport.Verdict, fullReport.PassedChecks, fullReport.TotalChecks)

	if reportPath := os.Getenv("STRESS_REPORT_PATH"); reportPath != "" {
		os.WriteFile(reportPath, reportJSON, 0644)
	}

	if fullReport.Verdict == "FAIL" {
		t.Errorf("agent stress test FAILED with %d hard-phase failures", failedPhases)
	}
}

// =============================================================================
// Multi-Provider Comparison Test
// =============================================================================

type providerSpec struct {
	name     string
	provider string // "openai", "anthropic", "gemini"
	model    string
	envKey   string // env var name for API key
}

// runAgentStressForProvider runs all 15 phases and returns the report.
func runAgentStressForProvider(t *testing.T, spec providerSpec) *agentStressReport {
	t.Helper()

	apiKey := os.Getenv(spec.envKey)
	if apiKey == "" {
		t.Skipf("  skipping %s: %s not set", spec.name, spec.envKey)
		return nil
	}

	var provider llm.Provider
	var err error
	switch spec.provider {
	case "openai":
		provider, err = llm.NewOpenAIProvider(apiKey, spec.model, "")
	case "anthropic":
		provider, err = llm.NewAnthropicProvider(apiKey, spec.model, "")
	case "gemini":
		provider, err = llm.NewGeminiProvider(apiKey, spec.model)
	default:
		t.Fatalf("unknown provider: %s", spec.provider)
	}
	if err != nil {
		t.Fatalf("  failed to create %s provider: %v", spec.name, err)
	}

	h := newAgentStressHarnessWithProvider(t, provider, spec.name)
	defer h.close()
	ctx := context.Background()
	fullStart := time.Now()

	report := &agentStressReport{}

	report.Identity = h.phaseIdentity(ctx)
	report.Preferences = h.phasePreferences(ctx)
	report.Relationships = h.phaseRelationships(ctx)
	report.Temporal = h.phaseTemporal(ctx)
	report.Conflicts = h.phaseConflicts(ctx)
	report.Contradictions = h.phaseContradictions(ctx)
	report.MultiSession = h.phaseMultiSession(ctx)
	report.Importance = h.phaseImportance(ctx)
	report.Dedup = h.phaseDedup(ctx)
	report.GraphDeep = h.phaseGraphDeep(ctx)
	report.EntityResolution = h.phaseEntityResolution(ctx)
	report.Retrieval = h.phaseRetrieval(ctx)
	report.Concurrent = h.phaseConcurrent(ctx)
	report.Lifecycle = h.phaseLifecycle(ctx)
	report.EventAudit = h.phaseEventAudit(ctx)

	report.Duration = time.Since(fullStart).String()

	phases := []*phaseReport{
		report.Identity, report.Preferences, report.Relationships,
		report.Temporal, report.Conflicts, report.Contradictions,
		report.MultiSession, report.Importance, report.Dedup,
		report.GraphDeep, report.EntityResolution, report.Retrieval,
		report.Concurrent, report.Lifecycle, report.EventAudit,
	}
	for _, p := range phases {
		for _, c := range p.Checks {
			report.TotalChecks++
			if c.Pass {
				report.PassedChecks++
			}
		}
	}

	failedPhases := 0
	hardPhases := []*phaseReport{
		report.Identity, report.Preferences, report.MultiSession,
		report.Retrieval, report.Concurrent, report.Lifecycle,
	}
	for _, p := range hardPhases {
		if !p.AllPass {
			failedPhases++
		}
	}

	switch {
	case failedPhases == 0:
		report.Verdict = "PASS"
	case failedPhases <= 2:
		report.Verdict = "WARN"
	default:
		report.Verdict = "FAIL"
	}

	return report
}

// TestStress_AgentCompareProviders runs the full agent stress test with 3 different
// LLM providers and compares their results side-by-side.
//
// Models tested:
//   - OpenAI:    gpt-5-mini  ($0.10/$0.40 per 1M tokens)
//   - Anthropic: claude-haiku-4-5-20251001  ($1.00/$5.00 per 1M tokens)
//   - Gemini:    gemini-2.5-flash  (~$0.10/$0.40 per 1M tokens)
func TestStress_AgentCompareProviders(t *testing.T) {
	providers := []providerSpec{
		{name: "OpenAI gpt-5-mini", provider: "openai", model: "gpt-5-mini", envKey: "OPENAI_API_KEY"},
		{name: "Anthropic claude-haiku-4-5", provider: "anthropic", model: "claude-haiku-4-5-20251001", envKey: "ANTHROPIC_API_KEY"},
		{name: "Gemini 3.1-flash-lite", provider: "gemini", model: "gemini-2.5-flash", envKey: "GEMINI_API_KEY"},
	}

	type providerResult struct {
		Name   string
		Report *agentStressReport
	}
	var results []providerResult

	for _, spec := range providers {
		t.Run(spec.name, func(t *testing.T) {
			t.Logf("=== Testing with %s ===", spec.name)
			report := runAgentStressForProvider(t, spec)
			if report != nil {
				results = append(results, providerResult{Name: spec.name, Report: report})

				// Log per-phase results
				phases := map[string]*phaseReport{
					"Identity":      report.Identity,
					"Preferences":   report.Preferences,
					"Relationships": report.Relationships,
					"Temporal":      report.Temporal,
					"Conflicts":     report.Conflicts,
					"Contradictions": report.Contradictions,
					"MultiSession":  report.MultiSession,
					"Importance":    report.Importance,
					"Dedup":         report.Dedup,
					"GraphDeep":     report.GraphDeep,
					"EntityRes":     report.EntityResolution,
					"Retrieval":     report.Retrieval,
					"Concurrent":    report.Concurrent,
					"Lifecycle":     report.Lifecycle,
					"EventAudit":    report.EventAudit,
				}
				for phaseName, p := range phases {
					for _, c := range p.Checks {
						status := "PASS"
						if !c.Pass {
							status = "FAIL"
						}
						t.Logf("  [%s] %s/%s: %s", status, phaseName, c.Name, c.Detail)
					}
				}

				t.Logf("  VERDICT: %s (%d/%d) in %s",
					report.Verdict, report.PassedChecks, report.TotalChecks, report.Duration)
			}
		})
	}

	// Print comparison table
	if len(results) > 0 {
		t.Log("\n=== PROVIDER COMPARISON ===")
		t.Log("Provider                        | Verdict | Passed | Total | Duration")
		t.Log("-------------------------------|---------|--------|-------|--------")
		for _, r := range results {
			t.Logf("%-31s | %-7s | %3d    | %3d   | %s",
				r.Name, r.Report.Verdict, r.Report.PassedChecks, r.Report.TotalChecks, r.Report.Duration)
		}

		// Per-phase comparison
		phaseNames := []string{
			"Identity", "Preferences", "Relationships", "Temporal",
			"Conflicts", "Contradictions", "MultiSession", "Importance",
			"Dedup", "GraphDeep", "EntityRes", "Retrieval",
			"Concurrent", "Lifecycle", "EventAudit",
		}
		t.Log("\nPhase-by-Phase:")
		t.Log("Phase            |" + strings.Repeat(" Provider |", len(results)))
		for _, pn := range phaseNames {
			line := fmt.Sprintf("%-17s|", pn)
			for _, r := range results {
				var p *phaseReport
				switch pn {
				case "Identity":
					p = r.Report.Identity
				case "Preferences":
					p = r.Report.Preferences
				case "Relationships":
					p = r.Report.Relationships
				case "Temporal":
					p = r.Report.Temporal
				case "Conflicts":
					p = r.Report.Conflicts
				case "Contradictions":
					p = r.Report.Contradictions
				case "MultiSession":
					p = r.Report.MultiSession
				case "Importance":
					p = r.Report.Importance
				case "Dedup":
					p = r.Report.Dedup
				case "GraphDeep":
					p = r.Report.GraphDeep
				case "EntityRes":
					p = r.Report.EntityResolution
				case "Retrieval":
					p = r.Report.Retrieval
				case "Concurrent":
					p = r.Report.Concurrent
				case "Lifecycle":
					p = r.Report.Lifecycle
				case "EventAudit":
					p = r.Report.EventAudit
				}
				passed := 0
				total := 0
				if p != nil {
					total = len(p.Checks)
					for _, c := range p.Checks {
						if c.Pass {
							passed++
						}
					}
				}
				status := "PASS"
				if p != nil && !p.AllPass {
					status = "FAIL"
				}
				line += fmt.Sprintf(" %s %d/%d |", status, passed, total)
			}
			t.Log(line)
		}

		// Save comparison report
		if reportPath := os.Getenv("STRESS_REPORT_PATH"); reportPath != "" {
			comparison := make(map[string]*agentStressReport)
			for _, r := range results {
				comparison[r.Name] = r.Report
			}
			reportJSON, _ := json.MarshalIndent(comparison, "", "  ")
			os.WriteFile(reportPath, reportJSON, 0644)
			t.Logf("\nComparison report saved to: %s", reportPath)
		}
	}
}

// =============================================================================
// Full Stack Comparison Test (OpenAI/Gemini × Rerank On/Off)
// =============================================================================

// stackSpec defines a full stack configuration: LLM provider + embedder + rerank flag.
type stackSpec struct {
	name           string
	llmProvider    string // "openai", "gemini"
	llmModel       string
	llmEnvKey      string
	embProvider    string // "openai", "gemini"
	embModel       string
	embEnvKey      string
	enableRerank   bool
}

// runAgentStressForStack runs all 15 phases with a specific stack config.
func runAgentStressForStack(t *testing.T, spec stackSpec) *agentStressReport {
	t.Helper()

	// Create LLM provider
	llmKey := os.Getenv(spec.llmEnvKey)
	if llmKey == "" {
		t.Skipf("  skipping %s: %s not set", spec.name, spec.llmEnvKey)
		return nil
	}

	var provider llm.Provider
	var err error
	switch spec.llmProvider {
	case "openai":
		provider, err = llm.NewOpenAIProvider(llmKey, spec.llmModel, "")
	case "gemini":
		provider, err = llm.NewGeminiProvider(llmKey, spec.llmModel)
	default:
		t.Fatalf("unknown LLM provider: %s", spec.llmProvider)
	}
	if err != nil {
		t.Fatalf("  failed to create LLM provider %s: %v", spec.name, err)
	}

	// Create embedder
	embKey := os.Getenv(spec.embEnvKey)
	if embKey == "" {
		t.Skipf("  skipping %s: %s not set for embeddings", spec.name, spec.embEnvKey)
		return nil
	}

	var emb embedder.Embedder
	switch spec.embProvider {
	case "openai":
		emb = embedder.NewOpenAI(embKey, spec.embModel)
	case "gemini":
		emb, err = embedder.NewGemini(embKey, spec.embModel, 0)
		if err != nil {
			t.Fatalf("  failed to create Gemini embedder: %v", err)
		}
	default:
		t.Fatalf("unknown embedder provider: %s", spec.embProvider)
	}

	h := newAgentStressHarnessWithStack(t, provider, spec.name, emb, spec.enableRerank)
	defer h.close()
	ctx := context.Background()
	fullStart := time.Now()

	report := &agentStressReport{}

	report.Identity = h.phaseIdentity(ctx)
	report.Preferences = h.phasePreferences(ctx)
	report.Relationships = h.phaseRelationships(ctx)
	report.Temporal = h.phaseTemporal(ctx)
	report.Conflicts = h.phaseConflicts(ctx)
	report.Contradictions = h.phaseContradictions(ctx)
	report.MultiSession = h.phaseMultiSession(ctx)
	report.Importance = h.phaseImportance(ctx)
	report.Dedup = h.phaseDedup(ctx)
	report.GraphDeep = h.phaseGraphDeep(ctx)
	report.EntityResolution = h.phaseEntityResolution(ctx)
	report.Retrieval = h.phaseRetrieval(ctx)
	report.Concurrent = h.phaseConcurrent(ctx)
	report.Lifecycle = h.phaseLifecycle(ctx)
	report.EventAudit = h.phaseEventAudit(ctx)

	report.Duration = time.Since(fullStart).String()

	phases := []*phaseReport{
		report.Identity, report.Preferences, report.Relationships,
		report.Temporal, report.Conflicts, report.Contradictions,
		report.MultiSession, report.Importance, report.Dedup,
		report.GraphDeep, report.EntityResolution, report.Retrieval,
		report.Concurrent, report.Lifecycle, report.EventAudit,
	}
	for _, p := range phases {
		for _, c := range p.Checks {
			report.TotalChecks++
			if c.Pass {
				report.PassedChecks++
			}
		}
	}

	failedPhases := 0
	hardPhases := []*phaseReport{
		report.Identity, report.Preferences, report.MultiSession,
		report.Retrieval, report.Concurrent, report.Lifecycle,
	}
	for _, p := range hardPhases {
		if !p.AllPass {
			failedPhases++
		}
	}

	switch {
	case failedPhases == 0:
		report.Verdict = "PASS"
	case failedPhases <= 2:
		report.Verdict = "WARN"
	default:
		report.Verdict = "FAIL"
	}

	return report
}

// TestStress_AgentCompareStacks runs the full agent stress test with 4 stack configurations:
//   - OpenAI LLM + OpenAI Embedder (rerank off)
//   - OpenAI LLM + OpenAI Embedder (rerank on)
//   - Gemini LLM + Gemini Embedder (rerank off)
//   - Gemini LLM + Gemini Embedder (rerank on)
func TestStress_AgentCompareStacks(t *testing.T) {
	stacks := []stackSpec{
		{
			name: "OpenAI+OpenAI/rerank-off", llmProvider: "openai", llmModel: "gpt-5-mini",
			llmEnvKey: "OPENAI_API_KEY", embProvider: "openai", embModel: "text-embedding-3-small",
			embEnvKey: "OPENAI_API_KEY", enableRerank: false,
		},
		{
			name: "OpenAI+OpenAI/rerank-on", llmProvider: "openai", llmModel: "gpt-5-mini",
			llmEnvKey: "OPENAI_API_KEY", embProvider: "openai", embModel: "text-embedding-3-small",
			embEnvKey: "OPENAI_API_KEY", enableRerank: true,
		},
		{
			name: "Gemini+Gemini/rerank-off", llmProvider: "gemini", llmModel: "gemini-2.5-flash",
			llmEnvKey: "GEMINI_API_KEY", embProvider: "gemini", embModel: "gemini-embedding-001",
			embEnvKey: "GEMINI_API_KEY", enableRerank: false,
		},
		{
			name: "Gemini+Gemini/rerank-on", llmProvider: "gemini", llmModel: "gemini-2.5-flash",
			llmEnvKey: "GEMINI_API_KEY", embProvider: "gemini", embModel: "gemini-embedding-001",
			embEnvKey: "GEMINI_API_KEY", enableRerank: true,
		},
	}

	type stackResult struct {
		Name   string
		Report *agentStressReport
	}
	var results []stackResult

	for _, spec := range stacks {
		t.Run(spec.name, func(t *testing.T) {
			t.Logf("=== Testing stack: %s ===", spec.name)
			report := runAgentStressForStack(t, spec)
			if report != nil {
				results = append(results, stackResult{Name: spec.name, Report: report})
				t.Logf("  VERDICT: %s (%d/%d) in %s",
					report.Verdict, report.PassedChecks, report.TotalChecks, report.Duration)
			}
		})
	}

	// Print comparison table
	if len(results) > 0 {
		t.Log("\n=== STACK COMPARISON (LLM+Embedder/Rerank) ===")
		t.Log("Stack                              | Verdict | Passed | Total | Duration")
		t.Log("-----------------------------------|---------|--------|-------|--------")
		for _, r := range results {
			t.Logf("%-35s | %-7s | %3d    | %3d   | %s",
				r.Name, r.Report.Verdict, r.Report.PassedChecks, r.Report.TotalChecks, r.Report.Duration)
		}

		// Save comparison report
		if reportPath := os.Getenv("STRESS_REPORT_PATH"); reportPath != "" {
			comparison := make(map[string]*agentStressReport)
			for _, r := range results {
				comparison[r.Name] = r.Report
			}
			reportJSON, _ := json.MarshalIndent(comparison, "", "  ")
			os.WriteFile(reportPath, reportJSON, 0644)
			t.Logf("\nStack comparison report saved to: %s", reportPath)
		}
	}
}

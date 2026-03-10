// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package keyoku

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// newTestKeyokuWithLogger creates a Keyoku with mock store, noop embedder, and logger.
func newTestKeyokuWithLogger(store storage.Store) *Keyoku {
	k := newTestKeyoku(store)
	k.logger = slog.Default()
	return k
}

// --- CreateSchedule ---

func TestCreateSchedule_HappyPath(t *testing.T) {
	var created *storage.Memory
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil // no duplicates
		},
		createMemoryFn: func(_ context.Context, mem *storage.Memory) error {
			created = mem
			return nil
		},
	}

	k := newTestKeyokuWithLogger(store)
	mem, err := k.CreateSchedule(context.Background(), "entity-1", "agent-1", "Check news every morning", "cron:daily:08:00")
	if err != nil {
		t.Fatalf("CreateSchedule error = %v", err)
	}
	if mem == nil {
		t.Fatal("expected non-nil memory")
	}

	// Verify the memory was created with correct fields
	if created == nil {
		t.Fatal("store.CreateMemory was not called")
	}
	if created.EntityID != "entity-1" {
		t.Errorf("EntityID = %q, want %q", created.EntityID, "entity-1")
	}
	if created.AgentID != "agent-1" {
		t.Errorf("AgentID = %q, want %q", created.AgentID, "agent-1")
	}
	if created.Content != "Check news every morning" {
		t.Errorf("Content = %q, want %q", created.Content, "Check news every morning")
	}
	if created.State != storage.StateActive {
		t.Errorf("State = %q, want %q", created.State, storage.StateActive)
	}
	if created.Importance != 0.9 {
		t.Errorf("Importance = %f, want 0.9", created.Importance)
	}
	if created.Stability != 365 {
		t.Errorf("Stability = %f, want 365", created.Stability)
	}
	if created.Source != "schedule_api" {
		t.Errorf("Source = %q, want %q", created.Source, "schedule_api")
	}

	// Verify cron tag is present
	hasCron := false
	for _, tag := range created.Tags {
		if tag == "cron:daily:08:00" {
			hasCron = true
		}
	}
	if !hasCron {
		t.Errorf("Tags = %v, expected cron:daily:08:00", created.Tags)
	}

	// Verify embedding was generated (noop embedder returns zero vector)
	if len(created.Embedding) == 0 {
		t.Error("expected non-empty embedding")
	}

	// Verify hash was set
	if created.Hash == "" {
		t.Error("expected non-empty hash")
	}
}

func TestCreateSchedule_InvalidCronTag(t *testing.T) {
	store := &testStore{}
	k := newTestKeyokuWithLogger(store)

	_, err := k.CreateSchedule(context.Background(), "entity-1", "", "Do something", "not-a-cron-tag")
	if err == nil {
		t.Fatal("expected error for invalid cron tag")
	}
	if !strings.Contains(err.Error(), "invalid cron tag") {
		t.Errorf("error = %q, expected to contain 'invalid cron tag'", err.Error())
	}
}

func TestCreateSchedule_DuplicateDetection(t *testing.T) {
	// Existing schedule with matching content should be archived
	var archivedIDs []string
	var historyOps []string
	var createdNew bool

	existingMem := &storage.Memory{
		ID:       "old-sched-1",
		EntityID: "entity-1",
		AgentID:  "agent-1",
		Content:  "Check news every morning",
		Tags:     storage.StringSlice{"cron:daily:09:00"},
		State:    storage.StateActive,
	}

	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if q.TagPrefix == "cron:" {
				return []*storage.Memory{existingMem}, nil
			}
			return nil, nil
		},
		updateMemoryFn: func(_ context.Context, id string, updates storage.MemoryUpdate) (*storage.Memory, error) {
			if updates.State != nil && *updates.State == storage.StateArchived {
				archivedIDs = append(archivedIDs, id)
			}
			return &storage.Memory{ID: id}, nil
		},
		createMemoryFn: func(_ context.Context, _ *storage.Memory) error {
			createdNew = true
			return nil
		},
		logHistoryFn: func(_ context.Context, entry *storage.HistoryEntry) error {
			historyOps = append(historyOps, entry.Operation)
			return nil
		},
	}

	k := newTestKeyokuWithLogger(store)
	_, err := k.CreateSchedule(context.Background(), "entity-1", "agent-1", "Check news every morning", "cron:daily:08:00")
	if err != nil {
		t.Fatalf("CreateSchedule error = %v", err)
	}

	// Old schedule should have been archived
	if len(archivedIDs) != 1 || archivedIDs[0] != "old-sched-1" {
		t.Errorf("archivedIDs = %v, want [old-sched-1]", archivedIDs)
	}

	// New schedule should have been created
	if !createdNew {
		t.Error("expected new schedule to be created")
	}

	// History should include both replacement and creation
	foundReplaced := false
	foundCreated := false
	for _, op := range historyOps {
		if op == "schedule_replaced" {
			foundReplaced = true
		}
		if op == "schedule_created" {
			foundCreated = true
		}
	}
	if !foundReplaced {
		t.Error("expected schedule_replaced history entry")
	}
	if !foundCreated {
		t.Error("expected schedule_created history entry")
	}
}

func TestCreateSchedule_NoDuplicateForDifferentContent(t *testing.T) {
	// Existing schedule with DIFFERENT content should NOT be archived
	var archivedIDs []string

	existingMem := &storage.Memory{
		ID:       "old-sched-1",
		EntityID: "entity-1",
		Content:  "Send weekly report",
		Tags:     storage.StringSlice{"cron:weekly:monday:09:00"},
		State:    storage.StateActive,
	}

	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return []*storage.Memory{existingMem}, nil
		},
		updateMemoryFn: func(_ context.Context, id string, updates storage.MemoryUpdate) (*storage.Memory, error) {
			if updates.State != nil && *updates.State == storage.StateArchived {
				archivedIDs = append(archivedIDs, id)
			}
			return &storage.Memory{ID: id}, nil
		},
		createMemoryFn: func(_ context.Context, _ *storage.Memory) error {
			return nil
		},
	}

	k := newTestKeyokuWithLogger(store)
	_, err := k.CreateSchedule(context.Background(), "entity-1", "", "Check news every morning", "cron:daily:08:00")
	if err != nil {
		t.Fatalf("CreateSchedule error = %v", err)
	}

	// Different content — nothing should be archived
	if len(archivedIDs) != 0 {
		t.Errorf("archivedIDs = %v, expected none (different content)", archivedIDs)
	}
}

func TestCreateSchedule_StoreError(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil
		},
		createMemoryFn: func(_ context.Context, _ *storage.Memory) error {
			return fmt.Errorf("db write error")
		},
	}

	k := newTestKeyokuWithLogger(store)
	_, err := k.CreateSchedule(context.Background(), "entity-1", "", "Task", "cron:daily:08:00")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to create schedule memory") {
		t.Errorf("error = %q, expected to contain 'failed to create schedule memory'", err.Error())
	}
}

func TestCreateSchedule_VariousCronFormats(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil
		},
		createMemoryFn: func(_ context.Context, _ *storage.Memory) error {
			return nil
		},
	}

	k := newTestKeyokuWithLogger(store)

	validTags := []string{
		"cron:daily:08:00",
		"cron:weekly:monday:09:00",
		"cron:every:48h",
		"cron:once:2026-03-01T08:00:00",
	}

	for _, tag := range validTags {
		t.Run(tag, func(t *testing.T) {
			_, err := k.CreateSchedule(context.Background(), "entity-1", "", "Task", tag)
			if err != nil {
				t.Errorf("CreateSchedule(%q) error = %v", tag, err)
			}
		})
	}
}

// --- UpdateSchedule ---

func TestUpdateSchedule_HappyPath(t *testing.T) {
	var updatedTags []string
	var updatedState *storage.MemoryState

	store := &testStore{
		getMemoryFn: func(_ context.Context, id string) (*storage.Memory, error) {
			return &storage.Memory{
				ID:      id,
				Content: "Check news",
				Tags:    storage.StringSlice{"cron:daily:08:00", "important"},
				State:   storage.StateActive,
			}, nil
		},
		updateMemoryFn: func(_ context.Context, _ string, updates storage.MemoryUpdate) (*storage.Memory, error) {
			if updates.Tags != nil {
				updatedTags = *updates.Tags
			}
			if updates.State != nil {
				updatedState = updates.State
			}
			return &storage.Memory{ID: "sched-1", Tags: storage.StringSlice(*updates.Tags)}, nil
		},
	}

	k := newTestKeyokuWithLogger(store)
	mem, err := k.UpdateSchedule(context.Background(), "sched-1", "cron:weekly:monday:09:00", nil)
	if err != nil {
		t.Fatalf("UpdateSchedule error = %v", err)
	}
	if mem == nil {
		t.Fatal("expected non-nil memory")
	}

	// Old cron tag should be replaced, non-cron tags preserved
	hasCronWeekly := false
	hasCronDaily := false
	hasImportant := false
	for _, tag := range updatedTags {
		if tag == "cron:weekly:monday:09:00" {
			hasCronWeekly = true
		}
		if tag == "cron:daily:08:00" {
			hasCronDaily = true
		}
		if tag == "important" {
			hasImportant = true
		}
	}
	if !hasCronWeekly {
		t.Error("expected new cron tag cron:weekly:monday:09:00")
	}
	if hasCronDaily {
		t.Error("old cron tag cron:daily:08:00 should have been removed")
	}
	if !hasImportant {
		t.Error("non-cron tag 'important' should have been preserved")
	}

	// Should ensure active state
	if updatedState == nil || *updatedState != storage.StateActive {
		t.Error("expected state to be set to active")
	}
}

func TestUpdateSchedule_WithNewContent(t *testing.T) {
	var updatedContent *string

	store := &testStore{
		getMemoryFn: func(_ context.Context, id string) (*storage.Memory, error) {
			return &storage.Memory{
				ID:      id,
				Content: "Old content",
				Tags:    storage.StringSlice{"cron:daily:08:00"},
				State:   storage.StateActive,
			}, nil
		},
		updateMemoryFn: func(_ context.Context, _ string, updates storage.MemoryUpdate) (*storage.Memory, error) {
			updatedContent = updates.Content
			return &storage.Memory{ID: "sched-1"}, nil
		},
	}

	k := newTestKeyokuWithLogger(store)
	newContent := "New content for the schedule"
	_, err := k.UpdateSchedule(context.Background(), "sched-1", "cron:daily:09:00", &newContent)
	if err != nil {
		t.Fatalf("UpdateSchedule error = %v", err)
	}

	if updatedContent == nil || *updatedContent != "New content for the schedule" {
		t.Errorf("content = %v, want 'New content for the schedule'", updatedContent)
	}
}

func TestUpdateSchedule_NonCronMemoryRejected(t *testing.T) {
	store := &testStore{
		getMemoryFn: func(_ context.Context, id string) (*storage.Memory, error) {
			return &storage.Memory{
				ID:      id,
				Content: "Just a normal memory",
				Tags:    storage.StringSlice{"important"},
				State:   storage.StateActive,
			}, nil
		},
	}

	k := newTestKeyokuWithLogger(store)
	_, err := k.UpdateSchedule(context.Background(), "mem-1", "cron:daily:08:00", nil)
	if err == nil {
		t.Fatal("expected error for non-cron memory")
	}
	if !strings.Contains(err.Error(), "not a scheduled memory") {
		t.Errorf("error = %q, expected to contain 'not a scheduled memory'", err.Error())
	}
}

func TestUpdateSchedule_InvalidCronTag(t *testing.T) {
	store := &testStore{}
	k := newTestKeyokuWithLogger(store)

	_, err := k.UpdateSchedule(context.Background(), "sched-1", "invalid-tag", nil)
	if err == nil {
		t.Fatal("expected error for invalid cron tag")
	}
	if !strings.Contains(err.Error(), "invalid cron tag") {
		t.Errorf("error = %q, expected to contain 'invalid cron tag'", err.Error())
	}
}

func TestUpdateSchedule_MemoryNotFound(t *testing.T) {
	store := &testStore{
		getMemoryFn: func(_ context.Context, _ string) (*storage.Memory, error) {
			return nil, fmt.Errorf("not found")
		},
	}

	k := newTestKeyokuWithLogger(store)
	_, err := k.UpdateSchedule(context.Background(), "nonexistent", "cron:daily:08:00", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent memory")
	}
	if !strings.Contains(err.Error(), "schedule not found") {
		t.Errorf("error = %q, expected to contain 'schedule not found'", err.Error())
	}
}

func TestUpdateSchedule_RecoversStaleState(t *testing.T) {
	var updatedState *storage.MemoryState

	store := &testStore{
		getMemoryFn: func(_ context.Context, id string) (*storage.Memory, error) {
			return &storage.Memory{
				ID:    id,
				Tags:  storage.StringSlice{"cron:daily:08:00"},
				State: storage.StateStale, // Was decayed
			}, nil
		},
		updateMemoryFn: func(_ context.Context, _ string, updates storage.MemoryUpdate) (*storage.Memory, error) {
			updatedState = updates.State
			return &storage.Memory{ID: "sched-1"}, nil
		},
	}

	k := newTestKeyokuWithLogger(store)
	_, err := k.UpdateSchedule(context.Background(), "sched-1", "cron:weekly:monday:09:00", nil)
	if err != nil {
		t.Fatalf("UpdateSchedule error = %v", err)
	}

	// Should force state back to active
	if updatedState == nil || *updatedState != storage.StateActive {
		t.Error("expected state to be forced to active (recovery from stale)")
	}
}

// --- CancelSchedule ---

func TestCancelSchedule_HappyPath(t *testing.T) {
	var updatedID string
	var updatedState *storage.MemoryState

	store := &testStore{
		getMemoryFn: func(_ context.Context, id string) (*storage.Memory, error) {
			return &storage.Memory{
				ID:      id,
				Content: "Check news daily",
				Tags:    storage.StringSlice{"cron:daily:08:00"},
				State:   storage.StateActive,
			}, nil
		},
		updateMemoryFn: func(_ context.Context, id string, updates storage.MemoryUpdate) (*storage.Memory, error) {
			updatedID = id
			updatedState = updates.State
			return &storage.Memory{ID: id}, nil
		},
	}

	k := newTestKeyokuWithLogger(store)
	err := k.CancelSchedule(context.Background(), "sched-1")
	if err != nil {
		t.Fatalf("CancelSchedule error = %v", err)
	}

	if updatedID != "sched-1" {
		t.Errorf("updated ID = %q, want %q", updatedID, "sched-1")
	}
	if updatedState == nil || *updatedState != storage.StateArchived {
		t.Error("expected state to be archived")
	}
}

func TestCancelSchedule_NonCronMemoryRejected(t *testing.T) {
	store := &testStore{
		getMemoryFn: func(_ context.Context, id string) (*storage.Memory, error) {
			return &storage.Memory{
				ID:    id,
				Tags:  storage.StringSlice{"important"},
				State: storage.StateActive,
			}, nil
		},
	}

	k := newTestKeyokuWithLogger(store)
	err := k.CancelSchedule(context.Background(), "mem-1")
	if err == nil {
		t.Fatal("expected error for non-cron memory")
	}
	if !strings.Contains(err.Error(), "not a scheduled memory") {
		t.Errorf("error = %q, expected to contain 'not a scheduled memory'", err.Error())
	}
}

func TestCancelSchedule_MemoryNotFound(t *testing.T) {
	store := &testStore{
		getMemoryFn: func(_ context.Context, _ string) (*storage.Memory, error) {
			return nil, fmt.Errorf("not found")
		},
	}

	k := newTestKeyokuWithLogger(store)
	err := k.CancelSchedule(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent memory")
	}
	if !strings.Contains(err.Error(), "schedule not found") {
		t.Errorf("error = %q, expected to contain 'schedule not found'", err.Error())
	}
}

func TestCancelSchedule_StoreError(t *testing.T) {
	store := &testStore{
		getMemoryFn: func(_ context.Context, id string) (*storage.Memory, error) {
			return &storage.Memory{
				ID:   id,
				Tags: storage.StringSlice{"cron:daily:08:00"},
			}, nil
		},
		updateMemoryFn: func(_ context.Context, _ string, _ storage.MemoryUpdate) (*storage.Memory, error) {
			return nil, fmt.Errorf("db error")
		},
	}

	k := newTestKeyokuWithLogger(store)
	err := k.CancelSchedule(context.Background(), "sched-1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to cancel schedule") {
		t.Errorf("error = %q, expected to contain 'failed to cancel schedule'", err.Error())
	}
}

// --- ListScheduled ---

func TestListScheduled_ActiveMemories(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			if len(q.States) == 1 && q.States[0] == storage.StateActive {
				return []*storage.Memory{
					{ID: "sched-1", Content: "Morning news", Tags: storage.StringSlice{"cron:daily:08:00"}},
					{ID: "sched-2", Content: "Weekly report", Tags: storage.StringSlice{"cron:weekly:monday:09:00"}},
				}, nil
			}
			// Stale query returns nothing
			return nil, nil
		},
	}

	k := newTestKeyokuWithLogger(store)
	mems, err := k.ListScheduled(context.Background(), "entity-1", "agent-1")
	if err != nil {
		t.Fatalf("ListScheduled error = %v", err)
	}
	if len(mems) != 2 {
		t.Errorf("ListScheduled returned %d, want 2", len(mems))
	}
}

func TestListScheduled_DefenseInDepth_RecoverStale(t *testing.T) {
	var recoveredIDs []string
	queryCount := 0

	store := &testStore{
		queryMemoriesFn: func(_ context.Context, q storage.MemoryQuery) ([]*storage.Memory, error) {
			queryCount++
			if len(q.States) == 1 && q.States[0] == storage.StateActive {
				// Active query: 1 active schedule
				return []*storage.Memory{
					{ID: "sched-1", Content: "Active task", Tags: storage.StringSlice{"cron:daily:08:00"}},
				}, nil
			}
			if len(q.States) == 1 && q.States[0] == storage.StateStale {
				// Stale query: 1 accidentally decayed cron memory
				return []*storage.Memory{
					{ID: "sched-stale", Content: "Stale task", Tags: storage.StringSlice{"cron:weekly:friday:17:00"}, State: storage.StateStale},
				}, nil
			}
			return nil, nil
		},
		updateMemoryFn: func(_ context.Context, id string, updates storage.MemoryUpdate) (*storage.Memory, error) {
			if updates.State != nil && *updates.State == storage.StateActive {
				recoveredIDs = append(recoveredIDs, id)
			}
			return &storage.Memory{ID: id}, nil
		},
	}

	k := newTestKeyokuWithLogger(store)
	mems, err := k.ListScheduled(context.Background(), "entity-1", "agent-1")
	if err != nil {
		t.Fatalf("ListScheduled error = %v", err)
	}

	// Should return both active + recovered stale
	if len(mems) != 2 {
		t.Errorf("ListScheduled returned %d, want 2 (1 active + 1 recovered)", len(mems))
	}

	// Stale memory should have been recovered to active
	if len(recoveredIDs) != 1 || recoveredIDs[0] != "sched-stale" {
		t.Errorf("recoveredIDs = %v, want [sched-stale]", recoveredIDs)
	}

	// Both queries should have been made (active + stale)
	if queryCount != 2 {
		t.Errorf("queryCount = %d, want 2 (active + stale)", queryCount)
	}
}

func TestListScheduled_Empty(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, nil
		},
	}

	k := newTestKeyokuWithLogger(store)
	mems, err := k.ListScheduled(context.Background(), "entity-1", "")
	if err != nil {
		t.Fatalf("ListScheduled error = %v", err)
	}
	if len(mems) != 0 {
		t.Errorf("ListScheduled returned %d, want 0", len(mems))
	}
}

func TestListScheduled_StoreError(t *testing.T) {
	store := &testStore{
		queryMemoriesFn: func(_ context.Context, _ storage.MemoryQuery) ([]*storage.Memory, error) {
			return nil, fmt.Errorf("db error")
		},
	}

	k := newTestKeyokuWithLogger(store)
	_, err := k.ListScheduled(context.Background(), "entity-1", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- AcknowledgeSchedule ---

func TestAcknowledgeSchedule_HappyPath(t *testing.T) {
	var ackedIDs []string
	store := &testStore{
		updateAccessStatsFn: func(_ context.Context, ids []string) error {
			ackedIDs = append(ackedIDs, ids...)
			return nil
		},
	}

	k := newTestKeyokuWithLogger(store)
	err := k.AcknowledgeSchedule(context.Background(), "sched-1")
	if err != nil {
		t.Fatalf("AcknowledgeSchedule error = %v", err)
	}
	if len(ackedIDs) != 1 || ackedIDs[0] != "sched-1" {
		t.Errorf("ackedIDs = %v, want [sched-1]", ackedIDs)
	}
}

// --- Helper tests ---

func TestIsScheduleContentMatch(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"Check news every morning", "Check news every morning", true},
		{"check news every morning", "Check News Every Morning", true},              // case insensitive
		{"  Check news  ", "Check news", true},                                      // whitespace trimmed
		{"Check news every morning", "Check weather forecast", false},               // different content
		{"Check news", "Check news every morning at 8am", true},                     // substring containment
		{"Send the daily report to the team", "Send the daily report", true},        // substring
		{"Totally different task A", "Completely unrelated task B", false},           // no match
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q_vs_%q", tt.a, tt.b), func(t *testing.T) {
			got := isScheduleContentMatch(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("isScheduleContentMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestScheduleContentHash(t *testing.T) {
	h1 := scheduleContentHash("hello")
	h2 := scheduleContentHash("hello")
	h3 := scheduleContentHash("world")

	if h1 != h2 {
		t.Error("same content should produce same hash")
	}
	if h1 == h3 {
		t.Error("different content should produce different hash")
	}
	if len(h1) != 64 { // SHA-256 hex = 64 chars
		t.Errorf("hash length = %d, want 64", len(h1))
	}
}

func TestEncodeEmbeddingBytes(t *testing.T) {
	// Empty input
	if got := encodeEmbeddingBytes(nil); got != nil {
		t.Errorf("nil input should return nil, got %v", got)
	}
	if got := encodeEmbeddingBytes([]float32{}); got != nil {
		t.Errorf("empty input should return nil, got %v", got)
	}

	// Non-empty input: each float32 = 4 bytes
	embedding := []float32{1.0, 2.0, 3.0}
	encoded := encodeEmbeddingBytes(embedding)
	if len(encoded) != 12 { // 3 * 4 bytes
		t.Errorf("encoded length = %d, want 12", len(encoded))
	}
}

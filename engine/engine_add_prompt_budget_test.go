// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package engine

import (
	"strings"
	"testing"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

func TestBuildConversationContext_Unlimited(t *testing.T) {
	msgs := []*storage.SessionMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	out, total := buildConversationContext(msgs, 0)
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if total == 0 {
		t.Fatalf("total bytes = 0, want > 0")
	}
	if out[0] != "[user]: hello" {
		t.Fatalf("out[0] = %q", out[0])
	}
}

func TestBuildConversationContext_TrimsByBudget(t *testing.T) {
	// Each formatted line is "[user]: msgN" ~= 13 bytes + 1 newline accounting
	msgs := []*storage.SessionMessage{
		{Role: "user", Content: "msg1"},
		{Role: "user", Content: "msg2"},
		{Role: "user", Content: "msg3"},
		{Role: "user", Content: "msg4"},
	}
	out, total := buildConversationContext(msgs, 30)
	// 30 bytes budget should admit roughly 2 entries (~14 bytes each with newline)
	if len(out) >= len(msgs) {
		t.Fatalf("expected trimming, got all %d entries", len(out))
	}
	if total > 30 {
		t.Fatalf("total %d exceeds budget 30", total)
	}
}

func TestBuildExistingMemoriesBlock_TrimsPerMemoryContent(t *testing.T) {
	longContent := strings.Repeat("x", 500)
	sims := []*storage.SimilarityResult{
		{Memory: &storage.Memory{Type: storage.TypeEvent, State: "active", Importance: 0.8, Content: longContent}},
	}
	out, _ := buildExistingMemoriesBlock(sims, 0, 100)
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	// 100 trim + "..." = 103-char content. Line prefix adds more, but content portion must be bounded.
	if !strings.Contains(out[0], strings.Repeat("x", 100)+"...") {
		t.Fatalf("expected per-memory trim to 100 chars with ellipsis, got: %q", out[0])
	}
	if strings.Count(out[0], "x") > 101 {
		t.Fatalf("trim leaked: too many x in output: %d", strings.Count(out[0], "x"))
	}
}

func TestBuildExistingMemoriesBlock_TrimsByByteBudget(t *testing.T) {
	sims := []*storage.SimilarityResult{
		{Memory: &storage.Memory{Type: storage.TypeEvent, State: "active", Importance: 0.8, Content: "one"}},
		{Memory: &storage.Memory{Type: storage.TypeEvent, State: "active", Importance: 0.8, Content: "two"}},
		{Memory: &storage.Memory{Type: storage.TypeEvent, State: "active", Importance: 0.8, Content: "three"}},
	}
	// Each formatted line is ~"[event, active, importance:0.8] xxx" ~= 35 bytes + 1.
	// Budget 40 should allow one, not more.
	out, total := buildExistingMemoriesBlock(sims, 40, 0)
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1 under tight budget", len(out))
	}
	if total > 40 {
		t.Fatalf("total %d exceeds budget 40", total)
	}
}

func TestBuildExistingMemoriesBlock_NilSafe(t *testing.T) {
	sims := []*storage.SimilarityResult{
		nil,
		{Memory: nil},
		{Memory: &storage.Memory{Type: storage.TypeEvent, State: "active", Importance: 0.5, Content: "ok"}},
	}
	out, _ := buildExistingMemoriesBlock(sims, 0, 0)
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1 (nil entries skipped)", len(out))
	}
}

func TestBuildExistingMemoriesBlock_PreservesMostSimilarFirst(t *testing.T) {
	// Input order mirrors store ordering (highest similarity first). We must
	// keep that order so tight budgets retain the most relevant neighbors.
	sims := []*storage.SimilarityResult{
		{Memory: &storage.Memory{Type: storage.TypeEvent, State: "active", Importance: 0.9, Content: "most_similar"}},
		{Memory: &storage.Memory{Type: storage.TypeEvent, State: "active", Importance: 0.5, Content: "least_similar"}},
	}
	out, _ := buildExistingMemoriesBlock(sims, 50, 0)
	if len(out) < 1 {
		t.Fatalf("expected at least 1 entry")
	}
	if !strings.Contains(out[0], "most_similar") {
		t.Fatalf("first entry should be most_similar, got: %q", out[0])
	}
}

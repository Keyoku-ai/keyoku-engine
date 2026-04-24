// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package engine

import (
	"fmt"

	"github.com/keyoku-ai/keyoku-engine/storage"
)

// Prompt-budget helpers for the Add() / ExtractMemories path.
//
// Rationale (issue #41): repeated same-entity writes returned steadily more
// 0.5+ similarity neighbors, which inflated the formatted existingMemories
// block inside the extraction prompt and pushed local LLMs (e.g. qwen2.5:7b)
// past their usable latency window (25–55s, then 60s timeouts). The v2 harness
// flagged k=10/minScore=0.5 as hardcoded and observability.md did not require
// per-stage timings. These helpers cap the prompt inputs by byte budget
// (most-similar first, since similarMemories arrives ordered by score) and are
// pure functions so they're cheap to test without provider stubs.
//
// Byte-budget semantics:
//   - budget > 0  → hard cap; stop once the formatted block would exceed it.
//   - budget == 0 → treated as "unlimited" (no trim). Defaults are applied in
//     NewEngine, so a zero-valued config struct still gets sensible caps.

// buildConversationContext formats recent session messages for the extraction
// prompt and trims by total byte budget (includes newline separators in the
// running count, approximated by adding 1 per entry).
func buildConversationContext(msgs []*storage.SessionMessage, maxBytes int) ([]string, int) {
	if len(msgs) == 0 {
		return nil, 0
	}
	out := make([]string, 0, len(msgs))
	total := 0
	for _, m := range msgs {
		if m == nil {
			continue
		}
		line := fmt.Sprintf("[%s]: %s", m.Role, m.Content)
		if maxBytes > 0 && total+len(line)+1 > maxBytes {
			break
		}
		out = append(out, line)
		total += len(line) + 1
	}
	return out, total
}

// buildExistingMemoriesBlock formats the similar-memory neighbors for the
// extraction prompt and trims by total byte budget. Each memory's content is
// trimmed per-entry via perMemContentTrim (0 = no trim). The format is kept
// compact: `[Type, State, importance:%.1f] Content`.
func buildExistingMemoriesBlock(sims []*storage.SimilarityResult, maxBytes int, perMemContentTrim int) ([]string, int) {
	if len(sims) == 0 {
		return nil, 0
	}
	out := make([]string, 0, len(sims))
	total := 0
	for _, sm := range sims {
		if sm == nil || sm.Memory == nil {
			continue
		}
		content := sm.Memory.Content
		if perMemContentTrim > 0 && len(content) > perMemContentTrim {
			content = content[:perMemContentTrim] + "..."
		}
		line := fmt.Sprintf("[%s, %s, importance:%.1f] %s",
			sm.Memory.Type, sm.Memory.State, sm.Memory.Importance, content)
		if maxBytes > 0 && total+len(line)+1 > maxBytes {
			break
		}
		out = append(out, line)
		total += len(line) + 1
	}
	return out, total
}

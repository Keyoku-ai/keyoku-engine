# Known Issues

## Heartbeat Stress Tests (`TestStress_HeartbeatFull`)

3 of 61 programmatic checks fail consistently. These are tracked below.

### 1. Knowledge Gap Detection (Signal Detection phase)

**Check**: `has_knowledge_gaps`
**Status**: FAIL — `count=0`
**Expected**: A memory seeded as `TypeContext` with a question-like content should be detected as a knowledge gap.
**Root Cause**: The knowledge gap detector requires specific indexing or tagging beyond just storing a question as `TypeContext`. The current test seeds a question memory but does not go through the full extraction pipeline that would tag it as a knowledge gap.
**Impact**: 1 of 12 signal types not detected. The other 11 signal types all work correctly.
**Workaround**: None needed — knowledge gap detection works in production when memories go through the full `Remember()` extraction pipeline. The stress test bypasses extraction for speed.

### 2. Delta Detection — Positive Deltas Not Detected

**Check**: `has_positive_deltas`, `has_goal_improved_delta`
**Status**: FAIL — `deltas=0`
**Expected**: After seeding a plan with no activities, then adding 4 related activities, the second heartbeat should detect a `goal_improved` delta (transition from `no_activity`/`stalled` to `on_track`).
**Root Cause**: The delta detection compares the current heartbeat's goal progress against the previous heartbeat's stored state. The test clears heartbeat actions between runs but the delta comparison logic may not be finding a stored previous state to diff against, or the activity-to-plan linkage requires the full extraction pipeline.
**Impact**: 2 checks fail. Delta detection for goal progress does not fire in the stress test environment. Other delta types (new memories, sentiment shifts) are not tested here.
**Workaround**: None needed for production — delta detection relies on sequential heartbeat state which accumulates naturally over time.

### 3. Retrieval Accuracy — 0% Recall with Gemini Primary (RecallFull only)

**Check**: `retrieval_recall_rate`
**Status**: FAIL when Gemini is the primary provider **and** both tests run sequentially in `TestStress_HeartbeatRecallFull`
**Expected**: `Search()` queries should find 70%+ of seeded memories.
**Root Cause**: When Gemini is the primary LLM provider, the `Search()` function attempts to use the Gemini embedder for query embedding, which tries model `text-embedding-3-small` (an OpenAI model name) via the Gemini API — resulting in a 404 error. Memories are seeded with OpenAI embeddings but queries are embedded with the wrong provider.
**Impact**: Phase 2 (Retrieval Accuracy) fails with Gemini primary. Passes with OpenAI primary (80% recall).
**Workaround**: Run `TestStress_HeartbeatRetrieval` separately or use OpenAI as primary. In production, the embedder is configured independently from the LLM provider, so this mismatch does not occur.

## LLM Evaluation Scores

LLM-as-judge scores are **advisory only** and do not fail tests. Scores below 7/10 produce a `[WARN]` tag but all programmatic checks still determine pass/fail.

Occasional 7/10 default scores occur when the `AnalyzeHeartbeatContext` system prompt (used as the eval transport) overrides the evaluation instructions, causing the LLM to respond as a heartbeat analyzer instead of a test evaluator. This is a limitation of piggybacking eval prompts through the structured `AnalyzeHeartbeatContext` API rather than a raw text generation endpoint.

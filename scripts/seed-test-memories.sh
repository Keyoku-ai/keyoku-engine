#!/usr/bin/env bash
# Seed test memories for heartbeat scenario testing.
# Each scenario targets a specific heartbeat signal path.
#
# Usage: ./scripts/seed-test-memories.sh [scenario] [entity_id] [base_url]
#   scenario   "all" (default), or one of: pending, deadline, scheduled, conflict,
#              continuity, sentiment, knowledge, first-contact, goal-progress, quiet
#   entity_id  defaults to "test-user"
#   base_url   defaults to "http://localhost:8100"

set -euo pipefail

SCENARIO="${1:-all}"
ENTITY_ID="${2:-test-user}"
BASE_URL="${3:-http://localhost:8100}"
AGENT_ID="kumo"

# Helpers
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TOMORROW=$(date -u -v+1d +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -d "+1 day" +"%Y-%m-%dT%H:%M:%SZ")
HOURS_4_AGO=$(date -u -v-4H +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -d "-4 hours" +"%Y-%m-%dT%H:%M:%SZ")
HOURS_2_AGO=$(date -u -v-2H +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -d "-2 hours" +"%Y-%m-%dT%H:%M:%SZ")
HOURS_1_AGO=$(date -u -v-1H +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -d "-1 hour" +"%Y-%m-%dT%H:%M:%SZ")
DAYS_2_AGO=$(date -u -v-2d +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -d "-2 days" +"%Y-%m-%dT%H:%M:%SZ")
DAYS_30_AGO=$(date -u -v-30d +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -d "-30 days" +"%Y-%m-%dT%H:%M:%SZ")

seed() {
  local label="$1"
  local data="$2"
  echo "  Seeding: $label"
  local resp
  resp=$(curl -s -X POST "$BASE_URL/api/v1/seed" \
    -H "Content-Type: application/json" \
    -d "$data")
  echo "    → $resp"
}

# ─────────────────────────────────────────────────────────────────────────────
# SCENARIO 1: Pending Work (CheckPendingWork — TierNormal)
# Triggers: PLAN or ACTIVITY memories, active state, importance >= 0.4
# Expected: ShouldAct=true, DecisionReason="act", PendingWork populated
# ─────────────────────────────────────────────────────────────────────────────
seed_pending() {
  echo ""
  echo "=== Scenario: PENDING WORK ==="
  echo "  Signal: CheckPendingWork (TierNormal)"
  echo "  Expect: ShouldAct=true, PendingWork=[2 items]"
  seed "high-importance plan" "$(cat <<EOF
{"memories":[
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Ship the Keyoku v2 release by end of week — includes heartbeat rewrite, content rotation, and escalation tracking","type":"PLAN","importance":0.8},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Review and merge the PR for the seed endpoint before testing heartbeats","type":"ACTIVITY","importance":0.6},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"User prefers dark mode in all applications","type":"PREFERENCE","importance":0.3}
]}
EOF
)"
}

# ─────────────────────────────────────────────────────────────────────────────
# SCENARIO 2: Deadline (CheckDeadlines — TierImmediate)
# Triggers: Any memory with expires_at within 24h
# Expected: ShouldAct=true, HighestUrgencyTier="immediate", Deadlines populated
# ─────────────────────────────────────────────────────────────────────────────
seed_deadline() {
  echo ""
  echo "=== Scenario: DEADLINE ==="
  echo "  Signal: CheckDeadlines (TierImmediate)"
  echo "  Expect: ShouldAct=true, HighestUrgencyTier=immediate, Deadlines=[1]"
  seed "expiring plan" "$(cat <<EOF
{"memories":[
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Investor pitch deck needs to be finished — meeting is tomorrow morning","type":"PLAN","importance":0.9,"expires_at":"$TOMORROW"},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"User is preparing slides about Sentai v2 architecture for the investor meeting","type":"ACTIVITY","importance":0.6}
]}
EOF
)"
}

# ─────────────────────────────────────────────────────────────────────────────
# SCENARIO 3: Scheduled/Cron (CheckScheduled — TierImmediate)
# Triggers: Memory tagged "cron:daily:HH:MM" where schedule is due
# Expected: ShouldAct=true, HighestUrgencyTier="immediate", Scheduled populated
# ─────────────────────────────────────────────────────────────────────────────
seed_scheduled() {
  echo ""
  echo "=== Scenario: SCHEDULED (CRON) ==="
  echo "  Signal: CheckScheduled (TierImmediate)"
  echo "  Expect: ShouldAct=true, Scheduled=[1]"

  # Get current hour to make a cron tag that's already due
  local CURRENT_HOUR
  CURRENT_HOUR=$(date -u +"%H")
  local PAST_HOUR
  PAST_HOUR=$(printf "%02d" $(( (10#$CURRENT_HOUR - 1 + 24) % 24 )))

  seed "daily reminder due now" "$(cat <<EOF
{"memories":[
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Check GitHub notifications and respond to any PR reviews","type":"PLAN","importance":0.5,"tags":["cron:daily:${PAST_HOUR}:00"],"created_at":"$DAYS_2_AGO"}
]}
EOF
)"
}

# ─────────────────────────────────────────────────────────────────────────────
# SCENARIO 4: Conflict (CheckConflicts — TierElevated)
# Triggers: Memory with confidence_factors containing "conflict_flagged:..."
# Expected: ShouldAct=true, Conflicts populated
# ─────────────────────────────────────────────────────────────────────────────
seed_conflict() {
  echo ""
  echo "=== Scenario: CONFLICT ==="
  echo "  Signal: CheckConflicts (TierElevated)"
  echo "  Expect: ShouldAct=true, Conflicts=[1]"
  seed "conflicting memories" "$(cat <<EOF
{"memories":[
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"User said they want to use PostgreSQL for the production database","type":"PREFERENCE","importance":0.7,"confidence_factors":["conflict_flagged: contradicts earlier preference for SQLite-only architecture"]},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"User strongly prefers SQLite for everything to keep deployment simple","type":"PREFERENCE","importance":0.6}
]}
EOF
)"
}

# ─────────────────────────────────────────────────────────────────────────────
# SCENARIO 5: Continuity (CheckContinuity — TierElevated)
# Triggers: Recent CONTEXT/ACTIVITY memories within 12h, with ACTIVITY/PLAN
#           in 2h session window of newest memory
# Expected: ShouldAct=true, Continuity.WasInterrupted=true
# ─────────────────────────────────────────────────────────────────────────────
seed_continuity() {
  echo ""
  echo "=== Scenario: CONTINUITY (interrupted session) ==="
  echo "  Signal: CheckContinuity (TierElevated)"
  echo "  Expect: ShouldAct=true, Continuity.WasInterrupted=true"
  seed "recent interrupted work" "$(cat <<EOF
{"memories":[
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Debugging the heartbeat fingerprint calculation — found that SHA256 input includes stale monitor IDs","type":"ACTIVITY","importance":0.5,"created_at":"$HOURS_2_AGO"},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Started refactoring evaluateShouldAct to use the new time period tiers","type":"ACTIVITY","importance":0.5,"created_at":"$HOURS_1_AGO"},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Need to update the mock stores to support the new surfaced_memories interface methods","type":"PLAN","importance":0.6,"created_at":"$HOURS_1_AGO"}
]}
EOF
)"
}

# ─────────────────────────────────────────────────────────────────────────────
# SCENARIO 6: Sentiment Decline (CheckSentiment — TierLow)
# Triggers: 6+ memories where recent half has avg sentiment 0.3+ lower than older half
# Expected: ShouldAct=true (if no higher signals suppress), Sentiment.Direction="declining"
# ─────────────────────────────────────────────────────────────────────────────
seed_sentiment() {
  echo ""
  echo "=== Scenario: SENTIMENT DECLINE ==="
  echo "  Signal: CheckSentiment (TierLow)"
  echo "  Expect: Sentiment.Direction=declining"
  echo "  Note: Needs 6+ memories. Older=positive, recent=negative."
  seed "sentiment shift memories" "$(cat <<EOF
{"memories":[
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Excited about the new agent architecture — it's coming together nicely","type":"CONTEXT","importance":0.4,"sentiment":0.8,"created_at":"$DAYS_30_AGO"},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Made great progress on the orchestrator today, everything clicked","type":"CONTEXT","importance":0.4,"sentiment":0.7,"created_at":"$DAYS_30_AGO"},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Happy with how the capability pack system turned out","type":"CONTEXT","importance":0.4,"sentiment":0.6,"created_at":"$DAYS_30_AGO"},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Frustrated that heartbeats aren't firing at all overnight","type":"CONTEXT","importance":0.5,"sentiment":-0.6,"created_at":"$HOURS_4_AGO"},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"221 ticks and zero messages — this is broken and wasting compute","type":"CONTEXT","importance":0.5,"sentiment":-0.8,"created_at":"$HOURS_2_AGO"},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"The GoalProgress noise is drowning out real signals, nothing works as intended","type":"CONTEXT","importance":0.5,"sentiment":-0.7,"created_at":"$HOURS_1_AGO"}
]}
EOF
)"
}

# ─────────────────────────────────────────────────────────────────────────────
# SCENARIO 7: Knowledge Gap (CheckKnowledge — TierNormal)
# Triggers: Active memory that looks like a question (contains "?") with no
#           similar memories that could answer it (similarity < 0.6)
# Expected: KnowledgeGaps populated
# ─────────────────────────────────────────────────────────────────────────────
seed_knowledge() {
  echo ""
  echo "=== Scenario: KNOWLEDGE GAP ==="
  echo "  Signal: CheckKnowledge (TierNormal)"
  echo "  Expect: KnowledgeGaps=[1-2]"
  seed "unanswered questions" "$(cat <<EOF
{"memories":[
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Should we use WebSockets or SSE for real-time heartbeat status in the dashboard?","type":"CONTEXT","importance":0.5},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"What's the best way to handle rate limiting for the embedding API calls during batch seed operations?","type":"CONTEXT","importance":0.5}
]}
EOF
)"
}

# ─────────────────────────────────────────────────────────────────────────────
# SCENARIO 8: Goal Progress (CheckGoalProgress — TierNormal)
# Triggers: PLAN memories with related ACTIVITY memories found by embedding
#           similarity. Filtered: no_activity goals are excluded.
# Expected: GoalProgress with status "on_track" or "at_risk", NOT "no_activity"
# ─────────────────────────────────────────────────────────────────────────────
seed_goal_progress() {
  echo ""
  echo "=== Scenario: GOAL PROGRESS ==="
  echo "  Signal: CheckGoalProgress (TierNormal)"
  echo "  Expect: GoalProgress with on_track/at_risk status (no_activity filtered)"
  seed "plan with matching activities" "$(cat <<EOF
{"memories":[
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Complete the heartbeat algorithm rewrite with all P0-P3 changes","type":"PLAN","importance":0.8,"expires_at":"$TOMORROW"},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Finished implementing time-of-day tiers for the heartbeat algorithm","type":"ACTIVITY","importance":0.5},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Implemented escalation tracking table and UpsertTopicSurfacing for heartbeat","type":"ACTIVITY","importance":0.5},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Added content rotation with surfaced_memories table to the heartbeat","type":"ACTIVITY","importance":0.5},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Build a new mobile app for expense tracking","type":"PLAN","importance":0.4}
]}
EOF
)"
  echo "  Note: 'heartbeat rewrite' plan should match 3 activities → on_track/at_risk."
  echo "  Note: 'mobile app' plan has no activities → filtered as no_activity."
}

# ─────────────────────────────────────────────────────────────────────────────
# SCENARIO 9: First Contact (< 5 total memories)
# Triggers: GetMemoryCount returns < 5
# Expected: ShouldAct=true, DecisionReason="first_contact"
# ─────────────────────────────────────────────────────────────────────────────
seed_first_contact() {
  echo ""
  echo "=== Scenario: FIRST CONTACT ==="
  echo "  Signal: Memory count < 5"
  echo "  Expect: ShouldAct=true, DecisionReason=first_contact"
  echo "  Use a FRESH entity ID with no prior memories!"
  local FC_ENTITY="first-contact-$(date +%s)"
  seed "minimal memories for new user" "$(cat <<EOF
{"memories":[
  {"entity_id":"$FC_ENTITY","agent_id":"$AGENT_ID","content":"New user just connected via Telegram","type":"CONTEXT","importance":0.3},
  {"entity_id":"$FC_ENTITY","agent_id":"$AGENT_ID","content":"User's name might be Alex","type":"IDENTITY","importance":0.5}
]}
EOF
)"
  echo ""
  echo "  Test with: curl -s -X POST '$BASE_URL/api/v1/heartbeat/context' \\"
  echo "    -H 'Content-Type: application/json' \\"
  echo "    -d '{\"entity_id\":\"$FC_ENTITY\",\"agent_id\":\"$AGENT_ID\",\"query\":\"check in\",\"top_k\":5}' | python3 -m json.tool"
}

# ─────────────────────────────────────────────────────────────────────────────
# SCENARIO 10: Quiet / No Signals (nudge fallback)
# Triggers: No active signals but user has memories → nudge path
# Expected: ShouldAct=true, DecisionReason="nudge", NudgeContext set
# ─────────────────────────────────────────────────────────────────────────────
seed_quiet() {
  echo ""
  echo "=== Scenario: QUIET (nudge fallback) ==="
  echo "  Signal: No active signals, but has interesting memories"
  echo "  Expect: ShouldAct=true (after cooldown), DecisionReason=nudge"
  seed "only facts and preferences (no plans/activities)" "$(cat <<EOF
{"memories":[
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"User is training for a half marathon in April","type":"IDENTITY","importance":0.5},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"User recently adopted a golden retriever puppy named Mochi","type":"IDENTITY","importance":0.6},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"User enjoys reading science fiction, especially Asimov and Le Guin","type":"PREFERENCE","importance":0.4},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"User mentioned they're learning to cook Japanese food","type":"IDENTITY","importance":0.4},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"User works remotely and lives in Portland, Oregon","type":"IDENTITY","importance":0.3},
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Just discovered a new café near their house with great matcha","type":"CONTEXT","importance":0.3,"created_at":"$DAYS_2_AGO"}
]}
EOF
)"
}

# ─────────────────────────────────────────────────────────────────────────────
# SCENARIO 11: Stale Monitor (CheckStale — TierElevated)
# Triggers: PLAN memory tagged "monitor" not accessed in 24h+
# Expected: StaleMonitors populated
# ─────────────────────────────────────────────────────────────────────────────
seed_stale_monitor() {
  echo ""
  echo "=== Scenario: STALE MONITOR ==="
  echo "  Signal: CheckStale (TierElevated)"
  echo "  Expect: StaleMonitors=[1]"
  seed "overdue monitor" "$(cat <<EOF
{"memories":[
  {"entity_id":"$ENTITY_ID","agent_id":"$AGENT_ID","content":"Monitor the CI pipeline — last build had flaky tests in the heartbeat suite","type":"PLAN","importance":0.6,"tags":["monitor"],"created_at":"$DAYS_2_AGO"}
]}
EOF
)"
}

# ─────────────────────────────────────────────────────────────────────────────
# Run scenarios
# ─────────────────────────────────────────────────────────────────────────────

echo "Heartbeat Scenario Seeder"
echo "========================="
echo "Entity: $ENTITY_ID | Agent: $AGENT_ID | Server: $BASE_URL"

case "$SCENARIO" in
  pending)          seed_pending ;;
  deadline)         seed_deadline ;;
  scheduled)        seed_scheduled ;;
  conflict)         seed_conflict ;;
  continuity)       seed_continuity ;;
  sentiment)        seed_sentiment ;;
  knowledge)        seed_knowledge ;;
  goal-progress)    seed_goal_progress ;;
  first-contact)    seed_first_contact ;;
  quiet)            seed_quiet ;;
  stale-monitor)    seed_stale_monitor ;;
  all)
    seed_pending
    seed_deadline
    seed_scheduled
    seed_conflict
    seed_continuity
    seed_sentiment
    seed_knowledge
    seed_goal_progress
    seed_first_contact
    seed_quiet
    seed_stale_monitor
    ;;
  *)
    echo "Unknown scenario: $SCENARIO"
    echo "Available: pending, deadline, scheduled, conflict, continuity,"
    echo "           sentiment, knowledge, goal-progress, first-contact,"
    echo "           quiet, stale-monitor, all"
    exit 1
    ;;
esac

echo ""
echo "════════════════════════════════════════════════════════════════"
echo "Test heartbeat:"
echo "  curl -s -X POST '$BASE_URL/api/v1/heartbeat/context' \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"entity_id\":\"$ENTITY_ID\",\"agent_id\":\"$AGENT_ID\",\"query\":\"what should I tell this user\",\"top_k\":10,\"analyze\":true}' \\"
echo "    | python3 -m json.tool"
echo ""
echo "Check raw heartbeat (no LLM analysis):"
echo "  curl -s -X POST '$BASE_URL/api/v1/heartbeat/context' \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"entity_id\":\"$ENTITY_ID\",\"agent_id\":\"$AGENT_ID\",\"query\":\"check in\",\"top_k\":10}' \\"
echo "    | python3 -m json.tool"
echo ""
echo "List memories:"
echo "  curl -s '$BASE_URL/api/v1/memories?entity_id=$ENTITY_ID' | python3 -m json.tool | head -50"
echo ""
echo "Clear all (start fresh):"
echo "  curl -s -X DELETE '$BASE_URL/api/v1/memories?entity_id=$ENTITY_ID'"
echo "════════════════════════════════════════════════════════════════"

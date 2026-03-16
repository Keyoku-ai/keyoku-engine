#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════════════
# Keyoku Simulation: Multi-entity demo seeder
# Seeds ~80 memories across 3 entities covering ALL 12 heartbeat signals.
#
# Scenarios:
#   1. "alex" — Full-stack dev building NovaPay (fintech)  → all 12 signals
#   2. "riley" — Designer on NovaPay                       → 8 signals
#   3. "team-nova" — Team aggregate view                  → 6 signals
#
# Usage:
#   KEYOKU_URL=http://localhost:51981 KEYOKU_TOKEN=dev-token ./simulate.sh
#   ./simulate.sh --diagnose    # Read-only diagnostics
#   ./simulate.sh --clean       # Wipe demo entities before seeding
#   ./simulate.sh --seed        # Seed only
#   ./simulate.sh --verify      # Verify heartbeat signals only
#   ./simulate.sh               # All phases: diagnose → seed → verify
# ═══════════════════════════════════════════════════════════════════════════
set -euo pipefail

BASE_URL="${KEYOKU_URL:-http://localhost:51981}"
TOKEN="${KEYOKU_TOKEN:-dev-token}"
AGENT="kumo"

ENTITIES=("alex" "riley" "team-nova")

# Flags
DO_DIAGNOSE=false
DO_CLEAN=false
DO_SEED=false
DO_VERIFY=false
DO_ALL=true

for arg in "$@"; do
  case "$arg" in
    --diagnose) DO_DIAGNOSE=true; DO_ALL=false ;;
    --clean)    DO_CLEAN=true; DO_ALL=false ;;
    --seed)     DO_SEED=true; DO_ALL=false ;;
    --verify)   DO_VERIFY=true; DO_ALL=false ;;
    --help|-h)
      echo "Usage: KEYOKU_URL=... KEYOKU_TOKEN=... $0 [--diagnose] [--clean] [--seed] [--verify]"
      exit 0 ;;
  esac
done

if $DO_ALL; then
  DO_DIAGNOSE=true
  DO_SEED=true
  DO_VERIFY=true
fi

# ─── Helpers ──────────────────────────────────────────────────────────────

api() {
  local method="$1" path="$2"
  shift 2
  curl -s -X "$method" "$BASE_URL/api/v1$path" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    "$@"
}

seed_batch() {
  local label="$1" data="$2"
  printf "  %-44s" "$label"
  local resp
  resp=$(api POST /seed -d "$data")
  local created
  created=$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('created',0))" 2>/dev/null || echo "?")
  echo "→ $created created"
}

# Date helpers (macOS + Linux compatible)
date_offset() {
  local offset="$1"
  if date -v+0S >/dev/null 2>&1; then
    date -u -v"$offset" +"%Y-%m-%dT%H:%M:%SZ"
  else
    local sign="${offset:0:1}"
    local rest="${offset:1}"
    local unit="${rest: -1}"
    local num="${rest:0:${#rest}-1}"
    local gnu_unit
    case "$unit" in
      H) gnu_unit="hours" ;;
      d) gnu_unit="days" ;;
      M) gnu_unit="minutes" ;;
      S) gnu_unit="seconds" ;;
      w) gnu_unit="weeks" ;;
      *) gnu_unit="$unit" ;;
    esac
    date -u -d "${sign}${num} ${gnu_unit}" +"%Y-%m-%dT%H:%M:%SZ"
  fi
}

NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
HOUR_1_AGO=$(date_offset "-1H")
HOUR_2_AGO=$(date_offset "-2H")
HOUR_3_AGO=$(date_offset "-3H")
HOUR_6_AGO=$(date_offset "-6H")
DAY_1_AGO=$(date_offset "-1d")
DAY_2_AGO=$(date_offset "-2d")
DAY_3_AGO=$(date_offset "-3d")
DAY_4_AGO=$(date_offset "-4d")
DAY_5_AGO=$(date_offset "-5d")
DAY_6_AGO=$(date_offset "-6d")
DAY_7_AGO=$(date_offset "-7d")
HOUR_20_FROM_NOW=$(date_offset "+20H")
HOUR_4_FROM_NOW=$(date_offset "+4H")
WEEK_1_AGO=$(date_offset "-1w")
WEEK_2_AGO=$(date_offset "-2w")
WEEK_3_AGO=$(date_offset "-3w")
WEEK_4_AGO=$(date_offset "-4w")
DAY_42_AGO=$(date_offset "-42d")
DAY_45_AGO=$(date_offset "-45d")

# Cron: use 1 hour ago so daily schedule is "due"
PAST_HOUR=$(printf "%02d" $(( (10#$(date -u +"%H") - 1 + 24) % 24 )))

# Current weekday name (portable capitalize)
WEEKDAY=$(date +"%A" | tr '[:upper:]' '[:lower:]')
WEEKDAY_SHORT=$(date +"%a" | tr '[:upper:]' '[:lower:]')
WEEKDAY_CAP="$(echo "${WEEKDAY:0:1}" | tr '[:lower:]' '[:upper:]')${WEEKDAY:1}"

echo "╔═══════════════════════════════════════════════════════════════╗"
echo "║  Keyoku Simulation: Multi-Entity Demo                       ║"
echo "╠═══════════════════════════════════════════════════════════════╣"
echo "║  Server:    $BASE_URL"
echo "║  Entities:  alex (dev), riley (designer), team-nova (team)"
echo "║  Today:     $(date +"%A, %B %d %Y")"
echo "╚═══════════════════════════════════════════════════════════════╝"
echo ""

# ═══════════════════════════════════════════════════════════════════════════
# PHASE 1: DIAGNOSE
# ═══════════════════════════════════════════════════════════════════════════
if $DO_DIAGNOSE; then
  echo "── Phase 1: Diagnostics ──────────────────────────────────────"
  echo ""

  printf "  %-30s" "Health check..."
  health=$(api GET /health)
  status=$(echo "$health" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','?'))" 2>/dev/null || echo "FAIL")
  echo "$status"

  printf "  %-30s" "Known entities..."
  entities=$(api GET /entities)
  echo "$entities" | python3 -c "import sys,json; d=json.load(sys.stdin); print(', '.join(d) if isinstance(d,list) else str(d))" 2>/dev/null || echo "?"

  printf "  %-30s" "Total memories..."
  stats=$(api GET /stats)
  echo "$stats" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('total_memories', d))" 2>/dev/null || echo "?"

  for entity in "${ENTITIES[@]}"; do
    printf "  %-30s" "Memories for '$entity'..."
    estats=$(api GET "/stats/$entity")
    echo "$estats" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('total_memories', d))" 2>/dev/null || echo "0"
  done

  printf "  %-30s" "Watcher..."
  watcher=$(api GET /watcher/status)
  echo "$watcher" | python3 -c "import sys,json; d=json.load(sys.stdin); print('running' if d.get('running') else 'stopped')" 2>/dev/null || echo "?"

  echo ""
fi

# ═══════════════════════════════════════════════════════════════════════════
# PHASE: CLEAN (optional)
# ═══════════════════════════════════════════════════════════════════════════
if $DO_CLEAN; then
  echo "── Cleaning demo entities ────────────────────────────────────"
  for entity in "${ENTITIES[@]}"; do
    printf "  %-30s" "Cleaning '$entity'..."
    api DELETE "/memories?entity_id=$entity" >/dev/null 2>&1
    echo "done"
  done
  echo ""
fi

# ═══════════════════════════════════════════════════════════════════════════
# PHASE 2: SEED ENTITY "alex" — Full-stack developer (all 12 signals)
# ═══════════════════════════════════════════════════════════════════════════
if $DO_SEED; then
  ENTITY="alex"
  echo "══════════════════════════════════════════════════════════════"
  echo "  ENTITY: alex — Full-stack developer building NovaPay"
  echo "══════════════════════════════════════════════════════════════"
  echo ""

  # ── Signal 1: Scheduled (cron tags) ─────────────────────────────────
  echo "  [Signal 1] SCHEDULED — cron-tagged memories"
  seed_batch "Daily standup 9am" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Daily standup meeting at 9am with the NovaPay team — discuss blockers and progress\",\"type\":\"PLAN\",\"importance\":0.7,\"tags\":[\"cron:daily:${PAST_HOUR}:00\",\"standup\",\"novapay\"],\"created_at\":\"$DAY_6_AGO\"}
  ]}"
  seed_batch "Weekly retrospective Mon 2pm" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Weekly retrospective every Monday at 2pm — review sprint progress and blockers\",\"type\":\"PLAN\",\"importance\":0.6,\"tags\":[\"cron:weekly:${WEEKDAY_SHORT}:14:00\",\"retrospective\",\"novapay\"],\"created_at\":\"$DAY_6_AGO\"}
  ]}"
  seed_batch "Error dashboard check every 4h" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Check NovaPay error monitoring dashboard for new crashes and regressions\",\"type\":\"PLAN\",\"importance\":0.5,\"tags\":[\"cron:every:4h\",\"monitoring\",\"novapay\"],\"created_at\":\"$DAY_5_AGO\"}
  ]}"

  # ── Signal 2: Deadlines (expires_at within 24h) ────────────────────
  echo ""
  echo "  [Signal 2] DEADLINES — expiring soon"
  seed_batch "Investor demo tomorrow" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Investor demo for NovaPay — present MVP to seed investors at Horizon Ventures\",\"type\":\"EVENT\",\"importance\":0.95,\"tags\":[\"demo\",\"investor\",\"novapay\"],\"expires_at\":\"$HOUR_20_FROM_NOW\",\"sentiment\":0.3,\"created_at\":\"$DAY_6_AGO\"}
  ]}"
  seed_batch "Sprint review today" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Sprint review presentation — demo new features to stakeholders and get sign-off\",\"type\":\"EVENT\",\"importance\":0.8,\"tags\":[\"sprint-review\",\"novapay\"],\"expires_at\":\"$HOUR_4_FROM_NOW\",\"sentiment\":0.2,\"created_at\":\"$DAY_5_AGO\"}
  ]}"

  # ── Signal 3: Pending Work (Plan/Activity, importance>=0.4) ────────
  echo ""
  echo "  [Signal 3] PENDING WORK — active plans and activities"
  seed_batch "Stripe integration plan" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Implement Stripe payment integration for NovaPay subscription tier including webhooks and retry logic\",\"type\":\"PLAN\",\"importance\":0.85,\"tags\":[\"stripe\",\"payments\",\"novapay\"],\"created_at\":\"$DAY_5_AGO\"}
  ]}"
  seed_batch "Dashboard build plan" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Build the transaction dashboard component with Tailwind and Recharts for data visualization\",\"type\":\"PLAN\",\"importance\":0.75,\"tags\":[\"dashboard\",\"frontend\",\"novapay\"],\"created_at\":\"$DAY_5_AGO\"}
  ]}"
  seed_batch "CI/CD setup plan" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Set up CI/CD pipeline with GitHub Actions for automated testing and deployment\",\"type\":\"PLAN\",\"importance\":0.6,\"tags\":[\"devops\",\"ci-cd\",\"novapay\"],\"created_at\":\"$DAY_4_AGO\"}
  ]}"
  seed_batch "Categorization algorithm" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Working on the transaction categorization algorithm using React and TypeScript\",\"type\":\"ACTIVITY\",\"importance\":0.7,\"tags\":[\"algorithm\",\"categorization\",\"novapay\"],\"sentiment\":0.4,\"created_at\":\"$DAY_2_AGO\"}
  ]}"

  # ── Signal 4: Conflicts (confidence_factors with conflict_flagged) ──
  echo ""
  echo "  [Signal 4] CONFLICTS — contradicting preferences"
  seed_batch "PostgreSQL vs MongoDB" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"NovaPay should use PostgreSQL for the production database — better for financial transactions\",\"type\":\"PREFERENCE\",\"importance\":0.7,\"tags\":[\"database\",\"novapay\"],\"confidence_factors\":[\"conflict_flagged: contradicts earlier preference for MongoDB flexible schemas\"],\"created_at\":\"$DAY_5_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Initially considered MongoDB for NovaPay because of flexible schemas for varied transaction types\",\"type\":\"PREFERENCE\",\"importance\":0.5,\"tags\":[\"database\",\"novapay\"],\"confidence_factors\":[\"conflict_flagged: superseded by PostgreSQL decision after team discussion\"],\"created_at\":\"$DAY_6_AGO\"}
  ]}"

  # ── Signal 5: Continuity (recent Context/Activity/Plan < 12h) ──────
  echo ""
  echo "  [Signal 5] CONTINUITY — recent interrupted session"
  seed_batch "Recent auth refactor work" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Refactoring the authentication flow to support OAuth2 with Google and GitHub sign-in\",\"type\":\"ACTIVITY\",\"importance\":0.6,\"tags\":[\"auth\",\"oauth2\",\"novapay\"],\"sentiment\":0.3,\"created_at\":\"$HOUR_3_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Debugging the Stripe webhook handler — getting 422 errors on payment.intent.succeeded events\",\"type\":\"CONTEXT\",\"importance\":0.5,\"tags\":[\"stripe\",\"debugging\",\"novapay\"],\"sentiment\":-0.2,\"created_at\":\"$HOUR_2_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Need to finalize the demo slide deck before tomorrow's investor meeting at Horizon Ventures\",\"type\":\"PLAN\",\"importance\":0.8,\"tags\":[\"demo\",\"investor\",\"novapay\"],\"sentiment\":0.1,\"created_at\":\"$HOUR_1_AGO\"}
  ]}"

  # ── Signal 6: Stale Monitors (Plan + tag "monitor" + old) ─────────
  echo ""
  echo "  [Signal 6] STALE MONITORS — overdue monitoring tasks"
  seed_batch "API perf monitor (stale)" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Monitor NovaPay API response times — alert if p95 exceeds 500ms in production\",\"type\":\"PLAN\",\"importance\":0.6,\"tags\":[\"monitor\",\"performance\",\"novapay\"],\"created_at\":\"$DAY_3_AGO\"}
  ]}"
  seed_batch "Competitor pricing monitor (stale)" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Monitor competitor pricing changes for payment apps like Square and Venmo\",\"type\":\"PLAN\",\"importance\":0.5,\"tags\":[\"monitor\",\"competitive-analysis\",\"novapay\"],\"created_at\":\"$DAY_4_AGO\"}
  ]}"

  # ── Signal 7: Goal Progress (Plan + related Activities) ────────────
  echo ""
  echo "  [Signal 7] GOAL PROGRESS — plan with matching activities"
  seed_batch "Onboarding wizard plan + activities" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Complete the NovaPay onboarding flow with step-by-step wizard for new users\",\"type\":\"PLAN\",\"importance\":0.8,\"tags\":[\"onboarding\",\"ux\",\"novapay\"],\"expires_at\":\"$(date_offset '+5d')\",\"created_at\":\"$DAY_6_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Built the first two steps of the NovaPay onboarding wizard — account setup and bank linking\",\"type\":\"ACTIVITY\",\"importance\":0.6,\"tags\":[\"onboarding\",\"novapay\"],\"sentiment\":0.5,\"created_at\":\"$DAY_4_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Added the category selection step to NovaPay onboarding wizard with preset categories\",\"type\":\"ACTIVITY\",\"importance\":0.6,\"tags\":[\"onboarding\",\"novapay\"],\"sentiment\":0.6,\"created_at\":\"$DAY_2_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Finished the budget goal setting step in the NovaPay onboarding flow\",\"type\":\"ACTIVITY\",\"importance\":0.5,\"tags\":[\"onboarding\",\"novapay\"],\"sentiment\":0.7,\"created_at\":\"$DAY_1_AGO\"}
  ]}"

  # ── Signal 8: Decaying (old important CONTEXT, 21-day stability) ───
  echo ""
  echo "  [Signal 8] DECAYING — old important memories"
  seed_batch "Old architecture decision (6 wks)" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Important architecture decision: NovaPay uses event-driven architecture for real-time balance updates across all accounts\",\"type\":\"CONTEXT\",\"importance\":0.9,\"tags\":[\"architecture\",\"novapay\"],\"created_at\":\"$DAY_42_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Key learning: React Server Components significantly reduce bundle size for the NovaPay dashboard pages\",\"type\":\"CONTEXT\",\"importance\":0.85,\"tags\":[\"react\",\"performance\",\"novapay\"],\"created_at\":\"$DAY_45_AGO\"}
  ]}"

  # ── Signal 9: Sentiment Trend (6+ memories, declining delta) ───────
  echo ""
  echo "  [Signal 9] SENTIMENT — positive start → mid-week decline"
  seed_batch "Positive early-week sentiment" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Excited to start the NovaPay project — great team assembled with Riley, Jordan, and Priya\",\"type\":\"EVENT\",\"importance\":0.5,\"tags\":[\"novapay\",\"team\"],\"sentiment\":0.8,\"created_at\":\"$DAY_7_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Riley's UI mockups for NovaPay look amazing — clean and intuitive design system\",\"type\":\"EVENT\",\"importance\":0.5,\"tags\":[\"design\",\"novapay\"],\"sentiment\":0.7,\"created_at\":\"$DAY_6_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Jordan set up the Node.js API scaffolding really quickly — solid architecture choices for NovaPay\",\"type\":\"EVENT\",\"importance\":0.5,\"tags\":[\"backend\",\"novapay\"],\"sentiment\":0.6,\"created_at\":\"$DAY_5_AGO\"}
  ]}"
  seed_batch "Negative mid-week sentiment" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Spent 6 hours debugging a race condition in the transaction sync — extremely frustrating\",\"type\":\"EVENT\",\"importance\":0.6,\"tags\":[\"debugging\",\"novapay\"],\"sentiment\":-0.8,\"created_at\":\"$DAY_4_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Still stuck on the Stripe webhook issue — payment confirmations are timing out in production\",\"type\":\"EVENT\",\"importance\":0.5,\"tags\":[\"stripe\",\"debugging\",\"novapay\"],\"sentiment\":-0.6,\"created_at\":\"$DAY_3_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Finally fixed the race condition but now worried about the demo timeline being too tight\",\"type\":\"EVENT\",\"importance\":0.5,\"tags\":[\"debugging\",\"novapay\"],\"sentiment\":-0.3,\"created_at\":\"$DAY_2_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Demo prep going okay but feeling time pressure from Priya about feature completeness for investors\",\"type\":\"EVENT\",\"importance\":0.5,\"tags\":[\"demo\",\"novapay\"],\"sentiment\":-0.4,\"created_at\":\"$DAY_1_AGO\"}
  ]}"

  # ── Signal 10: Relationship Alerts (via remember, needs LLM) ───────
  echo ""
  echo "  [Signal 10] RELATIONSHIPS — creating entities via remember"
  printf "  %-44s" "Team members via LLM extraction..."
  resp=$(api POST /remember -d "{
    \"entity_id\":\"$ENTITY\",
    \"content\":\"Had a meeting with Riley about the NovaPay design system. She showed the new color palette and component library she built in Figma. Jordan joined to discuss the API contract for the dashboard endpoints. Priya sent a Slack message about the investor demo at Horizon Ventures next week. We also discussed using Stripe for payments.\",
    \"agent_id\":\"$AGENT\"
  }" 2>/dev/null || echo '{"error":"LLM unavailable"}')
  created=$(echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('memories_created','skip'))" 2>/dev/null || echo "skip")
  if [ "$created" = "skip" ] || [ "$created" = "0" ]; then
    echo "→ SKIPPED (no LLM key)"
  else
    echo "→ $created memories extracted"
  fi

  # ── Signal 11: Knowledge Gaps (questions with no answers) ──────────
  echo ""
  echo "  [Signal 11] KNOWLEDGE GAPS — unanswered questions"
  seed_batch "Stripe webhooks question" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"How does Stripe handle webhooks for failed payments and automatic retry logic?\",\"type\":\"CONTEXT\",\"importance\":0.6,\"tags\":[\"stripe\",\"webhooks\",\"question\"],\"created_at\":\"$DAY_3_AGO\"}
  ]}"
  seed_batch "WebSockets vs SSE question" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"What is the best approach for implementing real-time balance updates — WebSockets or Server-Sent Events?\",\"type\":\"CONTEXT\",\"importance\":0.5,\"tags\":[\"real-time\",\"websockets\",\"question\"],\"created_at\":\"$DAY_2_AGO\"}
  ]}"
  seed_batch "Plaid vs Teller question" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Should we use Plaid or Teller for bank account linking in NovaPay?\",\"type\":\"CONTEXT\",\"importance\":0.55,\"tags\":[\"banking-api\",\"integration\",\"question\"],\"created_at\":\"$DAY_4_AGO\"}
  ]}"

  # ── Signal 12: Behavioral Patterns (3+ same weekday over 90d) ──────
  echo ""
  echo "  [Signal 12] PATTERNS — recurring ${WEEKDAY_CAP} activity"
  seed_batch "${WEEKDAY_CAP} pattern (4 weeks)" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Weekend coding session: refactoring NovaPay component structure and cleaning up imports\",\"type\":\"ACTIVITY\",\"importance\":0.5,\"tags\":[\"refactoring\",\"novapay\"],\"created_at\":\"$WEEK_1_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"${WEEKDAY_CAP} afternoon refactoring: cleaned up NovaPay API routes and middleware\",\"type\":\"ACTIVITY\",\"importance\":0.5,\"tags\":[\"refactoring\",\"novapay\"],\"created_at\":\"$WEEK_2_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"${WEEKDAY_CAP} refactoring session: optimized NovaPay database queries for pagination\",\"type\":\"ACTIVITY\",\"importance\":0.5,\"tags\":[\"refactoring\",\"novapay\"],\"created_at\":\"$WEEK_3_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"${WEEKDAY_CAP} coding: refactored NovaPay state management to use Zustand store\",\"type\":\"ACTIVITY\",\"importance\":0.5,\"tags\":[\"refactoring\",\"novapay\"],\"created_at\":\"$WEEK_4_AGO\"}
  ]}"

  # ── Identity & Preferences ─────────────────────────────────────────
  echo ""
  echo "  [Profile] Identity & Preferences"
  seed_batch "Identity + preferences" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Alex is a full-stack developer building NovaPay, a payment platform with AI-powered expense categorization\",\"type\":\"IDENTITY\",\"importance\":0.9,\"tags\":[\"identity\",\"developer\"],\"created_at\":\"$DAY_7_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Prefers dark mode across all development tools and applications\",\"type\":\"PREFERENCE\",\"importance\":0.6,\"tags\":[\"ui\",\"dark-mode\"],\"created_at\":\"$DAY_7_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Strongly prefers TypeScript over JavaScript for all new code in NovaPay\",\"type\":\"PREFERENCE\",\"importance\":0.7,\"tags\":[\"typescript\",\"coding-style\"],\"created_at\":\"$DAY_7_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Prefers Tailwind CSS for styling because of utility-first approach and smaller bundles\",\"type\":\"PREFERENCE\",\"importance\":0.6,\"tags\":[\"tailwind\",\"css\"],\"created_at\":\"$DAY_7_AGO\"}
  ]}"

  # ── Relationship memories ──────────────────────────────────────────
  echo ""
  echo "  [Profile] Relationships"
  seed_batch "Team relationships" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Riley is the lead designer on NovaPay — creates mockups in Figma and has great UX instincts\",\"type\":\"RELATIONSHIP\",\"importance\":0.7,\"tags\":[\"team\",\"riley\",\"design\"],\"created_at\":\"$DAY_7_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Jordan handles all backend work on NovaPay including the Node.js API and database migrations\",\"type\":\"RELATIONSHIP\",\"importance\":0.7,\"tags\":[\"team\",\"jordan\",\"backend\"],\"created_at\":\"$DAY_7_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Priya is the product manager for NovaPay, responsible for the roadmap and investor communications\",\"type\":\"RELATIONSHIP\",\"importance\":0.7,\"tags\":[\"team\",\"priya\",\"product\"],\"created_at\":\"$DAY_7_AGO\"}
  ]}"

  echo ""
  printf "  %-44s" "Triggering consolidation/decay..."
  api POST /consolidate -d "{\"entity_id\":\"$ENTITY\"}" >/dev/null 2>&1
  echo "→ done"

  total=$(api GET "/stats/$ENTITY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('total_memories','?'))" 2>/dev/null || echo "?")
  echo "  Total memories for '$ENTITY': $total"

  # ═════════════════════════════════════════════════════════════════════
  # ENTITY: riley — Designer (8 signals)
  # ═════════════════════════════════════════════════════════════════════
  ENTITY="riley"
  echo ""
  echo "══════════════════════════════════════════════════════════════"
  echo "  ENTITY: riley — Lead designer on NovaPay"
  echo "══════════════════════════════════════════════════════════════"
  echo ""

  seed_batch "Identity" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Riley is a UI/UX designer specializing in fintech products, lead designer on NovaPay\",\"type\":\"IDENTITY\",\"importance\":0.9,\"tags\":[\"identity\",\"designer\"],\"created_at\":\"$DAY_7_AGO\"}
  ]}"

  seed_batch "Design system plan" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Build the NovaPay design system in Figma with reusable components, color tokens, and typography scale\",\"type\":\"PLAN\",\"importance\":0.8,\"tags\":[\"design-system\",\"figma\",\"novapay\"],\"created_at\":\"$DAY_6_AGO\"}
  ]}"
  seed_batch "User testing activity" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Conducted 5 user testing sessions for the NovaPay onboarding flow — 3 found the bank linking step confusing\",\"type\":\"ACTIVITY\",\"importance\":0.7,\"tags\":[\"user-testing\",\"onboarding\",\"novapay\"],\"sentiment\":-0.2,\"created_at\":\"$DAY_3_AGO\"}
  ]}"
  seed_batch "Accessibility question" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"What WCAG 2.1 AA standards do we need to meet for NovaPay's financial dashboards?\",\"type\":\"CONTEXT\",\"importance\":0.6,\"tags\":[\"accessibility\",\"question\",\"novapay\"],\"created_at\":\"$DAY_4_AGO\"}
  ]}"
  seed_batch "Design iteration deadline" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Final design review for NovaPay investor demo — mockups must be approved by end of day\",\"type\":\"EVENT\",\"importance\":0.85,\"tags\":[\"deadline\",\"design-review\",\"novapay\"],\"expires_at\":\"$HOUR_20_FROM_NOW\",\"sentiment\":0.2,\"created_at\":\"$DAY_2_AGO\"}
  ]}"
  seed_batch "Recent Figma session" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Updating the NovaPay dashboard layout based on user testing feedback — moving key metrics above the fold\",\"type\":\"ACTIVITY\",\"importance\":0.5,\"tags\":[\"figma\",\"dashboard\",\"novapay\"],\"sentiment\":0.4,\"created_at\":\"$HOUR_2_AGO\"}
  ]}"
  seed_batch "Positive → neutral sentiment" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Love working on the NovaPay color system — the gradient tokens came out beautifully\",\"type\":\"EVENT\",\"importance\":0.4,\"sentiment\":0.8,\"created_at\":\"$DAY_6_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Concerned about tight timeline for polishing all screens before the investor demo\",\"type\":\"EVENT\",\"importance\":0.4,\"sentiment\":-0.3,\"created_at\":\"$DAY_1_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"User testing results were mixed — some flows need significant rework\",\"type\":\"EVENT\",\"importance\":0.4,\"sentiment\":-0.4,\"created_at\":\"$DAY_2_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"The component library is coming together nicely in Storybook\",\"type\":\"EVENT\",\"importance\":0.4,\"sentiment\":0.5,\"created_at\":\"$DAY_5_AGO\"}
  ]}"

  echo ""
  total=$(api GET "/stats/$ENTITY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('total_memories','?'))" 2>/dev/null || echo "?")
  echo "  Total memories for '$ENTITY': $total"

  # ═════════════════════════════════════════════════════════════════════
  # ENTITY: team-nova — Team aggregate view (6 signals)
  # ═════════════════════════════════════════════════════════════════════
  ENTITY="team-nova"
  echo ""
  echo "══════════════════════════════════════════════════════════════"
  echo "  ENTITY: team-nova — Team aggregate view"
  echo "══════════════════════════════════════════════════════════════"
  echo ""

  seed_batch "Team identity" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"NovaPay team: Alex (full-stack dev), Riley (designer), Jordan (backend), Priya (PM). Building a next-gen payment platform.\",\"type\":\"IDENTITY\",\"importance\":0.9,\"tags\":[\"team\",\"novapay\"],\"created_at\":\"$DAY_7_AGO\"}
  ]}"
  seed_batch "Shared milestone deadline" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Team deadline: NovaPay MVP demo to Horizon Ventures investors — all features must be integrated\",\"type\":\"EVENT\",\"importance\":0.95,\"tags\":[\"deadline\",\"investor\",\"mvp\",\"novapay\"],\"expires_at\":\"$HOUR_20_FROM_NOW\",\"sentiment\":0.2,\"created_at\":\"$DAY_6_AGO\"}
  ]}"
  seed_batch "Cross-team plan" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Launch plan: Complete onboarding wizard (Alex), finalize design system (Riley), API load testing (Jordan), investor deck (Priya)\",\"type\":\"PLAN\",\"importance\":0.85,\"tags\":[\"launch\",\"coordination\",\"novapay\"],\"created_at\":\"$DAY_5_AGO\"}
  ]}"
  seed_batch "Architecture conflict" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Team decided on PostgreSQL but Jordan prefers MongoDB for the analytics pipeline — need to resolve\",\"type\":\"CONTEXT\",\"importance\":0.6,\"tags\":[\"database\",\"conflict\",\"novapay\"],\"confidence_factors\":[\"conflict_flagged: PostgreSQL vs MongoDB disagreement between team members\"],\"created_at\":\"$DAY_4_AGO\"}
  ]}"
  seed_batch "Sprint standup (scheduled)" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Team standup every morning at 9am — all hands required for sprint week\",\"type\":\"PLAN\",\"importance\":0.7,\"tags\":[\"cron:daily:${PAST_HOUR}:00\",\"standup\",\"novapay\"],\"created_at\":\"$DAY_6_AGO\"}
  ]}"
  seed_batch "Team sentiment events" "{\"memories\":[
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Team morale is high after successful API prototype demo\",\"type\":\"EVENT\",\"importance\":0.4,\"sentiment\":0.7,\"created_at\":\"$DAY_6_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Stress levels rising across the team as investor demo approaches\",\"type\":\"EVENT\",\"importance\":0.5,\"sentiment\":-0.5,\"created_at\":\"$DAY_2_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Sprint velocity dropped due to debugging blocked the pipeline\",\"type\":\"EVENT\",\"importance\":0.5,\"sentiment\":-0.3,\"created_at\":\"$DAY_1_AGO\"},
    {\"entity_id\":\"$ENTITY\",\"agent_id\":\"$AGENT\",\"content\":\"Positive feedback from beta testers lifted team spirits\",\"type\":\"EVENT\",\"importance\":0.4,\"sentiment\":0.6,\"created_at\":\"$DAY_5_AGO\"}
  ]}"

  echo ""
  total=$(api GET "/stats/$ENTITY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('total_memories','?'))" 2>/dev/null || echo "?")
  echo "  Total memories for '$ENTITY': $total"
  echo ""
fi

# ═══════════════════════════════════════════════════════════════════════════
# PHASE 3: VERIFY
# ═══════════════════════════════════════════════════════════════════════════
if $DO_VERIFY; then
  echo "── Phase 3: Verifying heartbeat signals ──────────────────────"
  echo ""

  for ENTITY in "${ENTITIES[@]}"; do
    echo "  ── Entity: $ENTITY ──"

    RESULT=$(api POST /heartbeat/context -d "{
      \"entity_id\":\"$ENTITY\",
      \"agent_id\":\"$AGENT\",
      \"query\":\"NovaPay development progress\",
      \"autonomy\":\"act\",
      \"signal_cooldown_normal\":\"0s\",
      \"signal_cooldown_low\":\"0s\",
      \"max_results\":20
    }")

    parse() {
      echo "$RESULT" | python3 -c "
import sys, json
d = json.load(sys.stdin)
$1
" 2>/dev/null
    }

    s1=$(parse "print(len(d.get('scheduled',[])))")
    s2=$(parse "print(len(d.get('deadlines',[])))")
    s3=$(parse "print(len(d.get('pending_work',[])))")
    s4=$(parse "print(len(d.get('conflicts',[])))")
    s5=$(parse "c=d.get('continuity'); print('yes' if c and c.get('was_interrupted') else 'no')")
    s6=$(parse "print(len(d.get('stale_monitors',[])))")
    s7=$(parse "print(len(d.get('goal_progress',[])))")
    s8=$(parse "print(len(d.get('decaying',[])))")
    s9=$(parse "s=d.get('sentiment_trend'); print(s.get('direction','none') if s else 'none')")
    s10=$(parse "print(len(d.get('relationship_alerts',[])))")
    s11=$(parse "print(len(d.get('knowledge_gaps',[])))")
    s12=$(parse "print(len(d.get('behavioral_patterns',[])))")
    should_act=$(parse "print(d.get('should_act',False))")
    urgency=$(parse "print(d.get('highest_urgency_tier','?'))")

    pass() { [ "$1" != "0" ] && [ "$1" != "no" ] && [ "$1" != "none" ] && [ "$1" != "None" ]; }

    echo "  should_act: $should_act | urgency: $urgency"
    echo ""
    echo "  ┌──────────────────────────────┬────────┬───────────────────┐"
    echo "  │ Signal                       │ Status │ Detail            │"
    echo "  ├──────────────────────────────┼────────┼───────────────────┤"

    row() {
      local name="$1" val="$2" detail="$3"
      local status="✗ FAIL"
      pass "$val" && status="✓ PASS"
      printf "  │ %-28s │ %-6s │ %-17s │\n" "$name" "$status" "$detail"
    }

    row "1.  Scheduled"          "$s1"  "$s1 items"
    row "2.  Deadlines"          "$s2"  "$s2 items"
    row "3.  Pending Work"       "$s3"  "$s3 items"
    row "4.  Conflicts"          "$s4"  "$s4 items"
    row "5.  Continuity"         "$s5"  "interrupted=$s5"
    row "6.  Stale Monitors"     "$s6"  "$s6 items"
    row "7.  Goal Progress"      "$s7"  "$s7 goals"
    row "8.  Decaying"           "$s8"  "$s8 items"
    row "9.  Sentiment"          "$s9"  "$s9"
    row "10. Relationships"      "$s10" "$s10 alerts"
    row "11. Knowledge Gaps"     "$s11" "$s11 questions"
    row "12. Patterns"           "$s12" "$s12 patterns"

    echo "  └──────────────────────────────┴────────┴───────────────────┘"

    total_pass=0
    for v in "$s1" "$s2" "$s3" "$s4" "$s5" "$s6" "$s7" "$s8" "$s9" "$s10" "$s11" "$s12"; do
      pass "$v" && total_pass=$((total_pass + 1))
    done
    echo "  Result: $total_pass/12 signals active"
    echo ""
  done

  echo "  Dashboard: $BASE_URL/dashboard/"
  echo ""
fi

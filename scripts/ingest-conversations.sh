#!/usr/bin/env bash
# ingest-conversations.sh — Feed realistic conversations into Keyoku via /remember
#
# Sends ~90 user messages + ~40 agent responses over 3 simulated days.
# Each message goes through the full LLM extraction pipeline.
# Agent messages are prefixed with [Agent] to indicate the AI assistant's responses.
#
# Usage:
#   KEYOKU_URL=http://localhost:8787 KEYOKU_TOKEN=demo-token ./ingest-conversations.sh
#
# Options:
#   --dry-run    Print messages without sending
#   --entity     Entity ID (default: alex)
#   --delay      Seconds between API calls (default: 2)
#   --start-day  Which day to start from (1, 2, or 3) — useful for resuming

set -uo pipefail

KEYOKU_URL="${KEYOKU_URL:-http://localhost:8787}"
KEYOKU_TOKEN="${KEYOKU_TOKEN:-demo-token}"
ENTITY_ID="alex"
DRY_RUN=false
DELAY=2
START_DAY=1

for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=true ;;
    --entity=*) ENTITY_ID="${arg#*=}" ;;
    --delay=*) DELAY="${arg#*=}" ;;
    --start-day=*) START_DAY="${arg#*=}" ;;
  esac
done

# Base date: 3 days ago at midnight
BASE_TS=$(date -v-3d +%s 2>/dev/null || date -d "3 days ago" +%s)
BASE_DATE=$(date -r "$BASE_TS" +%Y-%m-%d 2>/dev/null || date -d "@$BASE_TS" +%Y-%m-%d)

echo "╔══════════════════════════════════════════════════════════╗"
echo "║  Keyoku Conversation Ingestion (with Agent messages)     ║"
echo "╠══════════════════════════════════════════════════════════╣"
echo "║  URL:        $KEYOKU_URL"
echo "║  Entity:     $ENTITY_ID"
echo "║  Base date:  $BASE_DATE (Day 1)"
echo "║  Delay:      ${DELAY}s between calls"
echo "║  Dry run:    $DRY_RUN"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

SENT=0
FAILED=0
SKIPPED=0

send_message() {
  local day=$1
  local hour=$2
  local minute=$3
  local content=$4
  local session_id="${5:-}"

  local day_offset=$((day - 1))
  local ts=$((BASE_TS + day_offset * 86400 + hour * 3600 + minute * 60))
  local created_at
  created_at=$(date -r "$ts" +%Y-%m-%dT%H:%M:%S-05:00 2>/dev/null || date -d "@$ts" --iso-8601=seconds)

  local day_label
  case $day in
    1) day_label="Day 1" ;;
    2) day_label="Day 2" ;;
    3) day_label="Day 3" ;;
  esac

  printf "  [%s %02d:%02d] " "$day_label" "$hour" "$minute"

  if [ "$DRY_RUN" = true ]; then
    echo "(dry) ${content:0:80}..."
    SKIPPED=$((SKIPPED + 1))
    return
  fi

  local body
  body=$(jq -n \
    --arg eid "$ENTITY_ID" \
    --arg content "$content" \
    --arg created_at "$created_at" \
    --arg session_id "$session_id" \
    '{entity_id: $eid, content: $content, created_at: $created_at, session_id: (if $session_id == "" then null else $session_id end)}')

  local response
  local http_code
  response=$(curl -s -w "\n%{http_code}" --max-time 120 -X POST "${KEYOKU_URL}/api/v1/remember" \
    -H "Authorization: Bearer ${KEYOKU_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "$body" 2>&1) || true

  http_code=$(echo "$response" | tail -1)
  local resp_body
  resp_body=$(echo "$response" | sed '$d')

  if [ "$http_code" != "200" ] && [ "$http_code" != "" ]; then
    echo "  retrying..."
    sleep 5
    response=$(curl -s -w "\n%{http_code}" --max-time 120 -X POST "${KEYOKU_URL}/api/v1/remember" \
      -H "Authorization: Bearer ${KEYOKU_TOKEN}" \
      -H "Content-Type: application/json" \
      -d "$body" 2>&1) || true
    http_code=$(echo "$response" | tail -1)
    resp_body=$(echo "$response" | sed '$d')
  fi

  if [ "$http_code" = "200" ]; then
    local created
    created=$(echo "$resp_body" | jq -r '.memories_created // 0')
    echo "✓ created=$created  ${content:0:60}..."
    SENT=$((SENT + 1))
  else
    echo "✗ HTTP $http_code  ${content:0:60}..."
    echo "    Response: ${resp_body:0:120}"
    FAILED=$((FAILED + 1))
  fi

  sleep "$DELAY"
}

# ═══════════════════════════════════════════════════════════════
# DAY 1 — Monday: Fresh start, new project kickoff
# ═══════════════════════════════════════════════════════════════

if [ "$START_DAY" -le 1 ]; then
echo ""
echo "━━━ DAY 1 (Monday) ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

send_message 1 7 15 \
  "Morning routine: woke up at 7am, feeling pretty good. Had coffee and oatmeal. Need to remember to call mom tonight — it's her birthday on Wednesday and I haven't gotten a gift yet. Maybe that pottery class gift card she mentioned?" \
  "morning-journal"
send_message 1 7 16 \
  "[Agent] Got it — I'll remind you to call your mom tonight. Her birthday is Wednesday. The pottery class gift card sounds like a thoughtful pick. Want me to set a reminder for tomorrow in case you haven't ordered it yet?" \
  "morning-journal"

send_message 1 7 45 \
  "Gym session was solid today. Hit a new PR on deadlift — 315 lbs. Been running the 5/3/1 program for about 8 weeks now and the progress is real. Left knee felt a little tight during squats though, should keep an eye on that." \
  "morning-journal"
send_message 1 7 46 \
  "[Agent] Nice PR! 315 on deadlift after 8 weeks of 5/3/1 is solid progress. I'll note the knee tightness during squats — if it comes up again I'll flag it so you can track the pattern." \
  "morning-journal"

send_message 1 8 30 \
  "Thinking about that apartment I saw on Zillow last night. 2BR in the Pearl District, 2100 dollars/month. It's more than I'm paying now but the location is incredible and it has in-unit laundry. Lease is up in April so I need to decide soon." \
  "morning-journal"
send_message 1 8 31 \
  "[Agent] Pearl District 2BR at \$2,100 with in-unit laundry — noted. Your lease is up in April so you have a few weeks to decide. I'll keep this in context if you want to think through the pros/cons later." \
  "morning-journal"

send_message 1 9 0 \
  "Team standup: Starting the new NovaPay integration project today. It's a payment processing platform for small businesses. The stack is React frontend, Go backend with PostgreSQL. Jordan is handling the API design, Riley is on the UI, and I'm leading the backend architecture. Sprint 1 goal: basic merchant onboarding flow." \
  "work-standup"
send_message 1 9 1 \
  "[Agent] NovaPay kickoff — got the team structure: you on backend architecture, Jordan on API design, Riley on UI. Sprint 1 target is merchant onboarding. I'll track project context as it develops." \
  "work-standup"

send_message 1 9 30 \
  "Setting up the project structure for NovaPay. Going with a clean architecture approach — separate domain, application, and infrastructure layers. Using sqlc for type-safe SQL instead of an ORM. Need to decide between chi and echo for the HTTP router." \
  "work-coding"

send_message 1 10 15 \
  "Interesting discussion with Jordan about the merchant verification flow. We need to integrate with Stripe Connect for KYC/KYB verification. The API looks straightforward but there are a lot of webhook events to handle — account.updated, person.updated, capability.updated. Going to map out all the state transitions." \
  "work-architecture"
send_message 1 10 16 \
  "[Agent] Stripe Connect webhook events can be tricky with out-of-order delivery. The state transition map is a good call — you'll want idempotent handlers for each event type. I'll remember this decision for when you get to the implementation." \
  "work-architecture"

send_message 1 11 0 \
  "Had a quick call with the PM, Priya. She wants us to support both individual merchants and business accounts from day one. That changes the data model — need separate tables for individuals vs businesses with different verification requirements. More complex but makes sense for the product." \
  "work-meeting"

send_message 1 11 30 \
  "Deep dive into Stripe Connect documentation. There are three approaches: Standard, Express, and Custom. Standard gives us the least control but fastest integration. Custom gives full control but we own the entire onboarding UI and compliance. Going to recommend Express as a middle ground — good user experience and Stripe handles most compliance." \
  "work-research"
send_message 1 11 31 \
  "[Agent] Express Connect is a solid middle ground — Stripe handles KYC/compliance while you keep a decent UX. This aligns well with Priya's requirement for both individual and business accounts since Express supports both account types natively." \
  "work-research"

send_message 1 12 0 \
  "Lunch break. Tried that new Thai place on 23rd — the pad see ew was amazing but the portions are small for the price. Jordan recommended it. Speaking of Jordan, he mentioned he's thinking about proposing to his girlfriend next month. Happy for him but also wow, we're at that age now." \
  "personal"

send_message 1 13 0 \
  "Back from lunch. Starting to build the merchant data model. Core tables: merchants, merchant_members, verification_documents, bank_accounts, payment_methods. Using UUIDs for all primary keys. Adding soft deletes for compliance — we can never truly delete merchant data due to financial regulations." \
  "work-coding"

send_message 1 13 45 \
  "Running into a design decision on multi-tenancy. Option A: schema-per-tenant (strong isolation, complex migrations). Option B: shared schema with tenant_id column (simpler, need to be careful about data leaks). Option C: row-level security in PostgreSQL. Going with Option C — RLS gives us strong isolation without the operational complexity of separate schemas." \
  "work-architecture"
send_message 1 13 46 \
  "[Agent] RLS is a good choice for your scale — you get the isolation guarantees without managing N schemas. One thing to watch: RLS policies apply to SELECT/UPDATE/DELETE but not INSERT. You'll need triggers or application-level checks for inserts." \
  "work-architecture"

send_message 1 14 30 \
  "Riley showed me the first mockups for the merchant dashboard. They look clean — she's going with a sidebar navigation pattern similar to Stripe's dashboard. Color scheme is navy blue and white with green accents for success states. Love the attention to the empty states — they have helpful illustrations and CTAs." \
  "work-design-review"

send_message 1 15 0 \
  "Debugging a weird issue with the PostgreSQL RLS policies. The policies work fine for SELECT but INSERT is failing silently — rows are being created but the merchant_id isn't being set by the policy. Turns out I need to use a trigger instead of RLS for INSERT operations. PostgreSQL RLS only filters, it doesn't transform." \
  "work-debugging"
send_message 1 15 1 \
  "[Agent] Ah, you hit exactly the RLS INSERT limitation I mentioned earlier! Good that you found the trigger workaround. That's a common gotcha — worth documenting for the team." \
  "work-debugging"

send_message 1 15 45 \
  "Coffee break chat with Riley. She asked about my weekend plans — I mentioned I might go hiking at Eagle Creek if the weather holds up. She's been wanting to try the trail to Punchbowl Falls. Maybe we should organize a team hike? Jordan would probably be down too." \
  "personal"

send_message 1 16 0 \
  "Code review from Jordan on my initial schema migration. Good feedback — he pointed out I should add a CHECK constraint on the merchant_status column instead of just using a VARCHAR. Also suggested adding created_by and updated_by audit columns. He's right, we'll need that for compliance." \
  "work-code-review"

send_message 1 16 30 \
  "Finished the merchant onboarding API endpoints: POST /merchants, GET /merchants/:id, PATCH /merchants/:id, POST /merchants/:id/verify. All using the clean architecture pattern with dependency injection. Unit tests passing. Need to add integration tests tomorrow." \
  "work-coding"

send_message 1 17 0 \
  "End of day summary: Good first day on NovaPay. Got the project structure set up, data model designed, core API endpoints built. Still need to: wire up Stripe Connect, build the webhook handler, add integration tests, and set up the CI pipeline. Feeling good about the architecture decisions so far." \
  "work-eod"
send_message 1 17 1 \
  "[Agent] Solid Day 1. You've got the foundation in place — schema with RLS, 4 core endpoints, clean architecture. Tomorrow's priorities: Stripe Connect integration and webhook handlers. Don't forget to call your mom tonight!" \
  "work-eod"

send_message 1 18 30 \
  "Called mom about her birthday. She doesn't want a big fuss, just dinner together on Wednesday. She mentioned dad's been having trouble with his knee again — might need surgery. That's worrying. Going to look into what kind of recovery time that involves." \
  "personal"
send_message 1 18 31 \
  "[Agent] Glad you called her. I'll note that your dad may need knee surgery — you mentioned your own knee was tight during squats today too. Dinner with mom Wednesday evening, got it." \
  "personal"

send_message 1 19 0 \
  "Making dinner — trying a new recipe for chicken tikka masala from that YouTube channel. The spice blend is: cumin, coriander, turmeric, garam masala, paprika, cayenne. Need to pick up more garam masala from the Indian grocery store this week." \
  "personal"

send_message 1 20 30 \
  "Reading 'Designing Data-Intensive Applications' before bed. Chapter on partitioning is really relevant to what we're building with NovaPay. The section on request routing and partition-aware load balancing is making me think about how we'll handle high-volume merchants." \
  "personal-learning"

send_message 1 21 0 \
  "Marcus texted asking if I want to play basketball Thursday evening. Haven't played in weeks. Also he's got an extra ticket to the Blazers game on Saturday — definitely going to that." \
  "personal"
send_message 1 21 1 \
  "[Agent] Basketball Thursday + Blazers game Saturday with Marcus — sounds like a good weekend. I'll keep these on your radar." \
  "personal"

send_message 1 23 15 \
  "Can't sleep. Thinking about whether we should add rate limiting to the merchant API from the start or add it later. If a merchant sends too many payout requests it could be a sign of fraud. Should talk to the team about implementing a token bucket algorithm per merchant." \
  "work-ideas"
send_message 1 23 16 \
  "[Agent] Rate limiting for fraud detection is worth discussing early. Token bucket per merchant is clean. You could also consider Redis sorted sets for sliding window — might be simpler to reason about for payout-specific limits. Try to get some sleep though!" \
  "work-ideas"

fi

# ═══════════════════════════════════════════════════════════════
# DAY 2 — Tuesday: Productive but hitting some roadblocks
# ═══════════════════════════════════════════════════════════════

if [ "$START_DAY" -le 2 ]; then
echo ""
echo "━━━ DAY 2 (Tuesday) ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

send_message 2 6 45 \
  "Rough sleep last night. Kept thinking about the rate limiting problem. But actually had a good idea in the shower — we can use Redis sorted sets for sliding window rate limiting. Each merchant gets a key, scores are timestamps, and we just count entries in the window. Clean and efficient." \
  "morning-journal"
send_message 2 6 46 \
  "[Agent] Ha — you landed on exactly the Redis sorted sets approach I suggested last night. Sliding window with timestamp scores is elegant. Did you sleep at all?" \
  "morning-journal"

send_message 2 7 30 \
  "Skipped the gym today, running late. Need to be better about sleep hygiene. Been doomscrolling Twitter before bed again. Should set a screen time limit. The phone goes on the nightstand at 10pm from now on — let's see if that sticks." \
  "personal"

send_message 2 8 15 \
  "Grabbed coffee at the new place on Hawthorne. The barista remembered my order from last time — cortado with oat milk. Small thing but it made my morning. Reminded me I should be more intentional about being a regular somewhere instead of always optimizing for convenience." \
  "personal"

send_message 2 9 0 \
  "Standup: Yesterday I finished the merchant data model and core endpoints. Today I'm tackling Stripe Connect integration and webhook handling. Blockers: none yet but I expect the webhook verification to be tricky. Jordan is working on the transaction processing pipeline. Riley is building the onboarding wizard components." \
  "work-standup"

send_message 2 9 30 \
  "Starting the Stripe Connect integration. First step: OAuth flow for connecting merchant Stripe accounts. The flow is: our app redirects to Stripe → merchant authorizes → Stripe redirects back with auth code → we exchange for access token. Need to store the token securely — going to encrypt it at rest using AES-256-GCM." \
  "work-coding"

send_message 2 10 15 \
  "Webhook handling is more complex than expected. Stripe sends events asynchronously and they can arrive out of order. For example, we might get 'payment_intent.succeeded' before 'payment_intent.created'. Need to implement idempotent processing — store event IDs and skip duplicates, and handle events regardless of order." \
  "work-coding"
send_message 2 10 16 \
  "[Agent] This is the state transition mapping you planned yesterday coming into play. For idempotent processing, make sure you verify the webhook signature against the raw request body — not the parsed JSON. Stripe computes the HMAC over the exact bytes they sent." \
  "work-coding"

send_message 2 11 0 \
  "Pair programming session with Jordan on the transaction pipeline. He's designed a nice event-driven architecture using Go channels. Transactions flow through stages: validate → authorize → capture → settle. Each stage is a goroutine. We debated whether to use channels or a message queue — channels for now, can swap to NATS later if needed." \
  "work-pair-programming"

send_message 2 11 45 \
  "Priya scheduled a product review for Thursday. She wants to see a working demo of merchant onboarding — from signup to first test payment. That's ambitious for Thursday but I think we can have the happy path working. Need to coordinate with Riley on getting the UI connected to the API." \
  "work-meeting"
send_message 2 11 46 \
  "[Agent] Thursday demo — that's tight. You've got Stripe Connect 80% done, Jordan has the pipeline, Riley has the wizard. The critical path is getting your API deployed to staging so Riley can integrate. Might want to prioritize that over the remaining edge cases." \
  "work-meeting"

send_message 2 12 15 \
  "Lunch at my desk today — leftover tikka masala from last night. Actually tastes better today, the flavors developed overnight. Reading through HackerNews while eating. Interesting article about a fintech startup that got fined 2 million dollars for inadequate KYC checks. Sobering reminder of why we're being careful with the verification flow." \
  "personal"

send_message 2 13 0 \
  "Hit a frustrating bug. The Stripe webhook signature verification keeps failing in our test environment. The signature is computed over the raw request body, but our middleware is reading and re-serializing the JSON, which changes the formatting. Need to capture the raw body before JSON parsing. This is a common gotcha but I wasted 45 minutes on it." \
  "work-debugging"
send_message 2 13 1 \
  "[Agent] That's exactly the raw body issue I flagged this morning! The signature HMAC is over the wire bytes. Use io.TeeReader to capture the raw body while still allowing your JSON decoder to read the stream." \
  "work-debugging"

send_message 2 13 30 \
  "Fixed the webhook signature issue by using io.TeeReader to capture the raw body while still allowing JSON decoding. Added a test case specifically for this — incoming webhook with exact Stripe formatting, verify signature matches. Never want to debug this again." \
  "work-debugging"
send_message 2 13 31 \
  "[Agent] Good fix, and smart to add the regression test. This is one of those bugs that bites every team integrating with Stripe exactly once." \
  "work-debugging"

send_message 2 14 0 \
  "Interesting conversation with Riley about accessibility. She's been auditing the dashboard mockups and found several contrast ratio issues — the light gray text on white backgrounds doesn't meet WCAG AA. She's proposing a design token system where all colors are defined centrally with contrast ratios pre-validated. Smart approach." \
  "work-design"

send_message 2 14 30 \
  "Started writing integration tests for the Stripe Connect flow using Stripe's test mode. Their test mode is really well done — you can simulate specific scenarios with magic card numbers. 4242424242424242 always succeeds, 4000000000000002 always declines. Writing tests for: successful connect, declined verification, expired documents." \
  "work-testing"

send_message 2 15 15 \
  "Question: should we build our own merchant notification system or use a service like SendGrid? We need to send emails for: verification status changes, payout completed, payout failed, new team member invited. Building our own gives more control but it's a lot of work. Leaning toward SendGrid with templates we control." \
  "work-architecture"
send_message 2 15 16 \
  "[Agent] SendGrid with your own templates is the right call for Sprint 1. You need notifications working for the Thursday demo but building a full email system would eat days. You can always migrate to a custom solution later if delivery/templating needs outgrow SendGrid." \
  "work-architecture"

send_message 2 15 45 \
  "Got a text from my sister. She's visiting Portland next weekend with her husband and kids. Need to figure out activities for a 4-year-old and a 7-year-old. OMSI is always good. Maybe the zoo if the weather cooperates. Should clean my apartment before they come — it's been... a while." \
  "personal"
send_message 2 15 46 \
  "[Agent] Sister visiting next weekend with the kids — OMSI and the zoo are both great picks. You might be busy with Marcus's resume help on Sunday though (you offered last night). Might want to plan the family stuff for Saturday before the Blazers game." \
  "personal"

send_message 2 16 0 \
  "Code review time. Jordan submitted a PR for the transaction state machine. It's well-structured but I noticed he's using string comparisons for state transitions instead of an enum type. In Go, we should use a custom type with iota. Also, the error handling in the capture stage swallows the original error — we need to wrap it for debugging." \
  "work-code-review"

send_message 2 16 30 \
  "Architecture discussion with the team about handling failed payouts. When a payout fails (invalid bank account, insufficient balance, etc.), we need to: 1) update the payout status, 2) notify the merchant, 3) potentially retry, 4) hold funds in escrow. The retry logic is tricky — we don't want to retry if the bank account is invalid, but we should retry on transient network errors." \
  "work-architecture"

send_message 2 17 0 \
  "End of day: Stripe Connect integration is 80% done. Webhook handling works with signature verification. Still need to implement: payout scheduling, merchant notification emails, and the retry logic for failed payouts. The Thursday demo is looking tight but achievable." \
  "work-eod"
send_message 2 17 1 \
  "[Agent] Day 2 recap: Stripe Connect mostly wired up, webhooks working with the TeeReader fix, integration tests started. Tomorrow you need: deploy to staging for Riley, payout scheduling, and the demo flow end-to-end. Also — your mom's birthday dinner is tomorrow evening, don't forget the pottery class gift card." \
  "work-eod"

send_message 2 17 30 \
  "Walking home from work. Beautiful sunset over the West Hills. I really do love living in Portland. Thinking about whether I should commit to this city long-term — buy instead of rent. The housing market is still crazy but mortgage rates are supposed to come down. Something to think about." \
  "personal"

send_message 2 18 0 \
  "Interesting podcast episode on the walk home about the history of payment processing. Visa's original network was literally just a bunch of banks calling each other on the phone. We've come a long way. Makes me appreciate what we're building with NovaPay — abstracting away all that complexity for small businesses." \
  "personal-learning"

send_message 2 19 30 \
  "Dinner with Marcus at the Thai place. He's going through a rough patch at work — his startup is running low on funding and they might need to do layoffs. Feeling grateful for my stable job but also guilty about it. Told him I'd review his resume this weekend and help him prep if he needs to start looking." \
  "personal"
send_message 2 19 31 \
  "[Agent] That's a good friend move, offering to help with his resume. You mentioned introducing him to Priya — her team at your company might actually be a good fit depending on his skills. Something to keep in mind." \
  "personal"

send_message 2 20 45 \
  "Researching PostgreSQL partitioning strategies for the transactions table. If NovaPay takes off, we could have millions of transactions per month. Range partitioning by created_at makes the most sense — keeps recent data hot and allows easy archival of old partitions. Should implement this before we have too much data to migrate." \
  "work-research"

send_message 2 21 30 \
  "Remembered I need to book a dentist appointment. Haven't been in over a year. Also need to renew my car registration before the end of the month. Adding both to my to-do list. The mundane stuff that keeps life running." \
  "personal"

send_message 2 22 0 \
  "Reading before bed — switched to fiction tonight. 'Project Hail Mary' by Andy Weir. The science is fascinating and the humor keeps it light. Rocky is one of the best characters I've read in years. Trying to put the phone down at 10pm like I said... it's 10pm, phone going on the nightstand now." \
  "personal"
send_message 2 22 1 \
  "[Agent] Good on you for the 10pm cutoff! Tomorrow is a big day — mom's birthday plus crunch for the Thursday demo. Get some rest." \
  "personal"

fi

# ═══════════════════════════════════════════════════════════════
# DAY 3 — Wednesday: Crunch day, mom's birthday, some stress
# ═══════════════════════════════════════════════════════════════

if [ "$START_DAY" -le 3 ]; then
echo ""
echo "━━━ DAY 3 (Wednesday) ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

send_message 3 6 30 \
  "Up early today. Mom's birthday — need to pick up flowers before work. Also realized I never ordered that pottery class gift card. Going to do an online order with same-day delivery. Fingers crossed it gets there in time." \
  "personal"
send_message 3 6 31 \
  "[Agent] Happy birthday to your mom! Good catch on the gift card — same-day delivery should work if you order in the next hour. I've been reminding you since Monday, glad you're on it." \
  "personal"

send_message 3 7 0 \
  "Quick gym session — just 30 minutes of cardio. Knee feels better today. The internet says I should do more hip mobility work to help with knee pain during squats. Going to add 10 minutes of stretching to my routine." \
  "personal"
send_message 3 7 1 \
  "[Agent] Good that the knee is better today. That's the second time you've mentioned it this week — hip mobility stretches before squats is a solid plan. I'll keep tracking this." \
  "personal"

send_message 3 8 0 \
  "Morning coffee and planning. Big day today: need to get the Stripe payout flow working for tomorrow's demo, coordinate with Riley on the UI, and still make it to dinner with mom by 6:30. Going to timeblock my day strictly." \
  "personal-planning"
send_message 3 8 1 \
  "[Agent] Busy day. Critical path: deploy to staging first so Riley isn't blocked, then payout flow. You need to leave by ~5:30 for mom's dinner at 6:30. That gives you about 8 hours of work time — tight but doable if you stay focused." \
  "personal-planning"

send_message 3 9 0 \
  "Standup: Yesterday completed Stripe Connect OAuth and webhook handling. Today's goal: payout scheduling and the full merchant-to-payout flow. Also need to deploy to staging for Riley to test against. Jordan is finishing the transaction state machine. He unblocked himself on the state transition issue I flagged in code review." \
  "work-standup"

send_message 3 9 15 \
  "Quick sync with Riley about the onboarding wizard. She needs the API for document upload — merchants need to submit proof of identity and proof of business. I promised this endpoint yesterday but haven't built it yet. Going to prioritize it so she's not blocked." \
  "work-meeting"
send_message 3 9 16 \
  "[Agent] Riley's blocked on document upload — that should be your first task before payout scheduling. A simple pre-signed S3 URL flow would be quickest: generate URL → frontend uploads directly → store the S3 key." \
  "work-meeting"

send_message 3 9 45 \
  "Building the document upload endpoint. Approach: multipart form upload → store in S3 → send to Stripe for verification. Using pre-signed URLs so the frontend uploads directly to S3, then sends us the key. This avoids our server being a bottleneck for large files. Security: pre-signed URLs expire after 5 minutes and are scoped to the specific merchant's prefix." \
  "work-coding"

send_message 3 10 30 \
  "The payout scheduling system is coming together. Design: merchants set a payout schedule (daily, weekly, monthly). A cron job runs every hour, checks which merchants are due for a payout, calculates their available balance (total captured - total refunded - platform fees - reserves), and initiates the transfer via Stripe. Using database advisory locks to prevent double-processing." \
  "work-coding"

send_message 3 11 0 \
  "Running into a problem with the fee calculation. Priya wants a tiered pricing model: 2.9% + 30 cents per transaction for the first 50K/month, then 2.5% + 25 cents above that. But we also need to handle: refunds (return our platform fee?), partial captures, multi-currency transactions, and promotional discounts. This is getting complicated. Need to build a proper fee engine." \
  "work-architecture"

send_message 3 11 30 \
  "Feeling a bit overwhelmed. The demo is tomorrow and there's still a lot to do. The fee engine alone could take a day. Going to simplify for the demo — flat 2.9% fee, no tiers — and build the real fee engine next sprint. Priya will understand. Perfect is the enemy of done." \
  "work-reflection"
send_message 3 11 31 \
  "[Agent] Good call simplifying. A flat 2.9% for the demo is totally fine — the fee engine is important but not demo-critical. Focus on the end-to-end flow looking smooth. You've got 5 hours of work time left before you need to leave for dinner." \
  "work-reflection"

send_message 3 12 0 \
  "Quick lunch — sandwich at my desk. Ordered mom's gift card successfully, delivery confirmed for 4pm today. Relief. Also texted my sister about next weekend — she wants to do OMSI and then dinner at that family-friendly Italian place on Division." \
  "personal"

send_message 3 12 30 \
  "Deployed to staging! The onboarding flow works end-to-end in happy path: create merchant → upload documents → connect Stripe → submit for verification. Using test mode so verification auto-approves. Riley is testing the UI against it now." \
  "work-deployment"
send_message 3 12 31 \
  "[Agent] Staging deployed with the full happy path — great milestone! Riley can now integrate the UI. That unblocks the demo flow. How's the payout scheduling looking?" \
  "work-deployment"

send_message 3 13 0 \
  "Riley found a bug — when the Stripe OAuth redirect comes back, if the user's session has expired, the auth code gets lost and they have to restart the whole onboarding. Need to handle this edge case. Storing the auth code in a temporary table with a 10-minute TTL and resuming from there after re-authentication." \
  "work-debugging"

send_message 3 13 30 \
  "Jordan just showed me something cool — he built a real-time transaction feed using Server-Sent Events. As transactions come in, they appear on the merchant dashboard instantly. No polling. The merchants are going to love this. He's on fire this sprint." \
  "work-collaboration"

send_message 3 14 0 \
  "Fixed the OAuth session expiry bug. Also added proper error handling for all the Stripe API calls — retry on 429 (rate limit) and 500 (Stripe down), fail fast on 400 (bad request) and 401 (auth error). Using exponential backoff with jitter for retries. Max 3 attempts." \
  "work-coding"

send_message 3 14 30 \
  "Priya wants to add a 'test payment' feature to the onboarding flow — after a merchant connects, they can send themselves a one-dollar test payment to verify everything works. Actually a great idea for user confidence. Simple to build since Stripe test mode handles it. Adding it to the demo." \
  "work-feature"
send_message 3 14 31 \
  "[Agent] Test payment is a great demo moment — nothing sells confidence like seeing real money move (even if it's \$1 in test mode). This will make the Thursday presentation land well." \
  "work-feature"

send_message 3 15 0 \
  "Code complete for the demo! Full flow: signup → document upload → Stripe Connect → test payment → payout schedule setup. Running through the whole thing myself now to find any rough edges. Found one: the success page says 'You will receive your first payout on undefined' because the date formatting is broken. Quick fix." \
  "work-testing"

send_message 3 15 30 \
  "Had a really good 1:1 with my manager. She mentioned that the tech lead position on the platform team is opening up. She thinks I'd be a strong candidate. I'm flattered but not sure if I want to go into management. I love coding and I'm worried a lead role would be all meetings and no building. Going to think about it." \
  "work-career"
send_message 3 15 31 \
  "[Agent] That's a big career decision. Worth noting: tech lead isn't necessarily management — it depends on the org. Some tech lead roles are still 60-70% coding with architectural decision-making authority. Might be worth asking your manager what the day-to-day actually looks like before deciding." \
  "work-career"

send_message 3 16 0 \
  "Final staging test passed. The demo app is running smoothly. Riley polished the UI with some nice micro-interactions — the verification status has a subtle pulse animation, and the test payment shows confetti on success. The little details matter." \
  "work-testing"

send_message 3 16 30 \
  "Writing up the demo script for tomorrow. Going to walk through: 1) the problem (small businesses struggle with payment processing), 2) our solution (NovaPay simplifies onboarding and payouts), 3) live demo of the full flow, 4) technical architecture overview, 5) roadmap. Jordan and Riley will each take a section." \
  "work-demo-prep"
send_message 3 16 31 \
  "[Agent] Demo script looks solid. Three days from kickoff to working prototype with full Stripe integration — that's going to impress Priya. Time to head out for dinner with your mom. Gift card delivery was confirmed for 4pm, should be there by now." \
  "work-demo-prep"

send_message 3 17 0 \
  "Leaving work a bit early for mom's birthday dinner. Feeling good about where the project is — we'll have a solid demo tomorrow. Quick stop to pick up flowers on the way. Got her favorite: sunflowers and dahlias. The pottery class gift card was delivered successfully." \
  "personal"

send_message 3 19 30 \
  "Dinner with mom was wonderful. She loved the flowers and the gift card. Dad seems to be doing okay with his knee — they're going to try physical therapy before considering surgery. Mom made her famous chocolate lava cake for dessert even though it's her birthday. That's so her — always taking care of everyone else." \
  "personal"
send_message 3 19 31 \
  "[Agent] Glad the dinner went well and she liked the gifts. Good news on your dad — PT before surgery is the right call. Sounds like a lovely evening." \
  "personal"

send_message 3 20 30 \
  "Back home. Reflecting on the day. Three days into NovaPay and we have a working prototype. The team is clicking — Jordan's backend skills complement mine well, and Riley's design instincts are sharp. Even the stress of the crunch day felt productive, not toxic. This is what a good team feels like." \
  "work-reflection"

send_message 3 21 0 \
  "Things I want to learn: 1) Kubernetes operators for managing database migrations, 2) WebAssembly for running validation logic client-side, 3) The new PostgreSQL 17 features, especially incremental backup. Also want to get back to that Rust side project I abandoned 2 months ago — a CLI tool for managing dotfiles." \
  "personal-learning"

send_message 3 21 30 \
  "Texted Marcus about the Blazers game Saturday. He's confirmed. Also going to help him with his resume on Sunday. I should introduce him to Priya actually — her team might be hiring. Small world, big network." \
  "personal"
send_message 3 21 31 \
  "[Agent] Good follow-through on the Marcus connection. You mentioned introducing him to Priya earlier this week — that could actually work out well for both of them. Weekend plan: Blazers Saturday, resume help Sunday." \
  "personal"

send_message 3 22 0 \
  "Tomorrow's priorities: 1) Demo at 2pm — practice the script in the morning, 2) After demo — start the real fee engine, 3) Set up the CI/CD pipeline with GitHub Actions, 4) Begin writing API documentation. Also need to follow up on that apartment in the Pearl District. Goodnight." \
  "personal-planning"
send_message 3 22 1 \
  "[Agent] Big day tomorrow — demo at 2pm. You've earned a good night's sleep. Quick recap of open items: Pearl District apartment (lease up in April), dentist appointment, car registration this month, sister visiting next weekend. Goodnight!" \
  "personal-planning"

fi

# ═══════════════════════════════════════════════════════════════
# Summary
# ═══════════════════════════════════════════════════════════════

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Ingestion Complete"
echo "  Sent:    $SENT"
echo "  Failed:  $FAILED"
echo "  Skipped: $SKIPPED"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

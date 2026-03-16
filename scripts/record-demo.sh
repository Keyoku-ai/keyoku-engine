#!/usr/bin/env bash
# record-demo.sh — Capture heartbeat snapshots for the interactive demo
#
# Steps through the 3-day demo timeline hour-by-hour, calling the heartbeat
# context endpoint with virtual_now and analyze=true at each step.
# Saves snapshots where should_act=true to a JSON file for static replay.
#
# Prerequisites:
#   1. Run ingest-conversations.sh first to populate the database
#   2. Server must be running with the test-ingest database
#
# Usage:
#   KEYOKU_URL=http://localhost:18950 KEYOKU_TOKEN=dev-token ./record-demo.sh
#
# Output:
#   ../demo-snapshots.json (or specify with --output=<path>)

set -uo pipefail

KEYOKU_URL="${KEYOKU_URL:-http://localhost:18950}"
KEYOKU_TOKEN="${KEYOKU_TOKEN:-dev-token}"
ENTITY_ID="${ENTITY_ID:-alex}"
OUTPUT="$(dirname "$0")/../demo-snapshots.json"
STEP_HOURS=1

for arg in "$@"; do
  case "$arg" in
    --entity=*) ENTITY_ID="${arg#*=}" ;;
    --output=*) OUTPUT="${arg#*=}" ;;
    --step=*) STEP_HOURS="${arg#*=}" ;;
    --url=*) KEYOKU_URL="${arg#*=}" ;;
  esac
done

echo "╔══════════════════════════════════════════════════════════╗"
echo "║  Keyoku Demo Snapshot Recorder                          ║"
echo "╠══════════════════════════════════════════════════════════╣"
echo "║  URL:       $KEYOKU_URL"
echo "║  Entity:    $ENTITY_ID"
echo "║  Step:      ${STEP_HOURS}h"
echo "║  Output:    $OUTPUT"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

# Temp directory for intermediate files
TMPDIR_REC=$(mktemp -d)
trap "rm -rf $TMPDIR_REC" EXIT

# Verify server is reachable
if ! curl -sf "${KEYOKU_URL}/api/v1/health" > /dev/null 2>&1; then
  echo "ERROR: Cannot reach ${KEYOKU_URL}/api/v1/health"
  echo "Make sure the Keyoku server is running with the ingested database."
  exit 1
fi

# Fetch all memories for the recording
echo "Fetching all memories..."
MEMORIES_FILE="$TMPDIR_REC/memories.json"
curl -sf \
  -H "Authorization: Bearer ${KEYOKU_TOKEN}" \
  -H "Content-Type: application/json" \
  "${KEYOKU_URL}/api/v1/memories?entity_id=${ENTITY_ID}&limit=500" > "$MEMORIES_FILE"

if [ ! -s "$MEMORIES_FILE" ]; then
  echo "ERROR: Failed to fetch memories. Check server and auth token."
  exit 1
fi

# Compute time range from actual memory timestamps (not "3 days ago from now")
TIME_RANGE=$(python3 -c "
import json
from datetime import datetime, timezone, timedelta

with open('$MEMORIES_FILE') as f:
    d = json.load(f)
mems = d if isinstance(d, list) else d.get('memories', [])
print(f'count={len(mems)}')

if not mems:
    print('start=0')
    print('end=0')
else:
    timestamps = [datetime.fromisoformat(m['created_at'].replace('Z', '+00:00')) for m in mems]
    earliest = min(timestamps)
    latest = max(timestamps)
    # Start 2 hours before first memory, end 2 hours after last
    start = earliest - timedelta(hours=2)
    end = latest + timedelta(hours=2)
    print(f'start={int(start.timestamp())}')
    print(f'end={int(end.timestamp())}')
    print(f'earliest={earliest.isoformat()}')
    print(f'latest={latest.isoformat()}')
")

eval "$TIME_RANGE"  # sets count, start, end, earliest, latest

echo "Found ${count} memories in database."
echo "  Time range: ${earliest:-?} to ${latest:-?}"
echo ""

if [ "${start:-0}" -eq 0 ]; then
  echo "ERROR: No memories found, cannot compute time range."
  exit 1
fi

START_TS=$start
END_TS=$end
STEP_SECS=$((STEP_HOURS * 3600))

# Initialize stops file
STOPS_FILE="$TMPDIR_REC/stops.json"
echo "[]" > "$STOPS_FILE"

STOP_INDEX=0
CURRENT_TS=$START_TS
TOTAL_STEPS=$(( (END_TS - START_TS) / STEP_SECS ))
STEP_NUM=0

while [ "$CURRENT_TS" -lt "$END_TS" ]; do
  STEP_NUM=$((STEP_NUM + 1))

  VIRTUAL_NOW=$(date -r "$CURRENT_TS" -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || \
                date -d "@$CURRENT_TS" -u +%Y-%m-%dT%H:%M:%SZ)

  HUMAN_TIME=$(date -r "$CURRENT_TS" "+%a %b %d %I:%M %p" 2>/dev/null || \
               date -d "@$CURRENT_TS" "+%a %b %d %I:%M %p")

  printf "  [%d/%d] %s ... " "$STEP_NUM" "$TOTAL_STEPS" "$HUMAN_TIME"

  # Call heartbeat context with virtual_now and analyze=true
  RESPONSE_FILE="$TMPDIR_REC/response.json"
  HTTP_CODE=$(curl -s -o "$RESPONSE_FILE" -w "%{http_code}" \
    -X POST \
    -H "Authorization: Bearer ${KEYOKU_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "{
      \"entity_id\": \"${ENTITY_ID}\",
      \"analyze\": true,
      \"virtual_now\": \"${VIRTUAL_NOW}\",
      \"autonomy\": \"suggest\"
    }" \
    "${KEYOKU_URL}/api/v1/heartbeat/context")

  if [ "$HTTP_CODE" != "200" ] || [ ! -s "$RESPONSE_FILE" ]; then
    printf "FAILED (HTTP %s)\n" "$HTTP_CODE"
    CURRENT_TS=$((CURRENT_TS + STEP_SECS))
    continue
  fi

  # Check should_act and build stop entry if true
  SHOULD_ACT=$(python3 -c "
import json
with open('$RESPONSE_FILE') as f:
    r = json.load(f)
print('yes' if r.get('should_act', False) else 'no')
" 2>/dev/null)

  if [ "$SHOULD_ACT" = "yes" ]; then
    printf "SHOULD_ACT! (stop #%d)\n" "$STOP_INDEX"

    # Build stop entry and append to stops file
    python3 -c "
import json

with open('$RESPONSE_FILE') as f:
    response = json.load(f)
with open('$MEMORIES_FILE') as f:
    mem_data = json.load(f)
with open('$STOPS_FILE') as f:
    stops = json.load(f)

mems = mem_data if isinstance(mem_data, list) else mem_data.get('memories', [])
from datetime import datetime
vn = datetime.fromisoformat('${VIRTUAL_NOW}'.replace('Z', '+00:00'))
mem_count = sum(1 for m in mems
    if datetime.fromisoformat(m['created_at'].replace('Z', '+00:00')) <= vn)

stop = {
    'index': ${STOP_INDEX},
    'virtual_time': '${VIRTUAL_NOW}',
    'memory_count': mem_count,
    'heartbeat': response,
    'analysis': response.get('analysis', None),
}
stops.append(stop)

with open('$STOPS_FILE', 'w') as f:
    json.dump(stops, f)
" 2>/dev/null

    STOP_INDEX=$((STOP_INDEX + 1))
  else
    printf "quiet\n"
  fi

  CURRENT_TS=$((CURRENT_TS + STEP_SECS))
done

echo ""
echo "Recording complete: ${STOP_INDEX} stops captured."

if [ "$STOP_INDEX" -eq 0 ]; then
  echo "WARNING: No should_act=true events found. The heartbeat may need more signal data."
  echo "Try running seed-test-memories.sh or ingest-conversations.sh first."
  exit 1
fi

# Build final output
RECORDED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)

python3 -c "
import json

with open('$MEMORIES_FILE') as f:
    mem_data = json.load(f)
with open('$STOPS_FILE') as f:
    stops = json.load(f)

memories = mem_data if isinstance(mem_data, list) else mem_data.get('memories', [])

recording = {
    'recorded_at': '${RECORDED_AT}',
    'entity_id': '${ENTITY_ID}',
    'memories': memories,
    'stops': stops,
}

with open('${OUTPUT}', 'w') as f:
    json.dump(recording, f, indent=2)

print(f'Saved to ${OUTPUT}')
print(f'  - {len(memories)} total memories')
print(f'  - {len(stops)} heartbeat stops')
for i, s in enumerate(stops):
    analysis = s.get('analysis') or {}
    brief = analysis.get('action_brief', 'N/A')
    urgency = analysis.get('urgency', 'N/A')
    print(f'  Stop {i}: {s[\"virtual_time\"]} ({s[\"memory_count\"]} mems) [{urgency}] {brief[:60]}')
"

echo ""
echo "Done! Import this file into the dashboard as demo-snapshots.ts"

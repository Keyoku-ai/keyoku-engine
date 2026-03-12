# Heartbeat Architecture Review

## Summary

The current heartbeat design has a strong **decision layer** but a weaker **triggering model**.

- **Strong:** cooldowns, novelty suppression, urgency tiers, nudge backoff, quiet-hours suppression
- **Weak:** the engine-side watcher default of `10s` is too aggressive for normal user-facing deployments

## What the code does today

### Engine-side heartbeat
- `HeartbeatCheck()` in `heartbeat.go` runs a multi-check scan over memory state
- It builds a `HeartbeatResult` with categories like:
  - scheduled
  - deadlines
  - pending work
  - continuity
  - sentiment
  - relationship alerts
  - knowledge gaps
  - behavioral patterns
- `evaluateShouldAct()` decides whether the system should intervene

### Watcher
- `watcher.go` defines a background watcher loop
- default `Interval` is **10 seconds**
- each tick:
  - loops watched entities
  - runs `HeartbeatCheck()`
  - emits events if `ShouldAct`

### OpenClaw plugin path
- `@keyoku/openclaw` does **not** continuously run the watcher by default
- it injects heartbeat context during actual **OpenClaw heartbeat prompts**
- in other words:
  - normal conversation turn -> recall/capture path
  - heartbeat tick -> `/heartbeat/context` path

## Assessment

## 1. Decision quality is pretty good
The best part of the system is the decision logic:
- urgency tiering
- immediate signals bypass cooldown
- repeated identical signals suppressed by fingerprint
- nudges back off over time
- quiet-hours suppression exists

This is already much better than a naive proactive agent loop.

## 2. The watcher default is too tight
A `10s` default is not a good general-purpose setting.

Why:
- `HeartbeatCheck()` is not cheap
- it touches multiple query paths
- some checks involve similarity/embedding-assisted logic
- repeated scans create unnecessary churn for little extra user value

For a user-facing assistant, `10s` feels like an implementation convenience, not a product choice.

## 3. The current split is directionally correct
The separation between:
- **conversation-time memory behavior**
- **periodic heartbeat evaluation**

is good.

The main problem is not the heartbeat concept — it is how often the engine-side watcher would run if used directly.

## 4. Quiet-hours inference is currently too naive
The current active-hours / quiet-hours logic is UTC-based and inferred from recent message history.

That creates a bad failure mode:
- the system can decide the user is in a quiet window
- heartbeat gets suppressed with `suppress_quiet`
- but the user is actually active in their real local timezone

This is especially bad for proactive assistants, because it makes the system look passive or broken even when heartbeat ticks are firing correctly.

### Recommendation
Quiet-hours should be based on:
1. explicit user timezone, when known
2. inferred local timezone, when confidence is high
3. behavioral windows only as a fallback

Raw UTC should not be the final decision basis for user-facing suppression.

## Recommendation

## Recommended cadence

### For OpenClaw-driven heartbeat
Use **periodic heartbeat only when OpenClaw generates a heartbeat prompt**.

Good range:
- **5 min** if you want more responsiveness
- **15 min** if you want calmer, more human pacing

### For engine watcher mode
Change the default from `10s` to something like:
- **5 min default**
- **1 min** for explicit high-frequency ops modes
- **30-60s** only for narrow monitoring use cases

## Recommended product position
The watcher should be treated as:
- an **ops/event-streaming primitive**
- not the primary heartbeat path for conversational assistants

The primary path for assistants should remain:
- OpenClaw heartbeat tick -> plugin -> engine evaluation

## Concrete changes
1. Change `WatcherConfig` default interval from `10s` to `5m`
2. Document watcher as opt-in / advanced
3. Make it explicit that conversational heartbeat should be tick-driven, not tight-polled
4. Consider separating checks into:
   - cheap checks every tick
   - expensive semantic checks less often
5. Add metrics for:
   - heartbeat duration
   - result counts by category
   - suppress reasons
   - nudge frequency

## Bottom line

The architecture is **solid enough to build on**.

The main thing to fix is not the core intelligence model.
It is the **trigger cadence** and the distinction between:
- periodic proactive behavior
- conversation-time behavior
- optional watcher/event streaming

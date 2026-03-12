# Heartbeat Redesign Notes for a More Human Autonomous Feel

## Goal

Make the assistant feel:
- proactive
- context-aware
- restrained
- human

Not:
- twitchy
- repetitive
- bot-reminder-y

## Core principle

**Do not run the full heartbeat on every conversation turn.**

That will overfire and degrade the feeling.

Instead, split the system into distinct modes.

---

## Proposed model

## 1. Conversation-time mode
Runs on every normal user/assistant exchange.

### What should happen
- auto-capture
- auto-recall
- lightweight bookkeeping
- event extraction

### What should NOT happen
- full multi-category heartbeat scan
- broad proactive decisioning
- generic check-in logic

### Why
While a conversation is active, the user is already engaged.
The system should focus on being helpful **within the flow**, not trying to start a second flow.

---

## 2. Event-triggered micro-heartbeats
Run only for specific high-signal moments.

### Trigger examples
- a deadline is created or updated
- a reminder/schedule is created
- the agent makes a commitment
- conflict detection fires
- a strong negative sentiment spike appears
- a task/plan changes state

### Behavior
Only run a **narrow targeted check**, not the full heartbeat.

Examples:
- deadline created -> confirm urgency / next reminder timing
- commitment made -> store follow-up expectation
- strong negative sentiment -> mark support-sensitive state

### Why
This gives responsiveness without making every conversation turn expensive or intrusive.

---

## 3. Periodic heartbeat mode
Runs on real heartbeat ticks.

### This is where the full heartbeat belongs
- due schedules
- deadlines
- stale monitors
- continuity
- sentiment drift
- relationship silence
- patterns
- knowledge gaps
- nudge logic

### Recommended cadence
- **5 min** = responsive
- **15 min** = calmer / more human

My bias for a consumer-feeling assistant: **15 min is reasonable**.

---

## 4. Intervention policy layer
This is the part that most affects whether the system feels human.

### The system should speak only when:
- urgency changed
- the user is likely to benefit now
- the topic has not already been surfaced recently
- the interruption cost is low

### The system should stay quiet when:
- the user is actively chatting already
- the same topic was surfaced recently
- the signal is weak or low-confidence
- there is nothing meaningfully actionable

---

## Important redesign ideas

## A. Topic-level suppression, not just fingerprint suppression
Current fingerprinting is good, but not enough.

Need memory of:
- “already nudged about API migration today”
- “already reminded about Friday deadline this morning”
- “already did a support-style check-in recently”

This prevents the same issue from being rephrased repeatedly.

## B. Conversation-aware heartbeat suppression
If a real conversation is active, heartbeat behavior should downgrade.

### Suggested rule
If the user has spoken in the last N minutes:
- suppress generic nudges
- suppress low-priority reminders
- allow only immediate/important items

This likely explains why a heartbeat may feel absent during active conversation.
That is probably a good feature, not a bug.

## C. Fix quiet-hours before trusting them
Current quiet-hours logic should not rely on raw UTC windows.

If the user is active but the system thinks they are in a quiet period, heartbeat gets suppressed for the wrong reason.
That makes the whole proactive system feel dead.

### Recommended order of truth
1. explicit user timezone
2. inferred local timezone
3. behavior-derived quiet window

Behavioral quiet-hours should be a refinement, not the primary truth source.

## D. Promote continuity over generic reminders
The most human-feeling interventions are continuity-based.

Examples:
- “how did that migration go?”
- “you were in the middle of X”
- “did you ever hear back from Y?”

These feel far better than:
- “checking in”
- “you have pending work”

## D. Tighten what counts as speak-worthy
The full heartbeat can detect many things.
That does **not** mean all detected things should become messages.

Use a stricter gate:
- immediate deadlines
- explicit commitments
- stalled important projects
- strong emotional change
- resumed interrupted threads

Everything else should often remain internal.

## E. Add “style of intervention” selection
Not all proactive messages should sound the same.

Possible intervention styles:
- remind
- resume
- check-in
- warn
- support
- confirm

The system should pick the minimal style needed.

---

## Recommended architecture

### Layer 1: Memory loop
- capture
- recall
- conflict handling
- state updates

### Layer 2: Event loop
- targeted micro-checks for important events

### Layer 3: Heartbeat loop
- periodic full scan
- strong suppression policy

### Layer 4: Social policy
- active conversation suppression
- topic-level cooldowns
- quiet hours
- anti-repetition

---

## Product answer to: “why didn’t heartbeat trigger while chatting?”

Likely because it should not aggressively interrupt active conversation.

That is a good property.

The right design is:
- while chatting -> stay inside the current thread unless urgency is high
- when quiet -> proactive behavior becomes more appropriate

That is much closer to a human assistant feel.

---

## Concrete recommendations

1. Keep full heartbeat tied to heartbeat ticks, not every message
2. Add event-triggered micro-heartbeats for high-signal events
3. Add active-conversation suppression
4. Add topic-level anti-repeat memory
5. Make continuity a first-class intervention type
6. Raise the threshold for low-value check-ins
7. Reduce watcher/default polling cadence substantially

## Bottom line

To make this feel autonomous **and** human:
- be less eager
- be more situational
- remember what was already surfaced
- interrupt only when it actually matters

That is where the intelligence should go.

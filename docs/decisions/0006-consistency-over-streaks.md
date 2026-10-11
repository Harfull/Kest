# 0006. Consistency score instead of streaks

- Status: Accepted
- Date: 2026-10-11

## Context

Streaks can raise short-term persistence, but a broken long streak can trigger the "what the hell" effect and abandonment. Evidence on this is mixed and partly from other domains. See [research](../research/behavior-science/README.md).

## Decision

- Show rolling completion rates (7 and 28 days) and total days showing up. Do not display or reset to-zero streak counters.
- Count the tiny version of a habit as a completion.
- Use a "never miss twice" reframe after a miss.

## Consequences

- Less dramatic gamification, more forgiving and sustainable.
- We forgo a mechanic that some users find motivating; revisit with opt-in experiments.

# 0007. Custom persistent scheduler

- Status: Accepted
- Date: 2026-10-11

## Context

Existing Go cron libraries do not persist jobs, and DST behavior varies. Our jobs are user-facing, adaptive and must survive restarts. See [research](../research/storage-scheduling/README.md).

## Decision

- Build `internal/scheduler` backed by SQLite with explicit recurrence, DST and misfire rules, and an injected clock.
- Evaluate recurrence in the owner's local zone and store `next_run_at` in UTC.

## Consequences

- Behavior we fully control and can test with fake clocks.
- More code to own than a library; mitigated by a small, well-tested surface.

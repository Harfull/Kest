# 0003. SQLite through modernc.org/sqlite

- Status: Accepted
- Date: 2026-10-11

## Context

We need durable state for messages, habits, jobs and logs with no external service. CGO-based drivers complicate cross-compilation and minimal images. See [research](../research/storage-scheduling/README.md).

## Decision

- Use SQLite in WAL mode through the pure-Go `modernc.org/sqlite` driver.
- Set pragmas with the driver's `_pragma=name(value)` DSN form and verify them at startup.
- One write connection and a small read pool.
- Embedded SQL migrations tracked in `schema_migrations`, applied by a small in-house runner.

## Consequences

- No CGO; about 2 MB larger binary and slower bulk writes, which do not matter at this scale.
- A known silent-failure mode (ignored DSN parameters) is guarded by startup verification.
- Revisit if write concurrency ever becomes a problem.

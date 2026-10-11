# Storage and scheduling

## Question

Which embedded database and scheduling approach give Kest durable state, restart-safe reminders and correct timezone handling in one binary?

## Sources

- Secondary: [Comparing Go SQLite to C SQLite](https://notes.billmill.org/blog/2024/09/Comparing_golang_sqlite_to_C_sqlite.html)
- Secondary: [go-sqlite-bench](https://github.com/cvilsmeier/go-sqlite-bench)
- Secondary: [modernc sqlite WAL and SQLITE_BUSY issue](https://gitlab.com/cznic/sqlite/-/issues/115)
- Secondary: [netresearch/go-cron README (DST rules)](https://github.com/netresearch/go-cron)
- Secondary: [gocron v2](https://upd.dev/go-co-op/gocron)
- Secondary: [Temporal cron docs](https://docs.temporal.io/cron-job.md) on DST risk

## Findings

### SQLite

- `modernc.org/sqlite` is pure Go (no CGO), so cross-compilation and minimal containers stay simple. Cost: about 2 MB of binary size and slower bulk inserts than the C driver in an informal 2024 benchmark. Kest's write volume is tiny.
- **Gotcha:** modernc silently ignores unknown DSN parameters. WAL and busy timeout must be passed as `_pragma=journal_mode(WAL)` and `_pragma=busy_timeout(5000)`. One project found its databases silently running in delete journal mode. Always verify with `PRAGMA journal_mode` at startup and fail loudly.
- Use `_txlock=immediate` for write transactions to avoid writer deadlocks.
- A reported modernc issue shows `SQLITE_BUSY` under concurrent reads in goroutines. Mitigation: one writer connection (`SetMaxOpenConns(1)` on the write pool) and a small separate read pool, plus a concurrency test.
- Foreign keys are off by default and the pragma is ignored inside a transaction; set `foreign_keys(ON)` on every connection through the DSN.
- Migrations: embedded SQL files applied in order inside transactions, tracked in a `schema_migrations` table.

### Scheduling

- `robfig/cron` is unmaintained since 2020 per forks' claims and skips jobs in the spring-forward gap. `gocron` v2 offers fluent scheduling but no built-in persistence (its locker and elector are for multi-instance coordination).
- Kest's scheduling is not "run function every N minutes". It is **user-facing and adaptive**: reminders created in chat, daily check-ins inside windows, quiet hours, snooze, skip, adjust to sleep schedule, survive restarts without duplicates or silent loss.
- DST is the main correctness trap. Jobs near the transition can run zero, one or two times with naive libraries.

## Decision

1. **SQLite via `modernc.org/sqlite`**, WAL mode, single writer, verified pragmas, embedded migrations. See [ADR 0003](../../decisions/0003-sqlite-modernc.md).
2. **Custom persistent scheduler** (`internal/scheduler`):
   - Jobs stored in SQLite with `next_run_at` in UTC, an owner (`system`, `coach`, `plugin:<id>`), a recurrence rule, payload and state.
   - A single loop sleeps until the earliest `next_run_at`, claims due jobs transactionally, executes them with a recovered goroutine, then computes the next run.
   - Recurrence is expressed in **local wall-clock time plus IANA zone** and converted to UTC at scheduling time. DST rules: a time in the skipped hour runs at the first valid instant after the gap; a time in the repeated hour runs once, on the first occurrence.
   - Missed jobs after downtime follow a per-job policy: `run_once` (catch up once), `skip`, or `expire_after`. Coaching nudges default to `expire_after = 30m` so Kest never sends a stale "good morning" at 3 pm.
   - Time comes from an injected `Clock`, which makes DST and restart behavior unit-testable.
3. A small recurrence parser handles `daily`, `weekdays`, `weekly:<days>`, `every:<duration>` and 5-field cron for power users.

## Open questions

- Should we adopt a cron-expression library only for the parser? Defer; own the semantics first.
- Backup strategy for the SQLite file under Pterodactyl (`VACUUM INTO` nightly to `data/backups/`).

# Data model

SQLite in WAL mode via `modernc.org/sqlite`. One database file, `data/kest.db`. All timestamps are UTC (`INTEGER` unix milliseconds); local time is computed from the owner's timezone at the edges. Migrations are embedded SQL under `internal/store/migrations/`. See [ADR 0003](../decisions/0003-sqlite-modernc.md).

## Tables

### Core

| Table | Purpose | Key columns |
| --- | --- | --- |
| `schema_migrations` | Applied migration versions | `version`, `applied_at` |
| `profile` | Owner preferences as key/value JSON | `key`, `value` (timezone, targets, tone, quiet-hour overrides) |
| `messages` | Recent conversation turns | `id`, `ts`, `role`, `content`, `tool_calls`, `tokens`, `origin` |
| `memories` | Long-term facts and summaries | `id`, `ts`, `kind`, `content`, `origin`, `confidence`, `source_msg_id`, `embedding?` |
| `pending_actions` | Confirmations awaiting the owner | `id`, `ts`, `tool`, `args_json`, `summary`, `expires_at`, `state` |
| `audit_log` | Tool calls | `id`, `ts`, `plugin`, `tool`, `risk`, `confirmed`, `status`, `summary` |
| `llm_usage` | Cost accounting | `ts`, `slot`, `model`, `prompt_tokens`, `completion_tokens`, `cost_usd`, `generation_id` |

### Scheduling

| Table | Purpose |
| --- | --- |
| `jobs` | See [scheduler.md](scheduler.md) |
| `job_runs` | Run history: `job_id`, `started_at`, `finished_at`, `status`, `error` |

### Coaching

| Table | Purpose |
| --- | --- |
| `habits` | Habit definition and plan (`anchor`, `behavior`, `tiny_version`, `cadence`, `window`, `state`) |
| `habit_logs` | `habit_id`, `date`, `status` (`done`, `tiny`, `skipped`, `missed`), `note` |
| `checkins` | Slot, composed text hash, sent_at, responded_at, response_kind |
| `notifications` | Every proactive message: kind, variant, sent_at, delivered, responded, latency_ms |
| `sleep_events` | Raw signals: `ts`, `kind` (`bedtime`, `wake`, `activity`, `import`), `source` |
| `sleep_nights` | One row per sleep day: onset, wake, duration, confidence, source, user_confirmed |
| `workouts` | Date, type, duration, intensity, notes |
| `meals` | Date, slot, description, optional estimate, notes |
| `tasks` | Title, due, state, priority (focus module) |
| `experiments` | Variant assignments and outcomes (opt-in) |

### Plugins

| Table | Purpose |
| --- | --- |
| `plugin_kv` | `plugin`, `key`, `value`, scoped storage |
| `plugin_state` | Enabled flag, last init error, version |

Secrets and OAuth tokens are **not** in the database; they live in `data/secrets.json` (mode 0600) or environment variables.

## Conventions

- Primary keys: ULID text, except natural keys such as `(habit_id, date)`.
- Dates that are local calendar days use `TEXT` in `YYYY-MM-DD` with the zone recorded at write time.
- Foreign keys on; cascades only where deletion is meaningful.
- Raw `messages` are pruned after `retention_days` (default 90). Summaries, habit logs and sleep nights persist.
- Export: `/export` writes a JSON archive of all tables except secrets. `/forget` deletes everything after confirmation.

## Startup checks

On open the store runs `PRAGMA journal_mode`, `PRAGMA foreign_keys` and `PRAGMA busy_timeout` and refuses to start if they do not match expectations. This guards against the silent DSN-parameter failure documented in the research.

## Backup

A nightly job runs `VACUUM INTO 'data/backups/kest-YYYYMMDD.db'` and keeps the last 7 copies.

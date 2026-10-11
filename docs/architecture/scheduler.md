# Scheduler

Rationale and sources: [../research/storage-scheduling/](../research/storage-scheduling/README.md).

## Requirements

- Survive restarts without losing or duplicating jobs.
- Express "every day at 07:30 local time" correctly across DST changes and timezone moves.
- Support one-off reminders, recurring reminders, coach slots and plugin jobs.
- Be testable with a fake clock.
- Respect quiet hours and the notification budget (enforced by `coach`, not the scheduler).

## Job model

| Field | Notes |
| --- | --- |
| `id` | ULID |
| `owner` | `system`, `coach`, or `plugin:<id>` |
| `name` | Handler name, for example `coach.morning` |
| `payload` | JSON for the handler |
| `rule` | Recurrence rule (see below), empty for one-off |
| `zone` | IANA timezone the rule is evaluated in |
| `next_run_at` | UTC timestamp, indexed |
| `misfire` | `run_once`, `skip` or `expire_after:<duration>` |
| `state` | `scheduled`, `running`, `done`, `paused`, `failed` |
| `last_run_at`, `run_count`, `last_error` | Diagnostics |

## Recurrence rules

```text
daily@07:30
weekdays@06:45
weekly:mon,wed,fri@18:00
every:90m             (elapsed time, not wall-clock)
cron:30 7 * * 1-5     (5-field, evaluated in zone)
once@2026-10-12T09:00
```

Wall-clock rules are evaluated in `zone`, then converted to UTC for storage.

## DST rules

| Case | Behavior |
| --- | --- |
| Time falls in a skipped hour (spring forward) | Runs at the first valid instant after the gap |
| Time falls in a repeated hour (fall back) | Runs once, on the first occurrence |
| Midnight does not exist | Normalized to the next valid time |
| Owner changes timezone | Rules keep their wall-clock meaning in the new zone; `next_run_at` is recomputed; the sleep model flags a shift and the coach asks before assuming |

## Loop

1. Query the earliest `next_run_at` among `scheduled` jobs.
2. Sleep until then, or until woken by an insert or update (channel signal).
3. In one transaction, claim all due jobs: set `state = running`.
4. Dispatch each to a bounded worker pool with a deadline. Workers recover panics and record errors.
5. On completion, compute the next run from the rule using the **scheduled** time (not "now") to prevent drift, store it, set `state = scheduled`; one-off jobs become `done`.
6. On startup, any job left in `running` is treated as interrupted and handled by its misfire policy.

## Misfire policy

| Policy | When to use |
| --- | --- |
| `run_once` | Owner reminders: run once on recovery even if late |
| `skip` | Periodic housekeeping |
| `expire_after:30m` | Coaching nudges: a late "good morning" is worse than none |

Missed recurring jobs never replay in bulk; at most one catch-up run happens.

## Interfaces

```go
type Scheduler interface {
    Schedule(ctx context.Context, spec Spec) (JobID, error)
    Cancel(ctx context.Context, id JobID) error
    Pause(ctx context.Context, id JobID) error
    Upcoming(ctx context.Context, n int) ([]Job, error)
    Register(name string, h Handler)
    Run(ctx context.Context) error
}

type Handler func(ctx context.Context, job Job) error
```

## Testing

- Fake clock advances through DST transitions for several zones (America/New_York, Europe/London, Australia/Lord_Howe, a zone without DST).
- Crash simulation: kill the loop mid-run and restart against the same database.
- Property test: for any rule, successive `next_run_at` values are strictly increasing.

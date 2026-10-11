# Architecture overview

## Goals

1. Help one person stay consistent, through well-timed, well-worded, low-friction contact.
2. Be modular: every integration and every coaching behavior is replaceable without touching the core.
3. Run as a single static Go binary with a single data directory, hostable on Pterodactyl.
4. Be safe: an agent with inbox access must not be steerable by the content it reads.

## System context

```text
                 ┌──────────────────────────────────────────────┐
  Owner  ◀──────▶│ Telegram (long polling, owner-only)          │
                 └───────────────┬──────────────────────────────┘
                                 │ Update / Reply
 ┌───────────────────────────────▼───────────────────────────────┐
 │                           Kest process                        │
 │                                                               │
 │  telegram ──▶ agent ◀──▶ llm ──▶ OpenRouter                   │
 │                 │  ▲        (general / transcription / tts)   │
 │                 │  │                                          │
 │                 ▼  │                                          │
 │              plugin registry ──▶ native plugins (gmail, ...)  │
 │                 │              └▶ MCP servers (stdio / HTTP)  │
 │                 │              └▶ declarative HTTP plugins    │
 │                                                               │
 │  scheduler ──▶ coach ──▶ agent (proactive turns)              │
 │      ▲           │                                            │
 │      └───────────┴──────────▶ store (SQLite, WAL)             │
 └───────────────────────────────────────────────────────────────┘
```

## Layers and dependency rule

Dependencies point downward only. A package may import packages below it, never above.

```text
cmd/kest                     entrypoint
  └─ app                     wiring, lifecycle
       ├─ telegram           transport adapter
       ├─ agent              turn loop, prompts, memory, confirmations
       │    ├─ llm           OpenRouter client
       │    └─ plugin        registry and tool dispatch
       ├─ coach              habits, check-ins, sleep, budget
       ├─ scheduler          persistent jobs
       └─ store              SQLite repositories
  foundation: config, clock, logging, secrets, audit, version
plugins/*                    import internal/plugin only
```

- `plugins/*` never import `agent`, `coach`, `telegram`, `llm` or `store`. They see the world through the `plugin.Host` interface.
- `coach` does not import `telegram`. It emits intents (`SendMessage`, `AskQuestion`) through an `Outbox` interface that `app` connects to the transport.
- Only `app` constructs concrete types. Everything else receives interfaces.

## Core flows

### 1. Inbound message (owner to Kest)

1. `telegram` receives an update, checks the sender against `owner_ids`, drops anything else.
2. Voice notes are downloaded and sent to `llm` transcription; the transcript becomes the user message and the original is kept only if configured.
3. `agent` builds the prompt: persona, current local time, coach state summary, relevant memories, recent turns, available tools.
4. `agent` calls the general model. If it returns tool calls, each is policy-checked (risk tier, capability), confirmed with the owner when required, executed through the registry, and fed back. The loop is bounded.
5. The final text is sent as HTML. If the reply mode is voice, `llm` synthesizes MP3 and `telegram` sends it with `sendVoice`.
6. The turn is persisted: messages, tool calls (audit), cost, and any coach events (a logged workout, a sleep confirmation).

### 2. Proactive message (Kest to owner)

1. `scheduler` claims a due job (for example `coach.morning_checkin`).
2. `coach` consults the **notification budget**, **quiet hours** (from the sleep model) and recent responsiveness. It may skip, delay or reshape the message.
3. If it proceeds, `coach` asks `agent` to compose the message with a task-specific prompt and strict persona rules, or fills a template for simple prompts.
4. The result goes through `Outbox` to Telegram, usually with inline quick-reply buttons.
5. The reply (or silence) is recorded as feedback for timing adaptation.

### 3. Confirmation of a risky action

1. The model requests `gmail.send_email`. Policy marks it `destructive`.
2. `agent` stores a `pending_action` with the exact structured arguments and sends a message with Confirm and Cancel inline buttons rendered from those arguments.
3. The model turn pauses. When the owner taps Confirm, the stored action executes and the result returns to the model. Cancel or a 10 minute timeout aborts it.

### 4. Plugin event (for example, new important email)

1. A plugin implementing `EventSource` emits an `Event` to the registry.
2. `coach` or `agent` decides whether the event deserves a nudge, subject to the same budget and quiet hours.

## Concurrency model

- One `errgroup` per process lifetime owned by `app`. Components start in dependency order and stop in reverse.
- Telegram polling runs in its own goroutine. Each owner message is handled serially through a per-chat queue so turns never interleave.
- The scheduler loop is a single goroutine; job execution runs in bounded workers, each with panic recovery and a context deadline.
- SQLite: one write connection, a small read pool, WAL mode.
- Shutdown on SIGINT or SIGTERM: stop accepting updates, cancel the root context, wait up to 10 seconds, flush, close the database.

## Configuration layering

Defaults, then `config.toml` in the data directory, then `KEST_*` environment variables. Validation runs once at startup and reports every problem together. See [../guides/configuration.md](../guides/configuration.md).

## Observability

- Structured logs with `log/slog`; JSON in production, text for terminals.
- Redaction on by default for tokens, emails and message bodies.
- Metrics are in the database: `llm_usage` (tokens and cost per call), `notifications` (sent, responded, latency), `audit_log`. A `/status` command summarizes health, spend and next jobs.
- A `kest: ready` line is printed when startup completes; the Pterodactyl egg uses it as the "server is running" marker.

## Failure behavior

| Failure | Behavior |
| --- | --- |
| OpenRouter unavailable | Retry with backoff, fall back to the next model, then tell the owner calmly and queue proactive messages only if still relevant |
| Telegram send error | Retry on 429 using `retry_after`; log once |
| Plugin init fails | Disable the plugin, keep the rest running, report in `/status` |
| Database error | Fail the operation; fatal if migrations or pragma verification fail at startup |
| Process restart | Scheduler recomputes from the database; missed jobs follow their policy |

## Extensibility points

- New integration: add a plugin package ([plugins.md](plugins.md)).
- New coaching domain: add a coach module implementing `coach.Module` ([coach-engine.md](coach-engine.md)).
- New channel (for example Signal): implement the `telegram.Transport`-style interface in a new package; `agent` is channel-agnostic through `Outbox` and `Inbox`.

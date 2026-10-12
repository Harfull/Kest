# Kest

A personal assistant that helps you stay consistent.

Kest lives in Telegram. It keeps track of your workouts, meals, sleep and focus, checks in at the right moments, and talks to you like a steady friend: warm, direct, never preachy. Behind the chat it can read and send email, work with Notion, and connect to hundreds of other tools through a plugin system.

> Status: early development. Phases 1 and 2 are largely built: Kest answers text and voice notes in Telegram (owner-only), keeps history and memory in SQLite, runs a persistent scheduler, sets reminders, keeps a task list, reads web pages, and asks before doing anything risky. Habit coaching and email/Notion integrations come next. See the [roadmap](docs/roadmap.md).

## What makes Kest different

- **Built around consistency, not streaks.** Missing a day does not reset your progress or trigger guilt. Kest uses research-backed methods (tiny habits, if-then plans, autonomy-supportive coaching) and a gentle "never miss twice" rule. See [the coaching approach](docs/research/behavior-science/README.md).
- **Knows your rhythm.** It estimates your sleep from how you use the chat and from simple check-ins, then times its messages around it and respects quiet hours. See [sleep modeling](docs/research/sleep/README.md).
- **Talks like a person.** Not formal, not slangy. See the [voice guide](docs/persona/voice.md).
- **Modular and careful.** Every integration is a plugin with a manifest and a risk level. Anything that sends data out or deletes needs your tap on a button, and content from the internet can never talk Kest into acting. Built-in plugins are compiled in; [MCP](https://modelcontextprotocol.io) servers are planned. See [plugins](docs/architecture/plugins.md) and [security](docs/architecture/security.md).
- **Easy to host.** One static Go binary, one data folder, SQLite inside. Ships with a Pterodactyl egg. See [deployment](docs/deployment/pterodactyl.md).

## How it works

```text
You ──▶ Telegram ──▶ Kest ──▶ OpenRouter (general / transcription / tts models)
                      │
                      ├──▶ Coach engine (habits, sleep model, check-ins)
                      ├──▶ Scheduler (persistent, timezone-aware, quiet hours)
                      └──▶ Plugins (Gmail, SMTP, Notion, MCP servers, ...)
```

Full design: [architecture overview](docs/architecture/overview.md) and [file structure](docs/architecture/file-structure.md).

## Quick start

You need a Telegram bot token from [@BotFather](https://t.me/BotFather), an [OpenRouter](https://openrouter.ai) API key, and your numeric Telegram user ID.

```bash
export KEST_TELEGRAM_TOKEN=123456:abc
export KEST_OPENROUTER_API_KEY=sk-or-...
export KEST_OWNER_ID=123456789
go run ./cmd/kest
```

Configuration reference: [docs/guides/configuration.md](docs/guides/configuration.md). Example files: [config.example.toml](config.example.toml) and [.env.example](.env.example).

## Hosting

- **Pterodactyl:** import `egg-kest.json` from the [latest release](https://github.com/Harfull/Kest/releases) (or [the file in this repo](deploy/pterodactyl/egg-kest.json)). Guide: [docs/deployment/pterodactyl.md](docs/deployment/pterodactyl.md). Versioning: [docs/guides/releasing.md](docs/guides/releasing.md).
- **Docker:** see [deploy/docker/Dockerfile](deploy/docker/Dockerfile).

## Documentation

| Area | Where |
| --- | --- |
| Conventions for contributors and AI agents | [AGENTS.md](AGENTS.md) |
| Roadmap | [docs/roadmap.md](docs/roadmap.md) |
| Architecture | [docs/architecture/](docs/architecture/) |
| Research notes and sources | [docs/research/](docs/research/) |
| Architecture decision records | [docs/decisions/](docs/decisions/) |
| Voice and persona | [docs/persona/](docs/persona/) |
| Guides | [docs/guides/](docs/guides/) |

## Development

```bash
make check   # format check, vet, lint, tests
make build   # builds ./bin/kest
```

Requires Go (version pinned in `go.mod`) and [golangci-lint](https://golangci-lint.run). Read [AGENTS.md](AGENTS.md) before contributing.

## Privacy

Kest is single-user and self-hosted. Your data stays in your data directory. Messages are sent to OpenRouter and to the services you connect, and nowhere else. See [security](docs/architecture/security.md).

## Not medical advice

Kest is a habit and scheduling aid. It does not diagnose, treat or give medical guidance. Talk to a professional about health concerns.

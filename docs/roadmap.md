# Roadmap

Phases are ordered so each one leaves a working, deployable Kest. Within a phase, work lands in batches (see [AGENTS.md](../AGENTS.md) section 7.3). Status key: ✅ done, 🚧 in progress, ⬜ planned.

## Phase 0: Foundation ✅

Repository, conventions, research, architecture, hosting artifacts.

- ✅ `AGENTS.md`, README, changelog, gitignore, editor and lint config
- ✅ CI (format, vet, lint, test, build), CodeQL, docs lint, release workflow, Dependabot
- ✅ Research notes for every design area
- ✅ Architecture, plugin spec, data model, security, coach engine, scheduler docs
- ✅ ADRs 0001 to 0007
- ✅ Persona and voice guide
- ✅ Pterodactyl egg, install script, Docker image
- ✅ Go skeleton: `cmd/kest`, config loading, clock, version, app lifecycle

## Phase 1: Talking ⬜

Kest answers in Telegram, owner-only, with real persistence.

- ⬜ `store`: SQLite open, pragma verification, migrations, repositories
- ⬜ `logging` with redaction, `secrets` file, `audit`
- ⬜ `telegram`: long polling, owner guard, HTML formatting and splitting, inline keyboards
- ⬜ `llm`: OpenRouter chat, retries, fallbacks, usage and budget
- ⬜ Voice in: download voice note, transcribe
- ⬜ Voice out: TTS to MP3, `sendVoice`
- ⬜ `scheduler`: persistent jobs, recurrence, DST tests, misfire policy
- ⬜ Commands: `/start`, `/help`, `/status`, `/settings`, `/export`, `/forget`
- ⬜ Egg verified on a real panel; release v0.1.0

Exit criteria: send a text or voice note and get a text or voice reply; restart without losing scheduled reminders.

## Phase 2: Tools ⬜

The agent can act, safely.

- ⬜ `plugin` framework: interfaces, manifest, registry, capabilities, risk, contract tests
- ⬜ `agent`: turn loop, tool calling, iteration caps, confirmations, untrusted data wrapping
- ⬜ Memory with provenance and summaries
- ⬜ Plugins: `reminders`, `tasks`, `webfetch`
- ⬜ `scripts/genplugins` and `plugins/all`
- ⬜ Model evaluation harness for persona and tool use

Exit criteria: "remind me to stretch at 3 and tomorrow at 9" works; a prompt-injection test suite passes.

## Phase 3: Coach core ⬜

The product's heart.

- ⬜ Habits with if-then plans; logs
- ⬜ Consistency metrics (rates, showing-up days)
- ⬜ Daily rhythm: morning, midday, evening, weekly
- ⬜ Notification budget, cooldowns, quiet hours
- ⬜ Sleep model v1: explicit events, confirmations, chat-gap inference, confidence
- ⬜ Lapse protocol and messages
- ⬜ Message templates as data; banned-phrase validator

Exit criteria: a 60 day simulated owner never receives a message in quiet hours or above budget; a user can set up a habit in under two minutes.

## Phase 4: Integrations ⬜

- ⬜ `gmail` (read, search, draft, send with confirmation)
- ⬜ `mailbox` (SMTP and IMAP)
- ⬜ `notion` (search, read, create, update; optional daily log mirror)
- ⬜ Workout, nutrition and focus modules
- ⬜ Email-driven nudges (opt-in)

## Phase 5: Reach and adaptation ⬜

- ⬜ MCP client plugin (stdio, then HTTP)
- ⬜ Declarative HTTP plugins
- ⬜ `calendar` plugin; schedule-aware planning
- ⬜ Timing adaptation (bandit) and opt-in experiments
- ⬜ Weekly insight reports

## Phase 6: Hardening and ecosystem ⬜

- ⬜ `pkg/kestsdk` public plugin SDK and authoring guide
- ⬜ Optional webhook mode
- ⬜ Backups and restore tooling
- ⬜ Wearable/sleep import plugin
- ⬜ Evaluate WASM plugin tier
- ⬜ Pelican panel compatibility check

## Backlog

- Streaming replies with `sendMessageDraft`
- Multi-language support
- Shared accountability (a trusted person receives weekly summaries, owner-controlled)
- Habit templates and library
- Home automation plugin via declarative tier

## Risks

| Risk | Mitigation |
| --- | --- |
| Messages feel naggy and get muted | Budget, adaptation, easy volume control, check preferences after silence |
| Prompt injection through email or web | Risk tiers, confirmations, untrusted wrappers, test suite |
| API drift (OpenRouter models, Notion versions, Telegram) | Models and versions are config; contract tests with recorded fixtures |
| Gmail OAuth friction | Offer IMAP/SMTP app-password path first |
| Sleep inference inaccurate | Always show confidence, ask for quick confirmations, never act on low confidence alone |
| Health sensitivity | No medical advice, no extreme targets, safe-completion path |

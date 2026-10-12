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

## Phase 1: Talking 🚧

Kest answers in Telegram, owner-only, with real persistence.

- ✅ `store`: SQLite open, verified pragmas, migrations, repositories, export and forget, backups
- ✅ `logging` with redaction of secrets, token shapes, emails and message content
- ✅ `telegram`: long polling, owner guard, HTML formatting and splitting, inline buttons, documents
- ✅ `llm`: OpenRouter chat with tool-call types, retries, fallbacks, usage and daily budget
- ✅ Voice in: download voice note, transcribe
- ✅ Voice out: TTS to MP3, `sendVoice`, falling back to text
- ✅ `scheduler`: persistent jobs, recurrence, DST tests, misfire policy
- ✅ Commands: `/start`, `/help`, `/status`, `/export`, `/forget` (with an expiring confirmation)
- ✅ Nightly maintenance job: backup (last 7 kept) and pruning of old messages
- ✅ `/settings` (shows check-ins and quiet hours; changes are made by asking) and `/lockdown`
- ✅ `secrets.json` in the data dir (mode 0600 enforced) and an `audit` package around the audit table, with `/audit`
- ⬜ Egg verified on a real panel; release v0.1.0

Exit criteria: send a text or voice note and get a text or voice reply; restart without losing scheduled reminders. The code paths are covered by automated tests against fake Telegram and OpenRouter servers; a live end-to-end run with a real bot token is still to be done by the owner.

## Phase 2: Tools 🚧

The agent can act, safely.

- ✅ `plugin` framework: interfaces, manifest, registry, capabilities, risk, SSRF-safe HTTP, `plugintest` and a contract check
- ✅ `agent`: tool loop with iteration and call caps, risk policy, taint tracking, approvals with exact arguments
- ✅ Untrusted-content wrapping that cannot be closed from inside
- ✅ Memory with provenance: `remember`, `recall`, `forget_fact`, quarantine after untrusted reads, summaries that skip tainted replies
- ✅ Plugins: `reminders`, `tasks`, `webfetch`
- ✅ `scripts/genplugins` and `plugins/all`, checked in CI
- ✅ Prompt-injection test suite (policy, wrapper breakout, exfiltration attempts, memory poisoning, forged approvals)
- ✅ Model evaluation harness (opt-in, real API): tone, tool use, injection resistance
- ✅ Run the evaluation against candidate models and record the results ([results](research/llm-openrouter/model-evaluation-2026-10.md))
- ✅ Cheap tool router for large tool sets: no model call; inactive up to 24 tools

Exit criteria: "remind me to stretch at 3 and tomorrow at 9" works; a prompt-injection test suite passes. Both hold in automated tests against a scripted model; a live check with a real model and a real bot token is still to be done.

## Phase 3: Coach core ✅

The product's heart.

- ✅ Habits with if-then plans; logs
- ✅ Consistency metrics (rates, showing-up days)
- ✅ Daily rhythm: morning, habit prompts (midday), evening, weekly
- ✅ Notification budget, cooldowns, quiet hours
- ✅ Sleep model v1: explicit events, confirmations, chat-gap inference, confidence
- ✅ Lapse protocol and messages
- ✅ Message templates as data; banned-phrase validator

Exit criteria: a 60 day simulated owner never receives a message in quiet hours or above budget (tested in `TestTwoMonthSimulation`); a user can set up a habit in under two minutes (the agent asks three questions and calls `coach__create_habit`; timing it needs a live run).

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

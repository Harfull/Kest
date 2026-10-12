# Changelog

All notable changes to Kest are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Repository conventions in `AGENTS.md`.
- Repository foundation: README, gitignore, editor config, Makefile, lint config and GitHub Actions workflows (CI, CodeQL, docs, release) with Dependabot.
- Research notes for OpenRouter, Telegram, Pterodactyl hosting, integrations, storage and scheduling, behavior science, sleep estimation and security.
- Architecture documentation: overview, file structure, plugin system, coach engine, scheduler, data model and security.
- Roadmap and seven architecture decision records.
- Persona and voice guide with the system prompt core.
- Pterodactyl egg (generated from `deploy/pterodactyl/install.sh` by `scripts/sync-egg`), Dockerfile and Compose file.
- Go skeleton: configuration loading with TOML and `KEST_*` overrides, validation, secret redaction, injectable clock, structured logging, and a lifecycle that prints `kest: ready` and shuts down gracefully.
- Deployment and operations guides.
- SQLite store with verified WAL pragmas, embedded migrations, serialized writer, backups, data export and forget.
- Log redaction of configured secrets, credential-shaped strings, email addresses and message content.
- OpenRouter client: chat with tool-call types and fallbacks, transcription, text to speech, retries with `Retry-After`, usage accounting and a daily budget.
- Persistent scheduler with daily, weekday, weekly, interval and cron rules, DST-correct wall-clock handling, misfire policies and crash recovery.
- Owner-only Telegram transport with ordered delivery, HTML formatting, message splitting, inline buttons, voice and documents.
- Persona-driven conversation, kept in sync with `docs/persona/prompt-core.md` by a test.
- Working bot: text and voice conversations, `/start`, `/help`, `/status`, `/export`, `/forget` with an expiring confirmation, and nightly backup and pruning.

### Changed

- CodeQL runs only when the repository is public or has Advanced Security; `govulncheck` runs in CI for all repositories.

### Fixed

- Scheduler no longer reports a cancelled context as a failure when shutdown races startup.

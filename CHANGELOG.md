# Changelog

All notable changes to Kest are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed

- Recognize Calagopus's blocked Chromium `sys_chroot` operation as a sandbox launch failure and document the host-policy requirement and explicit administrator override.

- Report safe, specific Chromium launch errors for sandbox, missing browser/libraries, display, profile-lock and permission problems, rather than hiding them behind a generic tool error.

- Use a plain Docker image reference in the browser egg, avoiding `invalid reference format` when a daemon does not process Wings' special local-image prefix.

- Include the secrets configuration Go source in Git and Docker builds; broad secret-file ignore rules previously omitted it and caused undefined configuration symbols in fresh checkouts.

### Added

- An optional Chromium browser plugin using Playwright, with persistent cookies and logins, page reading, navigation, clicks, fields, keys, scrolling, inbox uploads, local screenshots and tab management. Outbound actions require Telegram approval and browser content is untrusted.
- A browser-specific Pterodactyl egg, locked browser runtime image, standalone Docker/Compose and binary setup, and optional Xvfb/noVNC viewing through a private tunnel. Browser traffic uses an authenticated local proxy that blocks private destinations; Chromium's sandbox stays enabled by default.

## [0.1.0] - 2026-10-12

First stable release. It adds the coach, the audit log, a secrets file and a tool router on top of the two betas, and picks defaults that exist on OpenRouter today. The Pterodactyl egg was tried on a real panel with beta 1.

### Changed

- The default general model is now `anthropic/claude-haiku-5.5`. It passed every check in the model evaluation, which now has recorded results for four current cheap models.
- The default text-to-speech model is now `google/gemini-3.1-flash-tts-preview` with the voice `Kore`, because the previous default no longer exists on OpenRouter. Speech models need an explicit voice, so Kest also falls back to a built-in voice for the model family when the configured one is rejected.

### Added

- A tool router: once more than 24 tools are loaded, each turn shows the model the memory and coach tools plus the 14 best matches for the message, by word overlap with plugin and tool names and descriptions. It makes no model call, and below 24 tools nothing changes.
- `/audit` shows what Kest has done on your behalf. Entries are written by a new `audit` package that scrubs secrets, emails and control characters and keeps summaries short. Refused lockdown actions are recorded too.
- A `secrets.json` file in the data directory, as the last place Kest looks for `KEST_*` values. On Linux and macOS it must be mode 0600 or Kest refuses to start.
- A `.env` file in the working directory is read for local runs. Real environment variables still win.
- The coach: habits with if-then plans and a tiny version, rolling consistency instead of streaks, a gentle lapse protocol, and a daily rhythm of morning check-in, habit prompts, evening log and a Sunday review, with an opt-in wind-down.
- A notification budget: a daily cap, cooldowns, no messages in quiet hours, and a back-off ladder that asks "message less?" when messages go unanswered.
- A sleep model that learns from bedtime and wake messages, chat gaps and button confirmations, and sets quiet hours from them. Estimates are always approximate.
- Agent tools for habits, sleep and check-in preferences, and coaching context in the system prompt.
- `/settings` to see check-in settings and quiet hours, and `/lockdown` to stop all writes, sends and unprompted messages until restart.

## [0.1.0-beta.2] - 2026-10-12

Fixes found while testing beta 1 on a real panel.

### Fixed

- Voice replies failed with models that only return PCM audio, such as Gemini TTS (`Gemini TTS only supports response_format="pcm"`). Kest now learns each speech model's format from its error, encodes PCM to Ogg/Opus in-process (no ffmpeg needed), and falls back to the model's default voice if the configured one is rejected.

## [0.1.0-beta.1] - 2026-10-11

First beta, for testing the Pterodactyl egg and the Telegram bot end to end. Everything below was built before this release.

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
- Plugin framework: manifests with typed settings, a registry that rejects code that disagrees with its manifest, per-plugin storage, jobs and secrets, and an HTTP client that refuses private and metadata addresses.
- Agent tool loop with caps, a risk policy, taint tracking after untrusted content, and Telegram approvals that show the exact arguments and fail closed.
- Untrusted-content wrapping, memory with provenance and quarantine, summarization that skips tainted replies, and a voice checker.
- Plugins: `reminders`, `tasks` and `webfetch`, plus `scripts/genplugins` to keep `plugins/all` current.
- Prompt-injection test suite and an opt-in model evaluation harness (`go test -tags eval`).

### Changed

- CodeQL runs only when the repository is public or has Advanced Security; `govulncheck` runs in CI for all repositories.

### Fixed

- Scheduler no longer reports a cancelled context as a failure when shutdown races startup.

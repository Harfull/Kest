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

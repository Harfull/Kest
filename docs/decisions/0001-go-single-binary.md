# 0001. Go, single static binary, single data directory

- Status: Accepted
- Date: 2026-10-11

## Context

Kest must be hostable on Pterodactyl, run on small servers, start fast, and be easy to back up. The owner chose Go.

## Decision

- Implement in Go and ship one static binary (`CGO_ENABLED=0`) for linux/amd64 and linux/arm64.
- Keep all state under one data directory (`KEST_DATA_DIR`).
- Embed timezone data (`time/tzdata`), migrations and message templates in the binary.
- Require no external services (no Redis, no Postgres).

## Consequences

- Simple installs and tiny containers; trivial backups.
- Dependencies must be pure Go (see ADR 0003).
- Some ecosystems with only CGO libraries are off the table.

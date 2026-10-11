# 0002. Telegram via go-telegram/bot with long polling

- Status: Accepted
- Date: 2026-10-11

## Context

The bot runs inside a Pterodactyl container that may have no public HTTPS endpoint. Telegram supports long polling (outbound only) and webhooks (public HTTPS on ports 443, 80, 88 or 8443). See [research](../research/telegram/README.md).

## Decision

- Use `github.com/go-telegram/bot` (MIT, zero dependencies, context-based) behind our own `Transport` interface.
- Use long polling by default. Webhook mode may be added later behind config.
- Accept updates only from configured owner IDs.

## Consequences

- Works anywhere with outbound HTTPS.
- Slightly higher latency than webhooks, irrelevant for a single user.
- Swapping the library only touches `internal/telegram`.

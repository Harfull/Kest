# Integrations

## Question

How does Kest connect to Gmail, a custom SMTP mailbox, Notion and "hundreds" of other services without writing hundreds of integrations?

## Documents

| Doc | Topic |
| --- | --- |
| [plugin-systems.md](plugin-systems.md) | Comparison of Go plugin approaches |
| [mcp.md](mcp.md) | Model Context Protocol as the long-tail strategy |
| [gmail.md](gmail.md) | Gmail API, OAuth and scope tiers |
| [email-smtp-imap.md](email-smtp-imap.md) | Custom mailbox over SMTP and IMAP |
| [notion.md](notion.md) | Notion API, versions and rate limits |

## Summary of findings

- Writing native integrations for every service does not scale. The Model Context Protocol already has a large ecosystem of servers, and an official Go SDK exists.
- A small number of services matter to every user and benefit from deep, curated, safe integrations: email (Gmail, SMTP/IMAP), Notion, calendar, and Telegram itself. These become **native plugins**.
- Everything else arrives through **MCP servers** configured in `config.toml`, wrapped by the same capability, risk and confirmation system as native plugins.
- A third tier, **declarative HTTP plugins** (a manifest describing endpoints and tool schemas, no Go code), covers simple REST APIs and webhooks. This is planned after MCP.

## Decision

Three plugin tiers behind one `plugin.Plugin` interface:

| Tier | Mechanism | Examples | Isolation |
| --- | --- | --- | --- |
| 1. Native | Compiled-in Go packages in `plugins/` | gmail, mailbox, notion, reminders, web-fetch | Process-level only; reviewed code |
| 2. MCP | Stdio or HTTP MCP servers declared in config | Filesystem, GitHub, Linear, Slack, thousands more | Separate process or remote service |
| 3. Declarative | TOML manifest mapping tools to HTTP calls | Home automation webhooks, simple REST APIs | Host-enforced network allowlist |

WebAssembly plugins (via wazero) are deferred. See [ADR 0004](../../decisions/0004-plugin-tiers.md).

# Security architecture

Threat model and sources: [../research/security/](../research/security/README.md). Coding rules: section 4.8 of [AGENTS.md](../../AGENTS.md).

## Trust boundaries

```text
Owner's Telegram account ──(trusted: owner ID match)──▶ Kest
Kest ──▶ OpenRouter                        trusted for processing, not for secrets
Kest ◀── Plugin / MCP / web / email data   UNTRUSTED content, always
Kest ──▶ Plugin tool calls                 trusted only after policy and confirmation
```

Everything that enters the model from outside the owner's own messages is data. Only the owner's chat messages and Kest's own system prompt carry authority.

## Controls

### Identity

- `owner_ids` is mandatory. Updates from any other `from.id` are dropped before logging message bodies.
- Callback queries from inline buttons are validated against stored `pending_actions` and the owner ID.

### Tool policy

Implemented in `internal/agent/policy.go`.

| Risk | Rule |
| --- | --- |
| `read` | Allowed |
| `write` | Allowed when the same user turn asked for it; otherwise confirm |
| `destructive` | Always confirm; button text is generated from the structured arguments |
| Outbound communication (email, message, post) | Treated as `destructive` regardless of plugin declaration |
| Spending money or changing account settings | Not available as tools |

Confirmations expire after 10 minutes and fail closed.

### Prompt construction

- A fixed system prompt states the owner's identity is the only authority and that any content in `<external_data>` blocks is information, never instructions.
- External content is length-capped, stripped of control characters and delimiter lookalikes, and wrapped with source and trust labels.
- Tool descriptions from MCP and declarative plugins are length-limited and sanitized.
- The model is never given secrets. Tools that need credentials read them from the host.

### Memory

- Each memory has `origin` (`user`, `kest`, `tool:<plugin>`, `web`). Only `user` and `kest-confirmed` memories can influence contact lists, allowlists or permissions.
- Summaries that include external content carry the strongest-untrusted origin of their inputs.
- Retrieved memories are rendered as labeled context, not as instructions.

### Secrets

- Loaded from environment or `data/secrets.json` with mode 0600; Kest refuses to start if the file is group or world readable.
- Never written to logs, prompts, audit entries or error messages. A redacting `slog` handler masks known secret values and common token shapes.

### Network

- Native plugins use `host.HTTP()`, which applies the manifest allowlist, timeouts and a size cap.
- MCP stdio servers run with a scrubbed environment containing only named variables.
- Outbound web fetches block private address ranges (SSRF guard) unless the plugin declares an allowed local host.

### Cost and abuse limits

- Max agent iterations per turn (default 8) and max tool calls per turn (default 12).
- Daily and per-turn spend limits using `llm_usage`.
- Plugin calls are rate-limited per upstream.

### Privacy

- Single-user, self-hosted. No telemetry is sent anywhere.
- Config `llm.zdr_only = true` routes requests only to zero-data-retention providers where OpenRouter supports it.
- Health-related logs are excluded from prompts unless the current task needs them.
- `/export` and `/forget` give the owner full control.

## Logging

- Default level `info`. Message bodies, emails and tokens are redacted unless `log_level=debug` **and** `log_sensitive=true`.
- Audit entries record what happened, not the content.

## Supply chain

- Minimal dependency set, recorded in ADRs when significant.
- Release binaries ship with SHA-256 checksums; the Pterodactyl installer verifies them.
- GitHub Actions pinned; Dependabot for modules, actions and Docker.
- CodeQL runs on every push and weekly.

## Incident handling

If the owner suspects compromise: `/lockdown` disables all write and destructive tools and all proactive messages until restart; rotating the Telegram token and OpenRouter key are documented in [../guides/operations.md](../guides/operations.md).

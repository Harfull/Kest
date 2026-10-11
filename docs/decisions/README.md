# Architecture decision records

ADRs capture decisions that are hard to reverse. They are append-only: to change a decision, add a new ADR that supersedes the old one and update the status of the old one.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](0001-go-single-binary.md) | Go, single static binary, single data directory | Accepted |
| [0002](0002-telegram-long-polling.md) | Telegram via go-telegram/bot with long polling | Accepted |
| [0003](0003-sqlite-modernc.md) | SQLite through modernc.org/sqlite | Accepted |
| [0004](0004-plugin-tiers.md) | Three plugin tiers: native, MCP, declarative | Accepted |
| [0005](0005-openrouter-thin-client.md) | Thin in-house OpenRouter client with three model slots | Accepted |
| [0006](0006-consistency-over-streaks.md) | Consistency score instead of streaks | Accepted |
| [0007](0007-custom-scheduler.md) | Custom persistent scheduler | Accepted |

## Template

```markdown
# NNNN. Title

- Status: Proposed | Accepted | Superseded by NNNN
- Date: YYYY-MM-DD

## Context
## Decision
## Consequences
```

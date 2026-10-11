# Configuration

Kest reads configuration in layers. Later layers win.

1. Built-in defaults
2. `config.toml` (default location: `<data dir>/config.toml`, or the path in `KEST_CONFIG` or `--config`)
3. `KEST_*` environment variables (blank values are ignored)

Unknown keys in `config.toml` are an error, so typos are caught at startup. All problems are reported together. Examples: [config.example.toml](../../config.example.toml) and [.env.example](../../.env.example). Both are checked by tests so they cannot drift from the code.

## Required

| Env var | TOML | Meaning |
| --- | --- | --- |
| `KEST_TELEGRAM_TOKEN` | `telegram.token` | Bot token from @BotFather |
| `KEST_OWNER_ID` | `telegram.owner_ids` | Numeric Telegram user ID(s) allowed to use Kest, comma separated in the env var |
| `KEST_OPENROUTER_API_KEY` | `llm.api_key` | OpenRouter key |

Prefer environment variables for secrets. Secrets are never printed or logged.

## General

| Env var | TOML | Default | Meaning |
| --- | --- | --- | --- |
| `KEST_DATA_DIR` | `data_dir` | `./data` | Database, config, backups |
| `KEST_CONFIG` | none | `<data dir>/config.toml` | Config file path |
| `KEST_TIMEZONE` | `timezone` | `UTC` | IANA zone for all schedules |

## Logging

| Env var | TOML | Default | Meaning |
| --- | --- | --- | --- |
| `KEST_LOG_LEVEL` | `log.level` | `info` | `debug`, `info`, `warn`, `error` |
| `KEST_LOG_FORMAT` | `log.format` | `text` | `text` or `json` |
| `KEST_LOG_SENSITIVE` | `log.sensitive` | `false` | Allow message content in debug logs |

## Models and budget

| Env var | TOML | Default | Meaning |
| --- | --- | --- | --- |
| `KEST_MODEL_GENERAL` | `llm.models.general` | `google/gemini-3-flash-preview` | Conversation and tools; must support tool calling |
| `KEST_MODEL_FALLBACKS` | `llm.models.fallbacks` | none | Tried in order if the general model fails |
| `KEST_MODEL_TRANSCRIPTION` | `llm.models.transcription` | `openai/gpt-4o-mini-transcribe` | Voice notes to text |
| `KEST_MODEL_TTS` | `llm.models.tts` | `openai/gpt-4o-mini-tts-2025-12-15` | Voice replies |
| `KEST_TTS_VOICE` | `llm.tts_voice` | `alloy` | Voice name for the TTS model |
| `KEST_OPENROUTER_BASE_URL` | `llm.base_url` | `https://openrouter.ai/api/v1` | API base |
| `KEST_ZDR_ONLY` | `llm.zdr_only` | `false` | Only route to zero-data-retention providers where supported |
| `KEST_DAILY_BUDGET_USD` | `llm.daily_budget_usd` | `1.00` | Spend guard; `0` disables |

Model names are OpenRouter slugs and change over time. Check [openrouter.ai/models](https://openrouter.ai/models) and filter by tool support (general) or by audio output/transcription.

## Coaching

| Env var | TOML | Default | Meaning |
| --- | --- | --- | --- |
| `KEST_MAX_PROACTIVE_PER_DAY` | `coach.max_proactive_per_day` | `3` | Cap on unsolicited messages (0 to 20) |
| `KEST_QUIET_START` | `coach.quiet_start` | `23:00` | Local quiet hours start, until the sleep model has data |
| `KEST_QUIET_END` | `coach.quiet_end` | `07:00` | Local quiet hours end |

Most behavior (tone, volume, check-in times) is changed by chatting with Kest or through `/settings`, not through this file.

## Pterodactyl-only

| Env var | Meaning |
| --- | --- |
| `KEST_VERSION` | Release the installer downloads. Read by the install script, not by the binary |

## Plugins

Plugin settings live under `[plugins.<id>]` in `config.toml`, and MCP servers under `[[mcp.servers]]`. These sections arrive with the plugin framework in Phase 2; the format is specified in [../architecture/plugins.md](../architecture/plugins.md).

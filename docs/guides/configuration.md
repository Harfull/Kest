# Configuration

Kest reads configuration in layers. Later layers win.

1. Built-in defaults
2. `config.toml` (default location: `<data dir>/config.toml`, or the path in `KEST_CONFIG` or `--config`)
3. `KEST_*` environment variables (blank values are ignored). For local runs, a `.env` file in the working directory supplies any variable the real environment leaves unset

Unknown keys in `config.toml` are an error, so typos are caught at startup. All problems are reported together. Examples: [config.example.toml](../../config.example.toml) and [.env.example](../../.env.example). Both are checked by tests so they cannot drift from the code.

## Required

| Env var | TOML | Meaning |
| --- | --- | --- |
| `KEST_TELEGRAM_TOKEN` | `telegram.token` | Bot token from @BotFather |
| `KEST_OWNER_ID` | `telegram.owner_ids` | Numeric Telegram user ID(s) allowed to use Kest, comma separated in the env var |
| `KEST_OPENROUTER_API_KEY` | `llm.api_key` | OpenRouter key |

Prefer environment variables for secrets. Secrets are never printed or logged.

### Where secrets can live

Kest looks for each `KEST_*` variable in this order and uses the first it finds:

1. The real process environment (what Pterodactyl sets).
2. A `.env` file in the working directory, for running locally.
3. `<data dir>/secrets.json`, a flat JSON object such as `{"KEST_OPENROUTER_API_KEY": "sk-or-..."}`. Plugin secrets use the same file with their `KEST_SECRET_` names.

On Linux and macOS the secrets file must be mode 0600. Kest refuses to start if group or others can read it, and the error shows the `chmod 600` command to fix it. Windows has no such permission bits, so the check is skipped there.

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
| `KEST_MODEL_GENERAL` | `llm.models.general` | `anthropic/claude-haiku-5.5` | Conversation and tools; must support tool calling |
| `KEST_MODEL_FALLBACKS` | `llm.models.fallbacks` | none | Tried in order if the general model fails |
| `KEST_MODEL_TRANSCRIPTION` | `llm.models.transcription` | `openai/gpt-4o-mini-transcribe` | Voice notes to text |
| `KEST_MODEL_TTS` | `llm.models.tts` | `google/gemini-3.1-flash-tts-preview` | Voice replies |
| `KEST_TTS_VOICE` | `llm.tts_voice` | `Kore` | Voice name for the TTS model. Voices differ per model (OpenAI uses names like `alloy`, Gemini uses `Kore`, Kokoro uses `af_heart`). If the model rejects it, Kest tries a built-in voice for the model family, then none |
| `KEST_OPENROUTER_BASE_URL` | `llm.base_url` | `https://openrouter.ai/api/v1` | API base |
| `KEST_ZDR_ONLY` | `llm.zdr_only` | `false` | Only route to zero-data-retention providers where supported |
| `KEST_DAILY_BUDGET_USD` | `llm.daily_budget_usd` | `1.00` | Spend guard; `0` disables |

Voice replies work with any speech model: Kest asks for MP3 and, if a model only offers raw PCM (Gemini TTS does), switches by itself and encodes it to Ogg/Opus. Details: [../research/llm-openrouter/README.md](../research/llm-openrouter/README.md).

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

| Env var | TOML | Default | Meaning |
| --- | --- | --- | --- |
| `KEST_PLUGINS` | `enabled_plugins` | `reminders,tasks,webfetch` | Built-in plugins to load, by id |
| `KEST_SECRET_<NAME>` | none | none | A secret a plugin declared in its manifest, for example `KEST_SECRET_NOTION_TOKEN` |

| Plugin | Secrets | Needs settings | Guide |
| --- | --- | --- | --- |
| `mailbox` | `MAILBOX_PASSWORD` | `imap_host`, `smtp_host`, `username` | [mailbox-setup.md](mailbox-setup.md) |
| `gmail` | `GMAIL_CLIENT_ID`, `GMAIL_CLIENT_SECRET`, `GMAIL_REFRESH_TOKEN` | none | [gmail-setup.md](gmail-setup.md) |
| `notion` | `NOTION_TOKEN` | none | [notion-setup.md](notion-setup.md) |
| `workout`, `nutrition`, `focus` | none | none | their READMEs under `plugins/` |
| `browser` | none | the runtime image | [browser deployment](../deployment/browser.md) |

The Pterodactyl egg has a startup variable for each secret; leave the ones you do not use empty.

Per-plugin settings live under `[plugins.<id>]` and are checked against the plugin's manifest; unknown or wrongly typed settings disable that plugin and are reported by `/status`:

```toml
enabled_plugins = ["reminders", "tasks", "webfetch"]

[plugins.reminders]
max_active = 50

[plugins.webfetch]
max_chars = 6000
```

Each plugin's README lists its settings. Settings for a plugin that is not enabled are a configuration error. Format details: [../architecture/plugins.md](../architecture/plugins.md).

## Browser settings

The opt-in `browser` plugin requires its separate runtime. See [browser deployment](../deployment/browser.md) and [plugin settings](../../plugins/browser/README.md). Add `browser` to `KEST_PLUGINS` before defining `[plugins.browser]`.

| Env var | TOML under `plugins.browser` | Default |
| --- | --- | --- |
| `KEST_BROWSER_MODULE_PATH` | `module_path` | `/opt/kest-browser/node_modules/playwright` |
| `KEST_BROWSER_HEADLESS` | `headless` | `true` |
| `KEST_BROWSER_SANDBOX` | `sandbox` | `true` |

Browser env overrides only apply when the plugin is enabled. `KEST_SECRET_BROWSER_VNC_PASSWORD` is consumed by the optional desktop supervisor, not by the Go configuration loader; it is not passed to the browser worker.

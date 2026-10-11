# Operations

## Running

- Start: `./kest` (or the Pterodactyl start button, or `docker compose up -d`).
- Healthy start prints `kest: ready`.
- Version: `./kest -version`.
- Stop: SIGINT or SIGTERM; Kest exits within 10 seconds.

## Data

Everything is under `KEST_DATA_DIR`:

```text
data/
├── kest.db            SQLite database (WAL mode adds kest.db-wal and kest.db-shm)
├── config.toml        optional settings
├── secrets.json       optional secrets file, mode 0600
├── plugins.d/         declarative plugins
└── backups/           nightly VACUUM INTO copies, last 7 kept
```

Stop Kest before copying `kest.db` by hand, or use the backups folder. Layout is defined in [../architecture/data-model.md](../architecture/data-model.md).

## Chat commands (arrive in Phase 1)

| Command | Purpose |
| --- | --- |
| `/status` | Health, spend today, next scheduled jobs, plugin state |
| `/settings` | Tone, message volume, check-in times, quiet hours |
| `/export` | Export all your data as JSON |
| `/forget` | Delete all your data, after confirmation |
| `/lockdown` | Disable write tools and proactive messages until restart |

## Rotating credentials

| Credential | Steps |
| --- | --- |
| Telegram bot token | BotFather → `/revoke`, update `KEST_TELEGRAM_TOKEN`, restart |
| OpenRouter key | Create a new key, update `KEST_OPENROUTER_API_KEY`, restart, delete the old key |
| Google/Notion tokens | Revoke in the provider's console, re-run the plugin's connect flow |

## Troubleshooting

| Symptom | Likely cause |
| --- | --- |
| Exits immediately with `configuration:` | Missing or invalid setting; the message lists each one |
| Bot silent | Wrong owner ID, revoked token, or no outbound HTTPS |
| Replies fail with model errors | Model slug retired; update `KEST_MODEL_GENERAL` or add fallbacks |
| Messages arrive at odd hours | Timezone wrong; set `KEST_TIMEZONE` |
| Too many messages | Lower `KEST_MAX_PROACTIVE_PER_DAY` or tell Kest to check in less |

Set `KEST_LOG_LEVEL=debug` to see more. Message content stays redacted unless `KEST_LOG_SENSITIVE=true`; remove it before sharing logs.

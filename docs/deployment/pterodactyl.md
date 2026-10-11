# Hosting Kest on Pterodactyl

Kest ships as one static binary. The egg downloads a release binary, verifies its checksum, and runs it. No inbound port is needed because Kest uses Telegram long polling.

Background and field-level notes: [../research/hosting/README.md](../research/hosting/README.md).

## What you need

- A Pterodactyl panel (1.x) or Pelican panel with a Wings node on linux/amd64 or linux/arm64.
- A Telegram bot token from [@BotFather](https://t.me/BotFather).
- Your numeric Telegram user ID (message `@userinfobot`).
- An [OpenRouter](https://openrouter.ai/keys) API key with a little credit.
- A published Kest release, because the installer downloads binaries from GitHub Releases.

## 1. Import the egg

1. Download [egg-kest.json](../../deploy/pterodactyl/egg-kest.json) (also attached to every release).
2. In the panel: **Admin → Nests → Import Egg**. Pick a nest (create one named "Bots" if you like) and upload the file.

## 2. Create the server

1. **Admin → Servers → Create New**.
2. Choose the Kest egg. A small allocation is enough: 128 MB memory, 512 MB disk, one CPU thread. Kest needs a network allocation for the panel's requirements but does not listen on it.
3. Fill the variables:

| Variable | Notes |
| --- | --- |
| Telegram Bot Token | From BotFather |
| OpenRouter API Key | From openrouter.ai |
| Owner Telegram ID | Your numeric ID. Comma-separate several |
| Timezone | For example `Europe/London`. Controls every schedule |
| Kest Version | `latest` or a tag such as `v0.1.0` |
| Models | Defaults work; see the OpenRouter model list |
| Daily Budget (USD) | Spending guard, `0` disables |
| Max Proactive Messages Per Day | Default 3 |

4. Create the server and let the installer finish.

## 3. Start and check

Press **Start**. The panel marks the server running when the console prints `kest: ready`. Then open your bot in Telegram and send `/start`.

If the console shows `configuration:` followed by a list, a variable is missing or invalid; the message names the variable.

## Data and backups

Everything lives in `/home/container/data` on the server volume: the SQLite database, `config.toml` (optional), and nightly backups in `data/backups/`. Back up that folder with the panel's backup feature.

Advanced settings that have no panel variable can go in `data/config.toml`. Environment variables from the panel always override the file. Reference: [../guides/configuration.md](../guides/configuration.md).

## Updating

1. Change **Kest Version** (or leave it on `latest`).
2. **Settings → Reinstall Server**. Reinstalling replaces the binary and keeps the `data/` folder.
3. Start the server.

## Stopping

The egg uses `^C`, which sends SIGINT. Kest finishes in-flight work and exits within 10 seconds.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Installer fails with "checksum mismatch" | The release is missing `checksums.txt` or the asset; use a published release tag |
| Installer fails with "unsupported architecture" | Node must be linux/amd64 or linux/arm64 |
| Server never reaches "running" | Console should print `kest: ready`; read the lines above it |
| Bot does not answer | Owner ID wrong, or Telegram token revoked. Messages from other users are ignored by design |
| Times are off | Timezone variable is not a valid IANA name |
| Costs rising | Lower Daily Budget; check the `/status` command |

## Security notes

- Panel variables are visible to users with access to the server. For a single-owner deployment that is you. Do not share server access with anyone you would not trust with the bot token.
- Kest ignores every Telegram account except the configured owners.
- See [../architecture/security.md](../architecture/security.md).

## Maintaining the egg

The egg is generated. To change variables or the install script, edit `scripts/sync-egg/main.go` or `deploy/pterodactyl/install.sh`, then run:

```bash
make egg
```

CI fails if the committed egg is stale (`make egg-check`). Per [AGENTS.md](../../AGENTS.md), any change to environment variables, ports, startup command or data layout must update the egg in the same batch.

Before publishing a release, import the egg into a test panel, create a server, and confirm the install and the `kest: ready` detection work. The egg format was cross-checked against public references but not yet verified on a live panel.

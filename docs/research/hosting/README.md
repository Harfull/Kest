# Hosting: Pterodactyl

## Question

What does Kest need to run as a Pterodactyl server, and what goes into the egg?

## Sources

- Secondary: [Pterodactyl panel docs mirror, nests and eggs](https://www.mintlify.com/pterodactyl/panel/admin/nests-eggs)
- Secondary: [Pterodactyl egg creation mirror](https://www.mintlify.com/pterodactyl/panel/advanced/egg-creation)
- Primary (partial): [SchemaStore Pterodactyl egg schema](https://www.schemastore.org/pterodactyl.json)
- Primary: [parkervcp generic Go egg](https://github.com/parkervcp/eggs/tree/master/generic/golang) used as a structural reference
- Note: the official pterodactyl.io docs could not be fetched during research. The field names below were cross-checked across the schema, the mirrors and a real egg. **Import the egg into a test panel and re-export it before publishing a release.**

## Findings

### Egg format

- An egg is a JSON file. Current export version is `PTDL_v2`; Pelican panel imports it unchanged.
- Top-level keys: `_comment`, `meta`, `exported_at`, `name`, `author`, `description`, `features`, `docker_images`, `file_denylist`, `startup`, `config`, `scripts`, `variables`.
- `docker_images` maps a display name to an image reference. The first entry is the default.
- `startup` is the command run in the server container. It can reference variables as `{{VARIABLE}}` and panel values such as `{{SERVER_PORT}}` and `{{SERVER_MEMORY}}`.
- `config` is an object whose values are JSON-encoded strings:
  - `files`: config files the panel edits (parsers: `properties`, `file`, `yaml`, `json`, `ini`). Kest uses env vars, so this is `{}`.
  - `startup`: `{"done": "<console text>"}`. The panel marks the server running when this text appears.
  - `logs`: log file handling, `{}` for console-only.
  - `stop`: `^C` sends SIGINT, or a console command string.
- `scripts.installation` has `script` (bash), `container` (image the script runs in, for example `ghcr.io/pterodactyl/installers:alpine`) and `entrypoint` (`bash` or `ash`). The script runs with the server data volume mounted at `/mnt/server`.
- `variables[]` items: `name`, `description`, `env_variable`, `default_value`, `user_viewable`, `user_editable`, `rules` (Laravel validation syntax such as `required|string|max:128`), `field_type` (`text`).
- Secrets: variables are visible to admins and, when `user_viewable` is true, to the server user. Tokens should be `user_editable: true` and `user_viewable: true` only because the owner is the user. There is no encrypted variable type, which is acceptable for a single-owner deployment but should be documented.

### What Kest needs from the host

- Outbound HTTPS only (Telegram, OpenRouter, integrations). No inbound port is required with long polling.
- Persistent working directory. Pterodactyl mounts the server volume at `/home/container` and runs the startup command there, so `KEST_DATA_DIR=/home/container/data`.
- Timezone data: container images may lack tzdata. Kest embeds Go's `time/tzdata` so `time.LoadLocation` always works.
- CA certificates: required for HTTPS. Use a base image that includes them (`ghcr.io/parkervcp/yolks:debian` does).
- Graceful stop: the panel sends the configured stop signal and kills the container after a timeout. Kest must flush state and exit within 10 seconds on SIGINT or SIGTERM.
- Single process, low memory. Target under 64 MB resident for the idle bot.

### Install strategy

Two options were considered:

1. Download a prebuilt release binary matching the container architecture (`kest-linux-amd64` or `-arm64`) from GitHub Releases and verify its checksum.
2. Build from source in the installer container with the Go toolchain.

## Decision

- The egg installs **release binaries** by default (fast, no toolchain, small install container). A `KEST_VERSION` variable (default `latest`) selects the release. Verify the SHA-256 from `checksums.txt`.
- Offer `KEST_BUILD_FROM_SOURCE=1` as a fallback for development branches using `golang:` as the install container (documented, not default).
- Runtime image: `ghcr.io/parkervcp/yolks:debian`. Startup: `./kest`. Stop: `^C`. Done string: `kest: ready`.
- All configuration flows through `KEST_*` environment variables defined as egg variables, with rules that validate format.
- Persist everything under `/home/container/data`.
- The egg lives at `deploy/pterodactyl/egg-kest.json` and is attached to every release.

## Open questions

- Pelican panel differences, if the user migrates later.
- Whether to publish our own yolks-style image to guarantee tzdata and CA certificates.

# Chromium browser deployment

Kest can run its own Chromium browser inside the Pterodactyl server container, in standalone Docker, or beside a local Go binary. The optional [browser plugin](../../plugins/browser/README.md) uses a private Playwright worker with a persistent profile. Browser Use is unnecessary because Kest already owns tool orchestration and approvals.

## Requirements

- A Kest binary built from a revision containing the browser plugin; the older 0.1.0 release does not include it.
- Node 20 or newer, Playwright 1.64.0 and matching browser binaries. The custom image includes them.
- For Linux, host support for Chromium's sandbox: unprivileged user namespaces, compatible seccomp rules and an AppArmor policy that allows them. The sandbox is on by default and never falls back automatically.
- Plan for at least 2 GiB RAM and 4 GiB disk for a few normal pages, then adjust from measured use. This is a starting allocation, not a guarantee for heavy sites. Image storage is additional space on the Wings node.

The official [Playwright Docker guide](https://playwright.dev/docs/docker) describes non-root execution and the namespace permissions needed for Chromium. [Wings container configuration](https://github.com/pterodactyl/wings/blob/develop/environment/docker/container.go) uses a read-only root filesystem and `no-new-privileges`. The browser image keeps installed runtime files under `/opt`, puts user files under `/home/container/data`, and uses `/tmp` instead of relying on Wings' small `/dev/shm`.

An egg cannot grant kernel namespaces, change Docker's seccomp policy or install runtime system libraries from its separate installer container. On managed hosting, the node administrator must permit the custom image and sandbox requirements. If that is unavailable, use a standalone Docker host that supports the supplied seccomp profile. `KEST_BROWSER_SANDBOX=false` is an explicit administrator override for an already isolated trusted environment; it removes Chromium's own sandbox and is not the recommended fix for arbitrary browsing.

## Pterodactyl

Run this on the Wings node from a checkout of this revision:

```bash
docker build --target headless -f deploy/docker/Dockerfile.browser -t kest-browser:local .
```

1. Import [egg-kest-browser.json](../../deploy/pterodactyl/egg-kest-browser.json). It keeps the existing checksum-verified binary installer and enables `browser` in the plugin list.
2. Set the server's **Docker Image** to exactly `kest-browser:local`, without quotes or a `~` prefix. Check on the Wings node with `docker image inspect kest-browser:local`. Upstream Wings attempts a pull and falls back to a matching local image if the pull fails. Its special `~` prefix skips pulling on supported versions, but is not a valid Docker image reference if a daemon forwards it unchanged. The egg therefore uses the plain tag for compatibility. For multiple nodes, build on every node or push your own image and use that registry tag.
3. Use the allocation above, fill the normal Telegram/OpenRouter/owner variables, and leave **Chromium Sandbox** true.
4. Until a new release containing this work is published, extract the newly built binary with `docker run --rm --entrypoint cat kest-browser:local /usr/local/bin/kest > kest`, stop the server, and upload it through the panel after installation (ensure it is executable). The image prefers `/home/container/kest` when present. A reinstall downloads the configured release again, so do not reinstall an older release over this binary.
5. Start, check `/status`, then ask Kest to open a web page. Approve the navigation. `/status` confirms plugin loading; the first browser action also verifies Chromium can launch on this host.

No browser-control allocation is needed. Standard [Pterodactyl instructions](pterodactyl.md) still apply. The ordinary egg now lists `browser` as available, but it requires selecting the custom runtime image.

The host's Docker security profile is an administrator responsibility; the egg cannot apply the [supplied seccomp profile](../../deploy/browser/seccomp_profile.json). Do not globally disable Wings isolation to make Chromium start. A failed sandbox launch is a reason to configure a dedicated compatible node or use the standalone setup.

## Standalone Docker

Copy `.env.example` to `.env` at the repository root and fill the three required values. Then:

```bash
docker compose -f deploy/docker/compose.browser.yml up --build -d
docker compose -f deploy/docker/compose.browser.yml logs -f
```

This builds the browser image locally, enables the plugin, uses a dedicated persistent volume, runs as a non-root user, provides private shared memory, drops capabilities, and applies the supplied Playwright seccomp profile. It exposes no ports. Use the same Compose file for stop and updates. The ordinary distroless Compose file remains available for deployments without browsing.

To reuse an existing data volume, point `kest-browser-data` at that volume explicitly and make its files writable by the configured container UID/GID (10001 by default; Wings supplies its own numeric identity). Back up first. The browser image uses `/home/container/data`; the minimal image uses `/data`.

## Standalone binary

From the repository root, install the locked runtime:

```bash
npm --prefix deploy/browser ci --ignore-scripts
# Ubuntu/Debian: system libraries need administrator access.
cd deploy/browser
npx playwright install --with-deps chromium
cd ../..
export KEST_PLUGINS=reminders,tasks,webfetch,browser
export KEST_BROWSER_MODULE_PATH="$PWD/deploy/browser/node_modules/playwright"
go run ./cmd/kest
```

Set the normal three credentials in the environment or `.env`. On Windows use `npm.cmd`/`npx.cmd`, `npx.cmd playwright install chromium` without `--with-deps`, and PowerShell `$env:KEST_BROWSER_MODULE_PATH = (Resolve-Path deploy/browser/node_modules/playwright).Path`. Set `$env:KEST_PLUGINS = 'reminders,tasks,webfetch,browser'`, then run Kest normally. On macOS use the same local browser installation without Linux system dependencies.

For a release binary you can copy `deploy/browser` anywhere and set `KEST_BROWSER_MODULE_PATH` to its absolute `node_modules/playwright` directory. Node must be on PATH, or set `node_path` in `[plugins.browser]`. Only the optional browser runtime needs Node; the rest of Kest stays a static binary.

## Watching and signing in

For a local desktop, set `KEST_BROWSER_HEADLESS=false`. Ask Kest to open a site, approve the navigation, and sign in manually in the Chromium window. Use a dedicated assistant account. Cookies and local storage persist; Chromium's tab arrangement is not promised across restarts.

For Docker, an optional image target supplies Xvfb, x11vnc and noVNC. Add `KEST_SECRET_BROWSER_VNC_PASSWORD` to `.env` with at least eight characters, avoiding newlines. VNC uses the first eight characters of the password; the SSH tunnel is the main network protection. Start:

```bash
docker compose -f deploy/docker/compose.browser.yml -f deploy/docker/compose.desktop.yml up --build -d
```

On the Docker host open `http://127.0.0.1:6080/vnc.html` and enter the VNC password. From another computer tunnel first:

```bash
ssh -N -L 6080:127.0.0.1:6080 user@docker-host
```

Open that same localhost URL on your computer. Ask Kest to list browser tabs to launch Chromium, or approve a navigation. The desktop starts Kest with `headless=false`; it does not expose a DevTools or Playwright server. Manual desktop interaction is direct owner action and is outside the model's tool approvals. Close the tunnel when finished.

For Wings, build `--target desktop` with the same image tag and set **Headless Browser** false. Supply `KEST_SECRET_BROWSER_VNC_PASSWORD` as a private startup environment variable; the startup supervisor consumes it before launching Kest. Do not allocate port 6080 publicly. A node administrator can forward the internal port through SSH by entering the server's network namespace (replace the UUID):

```bash
pid=$(docker inspect --format '{{.State.Pid}}' SERVER_UUID)
sudo socat TCP-LISTEN:16080,bind=127.0.0.1,reuseaddr,fork \
  EXEC:"nsenter -t $pid -n socat STDIO TCP:127.0.0.1:6080"
```

This binds the forwarder only to the node's loopback and reaches noVNC inside the server's network namespace. From your computer use `ssh -N -L 6080:127.0.0.1:16080 user@wings-node`, then open the localhost URL. Both `socat` and `nsenter` must be installed on the node; stop the foreground forwarder when finished and restart it after the server container is recreated. Optional desktop viewing is not required for headless browsing.

## Data, updates and troubleshooting

To isolate a launch failure, run the following in the VPS SSH shell (not the bot's panel console). Get the server container ID with `docker ps --format '{{.ID}}  {{.Image}}  {{.Names}}`, then replace `SERVER_CONTAINER_ID`. This uses a temporary profile, performs no navigation, and passes no bot credentials to Chromium.

```bash
docker exec -i SERVER_CONTAINER_ID node <<'JS'
const { chromium } = require('/opt/kest-browser/node_modules/playwright');
(async () => {
  let browser;
  try {
    browser = await chromium.launch({
      headless: true, chromiumSandbox: true,
      args: ['--disable-dev-shm-usage'],
      env: { PATH: process.env.PATH, HOME: '/tmp', PLAYWRIGHT_BROWSERS_PATH: '/ms-playwright' },
    });
    console.log('Chromium launch OK');
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  } finally { if (browser) await browser.close(); }
})();
JS
```

`<data dir>/plugins/browser/profile/` persists logins; `inbox/` accepts owner upload candidates; `screenshots/` stores private PNG files. Back up the whole data directory while Kest is stopped. SQLite-only `/export` and nightly DB backups do not cover Chromium. Do not share profiles with your everyday browser or run two workers on one profile. Prune screenshots manually when they are no longer needed.

| Symptom | Action |
| --- | --- |
| Docker reports `invalid reference format` | Set the existing server's Docker Image field to `kest-browser:local`, without quotes or `~`; changing or reimporting the egg does not necessarily change an existing server's field |
| Registry pull reports access denied, then Kest reaches `kest: ready` | The daemon continued with the locally built image; this pull warning does not mean startup failed. Use your own published registry image if you want registry pulls to succeed |
| Plugin missing from `/status` | Include `browser` in `KEST_PLUGINS` and use the new binary |
| Could not locate Node | Use the browser runtime image or install Node and set `node_path` |
| Runtime unavailable | Install the locked module and matching Chromium; check `KEST_BROWSER_MODULE_PATH` |
| Chromium cannot launch on Wings | Ask the node administrator to check user namespaces, seccomp and AppArmor; use a compatible standalone host if unavailable |
| Calagopus reports `sys_chroot("/proc/self/fdinfo/") == 0` | Chromium's sandbox cannot perform its chroot operation under the container policy. Calagopus's default seccomp profile conditions chroot on `SYS_CHROOT`, which its server containers drop. Keep sandboxing with a compatible custom daemon policy or the standalone Compose profile. An explicit reduced-isolation workaround is setting the server's `KEST_BROWSER_SANDBOX=false` and restarting; this disables Chromium's own sandbox and requires no image rebuild |
| Private/internal URL refused | Expected: loopback, LAN, metadata and non-web ports are blocked |
| Click fails | Read the page first and choose an exact visible locator; iframes may require a frame-specific workflow that this version does not expose |
| Login disappears | Check volume mounting and stop gracefully; session-only cookies can expire by site policy |
| Screenshot not visible in Telegram | This version saves PNG files locally; inspect or download through the panel |
| Browser OOM or tmp full | Increase measured RAM/tmp capacity, reduce tabs, or use standalone Docker's larger `/dev/shm` |

To update Playwright, change and re-lock `deploy/browser/package.json`, match the Docker base version, reinstall Chromium for standalone use, and rerun the tagged integration test. The seccomp profile is copied verbatim from the [Playwright 1.64.0 source](https://github.com/microsoft/playwright/blob/v1.64.0/utils/docker/seccomp_profile.json); its Apache-2.0 license is preserved in [LICENSE.playwright](../../deploy/browser/LICENSE.playwright).

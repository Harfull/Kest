# Browser plugin

Kest's optional Chromium browser uses Playwright directly. It opens real pages, reads rendered text and element locators, navigates, clicks, fills fields, presses keys, scrolls, uploads owner-provided files, saves PNG screenshots and manages tabs. It does not expose arbitrary JavaScript or shell execution.

Enable it with `KEST_PLUGINS=reminders,tasks,webfetch,browser`. Node 20 or newer, Playwright 1.64.0 and its matching Chromium must be installed. The ordinary distroless image has no browser runtime. Follow the [complete deployment guide](../../docs/deployment/browser.md) for Pterodactyl, Docker, a local binary, and an optional noVNC desktop.

## Tools and approval

| Tool | Behavior |
| --- | --- |
| `browser.navigate_page` | Open HTTP/HTTPS; always asks approval |
| `browser.read_page` | Read text and interactive element locators |
| `browser.click_element` | Click one element; always asks approval |
| `browser.fill_field` | Fill one field; always asks approval |
| `browser.press_key` | Press a bounded key; always asks approval |
| `browser.scroll_page` | Scroll and read |
| `browser.upload_file` | Choose an inbox file up to 10 MiB; always asks approval |
| `browser.capture_screenshot` | Save a viewport PNG locally |
| `browser.list_tabs` | List tab IDs, titles and URLs |
| `browser.close_tab` | Close a tab |

Omit `tab` to use the active tab; use IDs from `list_tabs` to address another one. There are at most eight tabs. Selectors must match exactly one element; ambiguous selectors fail. Locators returned by a page and its text are untrusted. The browser cannot bypass Kest's lockdown or Telegram approval flow. Navigation also asks because a logged-in URL can perform a state change.

Screenshots are local artifacts: this version does not attach them to Telegram or feed image pixels to the model. The model uses rendered text and locators. Downloads, WebSockets and service workers are disabled. Files for upload must be placed by the owner in the browser inbox; arbitrary files from the host and the profile cannot be uploaded.

## Settings

| TOML key under `[plugins.browser]` | Default | Meaning |
| --- | --- | --- |
| `node_path` | `node` | Node executable; operator controlled |
| `module_path` | `/opt/kest-browser/node_modules/playwright` | Installed Playwright module directory |
| `headless` | `true` | Set false for manual login or desktop viewing |
| `sandbox` | `true` | Chromium's own sandbox; launch fails if unavailable |
| `timeout_seconds` | `30` | Per-action timeout, 1–60; worker calls have ten seconds of extra startup allowance |
| `max_chars` | `12000` | Page text bound, 100–50000 |

`KEST_BROWSER_MODULE_PATH`, `KEST_BROWSER_HEADLESS` and `KEST_BROWSER_SANDBOX` override those scoped settings when the browser plugin is enabled. No other plugin receives them.

## Storage and security

The registry gives the plugin `<KEST_DATA_DIR>/plugins/browser/`. Its `profile/` holds cookies, local storage and logins across restarts; `inbox/` holds approved upload candidates; `screenshots/` holds viewport PNGs. Directories are created with mode 0700 and screenshots with 0600 on Unix. Stop Kest before backing up a live Chromium profile. A SQLite-only export or backup does not include these files. Keep only one Kest instance on the profile.

The worker is embedded in the Go binary and controlled through stdin/stdout, with no browser-control listening port. A separate authenticated proxy listens on a random loopback port solely for browser web traffic. HTTP and HTTPS destinations must be public and use ports 80 or 443. The registry enforces manifest host capabilities and checks the actual dialed address after DNS resolution, including HTTPS CONNECT, subresources and redirects. Chromium's loopback proxy bypass and direct UDP WebRTC are disabled. These are application controls, not a defense against an exploited browser binary; keep the Chromium sandbox and container isolation enabled.

The worker does not inherit Telegram, OpenRouter or plugin secret environment variables. Raw Playwright errors and typed values are not logged. Page text and URLs still go to the model, so use a dedicated assistant account and enter credentials manually in the headed browser. Do not ask the model to type passwords or API tokens.

## Verification

```bash
go test ./plugins/browser ./internal/plugin
npm --prefix deploy/browser test
# Requires the installed matching Chromium; uses an in-memory web fixture.
KEST_BROWSER_TEST_MODULE_PATH="$PWD/deploy/browser/node_modules/playwright" \
  go test -tags integration -v ./plugins/browser
```

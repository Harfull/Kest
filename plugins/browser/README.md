# Browser plugin

Kest's optional Chromium browser uses Playwright directly. It opens real pages, reads the text and the interactive elements on screen, scrolls, finds text, clicks, fills fields, picks dropdown options, presses keys, handles popups and frames, uploads owner-provided files, and sends or looks at screenshots. It does not expose arbitrary JavaScript or shell execution.

Enable it with `KEST_PLUGINS=reminders,tasks,webfetch,browser`. Node 20 or newer, Playwright 1.64.0 and its matching Chromium must be installed. The ordinary distroless image has no browser runtime. Follow the [complete deployment guide](../../docs/deployment/browser.md) for Pterodactyl, Docker, a local binary, and an optional noVNC desktop.

## How the model uses it

The browser's tools are sent on every turn and the plugin adds usage guidance to the system prompt, so Kest uses the browser for "check the price on that site" or "scroll down" without the word "browser" appearing. A read returns the page text and the elements the model can act on, each with a short ref:

```text
Tab 1: https://shop.example.com/items/7
Title: Red shoes
Position: top of the page (screen shows 0 to 800 of 4000 px)

Page text (characters 0 to 912 of 912):
...

Elements:
[e0] link "Reviews" -> /items/7/reviews
[e1] button "Add to cart"
[e2] textbox "Search"
[e3] dropdown "size" options: 8 | 9* | 10 (* = selected)
```

Refs such as `e3` (or `f1e4` inside the first frame) are stable while the page is open and are checked at click time: if the page has changed so a ref no longer points at the same element, the action is refused and the model reads again.

## Tools and approval

| Tool | Behavior | Approval |
| --- | --- | --- |
| `navigate_page` | Open HTTP or HTTPS in the active or a new tab | Yes, grantable |
| `read_page` | Read text and refs; `scope=viewport` or `page`, `offset` pages long text | No |
| `scroll_page` | `down`, `up`, `top`, `bottom`, by pixels, or bring a ref into view; returns what is now on screen | No |
| `find_text` | Find visible text, scroll to it, show the surrounding screen | No |
| `click_element` | Click one element by ref | Yes, grantable |
| `click_position` | Click a screen coordinate (screen is 1280 by 800) | Yes, grantable but never covered by a grant |
| `fill_field` | Replace a field's text; `submit` presses Enter | Yes, grantable |
| `press_key` | Enter, Tab, Escape, Space, Backspace, Delete, arrows, PageUp, PageDown, Home, End | Yes, grantable |
| `select_option` | Choose a dropdown option by value or label | Yes, grantable |
| `hover_element` | Hover to open a menu or tooltip | Yes, grantable |
| `wait_for` | Wait for an element or text, or pause up to 15 seconds | No |
| `navigate_history` | Back, forward or reload | Yes, grantable (reload is never covered) |
| `screenshot` | Capture the screen, the whole page or one element; `send` delivers it to the owner in Telegram, `look` attaches it for the model | No |
| `upload_file` | Choose an inbox file up to 10 MiB for a file input | Yes, every time |
| `list_tabs`, `close_tab` | Tab management | No |

Omit `tab` to use the active tab; there are at most eight. A click that opens a popup moves to the new tab and says so. JavaScript dialogs are dismissed and reported. Downloads, WebSockets and service workers are disabled.

### Approval prompts

Anything that can change a website or send what the owner typed asks first, and the prompt says what will actually happen from the last page read: `Click button "Buy now" on shop.example.com`, never an opaque id. Typed text for password and card fields is hidden in the prompt.

The prompt has a third button, **Allow for this task**. It approves that step and the browser's further steps for the rest of the turn, so a ten-step job needs one tap. A grant:

- lasts one turn and covers only this plugin's grantable tools;
- stops at navigation to a different site than the page the browser is on;
- stops at controls whose labels commit something (buy, pay, send, post, delete, confirm, sign in, accept and similar), at password and payment fields, at text that looks like a card number or token, at disabled controls, at links to other sites, at blind coordinate clicks, at reloads, and at anything addressed by a raw selector instead of a ref the owner could have seen;
- never covers `upload_file`.

When a step is not covered, the normal prompt appears again. Reading, scrolling, finding text, waiting and screenshots never ask.

### Screenshots

`screenshot` has two independent destinations. `send=true` posts the image to the owner's Telegram chat with an optional caption, up to eight a minute; images that exceed Telegram's photo limits arrive as lossless documents. `look=true` attaches a JPEG to the model's input for that turn only, up to two images per turn, so it can read layouts, charts and pictures. The result says exactly which happened, and Kest only claims a screenshot was sent or seen when it was. Pixels are untrusted page content like the text. The owner can reply to a screenshot in Telegram and Kest will see that image again. Full-page capture is limited to 8000 px high (4000 when only looking); taller pages are refused with advice to scroll. Screenshot files are deleted as soon as they are read.

## Settings

| TOML key under `[plugins.browser]` | Default | Meaning |
| --- | --- | --- |
| `node_path` | `node` | Node executable; operator controlled |
| `module_path` | `/opt/kest-browser/node_modules/playwright` | Installed Playwright module directory |
| `headless` | `true` | Set false for manual login or desktop viewing |
| `sandbox` | `true` | Chromium's own sandbox; launch fails if unavailable |
| `timeout_seconds` | `30` | Per-action timeout, 1–60; worker calls have ten seconds of extra startup allowance |
| `max_chars` | `12000` | Page text bound per read, 100–50000; `read_page` with `offset` reads the rest |

`KEST_BROWSER_MODULE_PATH`, `KEST_BROWSER_HEADLESS` and `KEST_BROWSER_SANDBOX` override those scoped settings when the browser plugin is enabled. No other plugin receives them.

## Storage and security

The registry gives the plugin `<KEST_DATA_DIR>/plugins/browser/`. Its `profile/` holds cookies, local storage and logins across restarts; `inbox/` holds approved upload candidates; `screenshots/` holds a screenshot only for the instant between capture and delivery. Directories are created with mode 0700 and screenshots with 0600 on Unix. Stop Kest before backing up a live Chromium profile. A SQLite-only export or backup does not include these files. Keep only one Kest instance on the profile.

The worker is embedded in the Go binary and controlled through stdin/stdout, with no browser-control listening port. A separate authenticated proxy listens on a random loopback port solely for browser web traffic. HTTP and HTTPS destinations must be public and use ports 80 or 443. The registry enforces manifest host capabilities and checks the actual dialed address after DNS resolution, including HTTPS CONNECT, subresources and redirects. Chromium's loopback proxy bypass and direct UDP WebRTC are disabled. These are application controls, not a defense against an exploited browser binary; keep the Chromium sandbox and container isolation enabled.

A failed action (a ref that no longer exists, a covered element, a page that did not load) returns a short, specific error and leaves the browser running with its tabs. Only a timeout or a protocol failure restarts the browser. The worker never forwards raw Playwright errors, which can contain page content or typed values.

The worker does not inherit Telegram, OpenRouter or plugin secret environment variables. Page text and URLs still go to the model, so use a dedicated assistant account and enter credentials manually in the headed browser. Do not ask the model to type passwords or API tokens.

## Verification

```bash
go test ./plugins/browser ./internal/plugin
npm --prefix deploy/browser test
# Requires the installed matching Chromium; serves a fixture site from memory.
KEST_BROWSER_TEST_MODULE_PATH="$PWD/deploy/browser/node_modules/playwright" \
  go test -tags integration -v ./plugins/browser
```

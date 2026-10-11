# Telegram transport

## Question

Which Go library and update delivery mode should Kest use, and what platform limits shape the design?

## Sources

- Primary: [Bot API reference](https://core.telegram.org/bots/api) (checked against Bot API 10.3, 24 August 2026)
- Primary: [Bots FAQ](https://core.telegram.org/bots/faq)
- Primary: [go-telegram/bot](https://github.com/go-telegram/bot)
- Secondary: [aiogram sendVoice docs](https://docs.aiogram.dev/en/latest/api/methods/send_voice.html) (cross-checked against the primary page)

## Findings

### Platform limits

| Item | Limit |
| --- | --- |
| Message text | 1 to 4096 characters after entity parsing |
| Callback data on inline buttons | 1 to 64 bytes |
| Messages to one chat | about 1 per second sustained; bursts tolerated until 429 |
| Messages to one group | about 20 per minute |
| Bulk broadcast | about 30 per second |
| File download via `getFile` | up to 20 MB |
| File upload | up to 50 MB |
| Voice message send size | up to 50 MB |

### Receiving updates

- Long polling (`getUpdates`) and webhooks are mutually exclusive. Long polling confirms updates by advancing `offset`.
- `getUpdates` takes `timeout` (seconds; default 0 which is for testing only) and `allowed_updates`. Reactions are excluded unless requested.
- Webhooks need a public HTTPS endpoint on ports 443, 80, 88 or 8443 with a valid certificate.
- A Pterodactyl server is a container behind a panel with one or more allocated ports and no guaranteed public TLS. **Long polling needs only outbound HTTPS**, which is the simplest and most portable choice.

### Sending voice

- `sendVoice` plays as a voice message when the file is OGG with Opus, MP3, or M4A. Other formats arrive as audio or documents. Verified directly in the Bot API reference.
- OpenRouter TTS returns MP3, so it can be sent as-is.

### Formatting and UX

- `MarkdownV2` supports underline, strikethrough, spoiler and blockquote but requires escaping many characters. `HTML` parse mode is easier to generate safely. Kest will produce **HTML parse mode** through a small escaping helper.
- `sendChatAction` shows "typing" or "recording voice" while the model works.
- Inline keyboards carry confirmations and quick replies. Callback data is limited to 64 bytes, so store payloads in SQLite and send a short ID.
- Bot API 10.x adds streamed drafts (`sendMessageDraft`). It is optional polish, not required for v1.
- `setMyCommands` registers the slash command menu.

### Library choice

| Library | Notes |
| --- | --- |
| `go-telegram/bot` | MIT, "zero dependencies", context-based `Start(ctx)`, built-in long polling and webhooks, middleware option, handler matching by text, regex, callback data. States current Bot API support. |
| `tucnak/telebot` | Popular, but secondary sources report breaking changes between major versions. |
| `gotgbot` | Generated from the Bot API spec with a dispatcher; not evaluated in depth. |

Evidence for the comparison is mostly secondary. The deciding factors are zero dependencies, context-first design and first-class long polling.

## Decision

- Use **`github.com/go-telegram/bot`** behind our own `Transport` interface in `internal/telegram`, so the rest of the system never imports the library.
- Use **long polling** by default. Webhook mode is an optional later config switch.
- Drop every update whose sender is not in `owner_ids` before any other processing.
- Format output with HTML parse mode through a tested escaper; split messages at 4096 characters on paragraph boundaries.
- Send TTS replies as MP3 via `sendVoice`.
- Download voice notes (under 20 MB) and pass the OGG bytes straight to transcription.
- Keep inline-button payloads in the store; callback data holds only an ID.

## Open questions

- Confirm `go-telegram/bot` release cadence and that it tracks Bot API 10.x before pinning; check `pkg.go.dev` at implementation time.
- Do we want group-chat support? Out of scope; the product is single-user.

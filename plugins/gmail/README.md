# gmail

Gmail through the Gmail API: read, search, draft and send, plus optional nudges for mail you are waiting for. Setup, including how to get the refresh token, is in [the Gmail setup guide](../../docs/guides/gmail-setup.md). If you would rather use an app password, the [mailbox](../mailbox/README.md) plugin works with Gmail over IMAP and SMTP.

| Tool | Risk | What it does |
| --- | --- | --- |
| `search_mail` | read | Find messages with Gmail's search (words, sender, subject, unread, age). Newest first |
| `read_mail` | read | Read one message: headers, text, attachment names (never opened) |
| `draft_mail` | write | Save a draft in your account without sending it |
| `send_mail` | destructive, outbound | Send from your account. You approve the exact recipients, subject and text every time. A reply stays in its thread |
| `mark_mail_read` | write | Mark one message read |

Tool names and behavior match the `mailbox` plugin.

## Settings and secrets

```toml
[plugins.gmail]
from_address = ""        # optional; Gmail reports the account's address
from_name = ""
max_chars = 6000
nudges = false           # see the mailbox README for how nudges work
nudge_minutes = 15
nudge_from = ""
nudge_subject_words = ""
```

Secrets, from the environment or `secrets.json`: `KEST_SECRET_GMAIL_CLIENT_ID`, `KEST_SECRET_GMAIL_CLIENT_SECRET` and `KEST_SECRET_GMAIL_REFRESH_TOKEN`. They are never logged, shown to the model or included in error messages.

## Permissions

The refresh token decides what Kest can do. Each tool needs one Gmail scope; if the token lacks it, the tool says which permission is missing instead of failing vaguely.

| Scope | Allows |
| --- | --- |
| `gmail.readonly` | `search_mail`, `read_mail` |
| `gmail.compose` | `draft_mail` (and sending) |
| `gmail.send` | `send_mail` |
| `gmail.modify` | `mark_mail_read` (also covers reading) |

Start with `gmail.readonly` and `gmail.send`; add the others when you want them. Kest only ever talks to `gmail.googleapis.com` and `oauth2.googleapis.com`.

## Security

Mail is untrusted data, sending always needs your approval of the whole message, and sends are limited to 10 an hour, as in the mailbox plugin. Search terms are quoted so a value cannot add Gmail operators. The access token lives in memory only and is refreshed when it expires or Gmail rejects it. If Google no longer accepts the refresh token (it expired, or you revoked it), the error says so and points at the setup guide.

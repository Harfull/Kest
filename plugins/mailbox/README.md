# mailbox

Email for any provider over IMAP and SMTP, with an app password. Read, search, draft and send, plus optional nudges for mail you are waiting for. For Gmail's own API see [gmail](../gmail/README.md); [the setup guide](../../docs/guides/mailbox-setup.md) covers both paths.

| Tool | Risk | What it does |
| --- | --- | --- |
| `search_mail` | read | Find messages by words, sender, subject, unread and age. Newest first |
| `read_mail` | read | Read one message: headers, text, attachment names (never opened) |
| `draft_mail` | write | Save a message to the Drafts folder without sending |
| `send_mail` | destructive, outbound | Send from your account. You approve the exact recipients, subject and text every time |
| `mark_mail_read` | write | Mark one message read |

The same five tools exist in the `gmail` plugin, so Kest uses one vocabulary for either account.

## Settings

```toml
[plugins.mailbox]
imap_host = "imap.example.org"      # required
imap_port = 993
imap_tls = "implicit"               # or "starttls"
smtp_host = "smtp.example.org"      # required
smtp_port = 465                     # 587 with smtp_tls = "starttls"
smtp_tls = "implicit"
username = "you@example.org"        # required; also the From address unless from_address is set
from_address = ""
from_name = ""
folder = "INBOX"
drafts_folder = "Drafts"
max_chars = 6000                    # longest message text shown to the model

# Optional: tell me when mail I care about arrives.
nudges = false
nudge_minutes = 15
nudge_from = ""                     # comma separated: addresses, or @domain.com
nudge_subject_words = ""            # comma separated words, matched in the subject
```

The password is the secret `MAILBOX_PASSWORD`: set `KEST_SECRET_MAILBOX_PASSWORD` in the environment (a Pterodactyl startup variable) or in `secrets.json`. It is never logged and never shown to the model.

## Security

- Connections are always encrypted: TLS from the start (`implicit`) or STARTTLS before the password is sent. There is no clear-text mode, and certificates are always verified.
- Mail is untrusted input. Subjects and bodies are wrapped as data, and once you read mail in a turn, any later change in that turn needs your approval.
- Sending needs your approval each time, shows the whole message, takes at most 10 recipients, no attachments and no Bcc, and is limited to 10 messages an hour.
- Recipient addresses are parsed and re-checked before they reach SMTP, so a line break cannot add recipients or headers.
- Connections go through Kest's network guard, which refuses private and loopback addresses, so a mistyped or hostile host setting cannot be used to probe your network.

## Nudges

With `nudges = true`, Kest checks the inbox every `nudge_minutes` for unread mail from the last day that matches `nudge_from` or `nudge_subject_words`, and tells you "New email from Dana: Friday?" (name and subject only, never the text). Nudges go through the same budget as the daily rhythm: no messages in quiet hours, the daily cap, and the gap between messages. Mail that is held back is announced on a later check, within a day. Nudges are off by default and need at least one rule.

Each operation opens its own connection, so there is nothing to keep alive. IMAP ids are message UIDs in the configured folder; they stay valid until the folder is rebuilt.

# Email setup (IMAP and SMTP)

The [mailbox plugin](../../plugins/mailbox/README.md) connects Kest to any mailbox with an app password. It is the lowest-friction way to give Kest your email, including Gmail: no Google Cloud project, no OAuth. For Gmail's own API see [gmail-setup.md](gmail-setup.md).

## 1. Get an app password

Most providers refuse your normal password for programs and want an app-specific one. Create it in your account's security settings (look for "app passwords"). It needs two-step verification to be on. Use a dedicated one for Kest so you can revoke it alone.

| Provider | IMAP | SMTP |
| --- | --- | --- |
| Gmail | `imap.gmail.com`, 993, implicit TLS | `smtp.gmail.com`, 465, implicit TLS |
| Outlook and Microsoft 365 | `outlook.office365.com`, 993 | `smtp.office365.com`, 587, `starttls` |
| iCloud | `imap.mail.me.com`, 993 | `smtp.mail.me.com`, 587, `starttls` |
| Fastmail | `imap.fastmail.com`, 993 | `smtp.fastmail.com`, 465 |

Check your provider's current help page, because hosts and policies change. Some providers (notably Microsoft 365 for many organizations) have turned off password sign-in entirely; for those use a different account or provider, because the plugin does not support OAuth for IMAP.

## 2. Configure Kest

Add `mailbox` to the plugin list (`KEST_PLUGINS=reminders,tasks,webfetch,mailbox`) and put the settings in `config.toml` in the data directory:

```toml
[plugins.mailbox]
imap_host = "imap.gmail.com"
smtp_host = "smtp.gmail.com"
username = "you@gmail.com"
```

Every other setting has a default (ports 993 and 465 with implicit TLS). Use `smtp_port = 587` and `smtp_tls = "starttls"` for providers that want it. The full list is in the [plugin README](../../plugins/mailbox/README.md#settings).

Set the password as `KEST_SECRET_MAILBOX_PASSWORD`: on Pterodactyl it is the **Mailbox Password** startup variable; locally it can go in `.env` or in `secrets.json` in the data directory (mode 0600).

## 3. Check it

Restart Kest and send `/status`: `mailbox` should be listed under plugins. If it is under "Could not load", the log names the setting. Then ask: "any unread email from today?". The first connection also proves the host, port, TLS mode and password; an error such as `log in: the IMAP server said no` means the password or username is wrong, and a TLS error means the host name or port does not match the TLS mode.

Ask Kest to "send a test email to yourself" once: you will get an approval message showing the recipients, subject and full text. Nothing is ever sent without that.

## Nudges

To be told when specific mail arrives, add rules and turn nudges on:

```toml
nudges = true
nudge_from = "boss@work.example, @landlord.example"
nudge_subject_words = "invoice, urgent"
```

Kest checks every 15 minutes (`nudge_minutes`), sends one short line per message ("New email from Dana: Friday?"), and follows the same quiet hours and daily cap as its check-ins. If a nudge is held back it is retried on the next check for up to a day.

## Troubleshooting

| Symptom | Likely cause |
| --- | --- |
| `secret ... is not set` | `KEST_SECRET_MAILBOX_PASSWORD` is missing or empty; restart after setting it |
| `the IMAP server said no` on log in | Wrong username or password, or the provider needs an app password |
| `secure the connection (TLS)` | Wrong port for the TLS mode, or the host name does not match the server's certificate. Certificates are always verified |
| `address ... not public` / blocked | The host resolves to a private address; Kest refuses those on purpose |
| `the SMTP server does not offer STARTTLS` | Use `smtp_tls = "implicit"` with port 465, or a different server |
| Search finds nothing | The plugin reads `folder` (default `INBOX`); other folders need that setting changed |
| Draft fails | `drafts_folder` must match the real name, for example `[Gmail]/Drafts` on Gmail |

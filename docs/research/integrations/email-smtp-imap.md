# Custom mailbox: SMTP and IMAP

## Question

Which Go libraries should power a generic email plugin for a custom mailbox?

## Sources

- Secondary: [wneessen/go-mail on pkg.go.dev](https://pkg.go.dev/github.com/wneessen/go-mail@v0.8.0)
- Secondary: [emersion/go-imap](https://redirect.github.com/emersion/go-imap)

## Findings

- `wneessen/go-mail`: sending library built mostly on the standard library; implicit TLS and STARTTLS with policies; auth mechanisms including LOGIN, PLAIN, CRAM-MD5 and SCRAM; attachments, inline embeds, connection reuse. Version numbers seen in search results were inconsistent, so confirm the latest stable tag.
- `emersion/go-imap/v2`: IMAP4rev2 client and server library. Search results described v2 as still in development in older READMEs; the API differs from v1. **Verify stability and read the v2 client examples before depending on it.**
- Standard library `net/smtp` is frozen and lacks modern features; do not use it.
- Mailboxes differ widely: implicit TLS on 465 versus STARTTLS on 587; IMAP on 993. Config must expose host, port and TLS mode separately.

## Decision

> Update 2026-10-12: Phase 4 built the mailbox plugin on the standard library instead, for the reasons in [ADR 0009](../../decisions/0009-mail-on-the-standard-library.md). The text below is the original recommendation.

- Send with `go-mail`; read with `go-imap/v2` (subject to the stability check). Wrap both in `plugins/mailbox` behind the shared `mail` interface.
- Tools: `mail.search`, `mail.read`, `mail.send` (destructive risk, confirmation required), `mail.mark_read`. Keep tools identical to the Gmail plugin so the model sees one email vocabulary.
- Store credentials only in env or the 0600 secrets file; never echo them.
- Poll IMAP using IDLE where available, otherwise at a modest interval, to power "important email" nudges (opt-in).

## Open questions

- IMAP IDLE connection lifetime on constrained hosts.
- OAuth2 (XOAUTH2) for providers that disable passwords.

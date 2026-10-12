# 0009. Email over the standard library

- Status: Accepted
- Date: 2026-10-12

## Context

The [email research note](../research/integrations/email-smtp-imap.md) recommended `wneessen/go-mail` for sending and `emersion/go-imap/v2` for reading, with a warning to verify the stability of go-imap v2 first, and it ruled out `net/smtp` as frozen. [AGENTS.md](../../AGENTS.md) says to prefer the standard library and not to add dependencies without being asked, and every new dependency must be checked for maintenance, license and weight.

What the `mailbox` plugin needs is narrow: over an always-encrypted connection, log in, open one folder, search, fetch headers and one message, set the seen flag, append a draft, and send one plain-text message with PLAIN or LOGIN authentication.

## Decision

Implement that small slice directly in `plugins/mailbox`: an IMAP4rev1 client (`imap.go`, about 450 lines) and an SMTP sender on `net/textproto` (`smtp.go`), with MIME parsing and composing in `plugins/internal/mailkit` on `net/mail`, `mime`, `mime/multipart` and `mime/quotedprintable`. HTML bodies are reduced with `golang.org/x/net/html`, already a dependency of the `webfetch` plugin. Do not add `go-mail` or `go-imap`.

Constraints that make this safe to own: connections are always TLS (implicit or STARTTLS before any password), certificates are always verified, every value placed in a command is quoted or sent as a literal, addresses are validated before they reach SMTP, response sizes are bounded, and the clients are tested over real TLS connections against fake servers.

## Consequences

- No new dependency, no new license or supply-chain exposure, and the plugin keeps the network guard because it dials through the host.
- The IMAP client is ours to maintain. It handles the response forms real servers use (literals, quoted strings, nested lists, `BODY[...]` sections) but has only been exercised against test servers, not against Dovecot, Gmail IMAP or Exchange. Quirks will show up as bugs to fix.
- Missing on purpose: IMAP IDLE (nudges poll), OAuth2 (XOAUTH2) for providers that disabled passwords, attachments, HTML mail composing, and multiple folders. None blocks the first use.
- If the plugin grows (IDLE, several accounts, OAuth for IMAP), revisit this and adopt a library then, with an ADR superseding this one.
- Gmail has its own plugin over the REST API, which needs no mail protocol code.

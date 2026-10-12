# Gmail

## Question

How can a single-user, self-hosted bot read and send Gmail, and what does authorization cost?

## Sources

- Secondary: [Gmail API scopes reference (EmailEngine)](https://learn.emailengine.app/docs/accounts/gmail/gmail-api-scopes)
- Secondary: [Google OAuth refresh token expiry (Unipile)](https://www.unipile.com/google-oauth-refresh-token/)
- Primary (not fetched): Google Gmail API Go quickstart at developers.google.com/workspace/gmail/api/quickstart/go

The scope-tier and token-expiry claims come from secondary sources and must be re-verified against Google's current OAuth documentation at implementation time.

## Findings

- Scope tiers (secondary source): `gmail.send` is **sensitive**; `gmail.readonly`, `gmail.modify`, `gmail.compose`, `gmail.metadata` and full `mail.google.com` are **restricted**. Restricted scopes normally require a security assessment for public apps.
- An OAuth client in **Testing** mode with an external user type issues refresh tokens that expire after about **7 days** when Gmail scopes are used. Moving the consent screen to **Production** removes that limit, but an unverified app with restricted scopes shows a warning screen and has a user cap.
- A Google Workspace account can mark the app **Internal**, avoiding external-app limits.
- Refresh tokens can also be invalidated by password change or revocation, and a client is limited to 100 refresh tokens per account.
- Go libraries: `google.golang.org/api/gmail/v1` and `golang.org/x/oauth2/google`.
- OAuth for a headless server: use the **device authorization** flow or a one-time local consent run, then store the refresh token under the data dir with mode 0600. The Telegram bot can display the consent URL and receive the code from the owner.

## Decision

- Two email plugins sharing one `mail` domain interface:
  - `gmail` using the Gmail API with minimal scopes (start `gmail.readonly` + `gmail.send`; consider `gmail.modify` only if label changes are needed).
  - `mailbox` for any other mailbox over IMAP and SMTP. See [email-smtp-imap.md](email-smtp-imap.md).
- **Recommend an app password plus IMAP/SMTP** as the zero-friction path for personal Gmail and document the Google Cloud path as the richer option.
- Reading is always safe-by-default; **sending requires confirmation** in Telegram every time unless the owner enables an allowlist.
- Email bodies are untrusted input and are wrapped as data in prompts.

## Verified 2026-10-12

- The 7-day refresh token limit for apps in Testing status with an external user type is current, and applies as soon as any non-identity scope is requested ([Google OAuth documentation](https://developers.google.com/identity/protocols/oauth2#expiration)). Other expiry causes: revoked access, six months of non-use, a password change while the token holds Gmail scopes, and too many live tokens.
- The device flow does **not** support Gmail scopes (it allows only OpenID, Drive file and app data, and YouTube scopes), so a headless consent flow is not an option. The plugin uses a refresh token the owner creates once; see [the setup guide](../../guides/gmail-setup.md).
- Built as `plugins/gmail` over the REST API with `net/http` and no Google client library.

## Open questions

- Is the 7-day testing expiry still current? Verify, and document the consent-screen setup step by step in `docs/guides/gmail-setup.md`.
- Can the device flow be used with Gmail scopes for this client type? Verify at implementation.

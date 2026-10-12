# Gmail setup (Gmail API)

The [gmail plugin](../../plugins/gmail/README.md) uses the Gmail API with an OAuth refresh token. It needs a Google Cloud project and a one-time authorization, so it is more work than an app password. If you only want Kest to read and send mail, the [mailbox plugin](mailbox-setup.md) with a Gmail app password is simpler and works the same way for you. Choose the API when your account cannot make app passwords, or you want Gmail's own search and threading.

Facts checked against Google's documentation on 2026-10-12: apps in **Testing** status with an external user type get refresh tokens that expire after **7 days** once they use any non-identity scope; the OAuth device flow does **not** support Gmail scopes; refresh tokens also stop working if you revoke access, change the password while the token has Gmail scopes, or leave them unused for six months. These rules change, so re-check [Google's OAuth documentation](https://developers.google.com/identity/protocols/oauth2#expiration) if something stops working.

## 1. Create the OAuth client

1. In the [Google Cloud console](https://console.cloud.google.com/), create a project, then enable the **Gmail API** for it.
2. Configure the **OAuth consent screen**. Choose **External** (or **Internal** for a Google Workspace you administer, which avoids the limits below). Add yourself as a test user.
3. **Publish the app** (set its status to **In production**) if you want the refresh token to last. For a personal app this shows an "unverified app" warning when you authorize; click through it. A Testing-mode token works but expires after 7 days and then needs repeating from step 2 of the next section.
4. Under **Credentials**, create an **OAuth client ID** of type **Web application**, and add `https://developers.google.com/oauthplayground` as an authorized redirect URI. Note the client id and secret.

## 2. Get a refresh token

1. Open the [OAuth 2.0 Playground](https://developers.google.com/oauthplayground), click the gear, tick **Use your own OAuth credentials** and enter the client id and secret.
2. In step 1, enter the scopes you want, one per line, and authorize with your Google account:
   - `https://www.googleapis.com/auth/gmail.readonly` to search and read
   - `https://www.googleapis.com/auth/gmail.send` to send
   - `https://www.googleapis.com/auth/gmail.compose` to save drafts
   - `https://www.googleapis.com/auth/gmail.modify` to mark mail read
3. In step 2, click **Exchange authorization code for tokens** and copy the **refresh token**.

Start with the read and send scopes; you can make a new token with more later. The plugin tells you which scope is missing if a tool needs one the token lacks.

## 3. Configure Kest

Add `gmail` to `KEST_PLUGINS` and set three secrets, in the environment (Pterodactyl startup variables **Gmail Client ID**, **Gmail Client Secret** and **Gmail Refresh Token**) or in `secrets.json`:

```text
KEST_SECRET_GMAIL_CLIENT_ID=...
KEST_SECRET_GMAIL_CLIENT_SECRET=...
KEST_SECRET_GMAIL_REFRESH_TOKEN=...
```

There are no required settings. Restart, check `/status`, and ask "what unread email do I have?". Settings and nudges are described in the [plugin README](../../plugins/gmail/README.md); nudges work as described in the [mailbox guide](mailbox-setup.md#nudges).

## When it stops working

| Message | What to do |
| --- | --- |
| `the stored Gmail authorization is no longer accepted` | The refresh token expired or was revoked. Repeat section 2 and replace `KEST_SECRET_GMAIL_REFRESH_TOKEN`. If it keeps happening weekly, the app is still in Testing: publish it |
| `the OAuth client id or secret was rejected` | The two client values do not match the project the token came from |
| `does not include the permission for this` | Create a new token with the scope named in the [permissions table](../../plugins/gmail/README.md#permissions) |
| `secret ... is not set` | One of the three variables is missing or empty |

To revoke Kest's access, remove the app at [myaccount.google.com/permissions](https://myaccount.google.com/permissions).

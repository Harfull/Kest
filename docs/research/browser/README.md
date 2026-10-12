# Browser runtime research

## Question

Can Kest operate a persistent real browser inside Pterodactyl, while keeping its ordinary static-binary deployment and existing tool security policy?

## Sources

Checked 2026-10-11:

- [Playwright Docker](https://playwright.dev/docs/docker): runtime dependencies, matching versions, non-root execution, Chromium sandbox and seccomp requirements.
- [Persistent browser context](https://playwright.dev/docs/api/class-browsertype#browser-type-launch-persistent-context): a dedicated user-data directory preserves cookies and local storage; multiple instances cannot share it.
- [Wings container configuration](https://github.com/pterodactyl/wings/blob/develop/environment/docker/container.go): arbitrary configured images, numeric user identity, read-only root, writable `/tmp`, `no-new-privileges`, and `~` local-image references. This is upstream source, not a guarantee about every hosting provider's version.
- [Chromium AppArmor restrictions](https://chromium.googlesource.com/chromium/src/+/main/docs/security/apparmor-userns-restrictions.md): user namespace restrictions can block Chromium's sandbox on Ubuntu.
- [Playwright 1.64.0 package](https://registry.npmjs.org/playwright/1.64.0): Apache-2.0, Node >=20, one runtime dependency (`playwright-core`), no Go module changes. Its matching pinned browser image is available in the official Docker guide.

## Findings

- A real browser fits a Pterodactyl server container. The base runtime must include Node, Chromium and shared libraries; installing these in the egg's separate Alpine installer does not populate the server runtime's root filesystem.
- Browser profiles must live on the server's persistent data volume. A separate Playwright process can be driven over private stdin/stdout with no remote-control port.
- A Pterodactyl egg cannot change the node's kernel, AppArmor or Docker seccomp policy. The recommended sandboxed deployment is conditional on administrator support. The supplied standalone Compose setup can apply Playwright's seccomp profile to its own container.
- Browser navigation, typing and clicks can change remote state, including through URLs and autosaving fields. They therefore require owner approval. Page text, titles and locators remain untrusted.
- Checking a requested URL once does not protect subresources or redirects. An authenticated loopback proxy can enforce the registry's manifest allowlist and actual socket-address checks for both HTTP and HTTPS tunnels; browser settings disable implicit loopback bypass and non-proxied UDP WebRTC.
- noVNC is optional and carries manual session access. It belongs behind a loopback mapping plus an SSH tunnel, with VNC authentication; it does not replace tool approvals for model actions.

## Decision

Use a native optional browser plugin, an embedded bounded Playwright worker, a dedicated persistent profile, and matching pinned Node package/container versions. Keep Browser Use out because Kest already provides orchestration, risk policy and approvals. Ship separate browser and desktop image targets plus a browser-specific generated egg. Keep the minimal default deployment unchanged. See [ADR 0008](../../decisions/0008-optional-browser-runtime.md).

## Open questions

- The custom image and sandbox must be tested on the target Wings node; local Windows Chromium verification does not prove Linux host compatibility.
- This first plugin returns text/locators and local screenshots. Multimodal screenshot input, Telegram image delivery, downloads and richer iframe locators are possible future extensions.
- Browser compatibility and security updates require deliberate package/image/profile maintenance. There is no promised RAM usage for arbitrary sites.

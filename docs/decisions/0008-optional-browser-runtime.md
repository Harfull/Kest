# 0008. Optional Chromium and Playwright runtime

- Status: Accepted
- Date: 2026-10-11

## Context

Kest needs an actual browser for rendered web pages and interaction. Its ordinary deployment is a static Go binary with SQLite. Chromium needs an executable, system libraries and a persistent profile; Playwright's supported Node runtime supplies stable browser automation without adding a large Go client dependency. Pterodactyl separates install and runtime containers and cannot grant browser sandbox prerequisites through egg fields alone.

## Decision

Make browser support opt-in through the native `browser` plugin. Embed a Node worker in the Go binary and communicate through private process pipes. Pin Playwright 1.64.0 and its matching Ubuntu-based browser image. The registry grants declared `files` access to a plugin directory and offers manifest-scoped TCP dialing with the same public-address guard as HTTP. The browser uses an authenticated local proxy for web traffic.

Keep Chromium's sandbox enabled by default and fail if the host cannot support it; never fall back silently. Require approval for navigation, clicks, keys, filling and uploads. Mark every browser result untrusted, restrict uploads to the owner-populated inbox, and expose no arbitrary evaluation or remote-control interface. Keep cookies and local storage under the existing application data root.

Ship a separate browser image and generated egg, a standalone Compose setup with namespace-capable seccomp, and an optional noVNC desktop protected by authentication and SSH. Keep the minimal static image and the default enabled plugin list. This is an explicit optional runtime exception to ADR 0001, which remains the default deployment.

## Consequences

- Default Kest needs no Node, browser or extra service. Browser-enabled deployments do need these additional runtime components and more resources.
- The custom Wings image must be installed or published by its administrator. Host sandbox compatibility is a deployment prerequisite, not an egg promise.
- The profile contains sensitive sessions and needs whole-directory backups with Kest stopped; SQLite exports are insufficient.
- Worker environment variables exclude Kest credentials; raw browser errors are discarded because they may contain submitted values. Page text and URLs still enter model context.
- Text and locators support the first interaction workflow. Screenshots are local files, rather than automatic Telegram attachments or vision input. Downloads and WebSockets are disabled.
- Native plugin filesystem access is scoped by convention and declared capability; it does not turn in-process Go plugins into hostile-code sandboxes. Chromium's sandbox and container isolation remain essential.

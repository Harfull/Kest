# Optional browser runtime

This directory holds the locked Playwright package, private container entrypoint, optional desktop supervisor and upstream seccomp profile. [Deployment instructions](../../docs/deployment/browser.md) cover Pterodactyl, Docker and standalone binaries. The worker itself lives in [plugins/browser](../../plugins/browser/README.md) and is embedded in the Go binary.

`seccomp_profile.json` is copied verbatim from [Playwright v1.64.0](https://github.com/microsoft/playwright/blob/v1.64.0/utils/docker/seccomp_profile.json), copyright Microsoft Corporation, under Apache-2.0; see [LICENSE.playwright](LICENSE.playwright). The Playwright package is also Apache-2.0 and only brings `playwright-core` as a runtime dependency. It is isolated to this optional setup and does not change Kest's Go dependencies.

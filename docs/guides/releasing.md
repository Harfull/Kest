# Releases and versioning

Kest uses [Semantic Versioning](https://semver.org/) with pre-release tags while it is in beta.

## Version numbers

| Tag | Meaning |
| --- | --- |
| `v0.1.0-beta.1`, `v0.1.0-beta.2`, … | Beta builds of the upcoming 0.1.0. Features may change; data formats migrate forward. Published as GitHub **pre-releases** |
| `v0.1.0` | First stable release |
| `v0.2.0-beta.1` … | Betas of the next minor version |
| `v0.1.1` | Bug-fix release |

Rules:

- While the major version is `0`, the minor number may include breaking changes; they are called out in the changelog.
- A hyphen in the tag (`-beta.N`) marks a pre-release. The release workflow sets the GitHub pre-release flag from it, and Docker does not tag pre-releases as `latest`.
- Databases migrate forward only. Kest refuses to open a database written by a newer version, so downgrading means restoring a backup from `data/backups/`.
- Each beta increments `N` by one; the first stable release drops the suffix.

## What a release contains

Pushing a `v*` tag runs `.github/workflows/release.yml`, which:

1. checks that the generated egg and plugin list are current and runs the tests;
2. builds `kest-linux-amd64` and `kest-linux-arm64` (static, `CGO_ENABLED=0`) with the version stamped in, plus `checksums.txt`;
3. publishes a GitHub release with generated notes, the binaries, the checksums and `egg-kest.json`;
4. builds and pushes a multi-architecture image to `ghcr.io/harfull/kest`.

`kest -version` prints the tag, for example `kest v0.1.0-beta.1 (a1b2c3d)`.

## Cutting a release

1. Be on `main` with CI green.
2. In `CHANGELOG.md`, rename `Unreleased` content into a dated section for the new version and leave an empty `Unreleased` above it.
3. Commit (`docs: prepare v0.1.0-beta.2`) and push; wait for CI.
4. Tag and push:

   ```bash
   git tag -a v0.1.0-beta.2 -m "v0.1.0-beta.2"
   git push origin v0.1.0-beta.2
   ```

5. Watch the Release workflow, then open the release page and check the four assets are attached (two binaries, `checksums.txt`, `egg-kest.json`).

To retract a bad beta, delete the GitHub release and the tag, and publish the next number. Never reuse a tag.

## Testing the Pterodactyl egg from a release

1. Download `egg-kest.json` from the release page and import it: **Admin → Nests → Import Egg**.
2. Create a server from it. Leave **Kest Version** on `latest`, or set an exact tag such as `v0.1.0-beta.1`.
3. Follow [the Pterodactyl guide](../deployment/pterodactyl.md).

`latest` resolves to the newest stable release, or to the newest pre-release while no stable release exists. The installer verifies the binary against `checksums.txt` before installing. To move to a newer beta, change **Kest Version** (or leave `latest`) and use **Settings → Reinstall Server**; the `data/` folder is kept.

## Container image

The first image push creates the package as private. To pull it without logging in, open the package page on GitHub and set its visibility to public. Pterodactyl does not use this image; the egg installs the binary directly.

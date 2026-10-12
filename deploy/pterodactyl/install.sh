#!/bin/ash
# Kest installation script for Pterodactyl.
#
# Runs inside ghcr.io/pterodactyl/installers:alpine with the server volume at
# /mnt/server. The panel passes egg variables as environment variables.
#
# This file is the source of truth. `go run ./scripts/sync-egg` copies it into
# deploy/pterodactyl/egg-kest.json.
#
# KEST_VERSION is a release tag such as v0.1.0-beta.1, or "latest": the newest
# stable release, or the newest pre-release while no stable release exists.

set -eu

REPO="Harfull/Kest"
VERSION="${KEST_VERSION:-latest}"

# tag_from prints the tag_name of the first release in a GitHub API response
# fetched from the given URL, or nothing if the request or parsing fails.
tag_from() {
  curl -fsSL -H 'Accept: application/vnd.github+json' "$1" 2>/dev/null \
    | grep -m1 '"tag_name"' \
    | sed 's/.*"tag_name"[^"]*"\([^"]*\)".*/\1/' || true
}

# resolve_tag prints the release tag to install.
#
# GitHub's "latest" endpoint ignores pre-releases, so while Kest is in beta it
# answers 404. In that case the newest release of any kind is used.
resolve_tag() {
  if [ "$VERSION" != "latest" ]; then
    echo "$VERSION"
    return 0
  fi
  tag="$(tag_from "https://api.github.com/repos/${REPO}/releases/latest")"
  if [ -z "$tag" ]; then
    tag="$(tag_from "https://api.github.com/repos/${REPO}/releases?per_page=1")"
  fi
  if [ -z "$tag" ]; then
    echo "kest-install: no releases found for ${REPO}" >&2
    return 1
  fi
  echo "$tag"
}

# Tests source this file for the functions above without running the install.
if [ "${KEST_INSTALL_LIB:-}" = "1" ]; then
  return 0
fi

cd /mnt/server
mkdir -p data

case "$(uname -m)" in
  x86_64 | amd64) ARCH="amd64" ;;
  aarch64 | arm64) ARCH="arm64" ;;
  *)
    echo "kest-install: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

TAG="$(resolve_tag)"
case "$TAG" in
  *[!A-Za-z0-9._-]* | "")
    echo "kest-install: invalid release tag: ${TAG}" >&2
    exit 1
    ;;
esac

BASE="https://github.com/${REPO}/releases/download/${TAG}"
ASSET="kest-linux-${ARCH}"

echo "kest-install: downloading ${ASSET} (${TAG})"
curl -fsSL -o kest.download "${BASE}/${ASSET}"
curl -fsSL -o checksums.txt "${BASE}/checksums.txt"

expected="$(grep " ${ASSET}\$" checksums.txt | cut -d' ' -f1)"
actual="$(sha256sum kest.download | cut -d' ' -f1)"

if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
  echo "kest-install: checksum mismatch for ${ASSET}" >&2
  rm -f kest.download checksums.txt
  exit 1
fi

chmod +x kest.download
mv kest.download kest
rm -f checksums.txt

echo "kest-install: installed ${TAG}"

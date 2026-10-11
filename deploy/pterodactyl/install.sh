#!/bin/ash
# Kest installation script for Pterodactyl.
#
# Runs inside ghcr.io/pterodactyl/installers:alpine with the server volume at
# /mnt/server. The panel passes egg variables as environment variables.
#
# This file is the source of truth. `go run ./scripts/sync-egg` copies it into
# deploy/pterodactyl/egg-kest.json.

set -eu

REPO="Harfull/Kest"
VERSION="${KEST_VERSION:-latest}"

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

if [ "$VERSION" = "latest" ]; then
  BASE="https://github.com/${REPO}/releases/latest/download"
else
  BASE="https://github.com/${REPO}/releases/download/${VERSION}"
fi

ASSET="kest-linux-${ARCH}"

echo "kest-install: downloading ${ASSET} (${VERSION})"
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

echo "kest-install: done"

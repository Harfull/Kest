#!/bin/bash
set -euo pipefail
cd /home/container
# Wings supplies STARTUP; this image intentionally supports Kest's fixed command
# instead of evaluating panel text as shell code. A panel-installed binary wins.
binary=/usr/local/bin/kest
if [[ -x /home/container/kest ]]; then binary=/home/container/kest; fi
if [[ -x /desktop.sh ]]; then exec /desktop.sh "$binary" "$@"; fi
exec "$binary" "$@"

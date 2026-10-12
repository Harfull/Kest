#!/bin/bash
set -euo pipefail
umask 077
password=${KEST_SECRET_BROWSER_VNC_PASSWORD:-}
if [[ ${#password} -lt 8 || "$password" == *$'\n'* || "$password" == *$'\r'* ]]; then
  echo 'kest-desktop: set KEST_SECRET_BROWSER_VNC_PASSWORD to at least 8 characters' >&2
  exit 1
fi
runtime=$(mktemp -d /tmp/kest-desktop.XXXXXX)
children=()
cleanup() {
  trap - EXIT TERM INT
  if [[ ${#children[@]} -gt 0 ]]; then
    kill -TERM "${children[@]}" 2>/dev/null || true
    wait "${children[@]}" 2>/dev/null || true
  fi
  rm -f "$runtime/password"
  rmdir "$runtime" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 0' TERM INT
export DISPLAY=:99
export KEST_BROWSER_HEADLESS=false
# A private password file keeps credentials out of process arguments.
printf '%s\n' "$password" > "$runtime/password"
unset password KEST_SECRET_BROWSER_VNC_PASSWORD
env -i PATH="$PATH" HOME="$HOME" Xvfb :99 -screen 0 1280x800x24 -nolisten tcp >/dev/null 2>&1 &
children+=("$!")
for attempt in {1..50}; do
  [[ -S /tmp/.X11-unix/X99 ]] && break
  sleep 0.1
done
env -i PATH="$PATH" HOME="$HOME" x11vnc -display :99 -passwdfile "$runtime/password" -localhost -rfbport 5900 -forever -shared >/dev/null 2>&1 &
children+=("$!")
# In Docker, publish this only on host loopback. In Wings this remains internal
# unless an administrator explicitly assigns it a network allocation.
env -i PATH="$PATH" HOME="$HOME" websockify --web=/usr/share/novnc 0.0.0.0:6080 127.0.0.1:5900 >/dev/null 2>&1 &
children+=("$!")
"$@" &
children+=("$!")
wait -n "${children[@]}"

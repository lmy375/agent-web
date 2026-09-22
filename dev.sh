#!/usr/bin/env bash
# One command for development. The frontend reloads through Vite's own module
# replacement; the backend is rebuilt and restarted whenever a Go file changes.
# Ctrl-C stops both.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="$ROOT/backend/.env"
RUNDIR="$(mktemp -d "${TMPDIR:-/tmp}/agent-web-dev.XXXXXX")"
STAMP="$RUNDIR/stamp"
BIN="$RUNDIR/agentweb"
BACKEND_PID=""
BACKEND_PGID=""
FRONTEND_PGID=""

BACKEND_TAG=$'\033[36m[backend]\033[0m'
FRONTEND_TAG=$'\033[35m[frontend]\033[0m'
DEV_TAG=$'\033[33m[dev]\033[0m'

say() { printf '%s %s\n' "$DEV_TAG" "$*"; }
die() { printf '%s \033[31m%s\033[0m\n' "$DEV_TAG" "$*" >&2; exit 1; }
prefix() { awk -v tag="$1" '{ printf "%s %s\n", tag, $0; fflush() }'; }

# Each side runs in a process group of its own, so the whole tree behind it —
# pnpm's Vite child, the compiled server — goes down with one signal.
stop_group() {
  local pgid="$1" waited=0
  [ -n "$pgid" ] || return 0
  kill -TERM -"$pgid" 2>/dev/null || return 0
  while kill -0 -"$pgid" 2>/dev/null && [ "$waited" -lt 6 ]; do
    sleep 0.25
    waited=$((waited + 1))
  done
  kill -KILL -"$pgid" 2>/dev/null
  return 0
}

cleanup() {
  local code=$?
  trap - EXIT INT TERM
  say 'stopping'
  stop_group "$FRONTEND_PGID"
  stop_group "$BACKEND_PGID"
  disown -a 2>/dev/null
  rm -rf "$RUNDIR"
  exit "$code"
}
trap cleanup EXIT INT TERM

# Settings come from the real environment first and backend/.env second, the
# order the server resolves them in itself.
setting() {
  # Separate lines: local expands all its arguments before it assigns any of
  # them, so the indirect read has to come after key already holds a name.
  local key="AGENT_WEB_$1"
  local fallback="$2"
  local value="${!key:-}"
  if [ -z "$value" ] && [ -f "$ENV_FILE" ]; then
    value="$(sed -n "s/^[[:space:]]*$key[[:space:]]*=[[:space:]]*//p" "$ENV_FILE" | tail -n 1 | tr -d "\"' ")"
  fi
  printf '%s' "${value:-$fallback}"
}

# An open event stream keeps Shutdown waiting for its full timeout, so the
# server gets a moment to close on its own and is taken away after that.
stop_backend() {
  [ -n "$BACKEND_PID" ] || return 0
  kill "$BACKEND_PID" 2>/dev/null || { BACKEND_PID=""; return 0; }
  local waited=0
  while kill -0 "$BACKEND_PID" 2>/dev/null && [ "$waited" -lt 2 ]; do
    sleep 0.25
    waited=$((waited + 1))
  done
  kill -9 "$BACKEND_PID" 2>/dev/null
  wait "$BACKEND_PID" 2>/dev/null
  BACKEND_PID=""
}

rebuild() {
  touch "$STAMP"
  if ! go build -o "$BIN.next" ./cmd/agentweb; then
    if [ -n "$BACKEND_PID" ]; then
      echo 'build failed; the running server stays up'
    else
      echo 'build failed'
    fi
    return 0
  fi
  mv -f "$BIN.next" "$BIN"
  stop_backend
  "$BIN" &
  BACKEND_PID=$!
}

# A deleted or renamed file leaves behind no timestamp of its own, so the
# directories are watched next to the sources.
changed() {
  [ -n "$(find . \( -name '*.go' -o -name go.mod -o -name go.sum -o -type d \) -newer "$STAMP" -print -quit)" ]
}

backend_loop() {
  cd "$ROOT/backend" || exit 1
  trap 'stop_backend; exit 0' TERM INT
  rebuild
  while :; do
    sleep 0.5
    if changed; then
      echo 'change detected, rebuilding'
      rebuild
    fi
  done
}

command -v go >/dev/null || die 'go not found on PATH'
command -v pnpm >/dev/null || die 'pnpm not found on PATH; see https://pnpm.io'

if [ ! -d "$ROOT/frontend/node_modules" ]; then
  say 'installing frontend dependencies'
  (cd "$ROOT/frontend" && pnpm install) || die 'pnpm install failed'
fi

PORT="$(setting PORT 8000)"
HOST="$(setting HOST 127.0.0.1)"
case "$HOST" in
  0.0.0.0 | :: | '') PROXY_HOST=127.0.0.1 ;;
  *) PROXY_HOST="$HOST" ;;
esac

if command -v lsof >/dev/null; then
  holder="$(lsof -nP -iTCP:"$PORT" -sTCP:LISTEN 2>/dev/null | awk 'NR > 1 { print $1 " (pid " $2 ")"; exit }')"
  [ -n "$holder" ] && die "port $PORT is already taken by $holder; stop it, or set AGENT_WEB_PORT to another port"
fi

say "backend on http://$PROXY_HOST:$PORT, frontend on http://localhost:5173"

set -m
backend_loop 2>&1 | prefix "$BACKEND_TAG" &
BACKEND_PGID="$(ps -o pgid= -p $! | tr -d ' ')"
(
  cd "$ROOT/frontend" || exit 1
  BACKEND_URL="http://$PROXY_HOST:$PORT" FORCE_COLOR=1 exec pnpm dev
) 2>&1 | prefix "$FRONTEND_TAG" &
FRONTEND_PGID="$(ps -o pgid= -p $! | tr -d ' ')"
set +m

# Whichever side stops first takes the other one with it, so a crash is visible
# instead of leaving half the stack running.
while kill -0 -"$BACKEND_PGID" 2>/dev/null && kill -0 -"$FRONTEND_PGID" 2>/dev/null; do
  sleep 1
done

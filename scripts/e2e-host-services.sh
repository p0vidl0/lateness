#!/usr/bin/env bash
# Runs lateness serve and lateness sweep as host processes against the lateness-e2e
# compose stack (Postgres, the outlet/probe receiver and the test SMTP server). Host
# builds reuse the warm module cache; the two roles are still separate OS processes.
#
# Usage: e2e-host-services.sh up|down
# Required env (after sourcing .docker/env/lateness-e2e.env):
#   LATENESS_DATABASE_URL_HOST, LATENESS_HTTP_PORT, LATENESS_SWEEP_INTERVAL, LATENESS_PUBLIC_URL,
#   LATENESS_SECRET_KEY, LATENESS_TELEGRAM_API_URL
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib/host-process.sh
source "$ROOT/scripts/lib/host-process.sh"

RUN_DIR="${E2E_HOST_RUN_DIR:-$ROOT/.docker/e2e-host-run}"
BIN_DIR="$RUN_DIR/bin"
PID_DIR="$RUN_DIR/pids"
LOG_DIR="$RUN_DIR/logs"
cmd="${1:-}"

wait_health() {
  local name="$1" url="$2" attempts="${3:-40}"
  local _
  for _ in $(seq 1 "$attempts"); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  echo "$name did not become ready at $url" >&2
  tail -n 80 "$LOG_DIR/$name.log" >&2 || true
  return 1
}

stop_one() {
  local name="$1"
  local pid_file="$PID_DIR/$name.pid"
  if [[ ! -f "$pid_file" ]]; then
    return 0
  fi
  local pid
  pid="$(cat "$pid_file")"
  if kill -0 "$pid" >/dev/null 2>&1; then
    kill "$pid" >/dev/null 2>&1 || true
    local _
    for _ in $(seq 1 20); do
      if ! kill -0 "$pid" >/dev/null 2>&1; then
        break
      fi
      sleep 0.2
    done
    kill -9 "$pid" >/dev/null 2>&1 || true
  fi
  rm -f "$pid_file"
}

start_one() {
  local name="$1"
  local role="$2"
  shift 2
  stop_one "$name"
  (
    env "$@" "$BIN_DIR/lateness" "$role" >"$LOG_DIR/$name.log" 2>&1 &
    echo $! >"$PID_DIR/$name.pid"
  )
}

up() {
  : "${LATENESS_DATABASE_URL_HOST:?}"
  : "${LATENESS_HTTP_PORT:?}"
  : "${LATENESS_SWEEP_INTERVAL:?}"
  : "${LATENESS_SECRET_KEY:?}"
  ensure_port_free "$LATENESS_HTTP_PORT" "Stop make serve-dev or a previous e2e stack."
  mkdir -p "$BIN_DIR" "$PID_DIR" "$LOG_DIR"
  echo "Building lateness..."
  (cd "$ROOT/backend" && go build -o "$BIN_DIR/lateness" ./cmd/lateness)
  echo "Starting lateness serve on :${LATENESS_HTTP_PORT}..."
  start_one serve serve \
    LATENESS_DATABASE_URL="$LATENESS_DATABASE_URL_HOST" \
    LATENESS_HTTP_ADDR=":${LATENESS_HTTP_PORT}" \
    LATENESS_SEED_EMAIL= \
    LATENESS_SEED_SMTP_HOST=
  wait_health serve "http://127.0.0.1:${LATENESS_HTTP_PORT}/readyz"
  echo "Starting lateness sweep..."
  start_one sweep sweep \
    LATENESS_DATABASE_URL="$LATENESS_DATABASE_URL_HOST"
  sleep 0.3
  if ! kill -0 "$(cat "$PID_DIR/sweep.pid")" >/dev/null 2>&1; then
    echo "sweep exited" >&2
    tail -n 80 "$LOG_DIR/sweep.log" >&2 || true
    return 1
  fi
}

down() {
  stop_one sweep || true
  stop_one serve || true
}

case "$cmd" in
  up) up ;;
  down) down ;;
  *)
    echo "usage: $0 up|down" >&2
    exit 2
    ;;
esac

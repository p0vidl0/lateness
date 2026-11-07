#!/usr/bin/env bash
# Shared helpers for host processes started next to a compose stack.

ensure_port_free() {
  local port="$1"
  local hint="${2:-}"
  if command -v lsof >/dev/null 2>&1 && lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "port ${port} is in use. ${hint}" >&2
    return 1
  fi
}

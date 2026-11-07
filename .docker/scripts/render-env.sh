#!/usr/bin/env bash
# Render .docker/env/<COMPOSE_PROJECT_NAME>.env from template + profile.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DOCKER_DIR="${ROOT}/.docker"
COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:?COMPOSE_PROJECT_NAME is required}"

PROFILE="${DOCKER_DIR}/env/profiles/${COMPOSE_PROJECT_NAME}.env"
TEMPLATE="${DOCKER_DIR}/env/template.env"
OUT="${DOCKER_DIR}/env/${COMPOSE_PROJECT_NAME}.env"

if [[ ! -f "${PROFILE}" ]]; then
  echo "missing profile: ${PROFILE}" >&2
  exit 1
fi
if [[ ! -f "${TEMPLATE}" ]]; then
  echo "missing template: ${TEMPLATE}" >&2
  exit 1
fi

set -a
# shellcheck disable=SC1090
source "${PROFILE}"
set +a
export COMPOSE_PROJECT_NAME

(cd "${ROOT}/backend" && go run ./cmd/render-env "${TEMPLATE}" "${OUT}")

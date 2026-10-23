#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
test -f .env || { echo 'Please copy .env.example to .env'; exit 1; }
# Read only the port settings from .env so custom ports are probed correctly.
WEB_PORT="$(sed -n 's/^WEB_PORT=//p' .env | tail -n1)"
API_PORT="$(sed -n 's/^API_PORT=//p' .env | tail -n1)"
docker compose --env-file .env config --quiet
docker compose --env-file .env ps
curl --fail --silent --show-error "http://127.0.0.1:${WEB_PORT:-3000}/health"
printf '\n'
curl --fail --silent --show-error "http://127.0.0.1:${API_PORT:-8080}/health/live"
printf '\n'
curl --fail --silent --show-error "http://127.0.0.1:${API_PORT:-8080}/health/ready"
printf '\n'

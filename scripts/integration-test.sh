#!/usr/bin/env sh
set -euo pipefail

if [ ! -f ".env" ]; then
  echo "ERROR: .env not found in repo root." >&2
  echo "Copy it next to this script (repo root), then re-run." >&2
  exit 1
fi

# shellcheck disable=SC2046
export $(grep -v '^[[:space:]]*#' .env | grep -E '^[A-Za-z_][A-Za-z0-9_]*=' | xargs)

NET="${DOCKER_NETWORK:-medkvadrat-internal}"
if ! docker network inspect "${NET}" >/dev/null 2>&1; then
  echo "ERROR: docker network '${NET}' not found." >&2
  echo "Run this script on a host where api-gateway compose stack exists," >&2
  echo "or override with DOCKER_NETWORK=host (less isolated)." >&2
  exit 1
fi

for v in DB_SERVER DB_PORT DB_NAME DB_USER DB_PASSWORD; do
  if [ -z "${!v:-}" ]; then
    echo "ERROR: missing ${v} in .env" >&2
    exit 1
  fi
done

IMG="${GO_IMAGE:-golang:1.22-alpine}"

echo "Running integration tests in ${IMG} on network ${NET}"
echo "Repo: $(pwd)"

docker run --rm \
  --network "${NET}" \
  -v "$(pwd)":/src -w /src \
  -e INTEGRATION_MSSQL=1 \
  -e DB_SERVER -e DB_PORT -e DB_NAME -e DB_USER -e DB_PASSWORD \
  "${IMG}" \
  sh -ceu '
    apk add --no-cache git >/dev/null
    go test -tags=integration ./internal/integration/... -v
  '


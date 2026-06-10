#!/usr/bin/env bash
# Local dev API: LOCAL=1 LOCAL_SYNC=1 against REAL dev AWS (S3 + DynamoDB)
# and the real Anthropic API. No emulators (avoids local-vs-real
# split-brain). Env from secrets/dev.env (gitignored).
set -euo pipefail

cd "$(dirname "$0")/.."
if [ ! -f secrets/dev.env ]; then
  echo "secrets/dev.env missing — copy config/dev.env.example and fill it in" >&2
  exit 1
fi
set -a
# shellcheck disable=SC1091
source secrets/dev.env
set +a

export LOCAL=1 LOCAL_SYNC=1 ROLE=api
export GIT_SHA=$(git rev-parse --short HEAD 2>/dev/null || echo "nogit")
exec go run ./functions/api

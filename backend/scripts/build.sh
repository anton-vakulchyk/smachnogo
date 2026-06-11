#!/usr/bin/env bash
# Builds both Lambda binaries from ONE git SHA (shared pkg/ = shared
# message/SK contracts; split-SHA deploys mis-parse). Output: backend/bin/.
set -euo pipefail

cd "$(dirname "$0")/.."
SHA=$(git rev-parse --short HEAD 2>/dev/null || echo "nogit")
DIRTY=""
if ! git diff --quiet 2>/dev/null || ! git diff --cached --quiet 2>/dev/null; then
  DIRTY="-dirty"
fi
export GIT_SHA="${SHA}${DIRTY}"

BIN_DIR="$(pwd)/bin"
mkdir -p "$BIN_DIR"
build_one() {
  local name=$1 dir=$2
  echo "building $name from $dir (sha=$GIT_SHA)"
  (cd "$dir" && GOWORK=off GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
    go build -tags lambda.norpc -ldflags "-s -w" -o bootstrap .)
  (cd "$dir" && zip -q -j "$BIN_DIR/$name.zip" bootstrap)
  rm -f "$dir/bootstrap"
}

build_one api functions/api
build_one scanworker functions/workers/scanworker
build_one presignup functions/presignup
echo "built: bin/api.zip bin/scanworker.zip bin/presignup.zip (GIT_SHA=$GIT_SHA)"

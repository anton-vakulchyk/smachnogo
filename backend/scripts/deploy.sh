#!/usr/bin/env bash
set -euo pipefail
ENV=""; ACTION=""; FILE="tfplan"
while [ $# -gt 0 ]; do case "$1" in
  --env) ENV="$2"; shift 2;;
  --plan-out) ACTION="plan"; FILE="${2:-tfplan}"; shift 2;;
  --apply) ACTION="apply"; FILE="${2:-tfplan}"; shift 2;;
  --apply-auto) ACTION="apply-auto"; shift;;
  *) echo "bad arg: $1" >&2; exit 2;;
esac; done
[ "$ENV" = dev ] || [ "$ENV" = prod ] || { echo "--env must be dev|prod" >&2; exit 2; }
cd "$(dirname "$0")/.."           # backend/
TF="terraform -chdir=terraform/envs/$ENV"
$TF init -input=false
case "$ACTION" in
  plan)       ./scripts/build.sh; $TF plan -input=false -out="$FILE" ;;
  apply)      $TF apply -input=false "$FILE" ;;          # apply the SAVED plan; reuse the zip the plan step built (no rebuild → hash stays valid)
  apply-auto) ./scripts/build.sh; $TF apply -input=false -auto-approve ;;  # dev only (plan reviewed on PR)
  *) echo "need --plan-out|--apply|--apply-auto" >&2; exit 2 ;;
esac

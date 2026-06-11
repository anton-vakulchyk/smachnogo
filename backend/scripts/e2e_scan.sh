#!/usr/bin/env bash
# E2E scan flow: create → S3 PUT → uploaded → poll → confirm → meal listed.
# Usage: e2e_scan.sh [--url http://localhost:8090] [--image tests/fixtures/two_plates.jpg]
set -euo pipefail

cd "$(dirname "$0")/.."
URL="http://localhost:8090"
IMAGE="tests/fixtures/two_plates.jpg"
AUTH="static"
while [[ $# -gt 0 ]]; do
  case $1 in
    --url) URL=$2; shift 2 ;;
    --image) IMAGE=$2; shift 2 ;;
    --auth) AUTH=$2; shift 2 ;;
    *) echo "unknown arg $1" >&2; exit 2 ;;
  esac
done

if [ "$AUTH" = "cognito" ]; then
  CLIENT_ID=$(grep '^COGNITO_CLIENT_ID=' secrets/dev.env | cut -d= -f2)
  CUSER=$(grep '^COGNITO_DEV_USERNAME=' secrets/dev.env | cut -d= -f2)
  CPASS=$(grep '^COGNITO_DEV_PASSWORD=' secrets/dev.env | cut -d= -f2)
  [ -n "$CLIENT_ID" ] || { echo "no COGNITO_* in secrets/dev.env — run scripts/create-dev-user.sh" >&2; exit 1; }
  TOKEN=$(AWS_PROFILE="${AWS_PROFILE:-smachnogo}" aws cognito-idp initiate-auth \
    --auth-flow USER_PASSWORD_AUTH --client-id "$CLIENT_ID" \
    --auth-parameters "USERNAME=$CUSER,PASSWORD=$CPASS" \
    --query 'AuthenticationResult.AccessToken' --output text)
  [ "$TOKEN" != "None" ] && [ -n "$TOKEN" ] || { echo "cognito auth failed" >&2; exit 1; }
  echo "auth: cognito access token obtained"
else
  TOKEN=$(grep '^STATIC_BEARER_TOKEN=' secrets/dev.env | cut -d= -f2)
  [ -n "$TOKEN" ] || { echo "no STATIC_BEARER_TOKEN in secrets/dev.env" >&2; exit 1; }
fi
[ -f "$IMAGE" ] || { echo "fixture $IMAGE missing" >&2; exit 1; }
auth=(-H "Authorization: Bearer $TOKEN")

SCAN_ID=$(uuidgen | tr 'A-Z' 'a-z')
echo "1) create scan $SCAN_ID"
create=$(curl -sf "${auth[@]}" -X POST "$URL/v1/scans" -d "{\"scan_id\":\"$SCAN_ID\"}")
status=$(echo "$create" | jq -r .status)
upload_url=$(echo "$create" | jq -r .upload.url)
[ "$status" = "PENDING_UPLOAD" ] || { echo "unexpected status $status" >&2; exit 1; }

echo "2) PUT photo to S3 ($(du -h "$IMAGE" | cut -f1| xargs))"
curl -sf -X PUT -H "Content-Type: image/jpeg" --data-binary "@$IMAGE" "$upload_url" -o /dev/null

echo "3) confirm upload"
curl -sf "${auth[@]}" -X POST "$URL/v1/scans/$SCAN_ID/uploaded" | jq -c .

echo "4) poll until terminal"
deadline=$(( $(date +%s) + 120 ))
while :; do
  scan=$(curl -sf "${auth[@]}" "$URL/v1/scans/$SCAN_ID")
  status=$(echo "$scan" | jq -r .status)
  case $status in
    READY|FAILED) break ;;
    *) [ "$(date +%s)" -lt "$deadline" ] || { echo "timeout polling" >&2; exit 1; }
       sleep 1.5 ;;
  esac
done
echo "   status=$status"
if [ "$status" = "FAILED" ]; then
  echo "$scan" | jq .
  exit 1
fi

is_food=$(echo "$scan" | jq -r .result.is_food)
dishes=$(echo "$scan" | jq '.result.dishes | length')
echo "   is_food=$is_food dishes=$dishes"
echo "$scan" | jq -c '.result.dishes[] | {label, calories_kcal, protein_g, confidence, needs_clarification}'
[ "$is_food" = "true" ] || { echo "expected food" >&2; exit 1; }
[ "$dishes" -ge 1 ] || { echo "expected >=1 dish" >&2; exit 1; }

echo "5) confirm dish 0 (portion 1.0) for today"
TODAY=$(date +%F)
meals=$(curl -sf "${auth[@]}" -X POST "$URL/v1/scans/$SCAN_ID/confirm" \
  -d "{\"dishes\":[{\"index\":0,\"portion_factor\":1.0}],\"date\":\"$TODAY\"}")
echo "$meals" | jq -c '.meals[] | {meal_id, label, calories_kcal, state}'

echo "6) re-confirm (idempotency) + list meals"
curl -sf "${auth[@]}" -X POST "$URL/v1/scans/$SCAN_ID/confirm" \
  -d "{\"dishes\":[{\"index\":0}],\"date\":\"$TODAY\"}" | jq -c '.meals | length'
curl -sf "${auth[@]}" "$URL/v1/meals?from=$TODAY&to=$TODAY" | jq -c "[.meals[] | select(.scan_id==\"$SCAN_ID\")] | length"

echo "E2E OK ✓"

#!/usr/bin/env bash
# One-command CloudWatch access for the smachnogo Lambdas.
#
#   scripts/logs.sh tail [dev|prod] [api|worker|dlq|presignup]   live tail (default: dev api)
#   scripts/logs.sh errors [dev|prod] [hours]                    error lines, api+worker (default: 24h)
#   scripts/logs.sh scans [dev|prod] [hours]                     analysis events w/ tokens+latency
#   scripts/logs.sh dash [dev|prod]                              open the CloudWatch dashboard
set -euo pipefail
export AWS_PROFILE="${AWS_PROFILE:-smachnogo}"

CMD="${1:-tail}"
ENV="${2:-dev}"

fn() {
  case "$1" in
    api) echo "smachnogo-api-$ENV" ;;
    worker) echo "smachnogo-scanworker-$ENV" ;;
    dlq) echo "smachnogo-dlqconsumer-$ENV" ;;
    presignup) echo "smachnogo-presignup-$ENV" ;;
    *) echo "unknown function: $1" >&2; exit 1 ;;
  esac
}

insights() { # $1 = hours back, $2 = query
  local groups=(--log-group-names "/aws/lambda/smachnogo-api-$ENV" "/aws/lambda/smachnogo-scanworker-$ENV")
  local qid
  qid=$(aws logs start-query "${groups[@]}" \
    --start-time "$(($(date +%s) - $1 * 3600))" --end-time "$(date +%s)" \
    --query-string "$2" --query queryId --output text)
  sleep 3
  for _ in 1 2 3 4 5 6 7 8; do
    local out status
    out=$(aws logs get-query-results --query-id "$qid")
    status=$(echo "$out" | jq -r .status)
    [ "$status" = "Complete" ] && { echo "$out" | jq -r '.results[] | [.[] | select(.field != "@ptr") | .value] | join(" | ")'; return; }
    sleep 2
  done
  echo "query timed out" >&2
}

case "$CMD" in
  tail)
    exec aws logs tail "/aws/lambda/$(fn "${3:-api}")" --follow --format short
    ;;
  errors)
    insights "${3:-24}" "fields @timestamp, msg, error, user_id, scan_id | filter level = 'error' | sort @timestamp desc | limit 100"
    ;;
  scans)
    insights "${3:-24}" "fields @timestamp, user_id, scan_id, is_food, dishes, tokens_in, tokens_out, latency_ms | filter msg = 'analysis complete' | sort @timestamp desc | limit 100"
    ;;
  dash)
    open "https://us-east-1.console.aws.amazon.com/cloudwatch/home?region=us-east-1#dashboards/dashboard/smachnogo-$ENV"
    ;;
  *)
    grep '^#   ' "$0" | sed 's/^#   //'
    exit 1
    ;;
esac

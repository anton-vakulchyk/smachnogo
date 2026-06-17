# Scan queue. Visibility 240s MUST stay ≥ the 170s scanworker Lambda timeout
# (AWS hard requirement) — and the worker's worst case is 2× the ~75s vision
# deadline (≈150s) because scanproc retries the vision call once in-process, so
# the message must not become visible again until that whole budget is spent.
# 240s vs 170s (~1.4×) is intentionally short of AWS's 6× guidance because the
# worker is fully idempotent: a rare overlapping redelivery wastes one LLM call,
# nothing more. maxReceiveCount left as-is.
resource "aws_sqs_queue" "scans_dlq" {
  name                      = "${local.prefix}-scans-${var.env}-dlq"
  message_retention_seconds = 14 * 24 * 3600
}

resource "aws_sqs_queue" "scans" {
  name                       = "${local.prefix}-scans-${var.env}"
  visibility_timeout_seconds = 240
  message_retention_seconds  = 4 * 24 * 3600
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.scans_dlq.arn
    maxReceiveCount     = 5
  })
}

resource "aws_cloudwatch_metric_alarm" "dlq_depth" {
  alarm_name          = "${local.prefix}-${var.env}-scans-dlq-depth"
  namespace           = "AWS/SQS"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  dimensions          = { QueueName = aws_sqs_queue.scans_dlq.name }
  statistic           = "Maximum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 1
  comparison_operator = "GreaterThanOrEqualToThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alerts.arn]
}

# Scan queue. Visibility 240s vs 90s worker timeout — consciously ~2.7×
# (not AWS's 6× guidance) because the worker is fully idempotent; a rare
# overlapping redelivery wastes one LLM call, nothing more.
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

# Presignup's log group was never Terraform-managed (the other Lambda
# groups live in lambda.tf). Retention policy: 30d dev / 90d prod.
resource "aws_cloudwatch_log_group" "presignup" {
  name              = "/aws/lambda/${aws_lambda_function.presignup.function_name}"
  retention_in_days = 90
}

# One pane: traffic, errors, latency, queue health, recent error lines.
resource "aws_cloudwatch_dashboard" "main" {
  dashboard_name = "${local.prefix}-${var.env}"
  dashboard_body = jsonencode({
    widgets = [
      {
        type = "metric", x = 0, y = 0, width = 8, height = 6,
        properties = {
          title = "API requests / errors", region = "us-east-1", stat = "Sum", period = 300,
          metrics = [
            ["AWS/ApiGateway", "Count", "ApiId", aws_apigatewayv2_api.api.id],
            [".", "4xx", ".", "."],
            [".", "5xx", ".", "."],
          ]
        }
      },
      {
        type = "metric", x = 8, y = 0, width = 8, height = 6,
        properties = {
          title   = "API latency p95 (ms)", region = "us-east-1", stat = "p95", period = 300,
          metrics = [["AWS/ApiGateway", "Latency", "ApiId", aws_apigatewayv2_api.api.id]]
        }
      },
      {
        type = "metric", x = 16, y = 0, width = 8, height = 6,
        properties = {
          title = "Worker: scans / errors / duration", region = "us-east-1", period = 300,
          metrics = [
            ["AWS/Lambda", "Invocations", "FunctionName", aws_lambda_function.worker.function_name, { stat = "Sum" }],
            [".", "Errors", ".", ".", { stat = "Sum" }],
            [".", "Duration", ".", ".", { stat = "p95", yAxis = "right" }],
          ]
        }
      },
      {
        type = "metric", x = 0, y = 6, width = 8, height = 6,
        properties = {
          title = "Queue depth / DLQ", region = "us-east-1", stat = "Maximum", period = 300,
          metrics = [
            ["AWS/SQS", "ApproximateNumberOfMessagesVisible", "QueueName", aws_sqs_queue.scans.name],
            [".", ".", ".", aws_sqs_queue.scans_dlq.name, { label = "DLQ" }],
          ]
        }
      },
      {
        type = "metric", x = 8, y = 6, width = 8, height = 6,
        properties = {
          title = "API lambda errors / throttles", region = "us-east-1", stat = "Sum", period = 300,
          metrics = [
            ["AWS/Lambda", "Errors", "FunctionName", aws_lambda_function.api.function_name],
            [".", "Throttles", ".", "."],
          ]
        }
      },
      {
        type = "log", x = 0, y = 12, width = 24, height = 8,
        properties = {
          title = "Recent errors (api + worker)", region = "us-east-1",
          query = "SOURCE '/aws/lambda/${aws_lambda_function.api.function_name}' | SOURCE '/aws/lambda/${aws_lambda_function.worker.function_name}' | fields @timestamp, msg, error, user_id, scan_id | filter level = 'error' | sort @timestamp desc | limit 50",
        }
      },
    ]
  })
}

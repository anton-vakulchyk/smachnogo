# Both Lambdas deploy from the zips scripts/build.sh produces — one git SHA
# for both (shared pkg/ contracts). Secrets (anthropic key, bearer token)
# are NOT here: they live in SSM SecureStrings created out-of-band, so they
# never enter tfstate.
locals {
  common_env = {
    ENV                = var.env
    TABLE_NAME         = aws_dynamodb_table.main.name
    BUCKET             = aws_s3_bucket.photos.bucket
    QUEUE_URL          = aws_sqs_queue.scans.url
    SSM_PREFIX         = "/${local.prefix}/${var.env}"
    LLM_PROVIDER       = var.llm_provider
    LLM_MODEL_VISION   = var.llm_model_vision
    LLM_MODEL_TEXT     = var.llm_model_text
    DAILY_SCAN_CAP     = tostring(var.daily_scan_cap)
    DAILY_ESTIMATE_CAP = tostring(var.daily_estimate_cap)
    SCANS_ENABLED      = "true" # fallback only — the SSM parameter wins (apply can't un-kill)
  }
}

resource "aws_lambda_function" "api" {
  function_name = "${local.prefix}-api-${var.env}"
  role          = aws_iam_role.api.arn
  architectures = ["arm64"]
  runtime       = "provided.al2"
  handler       = "bootstrap"
  filename      = "${path.module}/../../../bin/api.zip"
  source_code_hash = filebase64sha256("${path.module}/../../../bin/api.zip")
  memory_size   = 256
  timeout       = 28 # under API GW's 30s hard cap

  environment {
    variables = merge(local.common_env, {
      ROLE              = "api"
      AUTH_MODE         = var.auth_mode
      COGNITO_POOL_ID   = aws_cognito_user_pool.main.id
      COGNITO_CLIENT_ID = aws_cognito_user_pool_client.ios.id
    })
  }
}

resource "aws_lambda_function" "worker" {
  function_name = "${local.prefix}-scanworker-${var.env}"
  role          = aws_iam_role.worker.arn
  architectures = ["arm64"]
  runtime       = "provided.al2"
  handler       = "bootstrap"
  filename      = "${path.module}/../../../bin/scanworker.zip"
  source_code_hash = filebase64sha256("${path.module}/../../../bin/scanworker.zip")
  memory_size   = 512
  timeout       = 90 # Opus vision worst case + image download + SDK retries

  # The global Claude-call ceiling and cost circuit-breaker.
  reserved_concurrent_executions = var.worker_reserved_concurrency

  environment {
    variables = merge(local.common_env, { ROLE = "worker" })
  }
}

resource "aws_lambda_event_source_mapping" "worker_sqs" {
  event_source_arn        = aws_sqs_queue.scans.arn
  function_name           = aws_lambda_function.worker.arn
  batch_size              = 1
  function_response_types = ["ReportBatchItemFailures"]
}

resource "aws_cloudwatch_log_group" "api" {
  name              = "/aws/lambda/${aws_lambda_function.api.function_name}"
  retention_in_days = 30
}

resource "aws_cloudwatch_log_group" "worker" {
  name              = "/aws/lambda/${aws_lambda_function.worker.function_name}"
  retention_in_days = 30
}

# Per-Lambda least-privilege roles — no wildcards.

data "aws_iam_policy_document" "lambda_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

locals {
  ssm_param_arns = [
    "arn:aws:ssm:${var.region}:${local.account_id}:parameter/${local.prefix}/${var.env}/*",
  ]
}

# --- API role ---

resource "aws_iam_role" "api" {
  name               = "${local.prefix}-api-${var.env}"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

data "aws_iam_policy_document" "api" {
  statement {
    sid = "Table"
    actions = [
      "dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem",
      "dynamodb:Query", "dynamodb:DescribeTable",
      "dynamodb:ConditionCheckItem", "dynamodb:DeleteItem",
    ]
    resources = [aws_dynamodb_table.main.arn]
  }
  statement {
    sid       = "PresignScanUploads"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.photos.arn}/scans/*"]
  }
  statement {
    sid       = "Enqueue"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.scans.arn]
  }
  statement {
    sid       = "Params"
    actions   = ["ssm:GetParameter"]
    resources = local.ssm_param_arns
  }
}

resource "aws_iam_role_policy" "api" {
  name   = "inline"
  role   = aws_iam_role.api.id
  policy = data.aws_iam_policy_document.api.json
}

resource "aws_iam_role_policy_attachment" "api_logs" {
  role       = aws_iam_role.api.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

# --- Worker role ---

resource "aws_iam_role" "worker" {
  name               = "${local.prefix}-scanworker-${var.env}"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

data "aws_iam_policy_document" "worker" {
  statement {
    sid = "Table"
    actions = [
      "dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem",
      "dynamodb:Query", "dynamodb:DescribeTable",
      "dynamodb:ConditionCheckItem",
    ]
    resources = [aws_dynamodb_table.main.arn]
  }
  statement {
    sid       = "ReadPhotos"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.photos.arn}/scans/*"]
  }
  statement {
    sid = "Consume"
    actions = [
      "sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes",
      "sqs:ChangeMessageVisibility",
    ]
    resources = [aws_sqs_queue.scans.arn]
  }
  statement {
    sid       = "Params"
    actions   = ["ssm:GetParameter"]
    resources = local.ssm_param_arns
  }
}

resource "aws_iam_role_policy" "worker" {
  name   = "inline"
  role   = aws_iam_role.worker.id
  policy = data.aws_iam_policy_document.worker.json
}

resource "aws_iam_role_policy_attachment" "worker_logs" {
  role       = aws_iam_role.worker.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

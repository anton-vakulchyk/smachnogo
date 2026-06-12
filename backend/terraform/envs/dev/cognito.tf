# User pool for anonymous-first auth. M2: one pre-created dev user
# (scripts/create-dev-user.sh). M6: per-install silent signup.
#
# Deletion protection is non-negotiable: PK is USER#<sub> — recreating the
# pool changes every sub and orphans all DDB data even though the table
# survives.
resource "aws_cognito_user_pool" "main" {
  name                = "${local.prefix}-users-${var.env}"
  deletion_protection = "ACTIVE"

  # Usernames are opaque (device-generated in M6); no email/phone aliases,
  # no self-service recovery — account linking (M8) is the recovery story.
  username_configuration {
    case_sensitive = true
  }

  password_policy {
    minimum_length    = 20 # device-generated random secrets, not human passwords
    require_lowercase = false
    require_numbers   = false
    require_symbols   = false
    require_uppercase = false
  }

  admin_create_user_config {
    allow_admin_create_user_only = false # M6: per-install silent self-signup
  }

  # Auto-confirm trigger: anonymous device identities have nothing to verify.
  lambda_config {
    pre_sign_up = aws_lambda_function.presignup.arn
  }

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_iam_role" "presignup" {
  name               = "${local.prefix}-presignup-${var.env}"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_iam_role_policy_attachment" "presignup_logs" {
  role       = aws_iam_role.presignup.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_lambda_function" "presignup" {
  function_name = "${local.prefix}-presignup-${var.env}"
  role          = aws_iam_role.presignup.arn
  architectures = ["arm64"]
  runtime       = "provided.al2023"
  handler       = "bootstrap"
  filename      = "${path.module}/../../../bin/presignup.zip"
  source_code_hash = filebase64sha256("${path.module}/../../../bin/presignup.zip")
  memory_size   = 128
  timeout       = 5
}

resource "aws_lambda_permission" "cognito_presignup" {
  statement_id  = "AllowCognitoInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.presignup.function_name
  principal     = "cognito-idp.amazonaws.com"
  source_arn    = aws_cognito_user_pool.main.arn
}

resource "aws_cognito_user_pool_client" "ios" {
  name         = "${local.prefix}-ios-${var.env}"
  user_pool_id = aws_cognito_user_pool.main.id

  # Public client (no secret — it's an iOS app).
  generate_secret = false

  explicit_auth_flows = [
    "ALLOW_USER_PASSWORD_AUTH",
    "ALLOW_REFRESH_TOKEN_AUTH",
  ]

  access_token_validity  = 1  # hours
  id_token_validity      = 1  # hours
  refresh_token_validity = 30 # days
  token_validity_units {
    access_token  = "hours"
    id_token      = "hours"
    refresh_token = "days"
  }

  prevent_user_existence_errors = "ENABLED"
}

output "cognito_pool_id" {
  value = aws_cognito_user_pool.main.id
}

output "cognito_client_id" {
  value = aws_cognito_user_pool_client.ios.id
}

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
    allow_admin_create_user_only = true # M6 flips this for self-signup
  }

  lifecycle {
    prevent_destroy = true
  }
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

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
  backend "s3" {
    bucket         = "smachnogo-tfstate-920071567477"
    key            = "github-oidc/terraform.tfstate"
    region         = "us-east-1"
    dynamodb_table = "smachnogo-tfstate-lock"
  }
}

provider "aws" { region = "us-east-1" }

locals {
  repo = "anton-vakulchyk/smachnogo"
  acct = "920071567477"
}

# GitHub OIDC provider. No thumbprint: AWS validates GitHub via its trusted
# root CA store since 2023 (thumbprint_list is non-load-bearing / optional).
resource "aws_iam_openid_connect_provider" "github" {
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
}

# --- trust policies (the "who can call" boundary) ---

data "aws_iam_policy_document" "trust_plan" { # read-only: PRs + main
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${local.repo}:pull_request", "repo:${local.repo}:ref:refs/heads/main"]
    }
  }
}

data "aws_iam_policy_document" "trust_dev" { # main only — NEVER pull_request
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${local.repo}:ref:refs/heads/main"]
    }
  }
}

data "aws_iam_policy_document" "trust_prod" { # prod environment only
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${local.repo}:environment:prod"]
    }
  }
}

# --- permission policies (the "what a call can do" boundary) ---

data "aws_iam_policy_document" "readonly" {
  statement { # tfstate read + lock
    actions   = ["s3:GetObject", "s3:ListBucket"]
    resources = ["arn:aws:s3:::smachnogo-tfstate-${local.acct}", "arn:aws:s3:::smachnogo-tfstate-${local.acct}/*"]
  }
  statement {
    actions   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem"]
    resources = ["arn:aws:dynamodb:us-east-1:${local.acct}:table/smachnogo-tfstate-lock"]
  }
  statement { # read everything terraform plan refreshes (must mirror every service the deploy policy can write)
    actions = [
      "lambda:Get*", "lambda:List*",
      "apigateway:GET",
      "dynamodb:Describe*", "dynamodb:List*",
      "sqs:Get*", "sqs:List*",
      "s3:GetBucket*", "s3:ListBucket",
      "logs:Describe*", "logs:ListTagsForResource",
      "ssm:Get*", "ssm:Describe*",
      "cognito-idp:Describe*", "cognito-idp:List*", "cognito-idp:Get*",
      "cloudwatch:Describe*", "cloudwatch:Get*", "cloudwatch:List*",
      "sns:Get*", "sns:List*",
      "budgets:ViewBudget", "budgets:DescribeBudget*", "budgets:ListTagsForResource",
      "iam:Get*", "iam:List*",
    ]
    resources = ["*"]
  }
}

data "aws_iam_policy_document" "deploy" {
  statement {
    actions   = ["s3:GetObject", "s3:PutObject", "s3:ListBucket"]
    resources = ["arn:aws:s3:::smachnogo-tfstate-${local.acct}", "arn:aws:s3:::smachnogo-tfstate-${local.acct}/*"]
  }
  statement {
    actions   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem"]
    resources = ["arn:aws:dynamodb:us-east-1:${local.acct}:table/smachnogo-tfstate-lock"]
  }
  statement { # app infra — broad actions, but scoped resources where the ARN is knowable
    sid     = "AppData"
    actions = ["dynamodb:*"]
    resources = [
      "arn:aws:dynamodb:us-east-1:${local.acct}:table/smachnogo-*",
      "arn:aws:dynamodb:us-east-1:${local.acct}:table/smachnogo-*/index/*",
    ]
  }
  statement {
    actions   = ["s3:*"]
    resources = ["arn:aws:s3:::smachnogo-*", "arn:aws:s3:::smachnogo-*/*"]
  }
  statement {
    actions   = ["ssm:*"]
    resources = ["arn:aws:ssm:us-east-1:${local.acct}:parameter/smachnogo/*"]
  }
  statement { # services whose ARNs are awkward to enumerate — account/region-scoped, no IAM
    sid       = "AppServices"
    actions   = ["lambda:*", "apigateway:*", "sqs:*", "logs:*", "cognito-idp:*"]
    resources = ["*"]
  }
  statement { # observability — envs/*/ops.tf creates an SNS topic+subs, CloudWatch alarms+dashboard, and a Budget.
    # NOTE: cloudwatch is a SEPARATE IAM service from logs.
    sid       = "AppObservability"
    # budgets:* — the provider's budget Read calls ListTagsForResource (not
    # covered by ViewBudget/ModifyBudget); budgets can't escalate, so full is fine.
    actions   = ["cloudwatch:*", "sns:*", "budgets:*"]
    resources = ["*"]
  }
  statement { # IAM: only the app's own roles, never account-wide
    sid = "AppRoles"
    actions = [
      "iam:CreateRole", "iam:DeleteRole", "iam:GetRole", "iam:TagRole", "iam:UpdateRole",
      "iam:PutRolePolicy", "iam:DeleteRolePolicy", "iam:GetRolePolicy", "iam:ListRolePolicies",
      "iam:ListAttachedRolePolicies", "iam:AttachRolePolicy", "iam:DetachRolePolicy",
    ]
    resources = ["arn:aws:iam::${local.acct}:role/smachnogo-*"]
  }
  # NOTE (residual risk): this role can create a smachnogo-* role, attach an
  # INLINE *:* policy via PutRolePolicy, and PassRole it to a Lambda → it is
  # effectively account-admin-equivalent for anything it can name. The Deny
  # below only blocks attaching admin-grade MANAGED policies (defense-in-depth,
  # not a takeover barrier). The REAL control is the trust boundary: only
  # main / the prod environment can assume the deploy roles, never a PR.
  # Fast-follow hardening: attach a permissions boundary requiring every
  # created role to carry that boundary (caps escalation). Tracked, not v1.
  statement { # defense-in-depth: block attaching admin-grade managed policies
    sid       = "DenyAdminAttach"
    effect    = "Deny"
    actions   = ["iam:AttachRolePolicy"]
    resources = ["*"]
    condition {
      test     = "ArnEquals"
      variable = "iam:PolicyARN"
      values = [
        "arn:aws:iam::aws:policy/AdministratorAccess",
        "arn:aws:iam::aws:policy/PowerUserAccess",
        "arn:aws:iam::aws:policy/IAMFullAccess",
      ]
    }
  }
  statement { # PassRole limited to Lambda
    sid       = "PassLambdaRoles"
    actions   = ["iam:PassRole"]
    resources = ["arn:aws:iam::${local.acct}:role/smachnogo-*"]
    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_policy" "readonly" {
  name   = "smachnogo-ci-readonly"
  policy = data.aws_iam_policy_document.readonly.json
}

resource "aws_iam_policy" "deploy" {
  name   = "smachnogo-ci-deploy"
  policy = data.aws_iam_policy_document.deploy.json
}

resource "aws_iam_role" "plan" {
  name               = "smachnogo-ci-plan"
  assume_role_policy = data.aws_iam_policy_document.trust_plan.json
}

resource "aws_iam_role" "dev" {
  name               = "smachnogo-ci-deploy-dev"
  assume_role_policy = data.aws_iam_policy_document.trust_dev.json
}

resource "aws_iam_role" "prod" {
  name               = "smachnogo-ci-deploy-prod"
  assume_role_policy = data.aws_iam_policy_document.trust_prod.json
}

resource "aws_iam_role_policy_attachment" "plan" {
  role       = aws_iam_role.plan.name
  policy_arn = aws_iam_policy.readonly.arn
}

resource "aws_iam_role_policy_attachment" "dev" {
  role       = aws_iam_role.dev.name
  policy_arn = aws_iam_policy.deploy.arn
}

resource "aws_iam_role_policy_attachment" "prod" {
  role       = aws_iam_role.prod.name
  policy_arn = aws_iam_policy.deploy.arn
}

output "plan_role_arn" { value = aws_iam_role.plan.arn }
output "dev_role_arn" { value = aws_iam_role.dev.arn }
output "prod_role_arn" { value = aws_iam_role.prod.arn }

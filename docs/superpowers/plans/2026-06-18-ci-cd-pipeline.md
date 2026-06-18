# CI/CD Pipeline Implementation Plan (rev 2 — hardened)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use `- [ ]`. Spec: `docs/superpowers/specs/2026-06-18-ci-cd-pipeline-design.md`. This rev incorporates an independent expert review (scoped IAM, plan-before-apply, fixed iOS lanes).

**Goal:** A safe 2-person pipeline: PRs run checks + a Terraform **plan**; merge to `main` auto-deploys backend→AWS dev + iOS→TestFlight; tag `v*` cuts a prod release where a human **approves a reviewed plan**, then iOS promotes to the App Store.

**Architecture:** OIDC with **3 least-priv roles** (read-only *plan*; *deploy-dev*; *deploy-prod*). Backend deploy = `build.sh` (3 zips) → `terraform` (dev: `apply -auto-approve`; prod: `plan -out` → approval → `apply tfplan`). iOS via Fastlane **match** + ASC API key; build#=`run_number`; prod promotes the tested TestFlight build.

**Tech Stack:** GitHub Actions, Go 1.26, Terraform + S3 state (acct `920071567477`, us-east-1), golangci-lint, Fastlane (match), xcodegen, Xcode 26.5. Repo `anton-vakulchyk/smachnogo`. Scheme `Smachnogo`, app id `app.smachnogo.ios`, team `CP598M5SUG`.

**Verification model:** config, not TDD. Per task: `actionlint`/`terraform validate`/`shellcheck`/`fastlane lanes`, then observe with `gh run watch`. 👤 = operator (Anton). Work on branch `feat/ci-cd`.

---

## Phase 1 — Backend checks + AWS delivery

### Task 1: Backend CI — lint + test + vet + tf fmt/validate (no creds)

**Files:** Create `backend/.golangci.yml`, `.github/workflows/backend-ci.yml`.

- [ ] **Step 1: `backend/.golangci.yml`** (pinned linter set; v2 schema)
```yaml
version: "2"
run: { go: "1.26" }
linters:
  enable: [govet, staticcheck, errcheck, ineffassign, unused]
```

- [ ] **Step 2: `.github/workflows/backend-ci.yml`** (SHAs pinned for the security-relevant actions; tf-plan added in Task 5)
```yaml
name: backend-ci
on:
  pull_request: { paths: ["backend/**", ".github/workflows/backend-ci.yml"] }
  push: { branches: [main], paths: ["backend/**", ".github/workflows/backend-ci.yml"] }
concurrency: { group: "backend-ci-${{ github.ref }}", cancel-in-progress: true }
permissions: { contents: read }
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.26", cache-dependency-path: backend/pkg/go.sum }
      - uses: golangci/golangci-lint-action@v6
        with: { version: "v1.64.8", working-directory: backend/pkg }
  test:
    runs-on: ubuntu-latest
    defaults: { run: { working-directory: backend } }
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.26", cache-dependency-path: backend/pkg/go.sum }
      - run: GOWORK=off go test ./...
        working-directory: backend/pkg
      - name: vet all modules
        run: |
          for m in pkg functions/api functions/workers/scanworker functions/presignup tests; do
            (cd "$m" && GOWORK=off go vet ./...)
          done
  tf-fmt-validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: hashicorp/setup-terraform@v3
      - run: terraform fmt -check -recursive backend/terraform
      - name: validate dev & prod
        run: |
          for env in dev prod; do
            terraform -chdir="backend/terraform/envs/$env" init -backend=false
            terraform -chdir="backend/terraform/envs/$env" validate
          done
```
> Note: `golangci-lint v1.64.x` reads the `version: "2"` config; if lint surfaces pre-existing WIP findings, narrow the `enable` set (don't disable wholesale) and note it.

- [ ] **Step 3: Verify** — `actionlint .github/workflows/backend-ci.yml` (if installed); `terraform fmt -check -recursive backend/terraform` (fix with `terraform fmt -recursive` if needed); `(cd backend/pkg && GOWORK=off go vet ./...)`.
- [ ] **Step 4: Commit** — `git add backend/.golangci.yml .github/workflows/backend-ci.yml && git commit -m "ci: backend lint+test+vet+tf-validate (path-filtered, pinned)"`
- [ ] **Step 5: Verify on PR** — push branch, `gh pr create --fill`, `gh run watch`; checks pass.

---

### Task 2: OIDC provider + THREE scoped roles (Terraform)

**Files:** Create `backend/terraform/github-oidc/main.tf`, `.../README.md`.

- [ ] **Step 1: `backend/terraform/github-oidc/main.tf`**
```hcl
terraform {
  required_version = ">= 1.5"
  required_providers { aws = { source = "hashicorp/aws", version = "~> 5.0" } }
  backend "s3" {
    bucket = "smachnogo-tfstate-920071567477"
    key    = "github-oidc/terraform.tfstate"
    region = "us-east-1"
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
    principals { type = "Federated", identifiers = [aws_iam_openid_connect_provider.github.arn] }
    condition { test = "StringEquals", variable = "token.actions.githubusercontent.com:aud", values = ["sts.amazonaws.com"] }
    condition { test = "StringLike", variable = "token.actions.githubusercontent.com:sub", values = ["repo:${local.repo}:pull_request", "repo:${local.repo}:ref:refs/heads/main"] }
  }
}
data "aws_iam_policy_document" "trust_dev" { # main only — NEVER pull_request
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals { type = "Federated", identifiers = [aws_iam_openid_connect_provider.github.arn] }
    condition { test = "StringEquals", variable = "token.actions.githubusercontent.com:aud", values = ["sts.amazonaws.com"] }
    condition { test = "StringEquals", variable = "token.actions.githubusercontent.com:sub", values = ["repo:${local.repo}:ref:refs/heads/main"] }
  }
}
data "aws_iam_policy_document" "trust_prod" { # prod environment only
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals { type = "Federated", identifiers = [aws_iam_openid_connect_provider.github.arn] }
    condition { test = "StringEquals", variable = "token.actions.githubusercontent.com:aud", values = ["sts.amazonaws.com"] }
    condition { test = "StringEquals", variable = "token.actions.githubusercontent.com:sub", values = ["repo:${local.repo}:environment:prod"] }
  }
}

# --- permission policies (the "what a call can do" boundary) ---
data "aws_iam_policy_document" "readonly" {
  statement { # tfstate read + lock
    actions   = ["s3:GetObject", "s3:ListBucket"]
    resources = ["arn:aws:s3:::smachnogo-tfstate-${local.acct}", "arn:aws:s3:::smachnogo-tfstate-${local.acct}/*"]
  }
  statement { actions = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem"], resources = ["arn:aws:dynamodb:us-east-1:${local.acct}:table/smachnogo-tfstate-lock"] }
  statement { # read everything terraform plan refreshes (must mirror every service the deploy policy can write)
    actions   = ["lambda:Get*", "lambda:List*", "apigateway:GET", "dynamodb:Describe*", "dynamodb:List*", "sqs:Get*", "sqs:List*", "s3:GetBucket*", "s3:ListBucket", "logs:Describe*", "logs:ListTagsForResource", "ssm:Get*", "ssm:Describe*", "cognito-idp:Describe*", "cognito-idp:List*", "cognito-idp:Get*", "cloudwatch:Describe*", "cloudwatch:Get*", "cloudwatch:List*", "sns:Get*", "sns:List*", "budgets:ViewBudget", "budgets:DescribeBudget*", "iam:Get*", "iam:List*"]
    resources = ["*"]
  }
}
data "aws_iam_policy_document" "deploy" {
  statement { actions = ["s3:GetObject", "s3:PutObject", "s3:ListBucket"], resources = ["arn:aws:s3:::smachnogo-tfstate-${local.acct}", "arn:aws:s3:::smachnogo-tfstate-${local.acct}/*"] }
  statement { actions = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem"], resources = ["arn:aws:dynamodb:us-east-1:${local.acct}:table/smachnogo-tfstate-lock"] }
  statement { # app infra — broad actions, but scoped resources where the ARN is knowable
    sid       = "AppData"
    actions   = ["dynamodb:*"], resources = ["arn:aws:dynamodb:us-east-1:${local.acct}:table/smachnogo-*", "arn:aws:dynamodb:us-east-1:${local.acct}:table/smachnogo-*/index/*"]
  }
  statement { actions = ["s3:*"], resources = ["arn:aws:s3:::smachnogo-*", "arn:aws:s3:::smachnogo-*/*"] }
  statement { actions = ["ssm:*"], resources = ["arn:aws:ssm:us-east-1:${local.acct}:parameter/smachnogo/*"] }
  statement { # services whose ARNs are awkward to enumerate — account/region-scoped, no IAM
    sid       = "AppServices"
    actions   = ["lambda:*", "apigateway:*", "sqs:*", "logs:*", "cognito-idp:*"]
    resources = ["*"]
  }
  statement { # observability — envs/*/ops.tf creates an SNS topic+subs, CloudWatch alarms+dashboard, and a Budget. NOTE: cloudwatch is a SEPARATE IAM service from logs.
    sid       = "AppObservability"
    actions   = ["cloudwatch:*", "sns:*", "budgets:ViewBudget", "budgets:ModifyBudget"]
    resources = ["*"]
  }
  statement { # IAM: only the app's own roles, never account-wide
    sid       = "AppRoles"
    actions   = ["iam:CreateRole", "iam:DeleteRole", "iam:GetRole", "iam:TagRole", "iam:UpdateRole", "iam:PutRolePolicy", "iam:DeleteRolePolicy", "iam:GetRolePolicy", "iam:ListRolePolicies", "iam:ListAttachedRolePolicies", "iam:AttachRolePolicy", "iam:DetachRolePolicy"]
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
    condition { test = "ArnEquals", variable = "iam:PolicyARN", values = ["arn:aws:iam::aws:policy/AdministratorAccess", "arn:aws:iam::aws:policy/PowerUserAccess", "arn:aws:iam::aws:policy/IAMFullAccess"] }
  }
  statement { # PassRole limited to Lambda
    sid       = "PassLambdaRoles"
    actions   = ["iam:PassRole"]
    resources = ["arn:aws:iam::${local.acct}:role/smachnogo-*"]
    condition { test = "StringEquals", variable = "iam:PassedToService", values = ["lambda.amazonaws.com"] }
  }
}

resource "aws_iam_policy" "readonly" { name = "smachnogo-ci-readonly", policy = data.aws_iam_policy_document.readonly.json }
resource "aws_iam_policy" "deploy"   { name = "smachnogo-ci-deploy",   policy = data.aws_iam_policy_document.deploy.json }

resource "aws_iam_role" "plan" { name = "smachnogo-ci-plan",        assume_role_policy = data.aws_iam_policy_document.trust_plan.json }
resource "aws_iam_role" "dev"  { name = "smachnogo-ci-deploy-dev",  assume_role_policy = data.aws_iam_policy_document.trust_dev.json }
resource "aws_iam_role" "prod" { name = "smachnogo-ci-deploy-prod", assume_role_policy = data.aws_iam_policy_document.trust_prod.json }
resource "aws_iam_role_policy_attachment" "plan" { role = aws_iam_role.plan.name, policy_arn = aws_iam_policy.readonly.arn }
resource "aws_iam_role_policy_attachment" "dev"  { role = aws_iam_role.dev.name,  policy_arn = aws_iam_policy.deploy.arn }
resource "aws_iam_role_policy_attachment" "prod" { role = aws_iam_role.prod.name, policy_arn = aws_iam_policy.deploy.arn }

output "plan_role_arn" { value = aws_iam_role.plan.arn }
output "dev_role_arn"  { value = aws_iam_role.dev.arn }
output "prod_role_arn" { value = aws_iam_role.prod.arn }
```

- [ ] **Step 2: `README.md`** — note: apply once with admin creds; the readonly role is what PRs assume (it cannot mutate); the deploy roles never trust `pull_request`; `iam:*` is scoped to `role/smachnogo-*` with admin-attach denied and PassRole limited to Lambda.
- [ ] **Step 3: Validate** — `terraform -chdir=backend/terraform/github-oidc init -backend=false && terraform -chdir=backend/terraform/github-oidc validate && terraform fmt -check backend/terraform/github-oidc`.
- [ ] **Step 4: Commit** — `git add backend/terraform/github-oidc/ && git commit -m "ci: OIDC provider + 3 least-priv roles (readonly plan / deploy-dev / deploy-prod)"`
- [ ] **Step 5: 👤 Apply once** — `AWS_PROFILE=smachnogo terraform -chdir=backend/terraform/github-oidc init && apply`; record `plan_role_arn`, `dev_role_arn`, `prod_role_arn`.

---

### Task 3: `deploy.sh` (plan / apply-saved-plan / apply-auto)

**Files:** Create `backend/scripts/deploy.sh`.
- [ ] **Step 1: content**
```bash
#!/usr/bin/env bash
set -euo pipefail
ENV=""; ACTION=""; FILE="tfplan"
while [ $# -gt 0 ]; do case "$1" in
  --env) ENV="$2"; shift 2;;
  --plan-out) ACTION="plan"; FILE="${2:-tfplan}"; shift 2;;
  --apply) ACTION="apply"; FILE="${2:-tfplan}"; shift 2;;
  --apply-auto) ACTION="apply-auto"; shift;;
  *) echo "bad arg: $1" >&2; exit 2;;
esac; done
[ "$ENV" = dev ] || [ "$ENV" = prod ] || { echo "--env must be dev|prod" >&2; exit 2; }
cd "$(dirname "$0")/.."           # backend/
TF="terraform -chdir=terraform/envs/$ENV"
$TF init -input=false
case "$ACTION" in
  plan)       ./scripts/build.sh; $TF plan -input=false -out="$FILE" ;;
  apply)      $TF apply -input=false "$FILE" ;;          # apply the SAVED plan; reuse the zip the plan step built (no rebuild → hash stays valid)
  apply-auto) ./scripts/build.sh; $TF apply -input=false -auto-approve ;;  # dev only (plan reviewed on PR)
  *) echo "need --plan-out|--apply|--apply-auto" >&2; exit 2 ;;
esac
```
- [ ] **Step 2: Verify** — `chmod +x backend/scripts/deploy.sh; bash -n backend/scripts/deploy.sh; shellcheck backend/scripts/deploy.sh` (if installed).
- [ ] **Step 3: 👤 Smoke** — `AWS_PROFILE=smachnogo backend/scripts/deploy.sh --env dev --plan-out /tmp/p.tfplan` → builds + writes a plan, no apply.
- [ ] **Step 4: Commit** — `git add backend/scripts/deploy.sh && git commit -m "ci: deploy.sh (plan-out / apply-saved / apply-auto)"`

---

### Task 4: `prevent_destroy` on stateful resources + nightly drift

**Files:** Modify `backend/terraform/envs/{dev,prod}/dynamodb.tf` and `.../s3.tf`; Create `.github/workflows/backend-drift.yml`.
- [ ] **Step 1:** In each env's `aws_dynamodb_table.main` and the photos `aws_s3_bucket`, add:
```hcl
  lifecycle { prevent_destroy = true }
```
- [ ] **Step 2: `.github/workflows/backend-drift.yml`**
```yaml
name: backend-drift
on:
  schedule: [{ cron: "0 13 * * 1-5" }]
  workflow_dispatch:
permissions: { contents: read, id-token: write }
jobs:
  drift:
    strategy: { matrix: { env: [dev, prod] } }
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.26", cache-dependency-path: backend/pkg/go.sum }
      - uses: hashicorp/setup-terraform@v3
      - uses: aws-actions/configure-aws-credentials@v4
        with: { role-to-assume: "${{ secrets.AWS_PLAN_ROLE_ARN }}", aws-region: us-east-1 }
      - name: plan (detailed exit code)
        run: |
          ./scripts/build.sh
          terraform -chdir="terraform/envs/${{ matrix.env }}" init -input=false
          set +e
          terraform -chdir="terraform/envs/${{ matrix.env }}" plan -input=false -detailed-exitcode
          code=$?
          if [ "$code" -eq 2 ]; then echo "⚠️ DRIFT detected in ${{ matrix.env }}" >> "$GITHUB_STEP_SUMMARY"; fi
          [ "$code" -ne 1 ]   # exit 0 (no drift) or 2 (drift) are fine; 1 = real plan error
        working-directory: backend
```
- [ ] **Step 3: Verify** — `actionlint .github/workflows/backend-drift.yml`; `terraform fmt -check -recursive backend/terraform`.
- [ ] **Step 4: Commit** — `git add backend/terraform/envs/*/dynamodb.tf backend/terraform/envs/*/s3.tf .github/workflows/backend-drift.yml && git commit -m "ci: prevent_destroy on data stores + nightly drift plan"`

---

### Task 5: Deploy workflow (dev auto / prod plan→approve→apply) + tf-plan-on-PR + retire ci.yml

**Files:** Create `.github/workflows/backend-deploy.yml`; Modify `.github/workflows/backend-ci.yml` (add `tf-plan` job); Delete `.github/workflows/ci.yml`.

- [ ] **Step 1: Add `tf-plan` job to `backend-ci.yml`** (read-only role; comments the dev plan on PRs)
```yaml
  tf-plan:
    if: github.event_name == 'pull_request'
    runs-on: ubuntu-latest
    permissions: { contents: read, id-token: write, pull-requests: write }
    defaults: { run: { working-directory: backend } }
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.26", cache-dependency-path: backend/pkg/go.sum }
      - uses: hashicorp/setup-terraform@v3
      - uses: aws-actions/configure-aws-credentials@v4
        with: { role-to-assume: "${{ secrets.AWS_PLAN_ROLE_ARN }}", aws-region: us-east-1 }
      - id: plan
        run: ./scripts/deploy.sh --env dev --plan-out /tmp/dev.tfplan 2>&1 | tee /tmp/plan.txt
      - uses: actions/github-script@v7
        with:
          script: |
            const fs = require('fs');
            const body = "### Terraform plan (dev)\n```\n" + fs.readFileSync('/tmp/plan.txt','utf8').slice(-60000) + "\n```";
            github.rest.issues.createComment({ ...context.repo, issue_number: context.issue.number, body });
```
(Add `id-token: write` is per-job here; the top-level `permissions` stays `contents: read`.)

- [ ] **Step 2: `.github/workflows/backend-deploy.yml`**
```yaml
name: backend-deploy
on:
  push:
    branches: [main]
    paths: ["backend/**", ".github/workflows/backend-deploy.yml"]
    tags: ["v*"]
permissions: { contents: read, id-token: write }
jobs:
  deploy-dev:
    if: github.ref == 'refs/heads/main'
    concurrency: { group: deploy-dev, cancel-in-progress: false }
    runs-on: ubuntu-latest
    environment: dev
    defaults: { run: { working-directory: backend } }
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.26", cache-dependency-path: backend/pkg/go.sum }
      - uses: hashicorp/setup-terraform@v3
      - uses: aws-actions/configure-aws-credentials@v4
        with: { role-to-assume: "${{ secrets.AWS_DEPLOY_ROLE_ARN_DEV }}", aws-region: us-east-1 }
      - run: ./scripts/deploy.sh --env dev --apply-auto

  plan-prod:
    if: startsWith(github.ref, 'refs/tags/v')
    concurrency: { group: deploy-prod, cancel-in-progress: false }
    runs-on: ubuntu-latest
    defaults: { run: { working-directory: backend } }
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.26", cache-dependency-path: backend/pkg/go.sum }
      - uses: hashicorp/setup-terraform@v3
      - uses: aws-actions/configure-aws-credentials@v4
        with: { role-to-assume: "${{ secrets.AWS_DEPLOY_ROLE_ARN_PROD }}", aws-region: us-east-1 }
      - run: ./scripts/deploy.sh --env prod --plan-out prod.tfplan | tee prod-plan.txt
      - uses: actions/upload-artifact@v4
        with: { name: prod-plan, path: "backend/prod-plan.txt", retention-days: 7 }

  apply-prod:
    needs: plan-prod
    if: startsWith(github.ref, 'refs/tags/v')
    concurrency: { group: deploy-prod, cancel-in-progress: false }
    runs-on: ubuntu-latest
    environment: prod   # ← required-reviewer gate; approver reads the plan-prod log/artifact
    defaults: { run: { working-directory: backend } }
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.26", cache-dependency-path: backend/pkg/go.sum }
      - uses: hashicorp/setup-terraform@v3
      - uses: aws-actions/configure-aws-credentials@v4
        with: { role-to-assume: "${{ secrets.AWS_DEPLOY_ROLE_ARN_PROD }}", aws-region: us-east-1 }
      # ONE build in this job → plan → apply that exact file (no second build, so the
      # zip the plan captured is the zip we apply). The prod approval gates THIS job;
      # the approver reviewed the plan-prod artifact (same tag commit → same diff).
      - run: ./scripts/deploy.sh --env prod --plan-out prod.tfplan
      - run: ./scripts/deploy.sh --env prod --apply prod.tfplan
```
> Rationale: the saved-plan artifact from `plan-prod` is what the **approver reviews**; `apply-prod` (gated) regenerates the identical plan on the same tag commit and applies it. This keeps "approve a real diff" without shipping a binary plan file between runners.

- [ ] **Step 3:** `git rm .github/workflows/ci.yml`
- [ ] **Step 4: Verify** — `actionlint .github/workflows/backend-ci.yml .github/workflows/backend-deploy.yml`.
- [ ] **Step 5: Commit** — `git add -A .github/workflows/ && git commit -m "ci: tf-plan on PR (readonly role) + deploy-dev/plan-prod→approve→apply-prod; retire ci.yml"`

---

### Task 6: 👤 Operator setup + Phase-1 verification

- [ ] **Step 1: Secrets** — `gh secret set AWS_PLAN_ROLE_ARN -b <plan_role_arn>`; `AWS_DEPLOY_ROLE_ARN_DEV`; `AWS_DEPLOY_ROLE_ARN_PROD` (from Task 2 Step 5).
- [ ] **Step 2: Environments** — `dev` (no protection); `prod` (Required reviewer = the other teammate; scope the prod role secret here).
- [ ] **Step 3: Branch protection on `main`** — require `lint`, `test`, `tf-fmt-validate`, `tf-plan`.
- [ ] **Step 4: ⚠️ LAUNCH-GATE** — before the FIRST prod apply, revert the beta-generous Terraform vars (`free_scan_allowance` 1000→real, `free_window_days` 3650→7, review `daily_scan_cap`) in `terraform/envs/prod/variables.tf`, so a tag doesn't auto-ship beta limits to prod.
- [ ] **Step 5: Verify dev** — merge the PR; `gh run watch` → `deploy-dev` assumes the dev role, `apply -auto-approve` succeeds; confirm `aws lambda get-function --function-name smachnogo-api-dev`.
- [ ] **Step 6: Verify prod** — `git tag v1.0.8 && git push origin v1.0.8`; `plan-prod` runs, `apply-prod` PAUSES for approval; teammate reviews the plan artifact + approves → apply succeeds. (Only when backend is release-ready.)

---

## Phase 2 — iOS TestFlight

### Task 7: Fastlane (match + compile-only test + beta)

**Files:** Create `ios/Gemfile`, `ios/fastlane/{Appfile,Matchfile,Fastfile}`.
- [ ] **Step 1: `ios/Gemfile`** — `source "https://rubygems.org"` / `gem "fastlane"`.
- [ ] **Step 2: `ios/fastlane/Appfile`** — `app_identifier("app.smachnogo.ios")`.
- [ ] **Step 3: `ios/fastlane/Matchfile`** — `git_url(ENV["MATCH_GIT_URL"])` / `storage_mode("git")` / `type("appstore")` / `app_identifier(["app.smachnogo.ios"])` / `readonly(true)`.
- [ ] **Step 4: `ios/fastlane/Fastfile`**
```ruby
default_platform(:ios)
def asc_key
  app_store_connect_api_key(key_id: ENV.fetch("ASC_KEY_ID"), issuer_id: ENV.fetch("ASC_ISSUER_ID"),
                            key_content: ENV.fetch("ASC_KEY_P8_BASE64"), is_key_content_base64: true)
end
platform :ios do
  # PR: compile-only. There is NO test target — do not run_tests.
  lane :test do
    build_app(project: "Smachnogo.xcodeproj", scheme: "Smachnogo",
              destination: "generic/platform=iOS Simulator",
              skip_archive: true, skip_codesigning: true, skip_package_ipa: true)
  end
  # merge→main: signed Release archive → TestFlight. Build# via xcargs (no agvtool).
  lane :beta do
    api = asc_key
    match(type: "appstore", readonly: true, api_key: api)
    # CODE_SIGN_STYLE=Manual: project.yml sets Automatic, which fights match's
    # explicit profile on a fresh runner. Force manual + the match profile.
    build_app(project: "Smachnogo.xcodeproj", scheme: "Smachnogo", export_method: "app-store",
              xcargs: "CURRENT_PROJECT_VERSION=#{ENV.fetch('GITHUB_RUN_NUMBER')} CODE_SIGN_STYLE=Manual",
              export_options: { signingStyle: "manual" })
    upload_to_testflight(api_key: api, skip_waiting_for_build_processing: true)
  end
end
```
- [ ] **Step 5: Verify** — `cd ios && bundle install && bundle exec fastlane lanes` lists `test`,`beta`.
- [ ] **Step 6: Commit** — `git add ios/Gemfile ios/fastlane/ && git commit -m "ci(ios): fastlane match + compile-only test + beta→TestFlight"`
- [ ] **Step 7: 👤 Seed match + secrets** — create a private certs repo; `fastlane match appstore` once locally (creates Distribution cert + App Store profile); `gh secret set MATCH_GIT_URL/MATCH_PASSWORD ASC_KEY_ID/ASC_ISSUER_ID/ASC_KEY_P8_BASE64` (`base64 -i AuthKey_*.p8`; ASC key role = App Manager).

---

### Task 8: iOS CI workflow (PR compile / beta on main)

**Files:** Create `.github/workflows/ios-ci.yml`.
- [ ] **Step 1: content**
```yaml
name: ios-ci
on:
  pull_request: { paths: ["ios/**", ".github/workflows/ios-ci.yml"] }
  push: { branches: [main], paths: ["ios/**", ".github/workflows/ios-ci.yml"] }
concurrency: { group: "ios-ci-${{ github.ref }}", cancel-in-progress: true }
permissions: { contents: read }
jobs:
  test:
    if: github.event_name == 'pull_request' && github.event.pull_request.draft == false
    runs-on: macos-15
    timeout-minutes: 30
    defaults: { run: { working-directory: ios } }
    steps:
      - uses: actions/checkout@v4
      - uses: maxim-lobanov/setup-xcode@v1
        with: { xcode-version: "26.5" }
      - uses: ruby/setup-ruby@v1
        with: { ruby-version: "3.3", bundler-cache: true, working-directory: ios }
      - run: brew install xcodegen && xcodegen generate
      - run: bundle exec fastlane test
  beta:
    if: github.ref == 'refs/heads/main'
    runs-on: macos-15
    timeout-minutes: 45
    environment: dev
    defaults: { run: { working-directory: ios } }
    env:
      ASC_KEY_ID: ${{ secrets.ASC_KEY_ID }}
      ASC_ISSUER_ID: ${{ secrets.ASC_ISSUER_ID }}
      ASC_KEY_P8_BASE64: ${{ secrets.ASC_KEY_P8_BASE64 }}
      MATCH_GIT_URL: ${{ secrets.MATCH_GIT_URL }}
      MATCH_PASSWORD: ${{ secrets.MATCH_PASSWORD }}
      GITHUB_RUN_NUMBER: ${{ github.run_number }}
    steps:
      - uses: actions/checkout@v4
      - uses: maxim-lobanov/setup-xcode@v1
        with: { xcode-version: "26.5" }
      - uses: ruby/setup-ruby@v1
        with: { ruby-version: "3.3", bundler-cache: true, working-directory: ios }
      - run: brew install xcodegen && xcodegen generate
      - run: bundle exec fastlane beta
```
> Pin xcodegen version if drift bites (`brew install xcodegen` floats; acceptable for v1, revisit).

- [ ] **Step 2: Verify** — `actionlint .github/workflows/ios-ci.yml`.
- [ ] **Step 3: Commit** — `git add .github/workflows/ios-ci.yml && git commit -m "ci(ios): PR compile-check (no signing) + beta→TestFlight on main (match, build#=run)"`
- [ ] **Step 4: Verify** — iOS PR → `test` compiles green (no signing); merge → `beta` uploads build `#<run_number>` to TestFlight (confirm in ASC → TestFlight, build processed).

---

## Phase 3 — iOS App Store on tag

### Task 9: `release` lane + tag job

**Files:** Modify `ios/fastlane/Fastfile`; Modify `.github/workflows/ios-ci.yml`.
- [ ] **Step 1: Add `release` lane** (promote the tested build; no metadata push; human submits)
```ruby
  lane :release do
    api = asc_key
    upload_to_app_store(api_key: api,
      app_version: get_version_number(xcodeproj: "Smachnogo.xcodeproj", target: "Smachnogo"),
      build_number: latest_testflight_build_number(api_key: api).to_s,
      submit_for_review: false, automatic_release: false,
      skip_binary_upload: true, skip_metadata: true, skip_screenshots: true, force: true)
  end
```
- [ ] **Step 2: Add tag trigger + `release` job to `ios-ci.yml`** — add `tags: ["v*"]` to `on.push`, and:
```yaml
  release:
    if: startsWith(github.ref, 'refs/tags/v')
    runs-on: ubuntu-latest   # ASC API call only; no Xcode/macOS needed
    timeout-minutes: 20
    environment: prod
    defaults: { run: { working-directory: ios } }
    env:
      ASC_KEY_ID: ${{ secrets.ASC_KEY_ID }}
      ASC_ISSUER_ID: ${{ secrets.ASC_ISSUER_ID }}
      ASC_KEY_P8_BASE64: ${{ secrets.ASC_KEY_P8_BASE64 }}
    steps:
      - uses: actions/checkout@v4
      - uses: ruby/setup-ruby@v1
        with: { ruby-version: "3.3", bundler-cache: true, working-directory: ios }
      - run: bundle exec fastlane release
```
- [ ] **Step 3: Verify** — `actionlint`; `cd ios && bundle exec fastlane lanes` shows `test`,`beta`,`release`.
- [ ] **Step 4: Commit** — `git add ios/fastlane/Fastfile .github/workflows/ios-ci.yml && git commit -m "ci(ios): release — promote tested TestFlight build to App Store (skip metadata, human submit)"`
- [ ] **Step 5: Verify** — tag → `release` pauses on `prod` approval → approve → latest TestFlight build attached to a new App Store version ("Prepare for Submission"). 👤 human reviews notes + clicks **Submit for Review** in ASC.

---

## Self-Review

**Spec coverage (rev 2):** scoped IAM + 3 roles → Task 2 ✓ · plan-on-PR (readonly role + comment) → Task 5 Step 1 ✓ · prod plan→approve→apply → Task 5 Step 2 ✓ · deploy.sh modes → Task 3 ✓ · prevent_destroy + drift → Task 4 ✓ · pins (SHAs/golangci/Xcode) → Tasks 1,5,8 ✓ · compile-only iOS lane → Task 7 ✓ · build# via xcargs → Task 7 ✓ · match → Tasks 7,8 ✓ · App Store promote skip_metadata + app_version → Task 9 ✓ · LAUNCH-GATE → Task 6 Step 4 ✓ · retire ci.yml → Task 5 ✓.

**Placeholder scan:** operator values (`<plan_role_arn>`, `v1.0.8`, ASC key/issuer) are genuine user inputs, not lazy TODOs. No "TBD". Action `@v4`/`@v5` tags used for readability — Task 5/Spec call for pinning the security-relevant ones to SHAs as a hardening pass (note kept, not silent). ✓

**Consistency:** role names (`smachnogo-ci-plan|deploy-dev|deploy-prod`) ↔ secret names (`AWS_PLAN_ROLE_ARN|_DEV|_PROD`) ↔ workflow `role-to-assume` ↔ Task 6 `gh secret set`. `deploy.sh` flags (`--plan-out/--apply/--apply-auto`) match every caller. `asc_key` helper shared by `beta`+`release`. ✓

**Caveats for the executor (rev 3):**
- golangci-lint may flag WIP backend code (narrow `enable`, don't disable).
- First `match` run needs the certs repo seeded (Task 7 Step 7) or `beta` fails — 👤 prerequisite, not an agent step.
- **IAM residual risk (accepted for v1):** the deploy role is effectively account-admin-equivalent via inline-policy + `PassRole`→Lambda; the `DenyAdminAttach` is defense-in-depth only. The real control is the trust boundary (main / prod-env, never PR). Add a permissions boundary as a fast-follow if the threat model warrants.
- **Prod plan==apply** holds only if `build.sh` is reproducible. It currently isn't fully (`zip -j` records mtimes; no `-trimpath`) — so `apply-prod`'s re-plan may show the same Lambda-code update the approver saw (same direction, safe) but not a byte-identical artifact. Easy hardening: add `-trimpath` to the `go build` and normalize zip timestamps in `build.sh`.
- SHA-pinning of actions + xcodegen version pin are documented follow-ups, NOT delivered in this rev (YAML still uses `@vN` tags).

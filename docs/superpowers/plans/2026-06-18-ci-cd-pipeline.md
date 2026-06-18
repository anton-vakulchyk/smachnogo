# CI/CD Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Spec: `docs/superpowers/specs/2026-06-18-ci-cd-pipeline-design.md`.

**Goal:** A 2-person GitHub Actions pipeline: PRs run checks; merge to `main` auto-deploys backend→AWS dev + iOS→TestFlight; tag `v*` cuts a prod release (backend→AWS prod + iOS→App Store) behind one teammate approval.

**Architecture:** Three triggers (PR / push `main` / tag). Backend deploys via `deploy.sh` = `build.sh` (3 lambda zips) → `terraform apply` per env (lambda code rides `source_code_hash`). AWS auth via GitHub OIDC roles codified in Terraform. iOS via Fastlane + an App Store Connect API key; build number = `github.run_number`; prod promotes the already-tested TestFlight build.

**Tech Stack:** GitHub Actions, Go 1.26 (workspace: `pkg`,`functions/api`,`functions/workers/scanworker`,`functions/presignup`,`tests`), Terraform + S3 state (acct `920071567477`, region `us-east-1`), golangci-lint, Fastlane, Xcode 26.5 (scheme `Smachnogo`, app id `app.smachnogo.ios`, team `CP598M5SUG`). Repo: `anton-vakulchyk/smachnogo`.

**Verification model:** No unit tests for config. Per task: `actionlint`/`terraform validate`/`shellcheck`/`fastlane lanes`, then observe the real run with `gh run watch`. 👤 = manual operator (Anton) action.

**Branch:** do this on a feature branch `feat/ci-cd` (PRs against `main`); the deploy paths only activate once merged.

---

## Phase 1 — Backend checks + AWS delivery

### Task 1: Backend CI (lint + test + vet + terraform check), path-filtered

**Files:**
- Create: `backend/.golangci.yml`
- Create: `.github/workflows/backend-ci.yml`
- (Leave the old `.github/workflows/ci.yml` for now; Task 4 removes it.)

- [ ] **Step 1: Create `backend/.golangci.yml`**
```yaml
version: "2"
run:
  go: "1.26"
linters:
  enable:
    - govet
    - staticcheck
    - errcheck
    - ineffassign
    - unused
issues:
  max-issues-per-linter: 0
  max-same-issues: 0
```

- [ ] **Step 2: Create `.github/workflows/backend-ci.yml`**
```yaml
name: backend-ci
on:
  pull_request:
    paths: ["backend/**", ".github/workflows/backend-ci.yml"]
  push:
    branches: [main]
    paths: ["backend/**", ".github/workflows/backend-ci.yml"]
concurrency:
  group: backend-ci-${{ github.ref }}
  cancel-in-progress: true
permissions:
  contents: read
jobs:
  lint:
    runs-on: ubuntu-latest
    defaults: { run: { working-directory: backend/pkg } }
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.26", cache-dependency-path: backend/pkg/go.sum }
      - uses: golangci/golangci-lint-action@v6
        with: { version: latest, working-directory: backend/pkg }
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.26", cache-dependency-path: backend/pkg/go.sum }
      - name: Unit tests
        run: GOWORK=off go test ./...
        working-directory: backend/pkg
      - name: Vet all modules
        run: |
          for m in pkg functions/api functions/workers/scanworker functions/presignup tests; do
            (cd "$m" && GOWORK=off go vet ./...)
          done
        working-directory: backend
  tf-check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: hashicorp/setup-terraform@v3
      - name: fmt + validate (dev & prod)
        run: |
          terraform fmt -check -recursive backend/terraform
          for env in dev prod; do
            terraform -chdir="backend/terraform/envs/$env" init -backend=false
            terraform -chdir="backend/terraform/envs/$env" validate
          done
```

- [ ] **Step 3: Lint the workflow & validate locally**
```bash
cd /Users/anton/smachnogo
command -v actionlint >/dev/null && actionlint .github/workflows/backend-ci.yml || echo "actionlint not installed — skip"
terraform fmt -check -recursive backend/terraform   # expect: no diff (or fix with `terraform fmt -recursive`)
(cd backend/pkg && GOWORK=off go vet ./... && echo "vet OK")
```
Expected: workflow parses; `go vet` clean. (Note: `terraform validate` with the S3 backend needs `-backend=false` as in the workflow.)

- [ ] **Step 4: Commit**
```bash
git add backend/.golangci.yml .github/workflows/backend-ci.yml
git commit -m "ci: backend lint+test+vet+tf-check workflow (path-filtered, concurrency)"
```

- [ ] **Step 5: Verify on a PR**
Push the feature branch, open a PR, and confirm the `backend-ci` checks run and pass: `gh pr create --fill && gh run watch`.

---

### Task 2: GitHub OIDC provider + deploy roles (Terraform)

**Files:**
- Create: `backend/terraform/github-oidc/main.tf`
- Create: `backend/terraform/github-oidc/README.md`

- [ ] **Step 1: Create `backend/terraform/github-oidc/main.tf`**
```hcl
terraform {
  required_version = ">= 1.5"
  required_providers { aws = { source = "hashicorp/aws", version = "~> 5.0" } }
  backend "s3" {
    bucket         = "smachnogo-tfstate-920071567477"
    key            = "github-oidc/terraform.tfstate"
    region         = "us-east-1"
    dynamodb_table = "smachnogo-tfstate-lock"
  }
}
provider "aws" { region = "us-east-1" }

locals { repo = "anton-vakulchyk/smachnogo" }

# GitHub's OIDC identity provider (one per account).
resource "aws_iam_openid_connect_provider" "github" {
  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = ["6938fd4d98bab03faadb97b34396831e3780aea1"]
}

# Trust: dev role assumable from main pushes and PRs (for read-only plan).
data "aws_iam_policy_document" "dev_trust" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals { type = "Federated", identifiers = [aws_iam_openid_connect_provider.github.arn] }
    condition { test = "StringEquals", variable = "token.actions.githubusercontent.com:aud", values = ["sts.amazonaws.com"] }
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${local.repo}:ref:refs/heads/main", "repo:${local.repo}:pull_request"]
    }
  }
}

# Trust: prod role assumable ONLY from the `prod` GitHub Environment.
data "aws_iam_policy_document" "prod_trust" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals { type = "Federated", identifiers = [aws_iam_openid_connect_provider.github.arn] }
    condition { test = "StringEquals", variable = "token.actions.githubusercontent.com:aud", values = ["sts.amazonaws.com"] }
    condition { test = "StringEquals", variable = "token.actions.githubusercontent.com:sub", values = ["repo:${local.repo}:environment:prod"] }
  }
}

resource "aws_iam_role" "dev"  { name = "smachnogo-ci-deploy-dev",  assume_role_policy = data.aws_iam_policy_document.dev_trust.json }
resource "aws_iam_role" "prod" { name = "smachnogo-ci-deploy-prod", assume_role_policy = data.aws_iam_policy_document.prod_trust.json }

# Deploy policy: Terraform manages all of the app's infra, so this is broad
# by necessity but scoped to this account/region. The OIDC trust above is the
# real boundary (only this repo's main/PR/prod-env can assume it).
data "aws_iam_policy_document" "deploy" {
  statement {
    sid     = "TFState"
    actions = ["s3:GetObject", "s3:PutObject", "s3:ListBucket"]
    resources = ["arn:aws:s3:::smachnogo-tfstate-920071567477", "arn:aws:s3:::smachnogo-tfstate-920071567477/*"]
  }
  statement {
    sid       = "TFLock"
    actions   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem"]
    resources = ["arn:aws:dynamodb:us-east-1:920071567477:table/smachnogo-tfstate-lock"]
  }
  statement {
    sid       = "AppInfra"
    actions   = ["lambda:*", "apigateway:*", "dynamodb:*", "sqs:*", "s3:*", "logs:*", "ssm:*", "cognito-idp:*", "events:*", "iam:*"]
    resources = ["*"]
  }
}
resource "aws_iam_policy" "deploy" { name = "smachnogo-ci-deploy", policy = data.aws_iam_policy_document.deploy.json }
resource "aws_iam_role_policy_attachment" "dev"  { role = aws_iam_role.dev.name,  policy_arn = aws_iam_policy.deploy.arn }
resource "aws_iam_role_policy_attachment" "prod" { role = aws_iam_role.prod.name, policy_arn = aws_iam_policy.deploy.arn }

output "dev_role_arn"  { value = aws_iam_role.dev.arn }
output "prod_role_arn" { value = aws_iam_role.prod.arn }
```

- [ ] **Step 2: Create `backend/terraform/github-oidc/README.md`**
```markdown
# GitHub Actions → AWS OIDC

One-time bootstrap. Apply locally with admin creds:

    AWS_PROFILE=smachnogo terraform -chdir=backend/terraform/github-oidc init
    AWS_PROFILE=smachnogo terraform -chdir=backend/terraform/github-oidc apply

Then set the two output ARNs as repo secrets (see plan Task 5).
The `iam:*` grant lets Terraform manage the app's own roles; the OIDC trust
conditions (repo + branch/PR/prod-environment) are the security boundary.
```

- [ ] **Step 3: Validate**
```bash
terraform -chdir=backend/terraform/github-oidc init -backend=false
terraform -chdir=backend/terraform/github-oidc validate   # expect: Success
terraform fmt -check backend/terraform/github-oidc
```

- [ ] **Step 4: Commit**
```bash
git add backend/terraform/github-oidc/
git commit -m "ci: codify GitHub OIDC provider + dev/prod deploy roles (terraform)"
```

- [ ] **Step 5: 👤 Operator — apply once + capture ARNs**
```bash
AWS_PROFILE=smachnogo terraform -chdir=backend/terraform/github-oidc init
AWS_PROFILE=smachnogo terraform -chdir=backend/terraform/github-oidc apply   # review + yes
AWS_PROFILE=smachnogo terraform -chdir=backend/terraform/github-oidc output  # note dev_role_arn / prod_role_arn
```
Expected: an OIDC provider + 2 roles in account `920071567477`; ARNs printed.

---

### Task 3: `deploy.sh`

**Files:**
- Create: `backend/scripts/deploy.sh`

- [ ] **Step 1: Create `backend/scripts/deploy.sh` (executable)**
```bash
#!/usr/bin/env bash
# Build lambda zips, then terraform apply for one env. Lambdas pick up new
# code via filename + source_code_hash on bin/*.zip (see envs/*/lambda.tf).
set -euo pipefail
ENV=""; MODE="apply"
while [ $# -gt 0 ]; do case "$1" in
  --env) ENV="$2"; shift 2;;
  --plan) MODE="plan"; shift;;
  *) echo "unknown arg: $1" >&2; exit 2;;
esac; done
[ "$ENV" = "dev" ] || [ "$ENV" = "prod" ] || { echo "--env must be dev|prod" >&2; exit 2; }

cd "$(dirname "$0")/.."          # backend/
./scripts/build.sh               # → bin/{api,scanworker,presignup}.zip
TF="terraform -chdir=terraform/envs/$ENV"
$TF init -input=false
if [ "$MODE" = "plan" ]; then
  $TF plan -input=false -no-color
else
  $TF apply -input=false -auto-approve
fi
```

- [ ] **Step 2: Make executable + syntax/shellcheck**
```bash
chmod +x backend/scripts/deploy.sh
bash -n backend/scripts/deploy.sh && echo "syntax OK"
command -v shellcheck >/dev/null && shellcheck backend/scripts/deploy.sh || echo "shellcheck not installed — skip"
```
Expected: `syntax OK`; no shellcheck errors.

- [ ] **Step 3: 👤 Operator — smoke-test plan against dev (read-only)**
```bash
AWS_PROFILE=smachnogo backend/scripts/deploy.sh --env dev --plan
```
Expected: builds 3 zips, `terraform plan` runs and shows a diff (lambda code update at minimum). No apply. If clean, the script works.

- [ ] **Step 4: Commit**
```bash
git add backend/scripts/deploy.sh
git commit -m "ci: add deploy.sh (build zips + terraform apply per env)"
```

---

### Task 4: Deploy workflow (dev auto / prod tag-gated) + retire old ci.yml

**Files:**
- Create: `.github/workflows/backend-deploy.yml`
- Delete: `.github/workflows/ci.yml`

- [ ] **Step 1: Create `.github/workflows/backend-deploy.yml`**
```yaml
name: backend-deploy
on:
  push:
    branches: [main]
    paths: ["backend/**", ".github/workflows/backend-deploy.yml"]
    tags: ["v*"]
concurrency:
  group: backend-deploy-${{ github.ref }}
  cancel-in-progress: false
permissions:
  contents: read
  id-token: write
jobs:
  deploy-dev:
    if: github.ref == 'refs/heads/main'
    runs-on: ubuntu-latest
    environment: dev
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.26", cache-dependency-path: backend/pkg/go.sum }
      - uses: hashicorp/setup-terraform@v3
      - uses: aws-actions/configure-aws-credentials@v4
        with: { role-to-assume: ${{ secrets.AWS_DEPLOY_ROLE_ARN_DEV }}, aws-region: us-east-1 }
      - run: ./scripts/deploy.sh --env dev
        working-directory: backend
  deploy-prod:
    if: startsWith(github.ref, 'refs/tags/v')
    runs-on: ubuntu-latest
    environment: prod   # require-reviewer gate lives here (Task 5)
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.26", cache-dependency-path: backend/pkg/go.sum }
      - uses: hashicorp/setup-terraform@v3
      - uses: aws-actions/configure-aws-credentials@v4
        with: { role-to-assume: ${{ secrets.AWS_DEPLOY_ROLE_ARN_PROD }}, aws-region: us-east-1 }
      - run: ./scripts/deploy.sh --env prod
        working-directory: backend
```

- [ ] **Step 2: Delete the obsolete combined workflow**
```bash
git rm .github/workflows/ci.yml
```

- [ ] **Step 3: Lint workflow**
```bash
command -v actionlint >/dev/null && actionlint .github/workflows/backend-deploy.yml || echo "actionlint not installed — skip"
```
Expected: parses cleanly.

- [ ] **Step 4: Commit**
```bash
git add .github/workflows/backend-deploy.yml .github/workflows/ci.yml
git commit -m "ci: backend-deploy (dev on main / prod on tag, OIDC); retire ci.yml"
```

---

### Task 5: 👤 Operator setup + Phase-1 end-to-end verification

No code. Configure GitHub, then prove the loop.

- [ ] **Step 1: Set repo secrets (ARNs from Task 2 Step 5)**
```bash
gh secret set AWS_DEPLOY_ROLE_ARN_DEV  -b "arn:aws:iam::920071567477:role/smachnogo-ci-deploy-dev"
gh secret set AWS_DEPLOY_ROLE_ARN_PROD -b "arn:aws:iam::920071567477:role/smachnogo-ci-deploy-prod"
```

- [ ] **Step 2: Create Environments + prod gate (GitHub UI: Settings → Environments)**
- `dev`: no protection.
- `prod`: add **Required reviewers** = the other teammate; (optional) restrict to tag refs. Scope `AWS_DEPLOY_ROLE_ARN_PROD` to this environment if you prefer env-scoped secrets.

- [ ] **Step 3: Branch protection (Settings → Branches → `main`)**
Require status checks: `lint`, `test`, `tf-check`. Require PR before merge.

- [ ] **Step 4: Verify dev auto-deploy**
Merge the Phase-1 PR to `main`, then: `gh run watch` on `backend-deploy`. Expected: `deploy-dev` runs, assumes the dev role via OIDC, `terraform apply` succeeds (lambda code updated). Confirm in AWS (`AWS_PROFILE=smachnogo aws lambda get-function --function-name smachnogo-api-dev --query 'Configuration.LastModified'`).

- [ ] **Step 5: Verify prod tag-deploy + gate**
```bash
git tag v1.0.8 && git push origin v1.0.8
gh run watch   # deploy-prod should PAUSE awaiting approval
```
Approve in the UI (other teammate) → expect `terraform apply` against prod succeeds. (This is a real prod deploy — only do it when the backend is release-ready.)

---

## Phase 2 — iOS TestFlight

### Task 6: Fastlane (TestFlight)

**Files:**
- Create: `ios/fastlane/Appfile`
- Create: `ios/fastlane/Fastfile`
- Create: `ios/Gemfile`

- [ ] **Step 1: Create `ios/Gemfile`**
```ruby
source "https://rubygems.org"
gem "fastlane"
```

- [ ] **Step 2: Create `ios/fastlane/Appfile`**
```ruby
app_identifier("app.smachnogo.ios")
itc_team_id(ENV["ASC_TEAM_ID"]) if ENV["ASC_TEAM_ID"]
```

- [ ] **Step 3: Create `ios/fastlane/Fastfile`**
```ruby
default_platform(:ios)

def asc_key
  app_store_connect_api_key(
    key_id: ENV.fetch("ASC_KEY_ID"),
    issuer_id: ENV.fetch("ASC_ISSUER_ID"),
    key_content: ENV.fetch("ASC_KEY_P8_BASE64"),
    is_key_content_base64: true,
  )
end

platform :ios do
  # PR check: compile + unit tests on the simulator, NO signing.
  lane :test do
    run_tests(
      project: "Smachnogo.xcodeproj",
      scheme: "Smachnogo",
      destination: "platform=iOS Simulator,name=iPhone 16,OS=latest",
      skip_package_dependencies_resolution: false,
    )
  end

  # merge→main: archive Release + upload to TestFlight. Build# = CI run number.
  lane :beta do
    api = asc_key
    increment_build_number(xcodeproj: "Smachnogo.xcodeproj", build_number: ENV.fetch("GITHUB_RUN_NUMBER"))
    build_app(
      project: "Smachnogo.xcodeproj",
      scheme: "Smachnogo",
      export_method: "app-store",
      xcargs: "-allowProvisioningUpdates",
    )
    upload_to_testflight(api_key: api, skip_waiting_for_build_processing: true)
  end
end
```

- [ ] **Step 4: List lanes (sanity)**
```bash
cd ios && bundle install && bundle exec fastlane lanes
```
Expected: `test` and `beta` lanes listed.

- [ ] **Step 5: Commit**
```bash
git add ios/Gemfile ios/fastlane/Appfile ios/fastlane/Fastfile
git commit -m "ci(ios): fastlane test + beta(TestFlight) lanes (ASC API key)"
```

---

### Task 7: iOS CI workflow (PR sim-build + beta on main)

**Files:**
- Create: `.github/workflows/ios-ci.yml`

- [ ] **Step 1: Create `.github/workflows/ios-ci.yml`**
```yaml
name: ios-ci
on:
  pull_request:
    paths: ["ios/**", ".github/workflows/ios-ci.yml"]
  push:
    branches: [main]
    paths: ["ios/**", ".github/workflows/ios-ci.yml"]
concurrency:
  group: ios-ci-${{ github.ref }}
  cancel-in-progress: true
permissions:
  contents: read
jobs:
  test:
    if: github.event_name == 'pull_request' && github.event.pull_request.draft == false
    runs-on: macos-15
    timeout-minutes: 30
    defaults: { run: { working-directory: ios } }
    steps:
      - uses: actions/checkout@v4
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
      GITHUB_RUN_NUMBER: ${{ github.run_number }}
    steps:
      - uses: actions/checkout@v4
      - uses: ruby/setup-ruby@v1
        with: { ruby-version: "3.3", bundler-cache: true, working-directory: ios }
      - run: brew install xcodegen && xcodegen generate
      - run: bundle exec fastlane beta
```

- [ ] **Step 2: Lint**
```bash
command -v actionlint >/dev/null && actionlint .github/workflows/ios-ci.yml || echo "actionlint not installed — skip"
```

- [ ] **Step 3: Commit**
```bash
git add .github/workflows/ios-ci.yml
git commit -m "ci(ios): PR sim-build/test (no signing) + beta→TestFlight on main"
```

- [ ] **Step 4: 👤 Operator — ASC key secrets**
Create an App Store Connect API key (role **App Manager**), download the `.p8` once, then:
```bash
gh secret set ASC_KEY_ID -b "<key id>"
gh secret set ASC_ISSUER_ID -b "<issuer id>"
gh secret set ASC_KEY_P8_BASE64 -b "$(base64 -i AuthKey_XXXX.p8)"
```

- [ ] **Step 5: Verify**
PR touching `ios/**` → `test` job runs on macOS, passes (no signing). Merge to `main` → `beta` uploads build `#<run_number>` to TestFlight (confirm in App Store Connect → TestFlight).

---

## Phase 3 — iOS App Store on tag

### Task 8: Fastlane `release` lane + tag trigger

**Files:**
- Modify: `ios/fastlane/Fastfile` (add `release` lane)
- Modify: `.github/workflows/ios-ci.yml` (add tag-triggered `release` job)

- [ ] **Step 1: Add the `release` lane to `ios/fastlane/Fastfile`** (inside `platform :ios do`, after `beta`)
```ruby
  # tag: promote the latest tested TestFlight build to App Store, staged for
  # review. We do NOT auto-submit — a human clicks "Submit for Review" in ASC.
  lane :release do
    api = asc_key
    upload_to_app_store(
      api_key: api,
      build_number: latest_testflight_build_number(api_key: api).to_s,
      submit_for_review: false,
      automatic_release: false,
      skip_binary_upload: true,
      skip_screenshots: true,
      skip_metadata: false,
      precheck_include_in_app_purchases: false,
      force: true,
    )
  end
```

- [ ] **Step 2: Add the `release` job to `.github/workflows/ios-ci.yml`** (new job; runs on Linux — it's an ASC API call, no Xcode)
```yaml
  release:
    if: startsWith(github.ref, 'refs/tags/v')
    runs-on: ubuntu-latest
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
Also add `tags: ["v*"]` to the workflow's `on.push` block so tags trigger it:
```yaml
  push:
    branches: [main]
    tags: ["v*"]
    paths: ["ios/**", ".github/workflows/ios-ci.yml"]
```
(Note: tag pushes ignore `paths`, so the `release` job fires on any `v*` tag.)

- [ ] **Step 3: Lint + lanes**
```bash
command -v actionlint >/dev/null && actionlint .github/workflows/ios-ci.yml || echo "skip"
cd ios && bundle exec fastlane lanes   # expect test, beta, release
```

- [ ] **Step 4: Commit**
```bash
git add ios/fastlane/Fastfile .github/workflows/ios-ci.yml
git commit -m "ci(ios): release lane — stage tested TestFlight build for App Store (manual submit)"
```

- [ ] **Step 5: Verify**
Tag `v1.0.8` → `release` job pauses on `prod` approval → approve → expect the latest TestFlight build attached to a new App Store version (status "Prepare for Submission"). Then 👤 a human reviews release notes and clicks **Submit for Review** in App Store Connect.

---

## Self-Review

**Spec coverage:**
- Backend lint/test/vet/tf-check, path-filtered, concurrency → Task 1 ✓
- OIDC codified (provider + dev/prod roles, env-scoped prod trust) → Task 2 ✓
- `deploy.sh` (build + tf apply per env) → Task 3 ✓
- dev auto on main / prod on tag, gated → Task 4 + 5 ✓
- Secrets, environments, branch protection → Task 5 ✓
- iOS Fastlane + ASC key, build#=run_number, PR sim-build, beta→TestFlight → Tasks 6,7 ✓
- App Store on tag, promote tested build, human-submit → Task 8 ✓
- Retire broken `ci.yml` → Task 4 ✓
- Phasing 1/2/3 → matches Phase headers ✓
- Watch-outs (build# monotonic, build reuse, signing via API key, macOS cost controls, export-compliance already set) → Tasks 6/7/8 ✓

**Placeholder scan:** Operator steps have real `gh`/`terraform`/`aws` commands; `<key id>`/`<issuer id>`/`v1.0.8` are genuine user-supplied values, not lazy placeholders. No TBDs. ✓

**Type/name consistency:** `asc_key` helper used by `beta` + `release`; secret names (`ASC_KEY_ID/ISSUER_ID/P8_BASE64`, `AWS_DEPLOY_ROLE_ARN_DEV/PROD`) consistent across Fastfile, workflows, and operator steps; role names match Task 2 outputs → Task 5 secret values. ✓

**Known caveats for the executor:**
- `golangci-lint` may surface pre-existing findings in the WIP backend; fix or narrow `.golangci.yml` `enable` set in Task 1 if it blocks (don't disable wholesale).
- First `fastlane build_app` with `-allowProvisioningUpdates` needs the Apple Distribution cert reachable; if the runner can't create it via the API key, switch to `match` (add a private certs repo) — note it and escalate rather than committing secrets.
- Action version tags (`@v4` etc.) are used for readability; pinning to commit SHAs is a hardening follow-up.

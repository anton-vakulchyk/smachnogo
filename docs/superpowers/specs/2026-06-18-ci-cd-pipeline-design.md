# CI/CD Pipeline Design — Smachnogo monorepo

**Date:** 2026-06-18
**Status:** Approved design → implementation plan to follow
**Topic:** GitHub Actions CI/CD for a 2-person team: backend checks + AWS delivery (dev auto / prod gated) + iOS TestFlight/App Store.

## Goal

Replace the current half-built `ci.yml` with a proper, low-ceremony pipeline so a 2-person team can ship without running the backend locally: every change is checked on PR, `main` continuously deploys to **dev** (AWS backend + iOS TestFlight), and a git **tag** cuts a **prod** release (AWS backend + App Store) behind one teammate approval.

## Current State & Gaps

- `ci.yml`: `test` (`go test ./...` in `pkg`, `go vet` across 5 modules) → `build` (`scripts/build.sh` → `bin/{api,scanworker,presignup}.zip` artifact). A `deploy` job exists but is **broken**: it calls `scripts/deploy.sh`, which **does not exist**, and **OIDC is not wired** (no IAM provider/role, no `AWS_DEPLOY_ROLE_ARN`).
- No `golangci-lint`, no Terraform `fmt/validate/plan` in CI, no path filters, no concurrency control, no pinned action SHAs.
- iOS has **no** CI; TestFlight is manual Xcode archiving (builds 3–7).
- Terraform: per-env duplicated `.tf` under `terraform/envs/{dev,prod}`, **S3 remote state** (`smachnogo-tfstate-920071567477`, lock table `smachnogo-tfstate-lock`), single AWS account `920071567477`. Lambdas deploy via `filename = bin/<fn>.zip` + `source_code_hash` — so **`terraform apply` IS the code+infra deploy**.

## Decisions (resolved) & Rationale

| Decision | Choice | Why (2-person team) |
|---|---|---|
| Branch model | Trunk + tags | Minimal ceremony; PR checks are the safety net. |
| Dev deploy | **Auto on merge to `main`** (backend `tf apply` + iOS→TestFlight) | "No local backend" — dev AWS always reflects `main`; latest app always in TestFlight to dogfood. |
| Prod deploy | **Git tag `vX.Y.Z`**, behind a GitHub `prod` Environment approval (the other teammate) | One tag = one coordinated backend+app release; the approval is the release review. |
| App Store step | **Upload/promote build; human clicks "Submit for Review"** in ASC | Apple's 24–48h review gates anyway; keep a human on the irreversible, judgment step (notes/timing/phasing). No 2am auto-submit with no on-call. |
| iOS on PRs | **Keep, but cheap**: path-filtered to `ios/**` + simulator-only, no-signing build/test | Merge auto-ships to TestFlight, so a broken iOS merge costs more than the bounded macOS minutes. Pure-backend PRs pay nothing. |
| AWS auth | **OIDC, codified in Terraform** (provider + least-priv dev/prod roles) | No static keys; reproducible; reviewable. |
| iOS signing/upload | **Fastlane + App Store Connect API key (.p8)** | Small-team standard; reproducible signing on ephemeral runners; same key for TestFlight + App Store. |
| AWS isolation | **Same account, per-env TF state** (existing) | Multi-account is overkill for 2 people. |
| Build-number source | **CI run number** (monotonic), not git state | Auto-uploading to TestFlight on every merge requires strictly-increasing build numbers. |
| Build reuse | **Promote the same TestFlight build to App Store** (no re-archive at tag) | Ship exactly what was tested; tag's iOS step is an ASC API call (runs on Linux), halving macOS minutes. |

## Architecture — three triggers

```
PR  ──▶ checks only (no deploy):
        • backend: golangci-lint + go test + go vet  (path: backend/**)
        • terraform: fmt -check + validate + PLAN (per env) → comment   (path: backend/terraform/**)
        • ios: simulator build + unit tests, NO signing                 (path: ios/**)
        required green to merge (branch protection)

merge main ──▶ DEV (automatic):
        • backend: build.sh → terraform apply  (envs/dev, OIDC dev role)
        • ios:     fastlane beta → archive(Release) → TestFlight   (build# = github.run_number)

tag vX.Y.Z ──▶ PROD (one approval):
        • GitHub Environment "prod" → teammate approves
        • backend: build.sh → terraform apply  (envs/prod, OIDC prod role)
        • ios:     fastlane release → select latest TestFlight build → submit-staging
                   → human clicks "Submit for Review" in App Store Connect
```

## Components

**1. `.github/workflows/` (split by concern, path-filtered)**
- `backend-ci.yml` — PR + push: `lint` (golangci-lint), `test` (`go test` in `pkg`; `go vet` across `pkg`, `functions/api`, `functions/workers/scanworker`, `functions/presignup`, `tests`; all `GOWORK=off`), `tf-check` (fmt+validate+plan). Go 1.26, module cache.
- `backend-deploy.yml` — `deploy-dev` (push `main`, env `dev`) and `deploy-prod` (push tag `v*`, env `prod`). Both: OIDC assume-role → `scripts/deploy.sh --env <env>`.
- `ios-ci.yml` — PR (`ios/**`): macOS, simulator build + test, no signing. push `main`: `fastlane beta` (TestFlight). tag `v*`: `fastlane release` (App Store submit-staging) behind env `prod`.
- Shared: `concurrency` (cancel-in-progress per ref+workflow), pinned action SHAs, `timeout-minutes` on macOS jobs, skip drafts.

**2. `backend/scripts/deploy.sh` (NEW)** — `--env {dev,prod}`: `build.sh` (→ `bin/*.zip`) → `cd terraform/envs/$env` → `terraform init` → `terraform apply -auto-approve`. (Plan variant for PR: `terraform plan -out` + render to comment.)

**3. `terraform/github-oidc/` (NEW, codified)** — `aws_iam_openid_connect_provider` for `token.actions.githubusercontent.com`; role `smachnogo-ci-deploy-dev` (trust: repo, `ref:refs/heads/main` + PR plan) and `smachnogo-ci-deploy-prod` (trust: repo + `environment:prod`); least-privilege policies sufficient for `terraform apply` (TF state S3+Dynamo lock, lambda, apigw, dynamodb, cognito, sqs, s3, iam, logs, ssm). Applied once locally by Anton.

**4. `backend/.golangci.yml` (NEW)** — standard linters (govet, staticcheck, errcheck, ineffassign, unused, gofmt/goimports).

**5. `ios/fastlane/` (NEW)** — `Fastfile` lanes: `test` (simulator, no signing), `beta` (build Release → `pilot`/`upload_to_testflight`, build# = `ENV[GITHUB_RUN_NUMBER]`), `release` (`upload_to_app_store` with `submit_for_review: false`, select latest build). `Appfile` (app id `app.smachnogo.ios`, team `CP598M5SUG`). API-key auth via `app_store_connect_api_key`.

## Secrets & one-time setup (Anton)

- Apply `terraform/github-oidc/` once → creates OIDC provider + 2 roles.
- GitHub repo secrets/vars: `AWS_DEPLOY_ROLE_ARN_DEV`, `AWS_DEPLOY_ROLE_ARN_PROD`; `ASC_KEY_ID`, `ASC_ISSUER_ID`, `ASC_KEY_P8_BASE64` (App-Manager-role key, base64 so newlines survive).
- GitHub Environments: `dev` (no protection), `prod` (required reviewer = the other teammate; scope the AWS-prod role + ASC secrets to this env).
- Branch protection on `main`: require `backend-ci` + `ios-ci`(PR) status checks.

## Implementation phasing (value-first)

1. **Phase 1 — Backend checks + AWS delivery.** Split/modernize `backend-ci.yml` (lint+test+tf-check, path-filtered, concurrency, pinned SHAs); write `deploy.sh`; `terraform/github-oidc/`; `deploy-dev` (auto) + `deploy-prod` (tag, gated). *Delivers the core: no local backend, real deploys.*
2. **Phase 2 — iOS TestFlight.** `ios/fastlane/` + `ios-ci.yml` PR sim-build + `beta` on merge→TestFlight.
3. **Phase 3 — iOS App Store.** `release` lane on tag (promote tested build, human submits) behind `prod` env.

## Risks / watch-outs (mitigations baked in)

- **Build-number monotonicity** → drive from `github.run_number`.
- **Build reuse** → tag's App Store step promotes the existing TestFlight build; no re-archive.
- **Plan == apply on same commit** → prod `tf plan` at PR and `apply` at tag must be the same diff; rely on tagged-commit checkout.
- **Signing on ephemeral runners** → API-key cloud signing (`-allowProvisioningUpdates`) or `match`; never local keychain.
- **`.p8` handling** → base64 secret, App-Manager (not Admin) role, env-scoped.
- **Shared AWS account blast radius** → keep dev/prod TF states non-cross-referencing; prod role scoped to `environment:prod`.
- **Export compliance** → already set (`ITSAppUsesNonExemptEncryption: NO`).
- **macOS cost** → path filter `ios/**`, pin `macos-15` + Xcode, cache SPM/DerivedData, `concurrency` cancel, `timeout-minutes`, skip drafts.

## Out of scope

- Multi-account AWS isolation; blue/green or canary backend deploys; auto-release-to-App-Store (Apple review stays a human gate); load/integration tests requiring live Gemini/AWS in PR CI (the `//go:build live` and `//go:build eval` suites stay manual/opt-in).

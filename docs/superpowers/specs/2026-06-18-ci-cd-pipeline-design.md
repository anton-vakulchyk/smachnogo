# CI/CD Pipeline Design — Smachnogo monorepo

**Date:** 2026-06-18 (rev 2 — hardened after independent expert review)
**Status:** Approved design → implementation plan
**Topic:** GitHub Actions CI/CD for a 2-person team: backend checks + AWS delivery (dev auto / prod gated) + iOS TestFlight/App Store.

## Goal

Replace the half-built `ci.yml` with a proper, low-ceremony, **safe** pipeline so a 2-person team can ship without running the backend locally: PRs run checks **and a Terraform plan**, `main` continuously deploys to **dev** (AWS + iOS TestFlight), and a git **tag** cuts a **prod** release (AWS + App Store) where the human **approves a reviewed plan**, not an unseen apply.

## Current State & Gaps

- `ci.yml`: `test`+`vet` → `build` (3 lambda zips). `deploy` job is **broken** (`scripts/deploy.sh` absent; OIDC not wired). No lint, no Terraform plan, no path filters, no concurrency, no iOS CI.
- Terraform: per-env `.tf` under `envs/{dev,prod}`, **S3 state** (`smachnogo-tfstate-920071567477`, lock `smachnogo-tfstate-lock`), single account `920071567477`. Lambdas deploy via `filename = bin/<fn>.zip` + `source_code_hash`, so **`terraform apply` IS the code+infra deploy**. Terraform manages 3 per-Lambda IAM roles (needs `iam:PassRole`).
- iOS: **no test target exists** (scheme `<Testables>` is empty); `VERSIONING_SYSTEM` is unset; built via **xcodegen** (`project.yml` is source of truth).

## Decisions (post-review) & Rationale

| Decision | Choice | Why |
|---|---|---|
| Branch model | Trunk + tags | Minimal ceremony; PR checks + plan are the safety net. |
| Dev deploy | Auto on merge to `main` (backend `apply -auto-approve` + iOS→TestFlight) | "No local backend"; dev's plan was already reviewed on the PR. |
| Prod deploy | Tag `vX.Y.Z` → **plan job → `prod` Environment approval → apply the *saved* plan** | The approval reviews a **real diff**, and apply == exactly what was approved. |
| AWS auth | **OIDC, 3 roles**: read-only **plan** (trusts `main` + `pull_request`), **deploy-dev** (trusts `main` only), **deploy-prod** (trusts `environment:prod` only) | A `pull_request`-assumable role must be **read-only**; write/IAM roles never trust PRs. |
| IAM scope | **Least-privilege**: app actions scoped to `smachnogo-*` / app ARNs; `iam:*`→ scoped role-CRUD on `role/smachnogo-*` + `iam:PassRole` limited to `lambda.amazonaws.com` + block attaching `AdministratorAccess` | A `iam:*`-on-`*` deploy role = account-takeover on any compromised step. Trust limits *who calls*; the policy must limit *what a call can do*. |
| iOS PR check | **Compile-only** (`build_app` skip-archive/skip-codesign) | There is **no test target**; `run_tests` would fail every PR and block merges. |
| iOS build number | `CURRENT_PROJECT_VERSION=$GITHUB_RUN_NUMBER` via `xcargs` (NOT `increment_build_number`) | agvtool needs `VERSIONING_SYSTEM` (unset) and edits the pbxproj that xcodegen regenerates. |
| iOS signing | **Fastlane `match`** (encrypted dist cert+profiles in a private repo) | `-allowProvisioningUpdates`+API key makes *profiles* but not the *Distribution cert*; fresh runners have an empty keychain → archive fails. |
| App Store step | Promote the tested TestFlight build; `submit_for_review:false` + **`skip_metadata:true`**; human clicks Submit | Apple review is the gate; don't push store metadata on every tag. |
| OIDC thumbprint | **Omit** (`thumbprint_list` unset) | AWS validates GitHub's OIDC via its root CA store since 2023; the pinned thumbprint is non-load-bearing cruft. |
| Concurrency | Deploy keyed by **environment** (not ref); `cancel-in-progress:false` | Same-env applies serialize cleanly on the shared lock table; never cancel an apply. |
| Action pinning | Pin security-sensitive actions to **commit SHAs** (`configure-aws-credentials`, `checkout`, `setup-ruby`, `golangci-lint-action`); pin Xcode + xcodegen + golangci-lint versions | A poisoned tag on the role-assuming action is the IAM nightmare made real; floating Xcode/lint = non-reproducible. |

## Architecture — three triggers

```
PR  ──▶ checks (no mutation):
        backend-ci: golangci-lint + go test + go vet                    (path backend/**)
        tf-plan:    assume READ-ONLY plan role (OIDC) → plan DEV → PR comment
        ios-ci:     compile-only build, NO signing                      (path ios/**, drafts skipped)
        branch protection requires these green to merge

merge main ──▶ DEV (auto):
        deploy-dev:  assume deploy-DEV role → build.sh → terraform apply -auto-approve (envs/dev)
        ios beta:    match(readonly) → build_app (build#=run_number) → TestFlight

tag vX.Y.Z ──▶ PROD (reviewed):
        plan job:    assume deploy-PROD role → build.sh → terraform plan -out=tfplan (artifact + log)
        ⏸ GitHub Environment "prod" approval (other teammate reviews the plan)
        apply job:   assume deploy-PROD role → build.sh → terraform apply tfplan   (== reviewed diff)
        ios release: promote latest TestFlight build → App Store (staged) → human Submits
```

## Components

- `.github/workflows/backend-ci.yml` — `lint` (golangci-lint, pinned), `test` (`go test` pkg + `go vet` 5 modules, `GOWORK=off`), `tf-fmt-validate` (no creds), `tf-plan` (OIDC **plan role**, `deploy.sh --env dev --plan-out`, comment).
- `.github/workflows/backend-deploy.yml` — `deploy-dev` (push `main`, env `dev`, deploy-dev role, `--apply-auto`); `plan-prod`+`apply-prod` (tag `v*`, env `prod` on apply, deploy-prod role, saved-plan handoff). Concurrency by env.
- `.github/workflows/backend-drift.yml` — nightly `schedule`: plan dev+prod with the read-only role, notify on non-empty diff.
- `backend/scripts/deploy.sh` — `--env {dev,prod}` + `--plan-out <f>` | `--apply <f>` | `--apply-auto`. Always `build.sh` first.
- `backend/terraform/github-oidc/` — provider (no thumbprint) + 3 scoped roles + 2 policies (read-only plan; least-priv deploy).
- `backend/.golangci.yml` — pinned linter set (govet, staticcheck, errcheck, ineffassign, unused).
- `ios/fastlane/{Appfile,Matchfile,Fastfile}` + `ios/Gemfile` — `test` (compile-only), `beta` (match→build#=run_number→TestFlight), `release` (promote→App Store, skip_metadata).
- `.github/workflows/ios-ci.yml` — PR compile-only (skip drafts), `beta` on `main`, `release` on tag (env `prod`). Pin Xcode + xcodegen.
- Terraform `prevent_destroy` on the DynamoDB table + photos bucket (deploy role can otherwise delete them).

## Secrets & one-time setup (👤 Anton)

- Apply `terraform/github-oidc/` once (admin creds) → OIDC provider + 3 roles.
- Repo secrets/vars: `AWS_PLAN_ROLE_ARN`, `AWS_DEPLOY_ROLE_ARN_DEV`, `AWS_DEPLOY_ROLE_ARN_PROD`; `ASC_KEY_ID`, `ASC_ISSUER_ID`, `ASC_KEY_P8_BASE64` (App-Manager role); `MATCH_GIT_URL`, `MATCH_PASSWORD` (or a deploy key) for the certs repo; a failure-notification webhook (`SLACK_WEBHOOK` or similar).
- **Fastlane match:** create the private certs repo, run `fastlane match appstore` once locally to seed the Distribution cert + App Store profile.
- GitHub Environments: `dev` (no protection); `prod` (**required reviewer** = the other teammate; scope prod role + ASC/match secrets here).
- Branch protection on `main`: require `lint`, `test`, `tf-fmt-validate`, `ios-ci` PR checks.
- **⚠️ LAUNCH-GATE:** revert the beta-generous limits (`free_scan_allowance` 1000→real, `free_window_days` 3650→7, review `daily_scan_cap`) **before the first prod `apply`** — the pipeline auto-ships `main`'s Terraform vars to prod on tag.

## Implementation phasing

1. **Phase 1 — Backend checks + AWS delivery.** backend-ci (lint/test/vet/fmt-validate + **tf-plan-on-PR**); `github-oidc` (3 scoped roles); `deploy.sh` (plan/apply-saved-plan/apply-auto); deploy-dev (auto) + plan-prod→approve→apply-prod; drift workflow; `prevent_destroy`; pins. *Closes the real gap, safely.*
2. **Phase 2 — iOS TestFlight.** Fastlane (`match` + compile-only `test` + `beta`); ios-ci PR compile-check + `beta` on merge; pin Xcode/xcodegen.
3. **Phase 3 — iOS App Store.** `release` lane (promote tested build, skip_metadata, human Submit) on tag behind `prod`.

## Risks / watch-outs (mitigations baked in)

- **Account takeover via broad IAM** → least-priv scoped policy; PR only ever assumes the read-only role; block `AdministratorAccess` attach; `iam:PassRole`→lambda only.
- **Approving an unseen apply** → prod = plan → approval → apply the saved plan (same commit rebuilds identical zips → hash matches).
- **iOS PR check that can't pass / broken build#** → compile-only lane; build# via `xcargs`; `match` for signing.
- **Concurrent applies on shared lock** → concurrency by env; document `force-unlock`; nightly drift plan.
- **No rollback** → documented: dev = revert+merge; prod = re-tag previous good SHA; `prevent_destroy` on stateful resources.
- **Non-reproducible toolchain** → pin action SHAs, Xcode, xcodegen, golangci-lint versions.
- **Silent failures (2-person team)** → notify on `backend-deploy`/`ios beta` failure + on prod approval request.
- **Beta limits leaking to prod** → LAUNCH-GATE revert before first prod apply.
- **Export compliance** → already set (`ITSAppUsesNonExemptEncryption: NO`).

## Out of scope

Multi-account isolation; blue/green/canary; auto-release-to-App-Store (Apple review stays human); live Gemini/AWS integration+eval suites in PR CI (`//go:build live`/`eval` stay opt-in); adding an iOS unit-test target (compile-only for now — a real test target is a worthwhile later addition).

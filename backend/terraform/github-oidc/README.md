# GitHub OIDC — CI/CD IAM roles

Apply **once** with admin credentials to bootstrap the CI/CD trust:

```sh
AWS_PROFILE=smachnogo terraform -chdir=backend/terraform/github-oidc init
AWS_PROFILE=smachnogo terraform -chdir=backend/terraform/github-oidc apply
```

## What this creates

| Resource | Purpose |
|---|---|
| `aws_iam_openid_connect_provider.github` | Trusts GitHub's OIDC token issuer |
| `smachnogo-ci-plan` role | **Read-only.** Assumed by PRs and main pushes for `terraform plan`. Cannot mutate anything. |
| `smachnogo-ci-deploy-dev` role | **Deploy.** Assumed only by pushes to `main` (never `pull_request`). |
| `smachnogo-ci-deploy-prod` role | **Deploy.** Assumed only by the `prod` GitHub Environment (required-reviewer gate). |

## Security properties

- The **readonly role** (`smachnogo-ci-plan`) can only read S3/DynamoDB state + describe existing infra. PRs never get write access.
- The **deploy roles** (`trust_dev`, `trust_prod`) condition on `sub = ref:refs/heads/main` / `environment:prod` — `pull_request` sub is structurally excluded.
- `iam:*` is scoped to `role/smachnogo-*` resources only; account-wide IAM is blocked.
- Attaching the three AWS-managed admin policies (`AdministratorAccess`, `PowerUserAccess`, `IAMFullAccess`) is explicitly Denied (defense-in-depth).
- `iam:PassRole` is limited to `lambda.amazonaws.com` as the receiving service.

## Residual risk (accepted for v1)

The deploy role can create a `smachnogo-*` role, attach an inline `*:*` policy via `PutRolePolicy`, and `PassRole` it to a Lambda — making it effectively account-admin-equivalent for anything it can name. The `DenyAdminAttach` block is defense-in-depth, not a takeover barrier. The real control is the trust boundary (only `main` / `prod` env can assume these roles, never a PR). Fast-follow: add a permissions boundary to every created role to cap escalation.

## Outputs → GitHub Secrets

After apply, record the three ARNs and set them as repository secrets:

```sh
gh secret set AWS_PLAN_ROLE_ARN       -b <plan_role_arn>
gh secret set AWS_DEPLOY_ROLE_ARN_DEV  -b <dev_role_arn>
gh secret set AWS_DEPLOY_ROLE_ARN_PROD -b <prod_role_arn>
```

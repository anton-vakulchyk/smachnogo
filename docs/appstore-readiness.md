# App Store Readiness — M6.5 Checklist

Status as of 2026-06-11. This gates the first external TestFlight build.
Split: what's already done in code/infra vs. what only Anton can do.

---

## ✅ Done (code & infrastructure)

| Item | Where |
|---|---|
| Prod environment (own tfstate, 41 resources) | `backend/terraform/envs/prod` |
| Prod API | `https://b37hzro1uk.execute-api.us-east-1.amazonaws.com` |
| Prod Cognito pool (deletion protection ACTIVE) | `us-east-1_6pYHqbzBO`, client `48fo7072sj31ul2mo25vop8j3h` |
| Prod DDB table (PITR, deletion protection, prevent_destroy) | `smachnogo-main-prod` |
| Prod S3 bucket (public-access-block, TLS-only, 90d lifecycle) | `smachnogo-photos-prod-920071567477` |
| Prod smoke test green (scan → confirm → text estimate, throwaway identity) | run 2026-06-10 |
| Prod alarms (DLQ, queue depth, worker errors, API 5xx, scans/day) → SNS | `ops.tf`, email `anton@vakulchyk.com` |
| AWS Budget alert ($10/mo) | `ops.tf` |
| Vision model on GA `gemini-2.5-flash` (preview model had demand-spike timeouts) | `config.go`, both `variables.tf` |
| iOS Release config → prod URL + prod Cognito client; Release build green | `ios/Configs/Release.xcconfig` |
| Legal pages written (need hosting — see Anton list) | `web/index.html`, `privacy.html`, `terms.html`, `support.html` |
| Privacy policy discloses: Gemini AI processing, GPS stripping, 90d photo / 30d scan retention, deletion/export, no ads | `web/privacy.html` |
| Terms include: AI-estimates disclaimer, auto-renew subscription language, Apple standard EULA link | `web/terms.html` |
| `ITSAppUsesNonExemptEncryption = NO` | `ios/project.yml` |
| Camera + photo-library purpose strings (specific, not vague) | `ios/project.yml` |
| Health disclaimer: shown once at first scan result + permanently in Settings | `ScanResultView.swift`, `SettingsView.swift` |
| Account deletion (5.1.1(v)) + data export in-app | Settings → `DELETE /v1/users/me`, `GET /v1/export` |
| ATT: N/A — zero third-party SDKs, no tracking | — |

## 📋 Privacy nutrition labels (answers prepared — Anton enters them in ASC)

Data types to declare, all **linked to user** (via anonymous account), **not used for tracking**:

| ASC category | What | Purpose |
|---|---|---|
| Photos or Videos | food photos uploaded for analysis | App Functionality |
| Other User Content | typed meal descriptions, diary | App Functionality |
| Health & Fitness → Other (dietary) | estimated calories/nutrition history | App Functionality |
| Identifiers → User ID | anonymous account id (Cognito sub) | App Functionality |
| Purchases | subscription status (M7+) | App Functionality |

Not collected: name, email, phone, precise location (GPS is stripped on-device), contacts, browsing, advertising data, crash data (until Sentry is added — update labels then).

## 📝 App Review notes (paste into ASC "Notes" when submitting)

> The app uses anonymous accounts — there is NO login screen and no demo account is needed. On first launch the app silently creates a private account; just point the camera at food (or pick the bundled simulator photos) and the scan completes in ~10s. Free allowance is once per physical device (DeviceCheck); if the device used it before, the paywall appears — purchase with a sandbox Apple ID to test the camera. Text-based diary entry ("Describe" button) is free forever and needs no purchase.

(DeviceCheck sentence becomes relevant at M7 — keep it in the notes from the first paywalled build onward.)

---

## 🧑 Anton — required before first external TestFlight

1. **Apple Developer Program** enrollment ($99/yr) — blocks everything below.
2. **App Store Connect**: create app, bundle id `app.smachnogo.ios`, name "smachnogo".
3. **smachnogo.app DNS + hosting**: deploy `web/` (Cloudflare Pages is the suggested zero-cost host), so privacy/terms/support URLs are live — all three are required ASC fields.
4. **support@smachnogo.app** email forwarding (Cloudflare Email Routing is free).
5. **Sentry project** (or decide to defer): create project, hand me the DSN — I'll add the SPM package and wire it. Plan requires crash reporting before strangers run the app. Update privacy labels (+ Crash Data) when added.
6. **Confirm the SNS subscription email** sent to anton@vakulchyk.com (prod + dev alarm topics are unconfirmed until clicked — alarms go nowhere otherwise).

## 🧑 Anton — required before the paywall launch (M7)

7. **Paid Apps Agreement** + banking/tax in ASC.
8. **Small Business Program** enrollment — **≥1 fiscal month before paywall launch** (margin math assumes 15%; un-enrolled = 30% and $6.99 nets ~$4.89).
9. **EU DSA trader declaration** in ASC (or accept EU delisting).
10. **Age rating questionnaire** (expect 4+; no objectionable content).
11. **Subscription products** in ASC: $6.99/mo + $39.99/yr with 7-day trial on annual.

## 🔐 Anton — security follow-ups (from the build, do these soon)

12. **Deactivate the root access keys** for the smachnogo AWS account (`AKIA5MOEJ7B2XHAVGFSP`) — they were shared in chat; the `claude-deployer` IAM user + `smachnogo-deploy` role now cover all operations.
13. **Dedicated prod Gemini API key** — prod currently shares the dev key (single blast radius, no per-env spend separation). Create a second key in Google AI Studio, then: `aws ssm put-parameter --profile smachnogo --name /smachnogo/prod/gemini_api_key --type SecureString --overwrite --value <key>`.
14. **Google AI Studio spend cap** on the Gemini key(s).
15. **Old ultra0-account cleanup** (account 912442530845): early-M0 smachnogo resources still exist there (DDB table, S3 bucket incl. tfstate bucket, SQS queues). Say the word and I'll tear them down — I didn't delete anything in that account without your explicit OK.

## ⏭ Deferred / nice-to-have

- `api.smachnogo.app` custom domain → after DNS exists, add ACM cert + API GW domain mapping in Terraform, then flip `Release.xcconfig` back to the domain.
- Lambda reserved concurrency: restore worker cap to 3–5 after the new account's quota increase (currently unlimited-within-account-limit-10; request bump via Service Quotas).
- Anthropic provider re-enable: uncomment the blank imports in both function mains when Anton has Claude API keys (M7 bake-off wants Opus/Sonnet in the eval).

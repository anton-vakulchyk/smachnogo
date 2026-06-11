# App Store Readiness — M6.5/M7 Checklist

Status as of 2026-06-11 (updated after M7 monetization). This gates the
first external TestFlight build. Split: what's already done in code/infra
vs. what only Anton can do.

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

## ✅ Done in M7 (monetization — built & verified)

| Item | Notes |
|---|---|
| Entitlement layer live in BOTH envs (`ENTITLEMENT_MODE=enforce`) | 10 free scans / 7 days → 402 PAYWALL w/ reason; subscriber daily cap 20; text diary free forever |
| Failed/not-food scans refund BOTH counters | verified through the real deployed worker |
| StoreKit 2 server side | `POST /v1/subscriptions/receipt` (JWS verified vs Apple root via go-iap), webhook w/ notificationUUID dedup + signedDate ordering + appAccountToken attribution + restore/transfer (latest claim wins) — full lifecycle e2e'd on dev (subscribe → scan → expire → 402, replay + out-of-order dropped) |
| Prod webhook rejects unverified JWS (401) | dev runs `insecure_dev` decode for Xcode-signed test transactions; config refuses that mode in prod |
| iOS paywall | PaywallView (displayPrice only, restore, full auto-renew disclosure, legal links, product-failure state), scans-remaining chip, paywalled scans park (photo kept) and auto-resume on subscribe — simulator-verified incl. webhook-driven unlock |
| Local StoreKit testing config | `ios/StoreKit/Smachnogo.storekit`, attached to the Xcode scheme (products mirror ASC) |
| DeviceCheck seam | `X-Device-Token` sent by app, server checker fails open; real Apple API call lands when the key exists (below) |
| Model bake-off + eval harness | `tests/eval` (5 fixtures × N models). 2026-06-11: gemini-2.5-flash 5/5 $0.0031/scan (default, kept); gemini-3.1-flash-lite 5/5 $0.0013 2.8s (cost-down candidate — needs bigger fixture set); 3-flash-preview/3.5-flash had latency spikes; Opus/Sonnet pending Anthropic keys |
| CI workflow | `.github/workflows/ci.yml` (test→build; manual deploy via OIDC role — see Anton list) |

## ✅ Done in M8 (Sign in with Apple — built & verified)

| Item | Notes |
|---|---|
| `POST /v1/users/apple` link/recover | native SIWA identity token verified vs Apple JWKS (`APPLE_VERIFY_MODE=full` in prod — forged-token 401 verified live; dev decode-only for testing); nonce check; one Apple ID per diary (409 on mismatch) |
| Recovery = bounded item-copy | meals+profile old→new user, subscription/TXN owner transferred, link repointed, old partition+photos+Cognito user deleted; idempotent re-sign-in; full lifecycle e2e'd on deployed dev |
| iOS "Back up & restore" in Settings | native SignInWithAppleButton + nonce, recovered→diary reload, `apple_linked` shown from /users/me; simulator shows the system sign-in prompt (no Apple ID on sim — expected) |
| SIWA entitlement | `com.apple.developer.applesignin` via project.yml → Smachnogo.entitlements |

## ✅ Done in M9 (limits & goals — built & verified)

Daily caps (calories/sugar/sodium/sat-fat/carbs/fat) editable in Settings,
persisted via `PATCH /v1/users/me {limits}` (allowlist-validated). Coloring
is pure client-side (`LimitsRule.swift` owns the thresholds): a logged day
is red iff ANY cap is exceeded, green iff all respected; weeks/months green
at ≥80% green logged days, red below 50%. Month-grid dots, stats bars, and
the period status dot recolor automatically — simulator-verified both ways
(sugar cap 5 → red day; 50 → green day).

**M8 caveats for Anton:** the on-device flow needs your **paid Apple Developer team** (capability registers with the App ID) and a device/simulator **signed into an Apple ID** — test once: link on device A, recover on device B (old device demotes to a fresh empty account by design — its Keychain identity was deleted server-side). The old device's already-issued access token keeps verifying ≤1h against an empty partition (same accepted residual as account deletion).

## 🧑 Anton — required before the paywall launch (M7 → live)

7. **Paid Apps Agreement** + banking/tax in ASC.
8. **Small Business Program** enrollment — **≥1 fiscal month before paywall launch** (margin math assumes 15%; un-enrolled = 30% and $6.99 nets ~$4.89).
9. **EU DSA trader declaration** in ASC (or accept EU delisting).
10. **Age rating questionnaire** (expect 4+; no objectionable content).
11. **Subscription products in ASC — IDs must match the code exactly:**
    - Group: `Premium`
    - `smachnogo.premium.monthly` — $6.99/month
    - `smachnogo.premium.annual` — $39.99/year + **7-day free introductory offer**
12. **App Store Server Notifications V2 URL** in ASC:
    - Production: `https://b37hzro1uk.execute-api.us-east-1.amazonaws.com/v1/webhooks/appstore`
    - Sandbox: `https://dy9kj15vph.execute-api.us-east-1.amazonaws.com/v1/webhooks/appstore` (dev)
13. **Enable Billing Grace Period** in ASC (off by default — without it a card hiccup hard-locks a paying user). **Leave Family Sharing OFF** (irreversible once on).
14. **DeviceCheck key**: Certificates → Keys → create a DeviceCheck `.p8`; hand me key ID + team ID + the file → I implement the Apple API caller and flip `DEVICECHECK_ENABLED` (until then reinstall abuse is bounded by Keychain persistence only).
15. **One Xcode-launched run** (Cmd-R) to exercise the StoreKit purchase sheet against `Smachnogo.storekit` — simctl launches can't inject the config (server flow already verified). Sandbox purchase test follows once ASC products exist.
16. *(optional)* GitHub repo + AWS OIDC deploy role + `AWS_DEPLOY_ROLE_ARN` secret to activate CI deploys.

## 🔐 Anton — security follow-ups (from the build, do these soon)

12. **Deactivate the root access keys** for the smachnogo AWS account (`AKIA5MOEJ7B2XHAVGFSP`) — they were shared in chat; the `claude-deployer` IAM user + `smachnogo-deploy` role now cover all operations.
13. **Dedicated prod Gemini API key** — prod currently shares the dev key (single blast radius, no per-env spend separation). Create a second key in Google AI Studio, then: `aws ssm put-parameter --profile smachnogo --name /smachnogo/prod/gemini_api_key --type SecureString --overwrite --value <key>`.
14. **Google AI Studio spend cap** on the Gemini key(s).
15. **Old ultra0-account cleanup** (account 912442530845): early-M0 smachnogo resources still exist there (DDB table, S3 bucket incl. tfstate bucket, SQS queues). Say the word and I'll tear them down — I didn't delete anything in that account without your explicit OK.

## ⏭ Deferred / nice-to-have

- `api.smachnogo.app` custom domain → after DNS exists, add ACM cert + API GW domain mapping in Terraform, then flip `Release.xcconfig` back to the domain.
- Lambda reserved concurrency: restore worker cap to 3–5 after the new account's quota increase (currently unlimited-within-account-limit-10; request bump via Service Quotas).
- Anthropic provider re-enable: uncomment the blank imports in both function mains when Anton has Claude API keys (M7 bake-off wants Opus/Sonnet in the eval).

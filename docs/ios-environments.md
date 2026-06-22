# iOS environments & build/release runbook

How the iOS app maps to backends, and how to cut a TestFlight (dev) or App Store
(prod) build. Both backends run on **AWS account `920071567477`** (profile
`smachnogo`); the iOS app picks which one purely via its build configuration.

## Environment matrix

The app reads its backend endpoint from `Info.plist` keys (`APIBaseURL`,
`CognitoClientID`, …), which `project.yml` populates from `$(API_BASE_URL)` etc.
Those come from the per-configuration xcconfig in `ios/Configs/`:

| Build config | xcconfig | Backend | API base | Cognito client / pool | Used for |
|---|---|---|---|---|---|
| **Debug** | `Debug.xcconfig` (+ gitignored `Secrets.xcconfig`) | local | `http://localhost:8090` | dev client `7oki1ajf…` + `dev-user-1` creds | Simulator / local dev (run a backend locally) |
| **Beta** | `Beta.xcconfig` | **AWS dev** | `https://dy9kj15vph.execute-api.us-east-1.amazonaws.com` | `7oki1ajf3fh3v6j3avacim3g8f` / `us-east-1_qiw4xv2NU` | **TestFlight** (test against real AWS dev) |
| **Release** | `Release.xcconfig` | **AWS prod** | `https://b37hzro1uk.execute-api.us-east-1.amazonaws.com` | `48fo7072sj31ul2mo25vop8j3h` / `us-east-1_6pYHqbzBO` | **App Store** |

These IDs are the terraform outputs of `backend/terraform/envs/{dev,prod}`
(`terraform output api_url cognito_client_id cognito_pool_id`). They are **not
secrets** — they ship inside the app's Info.plist regardless. Beta uses the same
per-install Cognito auth as Release (no `dev-user` creds), so a TestFlight tester
signs in exactly like a real user, but against the **dev** user pool.

> ⚠️ **Footgun:** the `Smachnogo` scheme's **Archive** action is wired to
> **Release (prod)**. So **Xcode GUI → Product ▸ Archive produces a PROD build.**
> A dev TestFlight build must be archived from the CLI with `-configuration Beta`
> (runbook below). Don't assume "TestFlight = test = dev" — by default it's prod.

## The Apple "agreement" gate (what blocks what)

The unsigned **Paid Applications Agreement** (ASC ▸ Business / Agreements, Tax &
Banking) gates **App Store distribution and in-app purchases / subscriptions** —
i.e. **prod / public launch**. It does **NOT** gate **TestFlight**. TestFlight
builds (the Beta config above) upload and install fine today; that's how builds
3–8 shipped. So: **dev + TestFlight now; prod/App Store after the agreement is
signed** (plus the rest of `appstore-readiness.md`).

## Prerequisites (already in place)

- Tooling: `xcodegen` (project.yml is the source of truth), `xcodebuild`,
  `xcrun altool`. `fastlane` is **not** installed/used for these manual builds.
- Signing: Apple team `CP598M5SUG`; the Apple Distribution cert is in the login
  keychain (from prior Xcode archives). `CODE_SIGN_STYLE: Automatic` +
  `-allowProvisioningUpdates` lets xcodebuild fetch/create the profile.
- App Store Connect: app record **6779397731**, bundle id `app.smachnogo.ios`.
  ASC API key **`AuthKey_2XBLARXMH2.p8`** (Admin) in `backend/secrets/`; key id
  `2XBLARXMH2` and `ASC_ISSUER_ID` are in `backend/secrets/dev.env`.

## Runbook — ship a TestFlight build against AWS dev

```bash
cd ios
# 1. Bump the build number in project.yml (CURRENT_PROJECT_VERSION) — must be
#    higher than any build already on TestFlight for version 1.0. Then:
xcodegen generate

# 2. Archive the **Beta** config (→ AWS dev). source the ASC key for signing.
set -a; . ../backend/secrets/dev.env; set +a   # ASC_KEY_ID, ASC_ISSUER_ID
xcodebuild archive \
  -project Smachnogo.xcodeproj -scheme Smachnogo -configuration Beta \
  -archivePath build/Smachnogo.xcarchive \
  -allowProvisioningUpdates \
  -authenticationKeyPath "$PWD/../backend/secrets/AuthKey_${ASC_KEY_ID}.p8" \
  -authenticationKeyID "$ASC_KEY_ID" -authenticationKeyIssuerID "$ASC_ISSUER_ID"

# 3. Export an App Store ipa.
cat > build/ExportOptions.plist <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>method</key><string>app-store</string>
  <key>teamID</key><string>CP598M5SUG</string>
</dict></plist>
PLIST
xcodebuild -exportArchive -archivePath build/Smachnogo.xcarchive \
  -exportPath build/export -exportOptionsPlist build/ExportOptions.plist \
  -allowProvisioningUpdates \
  -authenticationKeyPath "$PWD/../backend/secrets/AuthKey_${ASC_KEY_ID}.p8" \
  -authenticationKeyID "$ASC_KEY_ID" -authenticationKeyIssuerID "$ASC_ISSUER_ID"

# 4. Upload to TestFlight.
xcrun altool --upload-app -f build/export/Smachnogo.ipa --type ios \
  --apiKey "$ASC_KEY_ID" --apiIssuer "$ASC_ISSUER_ID"
```

The build appears in ASC ▸ TestFlight after processing (a few minutes). Add it to
an internal testing group to install via the TestFlight app.

> First CLI archive on a machine may prompt the keychain for codesign access to
> the private key — click **Always Allow** (or run `security unlock-keychain`).
> If signing fails headless, archive once via Xcode GUI to prime the keychain.

## What you're testing against on dev (caveats)

- **Free tier is effectively unlimited on dev:** `entitlement_mode = "enforce"`
  but dev caps are `free_scan_allowance = 1000`, `free_window_days = 3650`,
  `daily_scan_cap = 20`. Good for exercising the scan flow without hitting a
  paywall. (Prod launch values are 10 / 7 / 20 — see `appstore-readiness.md` /
  the prod LAUNCH-GATE.)
- **In-app purchases may not work in TestFlight pre-launch:** subscriptions sit
  in "Missing Metadata" in ASC until the first App Store version ships, so
  sandbox purchase of the monthly/annual plan may be unavailable. The free tier
  and the always-free text "Describe" diary still work.
- **DeviceCheck fails open on dev** (no `.p8`), so the per-device free-scan grant
  isn't enforced — reinstalls re-grant scans. Fine for testing.

## Eventual prod / App Store build

Once the Paid Apps Agreement is signed and `appstore-readiness.md` is green:
archive the **Release** config (the default scheme archive, or `-configuration
Release`) → prod backend → submit via App Store, not TestFlight-only. This is
what the iOS-CI `release` lane (plan Phase 3) will automate; the `beta` lane will
archive `-configuration Beta` exactly as above.

# iOS 26 Bottom-Bar Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the custom two-piece bottom bar with the native iOS 26 Liquid-Glass `TabView` (Diary · Stats), move the meal-add action into a system bottom accessory, consolidate the scattered/cryptic add entry points, and declutter the Diary header.

**Architecture:** `RootTabView` becomes a native `TabView`. On iOS 26 it uses `.tabViewBottomAccessory` (auto-glass, scroll-synced via `.tabBarMinimizeBehavior(.onScrollDown)`); on iOS 17–25 it uses `.safeAreaInset(edge: .bottom)` with a `.regularMaterial` pill (the pre-26 bar doesn't float/minimize, so a pinned accessory is stable). A new `AddMealAction` binding lets the accessory (which lives outside `DayView`) drive `DayView`'s existing scan/library/describe flows after switching to the Diary tab.

**Tech Stack:** SwiftUI, iOS 17 minimum / iOS 26 enhanced, xcodegen project generation, Swift 5.9. iPhone-only, portrait-only.

---

## ⚠️ Design correction baked into this plan (review this first)

During brainstorming we agreed the camera button would open the camera directly and the alternates (Choose from Library, Describe a meal) would be "visible on the capture screen." **That is not buildable as described:** `CameraPicker.swift` wraps the *system* `UIImagePickerController` (`sourceType = .camera`), which cannot host custom links without a fragile `cameraOverlayView`.

**Resolution used here (please confirm during plan review):**
- The accessory's **primary** tap = "Scan a meal" → opens the camera directly (one-tap dominant path, unchanged).
- The alternates live in a **labeled `Menu`** on the accessory (a trailing "more" control whose items are full-text labelled: "Describe a meal", "Choose from library") — a *persistent, visible* path that survives on a populated day, with no hidden long-press.
- The **first-run empty state** also surfaces the alternates as labelled buttons for discovery.

If you'd rather the accessory tap open a source sheet (Take Photo / Library / Describe) at the cost of one extra tap on every scan, say so and Task 1/2 change accordingly.

---

## Testing Approach (read before starting)

**There is no iOS test target in this project** (`project.yml` defines only the `Smachnogo` app target; no XCTest bundle, no test scheme). The work in this plan is almost entirely SwiftUI view-layer restructuring (tab container, accessory, toolbar, empty-state copy) for which unit tests provide little value and would require standing up a new test target — scope the user did not request.

**Therefore verification for every task is: the project builds with zero errors, then the change is confirmed by running it in the simulator and observing the specified behavior.** This matches the project's actual workflow (xcodegen + xcodebuild + simulator) and the user's "rebuild, restart, test" instruction.

Helper for booting/installing/launching/screenshotting the simulator: use the **ios-simulator-skill** scripts (semantic UI navigation, screenshots). Concrete build gate per task:

```bash
cd /Users/anton/smachnogo/ios
xcodegen generate                                    # only needed when files are ADDED/REMOVED
xcodebuild -project Smachnogo.xcodeproj -scheme Smachnogo \
  -destination 'platform=iOS Simulator,name=iPhone 16,OS=latest' \
  -configuration Debug build 2>&1 | tail -25
# Expected: ** BUILD SUCCEEDED **
```

Find available simulators (you need BOTH an iOS 26 device for the glass path and an iOS 17/18 device for the fallback path) with:

```bash
xcrun simctl list devices available | grep -Ei 'iPhone|iOS'
```

Adjust the `name=`/`OS=` in the destination to devices you actually have.

---

## File Structure

**Create:**
- `ios/Smachnogo/App/ScanAccessory.swift` — `AddMealAction` enum + `ScanAccessory` view (the accessory's content: primary "Scan a meal" button + labelled "more" menu). Shared by the iOS 26 accessory and the 17–25 fallback.
- `ios/Smachnogo/App/SettingsButton.swift` — small reusable gear button + Settings sheet, used by both Diary and Stats toolbars (so Settings is reachable from both tabs).

**Modify:**
- `ios/Smachnogo/App/RootTabView.swift` — full rewrite to native `TabView` + accessory/fallback + `AddMealAction` routing.
- `ios/Smachnogo/Features/Diary/DayView.swift` — `scanRequests: Int` binding → `addAction: AddMealAction?` binding + routing; remove library/manual-entry toolbar buttons; remove duplicate nav title; state-aware empty state; remove the manual `contentMargins(.bottom, 88)`; use `SettingsButton`.
- `ios/Smachnogo/Features/Stats/StatsView.swift` — add a `SettingsButton` to the toolbar; remove the manual `contentMargins(.bottom, 88)`.

**Unchanged:** `SmachnogoApp.swift` (still instantiates `RootTabView()`), `CameraPicker.swift`, `ManualEntrySheet.swift`, `ScanFlowView.swift`, `SettingsView.swift`, `CalendarStrip.swift`, `PendingScanQueue`, all services.

---

## Task 1: Shared add-action components

Create the action type and the accessory content view. Standalone — compiles without touching anything else yet.

**Files:**
- Create: `ios/Smachnogo/App/ScanAccessory.swift`
- Create: `ios/Smachnogo/App/SettingsButton.swift`

- [ ] **Step 1: Create `ScanAccessory.swift`**

```swift
import SwiftUI

/// A meal-add method requested from OUTSIDE DayView (the bottom-bar
/// accessory). DayView observes it, routes to the matching flow, then
/// clears it so the same action can fire again.
enum AddMealAction: Equatable {
    case camera, library, describe
}

/// Bottom-bar accessory content: a primary "Scan a meal" button (opens the
/// live camera) plus a compact, fully-labelled menu for the no-photo
/// methods. Shared by the iOS 26 `tabViewBottomAccessory` and the iOS 17–25
/// pinned fallback. Glass/material is supplied by the host (system on 26,
/// the fallback wrapper on 17–25) — this view stays plain.
struct ScanAccessory: View {
    let onAction: (AddMealAction) -> Void

    var body: some View {
        HStack(spacing: 8) {
            Button { onAction(.camera) } label: {
                Label("Scan a meal", systemImage: "camera.fill")
                    .font(.headline)
                    .frame(maxWidth: .infinity, minHeight: 36)
            }
            .accessibilityLabel("Scan a meal with the camera")

            Menu {
                Button { onAction(.describe) } label: {
                    Label("Describe a meal", systemImage: "square.and.pencil")
                }
                Button { onAction(.library) } label: {
                    Label("Choose from library", systemImage: "photo.on.rectangle")
                }
            } label: {
                Image(systemName: "ellipsis")
                    .font(.headline)
                    .frame(width: 40, height: 36)
            }
            .accessibilityLabel("More ways to add a meal")
        }
    }
}
```

- [ ] **Step 2: Create `SettingsButton.swift`**

```swift
import SwiftUI

/// Gear button + Settings sheet, reused by the Diary and Stats toolbars so
/// Settings is reachable from both tabs with identical placement.
struct SettingsButton: View {
    /// Called after the Settings sheet reports a data change (e.g. a wipe),
    /// so the host tab can refresh.
    let onDataChanged: () -> Void
    @State private var show = false

    var body: some View {
        Button { show = true } label: {
            Image(systemName: "gearshape")
        }
        .accessibilityLabel("Settings")
        .sheet(isPresented: $show) {
            SettingsView(onDataChanged: onDataChanged)
        }
    }
}
```

- [ ] **Step 3: Regenerate the project and build**

```bash
cd /Users/anton/smachnogo/ios
xcodegen generate
xcodebuild -project Smachnogo.xcodeproj -scheme Smachnogo \
  -destination 'platform=iOS Simulator,name=iPhone 16,OS=latest' \
  -configuration Debug build 2>&1 | tail -25
```
Expected: `** BUILD SUCCEEDED **`. (`ScanAccessory`/`SettingsButton` are defined but not yet referenced — that's fine.)

- [ ] **Step 4: Commit**

```bash
cd /Users/anton/smachnogo
git add ios/Smachnogo/App/ScanAccessory.swift ios/Smachnogo/App/SettingsButton.swift ios/Smachnogo.xcodeproj/project.pbxproj
git commit -m "ios: add ScanAccessory + SettingsButton bottom-bar components"
```

---

## Task 2: Migrate RootTabView to native TabView + accessory, and switch DayView's interface

This task is atomic: the `RootTabView` ↔ `DayView` interface changes together so the app keeps compiling. After it, the new bar works end-to-end. DayView's own (now-redundant) toolbar add buttons are left in place here and removed in Task 3.

**Files:**
- Modify: `ios/Smachnogo/App/RootTabView.swift` (full rewrite)
- Modify: `ios/Smachnogo/Features/Diary/DayView.swift:9-10` (binding), `:62-70` (tasks/onChange)

- [ ] **Step 1: Rewrite `RootTabView.swift`**

Replace the entire file with:

```swift
import SwiftUI

/// The app shell: a native TabView (Diary · Stats) so it renders the iOS 26
/// Liquid-Glass bar for free and degrades to the standard bar on 17–25. The
/// meal-add action lives in a bottom accessory (system-synced on 26, a
/// pinned material pill on 17–25). Because the accessory sits outside
/// DayView — which owns the scan/queue flow — taps switch to the Diary tab
/// and hand DayView an AddMealAction to perform.
struct RootTabView: View {
    enum Tab: Hashable { case diary, stats }

    @State private var selected: Tab = .diary
    @State private var addAction: AddMealAction?

    var body: some View {
        if #available(iOS 26, *) {
            glassTabView
        } else {
            fallbackTabView
        }
    }

    @available(iOS 26, *)
    private var glassTabView: some View {
        TabView(selection: $selected) {
            Tab("Diary", systemImage: "book", value: Tab.diary) {
                DayView(addAction: $addAction)
            }
            Tab("Stats", systemImage: "chart.bar", value: Tab.stats) {
                StatsView()
            }
        }
        .tabBarMinimizeBehavior(.onScrollDown)
        .tabViewBottomAccessory {
            ScanAccessory { trigger($0) }
        }
    }

    private var fallbackTabView: some View {
        TabView(selection: $selected) {
            DayView(addAction: $addAction)
                .tabItem { Label("Diary", systemImage: "book") }
                .tag(Tab.diary)
            StatsView()
                .tabItem { Label("Stats", systemImage: "chart.bar") }
                .tag(Tab.stats)
        }
        .safeAreaInset(edge: .bottom) {
            ScanAccessory { trigger($0) }
                .padding(.vertical, 8)
                .padding(.horizontal, 14)
                .background(.regularMaterial, in: Capsule())
                .shadow(color: .black.opacity(0.15), radius: 8, y: 3)
                .padding(.horizontal, 24)
                .padding(.bottom, 4)
        }
    }

    /// Adds always route through the Diary tab (it owns the scan/queue flow).
    private func trigger(_ action: AddMealAction) {
        selected = .diary
        addAction = action
    }
}

#Preview {
    RootTabView()
}
```

- [ ] **Step 2: Change DayView's binding** — `DayView.swift:9-10`

Replace:
```swift
    /// Bumped by the floating Scan button (RootTabView) — opens the camera.
    @Binding var scanRequests: Int
```
with:
```swift
    /// Set by the bottom-bar accessory (RootTabView) to drive an add flow;
    /// DayView performs it and clears it back to nil.
    @Binding var addAction: AddMealAction?
```

- [ ] **Step 3: Replace the scanRequests reaction with action routing** — `DayView.swift:70`

Replace:
```swift
        .onChange(of: scanRequests) { _, _ in openCamera() }
```
with:
```swift
        .onChange(of: addAction) { _, action in
            guard let action else { return }
            switch action {
            case .camera: openCamera()
            case .library: showLibrary = true
            case .describe: showManualEntry = true
            }
            addAction = nil
        }
```

- [ ] **Step 4: Build**

```bash
cd /Users/anton/smachnogo/ios
xcodebuild -project Smachnogo.xcodeproj -scheme Smachnogo \
  -destination 'platform=iOS Simulator,name=iPhone 16,OS=latest' \
  -configuration Debug build 2>&1 | tail -25
```
Expected: `** BUILD SUCCEEDED **`.

- [ ] **Step 5: Verify in the simulator (iOS 26 glass path)**

Boot an **iOS 26** simulator, install, launch (ios-simulator-skill scripts). Confirm:
- A floating glass tab bar shows **Diary** and **Stats**; switching tabs works.
- A **"Scan a meal"** accessory sits above the bar; tapping it opens the camera (or, in the simulator with no camera, falls through to the photo library — that path is in `openCamera()`).
- The accessory's **"…" menu** shows "Describe a meal" and "Choose from library", and each opens the right sheet.
- Scrolling the Diary list down **minimizes** the tab bar and the accessory collapses inline with it; scrolling up restores them.

- [ ] **Step 6: Commit**

```bash
cd /Users/anton/smachnogo
git add ios/Smachnogo/App/RootTabView.swift ios/Smachnogo/Features/Diary/DayView.swift
git commit -m "ios: native TabView + scan bottom accessory (glass on 26, pinned fallback on 17-25)"
```

---

## Task 3: Declutter the Diary header

Remove the now-redundant add buttons (the accessory owns adding), drop the duplicate "Today" title, switch the gear to `SettingsButton`, and drop the manual bottom inset (the system manages it now).

**Files:**
- Modify: `ios/Smachnogo/Features/Diary/DayView.swift:46-60` (toolbar + title), `:109-116` (settings sheet), `:129-131` (navTitle), `:190-191` (contentMargins), `:23` (showSettings state)

- [ ] **Step 1: Remove the navigation title** — `DayView.swift:46`

Delete this line (the `CalendarStrip` already shows the date, so the inline "Today" was a duplicate):
```swift
            .navigationTitle(navTitle)
```
Keep the existing `.navigationBarTitleDisplayMode(.inline)` line directly below it.

- [ ] **Step 2: Replace the toolbar** — `DayView.swift:48-60`

Replace the whole `.toolbar { … }` block:
```swift
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button { showSettings = true } label: { Image(systemName: "gearshape") }
                        .accessibilityLabel("Settings")
                }
                ToolbarItemGroup(placement: .topBarTrailing) {
                    Button { showManualEntry = true } label: { Image(systemName: "square.and.pencil") }
                        .accessibilityLabel("Describe a meal")
                    Button { showLibrary = true } label: { Image(systemName: "photo.on.rectangle") }
                        .accessibilityLabel("Scan from photo library")
                    // Camera lives on the floating Scan button (RootTabView).
                }
            }
```
with (gear only — describe/library now live on the accessory):
```swift
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    SettingsButton {
                        Task {
                            await load()
                            await store.refreshServerState()
                        }
                    }
                }
            }
```

- [ ] **Step 3: Remove the old Settings sheet and its state**

Delete the `showSettings` state declaration — `DayView.swift:23`:
```swift
    @State private var showSettings = false
```
Delete the now-unused Settings sheet modifier — `DayView.swift:109-116`:
```swift
        .sheet(isPresented: $showSettings) {
            SettingsView(onDataChanged: {
                Task {
                    await load()
                    await store.refreshServerState()
                }
            })
        }
```

- [ ] **Step 4: Remove the unused `navTitle`** — `DayView.swift:129-131`

Delete:
```swift
    private var navTitle: String {
        Calendar.current.isDateInToday(selectedDate) ? "Today" : selectedDate.formatted(.dateTime.day().month())
    }
```

- [ ] **Step 5: Remove the manual bottom inset** — `DayView.swift:191`

In the `List { … }` modifiers, delete this line (the native bar + accessory now inset content automatically):
```swift
            .contentMargins(.bottom, 88, for: .scrollContent)
```

- [ ] **Step 6: Build**

```bash
cd /Users/anton/smachnogo/ios
xcodebuild -project Smachnogo.xcodeproj -scheme Smachnogo \
  -destination 'platform=iOS Simulator,name=iPhone 16,OS=latest' \
  -configuration Debug build 2>&1 | tail -25
```
Expected: `** BUILD SUCCEEDED **` (no "unused variable"/"cannot find" errors for `showSettings`, `navTitle`).

- [ ] **Step 7: Verify in the simulator**

Confirm the Diary header now shows only the **gear (top-left)** and the **`CalendarStrip` date** — no second "Today", no pencil/photo glyphs. The gear opens Settings. With meals present, the list scrolls fully clear of the accessory (no content hidden behind it).

- [ ] **Step 8: Commit**

```bash
cd /Users/anton/smachnogo
git add ios/Smachnogo/Features/Diary/DayView.swift
git commit -m "ios: declutter Diary header — drop duplicate Today, move add methods to accessory"
```

---

## Task 4: State-aware empty state

Distinguish a genuine first-run user ("Add your first meal", with method teaching) from a returning user on an empty day ("Nothing logged yet", calm). Uses a persisted flag.

**Files:**
- Modify: `ios/Smachnogo/Features/Diary/DayView.swift:25-27` (add `@AppStorage`), `:241-250` (set flag in `load()`), `:252-281` (rewrite `emptyState`)

- [ ] **Step 1: Add the persisted "ever logged" flag** — near the other `@State` in `DayView`, after `DayView.swift:27`

Add:
```swift
    /// True once the user has ever had a logged meal — separates a brand-new
    /// user (teach the methods) from a veteran viewing an empty day (stay calm).
    @AppStorage("hasLoggedAnyMeal") private var hasLoggedAnyMeal = false
```

- [ ] **Step 2: Set the flag when meals load** — in `load()`, `DayView.swift:244-246`

Replace:
```swift
        do {
            meals = try await mealService.meals(on: dayKey)
            loadError = nil
```
with:
```swift
        do {
            meals = try await mealService.meals(on: dayKey)
            if !meals.isEmpty { hasLoggedAnyMeal = true }
            loadError = nil
```

- [ ] **Step 3: Rewrite `emptyState`** — `DayView.swift:252-281`

Replace the whole `emptyState` computed property with:
```swift
    private var emptyState: some View {
        VStack(spacing: 16) {
            Spacer()
            Image(systemName: isFutureDay ? "calendar.badge.plus" : "camera.viewfinder")
                .font(.system(size: 56))
                .foregroundStyle(.secondary)
            Text(emptyTitle)
                .font(.title3.weight(.semibold))
            Text(emptyBody)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 32)
            // First-run only: surface the non-obvious input methods (they
            // otherwise live behind the accessory's "…"). Scanning itself is
            // the prominent "Scan a meal" accessory below — no duplicate
            // primary button here (avoids the very stacked-CTA redundancy
            // this redesign set out to remove).
            if !isFutureDay && !hasLoggedAnyMeal {
                HStack(spacing: 20) {
                    Button { showManualEntry = true } label: {
                        Label("Describe", systemImage: "square.and.pencil")
                    }
                    Button { showLibrary = true } label: {
                        Label("Choose photo", systemImage: "photo.on.rectangle")
                    }
                }
                .font(.subheadline)
                .padding(.top, 4)
            }
            if let loadError {
                Text(loadError).font(.footnote).foregroundStyle(.red).padding(.horizontal)
            }
            Spacer()
            Spacer()
        }
    }

    private var emptyTitle: String {
        if isFutureDay { return "Nothing planned yet" }
        return hasLoggedAnyMeal ? "Nothing logged yet" : "Add your first meal"
    }

    private var emptyBody: String {
        if isFutureDay {
            return "Planning ahead? Scan or describe a meal and pick this date when saving."
        }
        if hasLoggedAnyMeal {
            return "Tap Scan a meal below to log something for today."
        }
        return "Tap Scan a meal below — point the camera at your plate and calories, macros and nutrition appear in seconds.\n\nTip: for packaged food, include the label in the shot."
    }
```

- [ ] **Step 4: Build**

```bash
cd /Users/anton/smachnogo/ios
xcodebuild -project Smachnogo.xcodeproj -scheme Smachnogo \
  -destination 'platform=iOS Simulator,name=iPhone 16,OS=latest' \
  -configuration Debug build 2>&1 | tail -25
```
Expected: `** BUILD SUCCEEDED **`.

- [ ] **Step 5: Verify both empty-state variants in the simulator**

- **First run:** fresh install (or reset: `xcrun simctl uninstall booted app.smachnogo.ios` then reinstall). Empty Diary shows **"Add your first meal"** + the value/tip copy + the **Describe / Choose photo** buttons (scanning is the prominent "Scan a meal" accessory below — intentionally NOT duplicated as a body button).
- **Returning, empty day:** log a meal (so `hasLoggedAnyMeal` flips), then navigate to a different empty day with the `CalendarStrip` chevrons. It should read **"Nothing logged yet"** + "Use Scan a meal below…" with **no** teaching buttons.

- [ ] **Step 6: Commit**

```bash
cd /Users/anton/smachnogo
git add ios/Smachnogo/Features/Diary/DayView.swift
git commit -m "ios: state-aware Diary empty state (first-run teaching vs calm empty-day)"
```

---

## Task 5: Settings reachable from Stats; drop Stats' manual inset

`StatsView` is now a peer tab, so it needs its own Settings entry point. Reuse `SettingsButton`.

**Files:**
- Modify: `ios/Smachnogo/Features/Stats/StatsView.swift:67` (contentMargins), `:68` (add toolbar)

- [ ] **Step 1: Remove the manual bottom inset** — `StatsView.swift:67`

Delete:
```swift
            .contentMargins(.bottom, 88, for: .scrollContent)
```

- [ ] **Step 2: Add the Settings toolbar button** — after `.navigationTitle("Stats")`, `StatsView.swift:68`

Change:
```swift
            .navigationTitle("Stats")
            }
```
to:
```swift
            .navigationTitle("Stats")
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    SettingsButton { Task { await load() } }
                }
            }
            }
```
(The `.toolbar` attaches to the `ScrollView` inside the `NavigationStack`, alongside the existing `.navigationTitle` and `.contentMargins` modifiers — place it among them, before the closing brace of the `NavigationStack`'s content.)

- [ ] **Step 3: Build**

```bash
cd /Users/anton/smachnogo/ios
xcodebuild -project Smachnogo.xcodeproj -scheme Smachnogo \
  -destination 'platform=iOS Simulator,name=iPhone 16,OS=latest' \
  -configuration Debug build 2>&1 | tail -25
```
Expected: `** BUILD SUCCEEDED **`.

- [ ] **Step 4: Verify in the simulator**

On the **Stats** tab, a gear appears top-left and opens Settings. Stats content scrolls clear of the accessory.

- [ ] **Step 5: Commit**

```bash
cd /Users/anton/smachnogo
git add ios/Smachnogo/Features/Stats/StatsView.swift
git commit -m "ios: Settings reachable from Stats tab; drop manual bottom inset"
```

---

## Task 6: Cross-OS verification & polish

No new code unless a defect is found — this task confirms both rendering paths and the full add-flow.

- [ ] **Step 1: iOS 26 glass path** — on an iOS 26 simulator, confirm: floating glass bar, accessory glass, scroll-minimize sync, all three add methods (camera/describe/library) complete a save and the new meal appears in the Diary list.

- [ ] **Step 2: iOS 17/18 fallback path** — on an iOS 17 or 18 simulator, confirm: standard tab bar with a pinned `.regularMaterial` "Scan a meal" pill above it (stable, no overlap with content or the home indicator), all three add methods work, tab switching works, Settings reachable from both tabs.

- [ ] **Step 3: Add-from-Stats** — while on the **Stats** tab, tap the accessory's "Scan a meal" and each "…" menu item; confirm the app switches to **Diary** and performs the action (this exercises the `trigger()` → tab switch → `addAction` routing).

- [ ] **Step 4: Regression sweep** — confirm the scans-remaining chip still appears/opens the paywall, swipe-to-"Log again" still works on meal rows, the `CalendarStrip` month-grid still opens, and pending/in-progress scans still show in the "In progress" section.

- [ ] **Step 5: Final commit (if any fixes were made)**

```bash
cd /Users/anton/smachnogo
git add ios/Smachnogo/App/ScanAccessory.swift ios/Smachnogo/App/SettingsButton.swift \
  ios/Smachnogo/App/RootTabView.swift ios/Smachnogo/Features/Diary/DayView.swift \
  ios/Smachnogo/Features/Stats/StatsView.swift ios/Smachnogo.xcodeproj/project.pbxproj
git commit -m "ios: bottom-bar redesign — cross-OS verification fixes"
```
(Scoped add — never `git add -A` here: the working tree carries ~54 unrelated WIP files that must stay uncommitted.)

---

## Self-Review

**Spec coverage:**
- Native TabView, Diary + Stats → Task 2. ✓
- Liquid Glass on 26 / standard on 17–25 → Task 2 (`#available` split). ✓
- Camera as single prominent action in a bottom accessory → Tasks 1–2. ✓
- Scroll-synced accessory → Task 2 (`.tabBarMinimizeBehavior` + `.tabViewBottomAccessory`). ✓
- Add-methods discoverable without hidden gestures → Task 1 (labelled `Menu`) + Task 4 (first-run buttons). ✓ (Replaces the infeasible "alternates on the system-camera screen" — flagged at top.)
- Declutter Diary header / kill duplicate "Today" → Task 3. ✓
- Fix "Scan your first meal" copy (first-run vs empty-day) → Task 4. ✓
- Settings stays a gear, reachable from both tabs → Tasks 1 (`SettingsButton`), 3, 5. ✓
- Stats untouched functionally; Settings added → Task 5. ✓
- Deferred (NOT in this plan): empty-state preview card, daily-goal/Me hub. ✓ (intentional)

**Placeholder scan:** No TBD/TODO; every code step shows complete code; all commands have expected output. ✓

**Type consistency:** `AddMealAction` (`.camera/.library/.describe`) defined in Task 1, consumed identically in Task 2's `onChange` and `RootTabView.trigger`. `RootTabView.Tab` (`.diary/.stats`) used consistently for selection + tags. `SettingsButton(onDataChanged:)` signature matches both call sites (Tasks 3, 5). `DayView(addAction:)` binding matches both `TabView` paths. ✓

**Known risk to watch during execution:** if a fresh launch lands on Stats before DayView is instantiated, an accessory action would set `addAction` before DayView's `onChange` is registered. Mitigated because Diary is the default `selected` tab (DayView instantiated at launch). If a defect appears, hoist the scan flow to `RootTabView`; noted for Task 6.

import SwiftUI
import PhotosUI
import AVFoundation

/// The diary: navigate any day (strip ±1, month-grid picker), see logged +
/// planned meals, tap to edit, scan via camera/library. The empty state IS
/// the onboarding.
struct DayView: View {
    /// Set by the bottom-bar accessory (RootTabView) to drive an add flow;
    /// DayView performs it and clears it back to nil.
    @Binding var addAction: AddMealAction?

    @State private var selectedDate = Date()
    @State private var meals: [Meal] = []
    @State private var loading = false
    @State private var loadError: String?

    @State private var showCamera = false
    @State private var cameraDenied = false
    @State private var showLibrary = false
    @State private var photoItem: PhotosPickerItem?
    @State private var activeScan: ActiveScan?
    @State private var editingMeal: Meal?
    @State private var showManualEntry = false
    @State private var queue = PendingScanQueue.shared
    @State private var store = StoreService.shared
    @State private var showPaywall = false
    @State private var showLimits = false

    /// Drives `.sensoryFeedback` for diary actions (iOS 17). Bumped with a
    /// concrete feedback each time something log-worthy happens.
    @State private var feedback: DiaryFeedback?
    /// Transient "Added to today" toast — set when a Log-again lands on today
    /// while another day is on screen (the action's result is otherwise
    /// off-screen, so there'd be no confirmation).
    @State private var addedToTodayToken = 0
    /// Meal waiting for swipe-to-delete confirmation. Set by the trailing
    /// swipe action; cleared on confirm (deletes) or cancel.
    @State private var mealPendingDelete: Meal?

    /// One per haptic kind; `id` changes every fire so repeats re-trigger.
    private struct DiaryFeedback: Equatable {
        enum Kind { case success, warning }
        let kind: Kind
        let id: Int
    }

    /// True once the user has ever had a logged meal — separates a brand-new
    /// user (surface the methods) from a veteran viewing an empty day (calm).
    @AppStorage("hasLoggedAnyMeal") private var hasLoggedAnyMeal = false

    struct ActiveScan: Identifiable {
        let scanId: String
        let image: UIImage
        var id: String { scanId }
    }

    private let mealService = MealService()
    private var dayKey: String { DateUtil.dayString(selectedDate) }
    private var isFutureDay: Bool { dayKey > DateUtil.dayString() }

    var body: some View {
        NavigationStack {
            VStack(spacing: 8) {
                CalendarStrip(selectedDate: $selectedDate)
                scansRemainingChip
                content
            }
            .overlay(alignment: .bottom) { addedToTodayToast }
            .navigationBarTitleDisplayMode(.inline)
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
        }
        .task(id: dayKey) { await load() }
        .task {
            queue.resumeAll()
            await store.refreshServerState()
            // Subscription activated elsewhere (restore on another device,
            // webhook) — un-park photos that were waiting on the paywall.
            if store.isSubscribed { queue.retryPaywalled() }
        }
        .onChange(of: addAction) { _, _ in consumeAddAction() }
        .onAppear { consumeAddAction() }
        .photosPicker(isPresented: $showLibrary, selection: $photoItem, matching: .images)
        .sheet(isPresented: $showPaywall) {
            PaywallView(reason: store.me.flatMap { $0.scansRemaining <= 0 ? "scans_exhausted" : nil })
        }
        .onChange(of: photoItem) { _, item in
            guard let item else { return }
            Task {
                if let data = try? await item.loadTransferable(type: Data.self),
                   let image = UIImage(data: data) {
                    // EXIF creation date (read BEFORE the compressor strips
                    // metadata) prefills the confirm sheet's date — the
                    // "photographed at lunch, logging at night" flow.
                    startScan(image, suggestedDate: Self.exifCreationDate(data))
                }
                photoItem = nil
            }
        }
        .fullScreenCover(isPresented: $showCamera) {
            CameraPicker { image in
                startScan(image, suggestedDate: nil) // live camera = now
            }
            .ignoresSafeArea()
        }
        .sheet(item: $activeScan) { scan in
            ScanFlowView(scanId: scan.scanId, image: scan.image) { _ in
                fire(.success) // dishes saved from a scan
                Task { await load() }
            }
        }
        .sheet(isPresented: $showManualEntry) {
            ManualEntrySheet {
                fire(.success) // a described meal was added
                Task { await load() }
            }
        }
        .sheet(item: $editingMeal) { meal in
            MealEditSheet(meal: meal) {
                fire(.success) // a meal was saved/edited; confirm on return
                Task { await load() }
            }
        }
        .sheet(isPresented: $showLimits) {
            LimitsEditorSheet {
                Task { await store.refreshServerState() } // recolor the goal card
            }
        }
        .sensoryFeedback(trigger: feedback) { _, new in
            switch new?.kind {
            case .success: return .success
            case .warning: return .warning
            case .none: return nil
            }
        }
        .alert("Camera access is off", isPresented: $cameraDenied) {
            Button("Open Settings") {
                if let url = URL(string: UIApplication.openSettingsURLString) {
                    UIApplication.shared.open(url)
                }
            }
            Button("Use photo library instead", role: .cancel) {}
        } message: {
            Text("Enable camera access in Settings, or pick a photo from your library — scanning works either way.")
        }
    }

    /// Free-tier camera allowance, always visible while it's the scarce
    /// resource. Tapping opens the paywall — the proactive moment, no
    /// probing-by-scanning needed.
    @ViewBuilder
    private var scansRemainingChip: some View {
        if !store.isSubscribed, let me = store.me, !me.subscribed {
            Button { showPaywall = true } label: {
                HStack(spacing: 6) {
                    Image(systemName: "camera")
                    Text(me.scansRemaining > 0
                         ? "\(me.scansRemaining) free scan\(me.scansRemaining == 1 ? "" : "s") left"
                         : "Free scans used — go Premium")
                    Image(systemName: "chevron.right").font(.caption2)
                }
                .font(.footnote.weight(.medium))
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
                .background(Capsule().fill(me.scansRemaining > 0 ? AnyShapeStyle(.quaternary) : AnyShapeStyle(.tint.opacity(0.15))))
            }
            .buttonStyle(.plain)
        }
    }

    @ViewBuilder
    private var content: some View {
        let logged = meals.filter { $0.state == "logged" }
        let planned = meals.filter { $0.state == "planned" }
        let isToday = Calendar.current.isDateInToday(selectedDate)
        let hasInProgress = isToday && !queue.entries.isEmpty
        // Day-switch shows a skeleton instead of blanking — keeps the layout
        // stable and lets real content cross-fade in. (When today already has
        // in-progress scans there's a real List to show, so no skeleton.)
        if loading && meals.isEmpty && !hasInProgress {
            skeletonList
                .overlay(alignment: .top) { errorBanner }
        } else if meals.isEmpty && !hasInProgress {
            emptyState
                .overlay(alignment: .top) { errorBanner }
        } else {
            List {
                if isToday && !queue.entries.isEmpty {
                    Section("In progress") {
                        ForEach(queue.entries) { entry in
                            PendingScanRow(entry: entry) { activeScan = makeActive(entry) }
                        }
                    }
                }
                if !logged.isEmpty {
                    Section { goalCard(logged) }
                }
                if !planned.isEmpty {
                    Section("Planned") {
                        ForEach(planned) { meal in
                            mealRowButton(meal)
                        }
                    }
                }
                if !logged.isEmpty {
                    Section("Meals") {
                        ForEach(logged) { meal in
                            mealRowButton(meal)
                        }
                    }
                }
            }
            .listStyle(.insetGrouped)
            .transition(.opacity)
            .refreshable { await load() }
            // A failed pull-to-refresh on a populated day was silent (stale
            // data shown with no signal). Surface it inline.
            .overlay(alignment: .top) { errorBanner }
            .confirmationDialog(
                "Delete this meal?",
                isPresented: Binding(
                    get: { mealPendingDelete != nil },
                    set: { if !$0 { mealPendingDelete = nil } }
                ),
                titleVisibility: .visible
            ) {
                Button("Delete", role: .destructive) {
                    guard let meal = mealPendingDelete else { return }
                    mealPendingDelete = nil
                    Task {
                        do {
                            try await mealService.delete(mealId: meal.mealId, date: meal.date)
                            fire(.warning)
                            await load()
                        } catch {
                            loadError = error.localizedDescription
                        }
                    }
                }
            }
        }
    }

    /// Redacted placeholder rows shown while switching days so the screen
    /// never blanks. Static sample text gives the redaction realistic shape.
    private var skeletonList: some View {
        List {
            Section { goalCardSkeleton }
            Section("Meals") {
                ForEach(0..<4, id: \.self) { _ in
                    MealRow(meal: Self.placeholderMeal)
                }
            }
        }
        .listStyle(.insetGrouped)
        .redacted(reason: .placeholder)
        .disabled(true)
        .transition(.opacity)
    }

    /// Inline "couldn't refresh" banner. Visible regardless of whether meals
    /// are populated — a failed refresh on a populated day is otherwise mute.
    @ViewBuilder
    private var errorBanner: some View {
        if loadError != nil {
            HStack(spacing: 8) {
                Image(systemName: "wifi.exclamationmark")
                Text("Couldn't refresh — showing last saved")
                    .font(.footnote.weight(.medium))
            }
            .foregroundStyle(.white)
            .padding(.horizontal, 14)
            .padding(.vertical, 8)
            .background(Capsule().fill(.red))
            .padding(.top, 8)
            .shadow(radius: 3, y: 1)
            .transition(.move(edge: .top).combined(with: .opacity))
            .accessibilityElement(children: .combine)
        }
    }

    private func mealRowButton(_ meal: Meal) -> some View {
        Button { editingMeal = meal } label: {
            MealRow(meal: meal)
        }
        .buttonStyle(.plain)
        .swipeActions(edge: .leading) {
            Button {
                Task {
                    do {
                        _ = try await mealService.logAgainToday(meal)
                        fire(.success)
                        // The copy lands on TODAY. If the user is viewing
                        // another day the new meal is off-screen, so surface a
                        // brief "Added to today" toast as the only feedback.
                        if !Calendar.current.isDateInToday(selectedDate) {
                            withAnimation { addedToTodayToken += 1 }
                        }
                        await load()
                    } catch {
                        loadError = error.localizedDescription
                    }
                }
            } label: {
                Label("Log again", systemImage: "arrow.counterclockwise")
            }
            .tint(.green)
        }
        .swipeActions(edge: .trailing, allowsFullSwipe: false) {
            Button(role: .destructive) {
                mealPendingDelete = meal
            } label: {
                Label("Delete", systemImage: "trash")
            }
        }
    }

    /// Bump the haptic trigger so SwiftUI re-fires even on repeats.
    private func fire(_ kind: DiaryFeedback.Kind) {
        feedback = DiaryFeedback(kind: kind, id: (feedback?.id ?? 0) + 1)
    }

    /// Perform a pending add-action from the bottom-bar accessory. Driven by
    /// BOTH onChange (DayView already on-screen) and onAppear (a Stats→Diary
    /// switch can mount DayView with addAction already set, and a native
    /// TabView delivers no onChange for a value present at mount). Clearing to
    /// nil makes whichever fires second a no-op.
    private func consumeAddAction() {
        guard let action = addAction else { return }
        switch action {
        case .camera: openCamera()
        case .library: showLibrary = true
        case .describe: showManualEntry = true
        }
        addAction = nil
    }

    private func openCamera() {
        guard UIImagePickerController.isSourceTypeAvailable(.camera) else {
            showLibrary = true // simulator/no camera: photo library instead
            return
        }
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .denied, .restricted:
            cameraDenied = true
        default:
            showCamera = true
        }
    }

    /// Photo into the crash-safe queue first, THEN the live sheet over it.
    private func startScan(_ image: UIImage, suggestedDate: Date?) {
        guard let scanId = queue.enqueue(image: image, suggestedDate: suggestedDate) else { return }
        activeScan = ActiveScan(scanId: scanId, image: image)
    }

    private func makeActive(_ entry: PendingScanQueue.Entry) -> ActiveScan? {
        guard let image = queue.loadImage(entry.scanId) else {
            queue.discard(entry.scanId)
            return nil
        }
        return ActiveScan(scanId: entry.scanId, image: image)
    }

    private func load() async {
        loading = true
        defer { withAnimation(.easeInOut(duration: 0.2)) { loading = false } }
        do {
            let fetched = try await mealService.meals(on: dayKey)
            if !fetched.isEmpty { hasLoggedAnyMeal = true }
            withAnimation(.easeInOut(duration: 0.2)) {
                meals = fetched
                loadError = nil
            }
        } catch {
            // Keep whatever meals we have (stale > blank); surface the failure
            // inline via errorBanner rather than only in the empty state.
            withAnimation { loadError = error.localizedDescription }
        }
    }

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
            // primary button here.
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
            Spacer()
            Spacer()
        }
        // loadError is surfaced by the shared errorBanner overlay (so it shows
        // on populated days too) — no separate empty-state copy needed.
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

    // MARK: - Daily goal / progress card

    /// Header label for the goal card: "Today" when the selected day is today,
    /// else the medium-style date (e.g. "Jun 12, 2026").
    private var cardTitle: String {
        if Calendar.current.isDateInToday(selectedDate) { return "Today" }
        return selectedDate.formatted(date: .abbreviated, time: .omitted)
    }

    /// The user's daily limits — same source the calendar dots & Stats use.
    private var limits: [String: Double] { store.me?.limits ?? [:] }

    /// Replaces the old unlabeled totals strip: a titled summary card. When a
    /// calorie cap is set, shows consumed-vs-cap on a gauge tinted by the SAME
    /// `LimitsRule.dayStatus` as the calendar/Stats; otherwise totals + a
    /// "Set a daily goal" affordance.
    @ViewBuilder
    private func goalCard(_ logged: [Meal]) -> some View {
        let totals = logged.reduce(Nutrients.zero) { $0 + $1.nutrients }
        VStack(alignment: .leading, spacing: 12) {
            Text(cardTitle)
                .font(.headline)
            if let cap = limits["calories_kcal"], cap > 0 {
                calorieGauge(totals: totals, cap: cap)
            } else {
                noGoalSummary(totals)
            }
            macroBreakdown(totals)
        }
        .padding(.vertical, 4)
        .accessibilityElement(children: .combine)
        .accessibilityValue(accessibilityValue(totals))
    }

    /// kcal cap set → a gauge, tinted green within limit / red over, via the
    /// shared LimitsRule (a single-day bucket built from today's totals).
    @ViewBuilder
    private func calorieGauge(totals: Nutrients, cap: Double) -> some View {
        let consumed = Double(totals.caloriesKcal)
        let status = LimitsRule.dayStatus(SummaryBucket(dayTotals: totals), limits: limits)
        let tint: Color = status == .red ? .red : .green
        Gauge(value: min(consumed, cap), in: 0...cap) {
            EmptyView()
        } currentValueLabel: {
            HStack(alignment: .firstTextBaseline, spacing: 4) {
                Text("\(totals.caloriesKcal)")
                    .font(.title2.weight(.semibold))
                    .monospacedDigit()
                Text("/ \(Int(cap)) kcal")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                Spacer()
                if status == .red {
                    Text("\(totals.caloriesKcal - Int(cap)) over")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.red)
                }
            }
        }
        .gaugeStyle(.linearCapacity)
        .tint(tint)
    }

    /// No calorie cap set → calories as a plain headline plus a subtle
    /// "Set a daily goal" button that opens the limits editor.
    private func noGoalSummary(_ totals: Nutrients) -> some View {
        HStack(alignment: .firstTextBaseline) {
            HStack(alignment: .firstTextBaseline, spacing: 4) {
                Text("\(totals.caloriesKcal)")
                    .font(.title2.weight(.semibold))
                    .monospacedDigit()
                Text("kcal").font(.subheadline).foregroundStyle(.secondary)
            }
            Spacer()
            Button { showLimits = true } label: {
                Label("Set a daily goal", systemImage: "target")
                    .font(.footnote.weight(.medium))
            }
            .buttonStyle(.plain)
            .foregroundStyle(.tint)
        }
    }

    private func macroBreakdown(_ totals: Nutrients) -> some View {
        HStack {
            stat(String(format: "%.0fg", totals.proteinG), "protein")
            stat(String(format: "%.0fg", totals.fatG), "fat")
            stat(String(format: "%.0fg", totals.carbsG), "carbs")
        }
    }

    /// Combined VoiceOver value, e.g. "1,450 of 2,000 kilocalories, within
    /// goal" (or "…, over goal"), falling back to a plain total with no cap.
    private func accessibilityValue(_ totals: Nutrients) -> String {
        let kcal = totals.caloriesKcal.formatted() // locale grouping, e.g. "1,450"
        if let cap = limits["calories_kcal"], cap > 0 {
            let status = LimitsRule.dayStatus(SummaryBucket(dayTotals: totals), limits: limits)
            let verdict = status == .red ? "over goal" : "within goal"
            return "\(kcal) of \(Int(cap).formatted()) kilocalories, \(verdict)"
        }
        return "\(kcal) kilocalories, no daily goal set"
    }

    private func stat(_ value: String, _ name: String) -> some View {
        VStack {
            Text(value).font(.headline)
            Text(name).font(.caption2).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
    }

    /// Redacted stand-in for the goal card while a day is loading.
    private var goalCardSkeleton: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Today").font(.headline)
            HStack(alignment: .firstTextBaseline, spacing: 4) {
                Text("1450").font(.title2.weight(.semibold))
                Text("/ 2000 kcal").font(.subheadline)
            }
            macroBreakdown(Nutrients(caloriesKcal: 0, proteinG: 90, fatG: 60, carbsG: 180,
                                     fiberG: 0, sugarG: 0, sodiumMg: 0, saturatedFatG: 0,
                                     ironMg: 0, calciumMg: 0, omega3G: 0))
        }
        .padding(.vertical, 4)
    }

    /// Brief bottom toast confirming a Log-again landed on today while the
    /// user is viewing another day. Auto-dismisses; `addedToTodayToken` drives
    /// re-appearance on repeats.
    @ViewBuilder
    private var addedToTodayToast: some View {
        if addedToTodayToken > 0 {
            HStack(spacing: 8) {
                Image(systemName: "checkmark.circle.fill")
                Text("Added to today").font(.subheadline.weight(.medium))
            }
            .foregroundStyle(.white)
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            .background(Capsule().fill(.green))
            .shadow(radius: 4, y: 2)
            .padding(.bottom, 12)
            .transition(.move(edge: .bottom).combined(with: .opacity))
            .id(addedToTodayToken)
            .task(id: addedToTodayToken) {
                try? await Task.sleep(nanoseconds: 1_800_000_000)
                withAnimation { addedToTodayToken = 0 }
            }
            .accessibilityElement(children: .combine)
        }
    }

    /// A neutral sample meal used only to give the loading skeleton realistic
    /// row shape before redaction blurs the text.
    private static let placeholderMeal: Meal = {
        let json = """
        {"meal_id":"placeholder","date":"","state":"logged","consumed_at":"",
         "label":"Loading meal","source":"","calories_kcal":420,"protein_g":24,
         "fat_g":18,"carbs_g":40,"fiber_g":0,"sugar_g":0,"sodium_mg":0,
         "saturated_fat_g":0,"iron_mg":0,"calcium_mg":0,"omega3_g":0,
         "nutrition_score":0,"diet_quality_score":0,"portion_factor":1.0}
        """.data(using: .utf8)!
        // Force-decode is safe: the literal above is a complete, valid Meal.
        return try! JSONDecoder().decode(Meal.self, from: json)
    }()
}

/// Memberwise-style construction for the goal card / accessibility value.
/// SummaryBucket's only initializer is `init(from:)` (the synthesized
/// memberwise init is suppressed because that init lives in the main
/// declaration). Adding one in an extension restores direct construction
/// WITHOUT touching MealService — letting DayView route through the SAME
/// `LimitsRule.dayStatus` the calendar dots and Stats bars use, so the card's
/// green/red tint stays consistent with them. meal_count is 1 (a non-empty
/// day) — dayStatus treats mealCount == 0 as neutral.
private extension SummaryBucket {
    init(dayTotals: Nutrients) {
        self.key = "day"
        self.nutrients = dayTotals
        self.nutritionScore = 0
        self.dietQualityScore = 0
        self.mealCount = 1 // non-empty day; dayStatus treats 0 as neutral
        self.daysLogged = 1
    }
}

struct MealRow: View {
    let meal: Meal

    var body: some View {
        HStack(spacing: 12) {
            if let scanId = meal.scanId, let thumb = LocalPhotoStore.thumbnail(scanId: scanId) {
                Image(uiImage: thumb)
                    .resizable().scaledToFill()
                    .frame(width: 48, height: 48)
                    .clipShape(RoundedRectangle(cornerRadius: 8))
            } else {
                RoundedRectangle(cornerRadius: 8)
                    .fill(.quaternary)
                    .frame(width: 48, height: 48)
                    .overlay(Image(systemName: "fork.knife").foregroundStyle(.secondary))
            }
            VStack(alignment: .leading, spacing: 2) {
                Text(meal.label).font(.body)
                Text("\(meal.nutrients.caloriesKcal) kcal · P \(Int(meal.nutrients.proteinG)) · F \(Int(meal.nutrients.fatG)) · C \(Int(meal.nutrients.carbsG))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if meal.portionFactor != 1.0 {
                Text("×\(meal.portionFactor, specifier: "%.2g")")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            Image(systemName: "chevron.right")
                .font(.caption2)
                .foregroundStyle(.tertiary)
        }
    }
}

// Allows .sheet(item:) on a UIImage payload.
extension UIImage: @retroactive Identifiable {
    public var id: ObjectIdentifier { ObjectIdentifier(self) }
}

/// One pending-scan row: thumbnail + live step state. Tap opens the scan
/// sheet (selection, retry, or progress).
struct PendingScanRow: View {
    let entry: PendingScanQueue.Entry
    let onTap: () -> Void

    var body: some View {
        Button(action: onTap) {
            HStack(spacing: 12) {
                if let thumb = LocalPhotoStore.thumbnail(scanId: entry.scanId) {
                    Image(uiImage: thumb)
                        .resizable().scaledToFill()
                        .frame(width: 48, height: 48)
                        .clipShape(RoundedRectangle(cornerRadius: 8))
                }
                VStack(alignment: .leading, spacing: 3) {
                    Text(title).font(.body)
                    Text(subtitle).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                trailing
            }
        }
        .buttonStyle(.plain)
    }

    private var title: String {
        switch entry.step {
        case .awaitingSelection: return "Ready — pick your dishes"
        case .notFood: return "No food found"
        case .paywalled: return "Waiting for subscription"
        case .failed: return "Scan failed"
        default: return "Analyzing…"
        }
    }

    private var subtitle: String {
        switch entry.step {
        case .awaitingSelection: return "Tap to review and save"
        case .notFood: return "Tap to dismiss"
        case .paywalled: return "Your photo is saved — subscribe to scan it"
        case .failed: return entry.failureMessage ?? "Tap to retry or discard"
        default: return "Keeps going even if you close the app"
        }
    }

    @ViewBuilder
    private var trailing: some View {
        switch entry.step {
        case .awaitingSelection:
            Image(systemName: "chevron.right").font(.caption).foregroundStyle(.tertiary)
        case .failed:
            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
        case .paywalled:
            Image(systemName: "lock.fill").foregroundStyle(.tint)
        case .notFood:
            Image(systemName: "questionmark.circle").foregroundStyle(.secondary)
        default:
            ProgressView().controlSize(.small)
        }
    }
}

import ImageIO

extension DayView {
    /// EXIF DateTimeOriginal from raw photo data (library picks carry it;
    /// must run before ImageCompressor strips metadata).
    static func exifCreationDate(_ data: Data) -> Date? {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil),
              let props = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
              let exif = props[kCGImagePropertyExifDictionary] as? [CFString: Any],
              let raw = exif[kCGImagePropertyExifDateTimeOriginal] as? String else {
            return nil
        }
        let f = DateFormatter()
        f.dateFormat = "yyyy:MM:dd HH:mm:ss"
        f.timeZone = TimeZone.current
        return f.date(from: raw)
    }
}

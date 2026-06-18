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
                Task { await load() }
            }
        }
        .sheet(isPresented: $showManualEntry) {
            ManualEntrySheet {
                Task { await load() }
            }
        }
        .sheet(item: $editingMeal) { meal in
            MealEditSheet(meal: meal) {
                Task { await load() }
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
        if meals.isEmpty && !loading && !(isToday && !queue.entries.isEmpty) {
            emptyState
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
                    Section { totalsHeader(logged) }
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
            .refreshable { await load() }
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
                    _ = try? await mealService.logAgainToday(meal)
                    await load()
                }
            } label: {
                Label("Log again", systemImage: "arrow.counterclockwise")
            }
            .tint(.green)
        }
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
        defer { loading = false }
        do {
            meals = try await mealService.meals(on: dayKey)
            if !meals.isEmpty { hasLoggedAnyMeal = true }
            loadError = nil
        } catch {
            loadError = error.localizedDescription
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

    private func totalsHeader(_ logged: [Meal]) -> some View {
        let totals = logged.reduce(Nutrients.zero) { $0 + $1.nutrients }
        return HStack {
            stat("\(totals.caloriesKcal)", "kcal")
            stat(String(format: "%.0fg", totals.proteinG), "protein")
            stat(String(format: "%.0fg", totals.fatG), "fat")
            stat(String(format: "%.0fg", totals.carbsG), "carbs")
        }
    }

    private func stat(_ value: String, _ name: String) -> some View {
        VStack {
            Text(value).font(.headline)
            Text(name).font(.caption2).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
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

import SwiftUI

/// Live view over a PendingScanQueue entry. The QUEUE owns the pipeline
/// (photo persisted before any network step) — dismissing this sheet
/// abandons nothing; the scan continues and surfaces in the diary's
/// pending section.
struct ScanFlowView: View {
    let scanId: String
    let image: UIImage
    let onSaved: ([Meal]) -> Void

    @State private var queue = PendingScanQueue.shared
    @State private var job: ScanJob?
    @State private var fetchingJob = false
    @State private var fetchError: String?
    @State private var showPaywall = false
    // Haptic (iOS 17 `.sensoryFeedback`): bumped once the analyzed result is
    // ready to show, so the result appearing lands with a `.success` tap.
    @State private var readyTick = 0
    // Retake: the live result drives a fresh photo without bouncing back to
    // the diary. We present the camera over this flow and, on a new shot,
    // discard the old scan and re-point the flow at the new one — so the
    // parent's `.sheet(item:)` identity stays put and nothing dangles.
    @State private var showRetakeCamera = false
    // The scan this flow is currently following. nil until first appearance
    // (then seeded from the `scanId`/`image` props); reassigned after a
    // retake. All pipeline reads key off these — not the immutable props —
    // so a retake swaps the followed scan in place.
    @State private var retakenScanId: String?
    @State private var retakenImage: UIImage?
    // Anchor for the staged "Analyzing…" timeline (set when it first appears),
    // so the cycling stage is derived from elapsed time, not re-rendered drift.
    @State private var analyzeStart: Date?
    @Environment(\.dismiss) private var dismiss

    /// The scan currently driven by this flow (the prop, or a retaken one).
    private var currentScanId: String { retakenScanId ?? scanId }
    private var currentImage: UIImage { retakenImage ?? image }

    private var entry: PendingScanQueue.Entry? {
        queue.entries.first { $0.scanId == currentScanId }
    }

    var body: some View {
        NavigationStack {
            content
                .navigationTitle("Scan")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button(dismissLabel) { dismiss() }
                    }
                }
        }
        .task(id: entry?.step) {
            if entry?.step == .awaitingSelection && job == nil && !fetchingJob && fetchError == nil {
                await fetchJob()
            }
        }
        .sensoryFeedback(.success, trigger: readyTick)
        .fullScreenCover(isPresented: $showRetakeCamera) {
            CameraPicker { newImage in startRetake(newImage) }
                .ignoresSafeArea()
        }
    }

    /// A retaken photo replaces the current scan in place: discard the old
    /// scan + photo, enqueue the new one through the same crash-safe pipeline,
    /// and re-point this flow at it (resetting the per-scan fetch state). The
    /// parent's `.sheet(item:)` identity is untouched, so the sheet stays up
    /// and follows the new scan — no dangling or duplicate result.
    private func startRetake(_ newImage: UIImage) {
        let oldScanId = currentScanId
        guard let newScanId = queue.enqueue(image: newImage, suggestedDate: entry?.suggestedDate) else { return }
        queue.discard(oldScanId)
        retakenImage = newImage
        retakenScanId = newScanId
        // New scan → forget the old result and let the pipeline re-fetch.
        job = nil
        fetchError = nil
        fetchingJob = false
        analyzeStart = nil // restart the staged "Analyzing…" copy for the retake
    }

    /// Fetch the READY job so the result sheet can render. A failure here —
    /// most plausibly a malformed/partial result that won't decode — must be
    /// terminal with a Retry, never a perpetual "Loading result…" spinner.
    private func fetchJob() async {
        fetchingJob = true
        fetchError = nil
        do {
            let fetched = try await ScanService().getScan(scanId: currentScanId)
            job = fetched
            // Result is now ready to show — give it a success tap.
            if fetched.result != nil { readyTick &+= 1 }
        } catch let APIError.http(status, _, _) where status == 404 {
            // Scan belongs to a previous identity (or TTL'd out) —
            // the entry is unrecoverable; clean it up.
            queue.discard(currentScanId)
            dismiss()
        } catch {
            // Decode/transport failure: surface a recoverable error instead
            // of falling through to the infinite spinner. Retry re-fetches.
            fetchError = error.localizedDescription
        }
        fetchingJob = false
    }

    private var dismissLabel: String {
        switch entry?.step {
        case .awaitingSelection, .notFood, .failed, .paywalled, nil: return "Close"
        default: return "Continue in background"
        }
    }

    @ViewBuilder
    private var content: some View {
        switch entry?.step {
        case .needsCreate, .needsUpload, .needsConfirm:
            progress("Uploading…")
        case .awaitingResult:
            // Analysis can take up to ~2 min; a static spinner that long reads
            // as stuck. Cycle honest stages instead (see `analyzingProgress`).
            analyzingProgress
        case .awaitingSelection:
            if let job, let analysis = job.result {
                ScanResultView(scanId: currentScanId, analysis: analysis, image: currentImage,
                               suggestedDate: entry?.suggestedDate) { meals in
                    queue.completeSaved(currentScanId)
                    onSaved(meals)
                    dismiss()
                } onRetake: {
                    showRetakeCamera = true
                }
            } else if fetchError != nil {
                resultError
            } else {
                progress("Loading result…")
            }
        case .notFood:
            ContentUnavailableView {
                Label("No food found", systemImage: "fork.knife.circle")
            } description: {
                Text("This photo doesn't seem to show food — it wasn't counted against anything. Try another shot.")
            } actions: {
                Button("Close") {
                    queue.discard(currentScanId)
                    dismiss()
                }
            }
        case .paywalled:
            ContentUnavailableView {
                Label("Subscribe to scan", systemImage: "lock.fill")
            } description: {
                Text("Your photo is saved and scans automatically once you subscribe. The text diary stays free.")
            } actions: {
                Button("See plans") { showPaywall = true }
                    .buttonStyle(.borderedProminent)
                Button("Discard photo", role: .destructive) {
                    queue.discard(currentScanId)
                    dismiss()
                }
            }
            .sheet(isPresented: $showPaywall) {
                PaywallView(reason: "scans_exhausted")
            }
        case .failed:
            ContentUnavailableView {
                Label("Scan failed", systemImage: "exclamationmark.triangle")
            } description: {
                Text(entry?.failureMessage ?? "Something went wrong — your photo is kept; you can retry.")
            } actions: {
                Button("Retry") { queue.retry(currentScanId); }
                Button("Discard", role: .destructive) {
                    queue.discard(currentScanId)
                    dismiss()
                }
            }
        case nil:
            // Entry gone (saved or discarded elsewhere).
            Color.clear.onAppear { dismiss() }
        }
    }

    /// READY but the result couldn't be loaded/parsed. The scan is kept;
    /// Retry re-fetches. Discard removes it (the photo is recoverable from
    /// the diary's pending section if it lingers).
    private var resultError: some View {
        ContentUnavailableView {
            Label("Couldn't load result", systemImage: "exclamationmark.triangle")
        } description: {
            Text("Your meal was scanned, but the result couldn't be shown. Your photo is kept — try again.")
        } actions: {
            Button("Retry") {
                Task { await fetchJob() }
            }
            .buttonStyle(.borderedProminent)
            Button("Discard", role: .destructive) {
                queue.discard(currentScanId)
                dismiss()
            }
        }
    }

    private func progress(_ label: String) -> some View {
        progressChrome(label: label)
    }

    /// Honest stages for the long (~up to 2 min) analysis wait, so the spinner
    /// doesn't read as stuck. We advance the line on a timer and then hold on
    /// the last stage for the tail — we don't claim precise progress we lack.
    private static let analyzingStages = [
        "Reading the photo…",
        "Identifying dishes…",
        "Estimating nutrition…",
    ]
    private static let analyzingStageSeconds: TimeInterval = 3.5

    /// `TimelineView(.periodic)` ticks only while this view is on screen and
    /// stops when it leaves — so the staged copy needs no manual Timer and
    /// can't leak one. The stage is derived from time elapsed since the
    /// timeline anchor, holding on the final stage once reached.
    private var analyzingProgress: some View {
        let start = analyzeStart ?? Date()
        return TimelineView(.periodic(from: start, by: Self.analyzingStageSeconds)) { context in
            let elapsed = max(0, context.date.timeIntervalSince(start))
            let step = Int(elapsed / Self.analyzingStageSeconds)
            let idx = min(step, Self.analyzingStages.count - 1)
            progressChrome(label: Self.analyzingStages[idx])
        }
        .onAppear { if analyzeStart == nil { analyzeStart = Date() } }
    }

    private func progressChrome(label: String) -> some View {
        VStack(spacing: 16) {
            Image(uiImage: currentImage)
                .resizable().scaledToFit()
                .frame(maxHeight: 280)
                .clipShape(RoundedRectangle(cornerRadius: 16))
            ProgressView()
            Text(label)
                .foregroundStyle(.secondary)
                .animation(.default, value: label)
                .contentTransition(.opacity)
            Text("You can close this — the scan keeps going and appears in your diary when ready.")
                .font(.caption)
                .foregroundStyle(.tertiary)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 32)
        }
        .padding()
    }
}

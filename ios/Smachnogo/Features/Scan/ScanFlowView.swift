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
    @Environment(\.dismiss) private var dismiss

    private var entry: PendingScanQueue.Entry? {
        queue.entries.first { $0.scanId == scanId }
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
    }

    /// Fetch the READY job so the result sheet can render. A failure here —
    /// most plausibly a malformed/partial result that won't decode — must be
    /// terminal with a Retry, never a perpetual "Loading result…" spinner.
    private func fetchJob() async {
        fetchingJob = true
        fetchError = nil
        do {
            job = try await ScanService().getScan(scanId: scanId)
        } catch let APIError.http(status, _, _) where status == 404 {
            // Scan belongs to a previous identity (or TTL'd out) —
            // the entry is unrecoverable; clean it up.
            queue.discard(scanId)
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
            progress("Analyzing your meal…")
        case .awaitingSelection:
            if let job, let analysis = job.result {
                ScanResultView(scanId: scanId, analysis: analysis, image: image,
                               suggestedDate: entry?.suggestedDate) { meals in
                    queue.completeSaved(scanId)
                    onSaved(meals)
                    dismiss()
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
                    queue.discard(scanId)
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
                    queue.discard(scanId)
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
                Button("Retry") { queue.retry(scanId); }
                Button("Discard", role: .destructive) {
                    queue.discard(scanId)
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
                queue.discard(scanId)
                dismiss()
            }
        }
    }

    private func progress(_ label: String) -> some View {
        VStack(spacing: 16) {
            Image(uiImage: image)
                .resizable().scaledToFit()
                .frame(maxHeight: 280)
                .clipShape(RoundedRectangle(cornerRadius: 16))
            ProgressView()
            Text(label).foregroundStyle(.secondary)
            Text("You can close this — the scan keeps going and appears in your diary when ready.")
                .font(.caption)
                .foregroundStyle(.tertiary)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 32)
        }
        .padding()
    }
}

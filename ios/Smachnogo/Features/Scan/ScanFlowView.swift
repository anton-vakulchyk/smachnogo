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
            if entry?.step == .awaitingSelection && job == nil && !fetchingJob {
                fetchingJob = true
                job = try? await ScanService().getScan(scanId: scanId)
                fetchingJob = false
            }
        }
    }

    private var dismissLabel: String {
        switch entry?.step {
        case .awaitingSelection, .notFood, .failed, nil: return "Close"
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

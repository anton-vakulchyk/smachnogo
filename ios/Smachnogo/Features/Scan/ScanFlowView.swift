import SwiftUI
import PhotosUI

/// The scan state machine: compress → create → upload → confirm-upload →
/// poll → result (dish selection) → save. The model owns the pipeline so
/// progress survives sheet gymnastics.
@MainActor
@Observable
final class ScanFlowModel {
    enum Phase {
        case idle
        case working(String) // progress label
        case result(ScanJob, UIImage)
        case notFood
        case failed(String)
    }

    var phase: Phase = .idle
    private let service = ScanService()

    func run(image: UIImage) {
        phase = .working("Preparing photo…")
        Task {
            do {
                guard let jpeg = ImageCompressor.compressForUpload(image) else {
                    phase = .failed("Couldn't read that photo.")
                    return
                }
                phase = .working("Uploading…")
                let created = try await service.createScan()
                try await service.uploadPhoto(jpeg, to: created.uploadURL)
                try await service.confirmUploaded(scanId: created.scanId)
                LocalPhotoStore.saveThumbnail(image, scanId: created.scanId)

                phase = .working("Analyzing your meal…")
                let job = try await service.awaitResult(scanId: created.scanId)
                if job.status == .failed {
                    phase = .failed(friendlyFailure(job.failureReason))
                    return
                }
                if job.result?.isFood != true {
                    phase = .notFood
                    return
                }
                phase = .result(job, image)
            } catch {
                phase = .failed(error.localizedDescription)
            }
        }
    }

    private func friendlyFailure(_ reason: String?) -> String {
        switch reason {
        case "image_unreadable": return "That photo couldn't be processed — try another shot."
        case "no_image": return "The photo didn't finish uploading — try again."
        case "analysis_implausible": return "The analysis didn't look right — try a clearer shot."
        default: return "Something went wrong — your photo wasn't counted. Try again."
        }
    }
}

struct ScanFlowView: View {
    let image: UIImage
    let onSaved: ([Meal]) -> Void
    @State private var model = ScanFlowModel()
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            content
                .navigationTitle("Scan")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("Cancel") { dismiss() }
                    }
                }
        }
        .task { model.run(image: image) }
        .interactiveDismissDisabled()
    }

    @ViewBuilder
    private var content: some View {
        switch model.phase {
        case .idle:
            Color.clear
        case let .working(label):
            VStack(spacing: 16) {
                Image(uiImage: image)
                    .resizable().scaledToFit()
                    .frame(maxHeight: 280)
                    .clipShape(RoundedRectangle(cornerRadius: 16))
                ProgressView()
                Text(label).foregroundStyle(.secondary)
            }
            .padding()
        case let .result(job, img):
            if let analysis = job.result {
                ScanResultView(scanId: job.scanId, analysis: analysis, image: img) { meals in
                    onSaved(meals)
                    dismiss()
                }
            }
        case .notFood:
            ContentUnavailableView {
                Label("No food found", systemImage: "fork.knife.circle")
            } description: {
                Text("This photo doesn't seem to show food — it wasn't counted against anything. Try another shot.")
            } actions: {
                Button("Close") { dismiss() }
            }
        case let .failed(message):
            ContentUnavailableView {
                Label("Scan failed", systemImage: "exclamationmark.triangle")
            } description: {
                Text(message)
            } actions: {
                Button("Close") { dismiss() }
            }
        }
    }
}

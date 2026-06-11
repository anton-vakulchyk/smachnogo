import Foundation
import UIKit
import Network

/// Crash/offline-safe scan engine: the photo is persisted to disk BEFORE
/// any network step, each step transition is checkpointed, and entries
/// resume on launch and on connectivity-regained. Killing the app at any
/// point loses nothing — the scan finishes next time.
@MainActor
@Observable
final class PendingScanQueue {
    static let shared = PendingScanQueue()

    enum Step: String, Codable {
        case needsCreate        // nothing sent yet
        case needsUpload        // scan created server-side; photo not uploaded
        case needsConfirm       // uploaded; confirm call pending
        case awaitingResult     // processing server-side; poll
        case awaitingSelection  // READY with food — user must pick dishes
        case notFood            // READY, no food — informational, dismissible
        case failed             // terminal failure — retry or discard
    }

    struct Entry: Codable, Identifiable {
        var scanId: String
        var step: Step
        var createdAt: Date
        var suggestedDate: Date?
        var failureMessage: String?

        var id: String { scanId }
    }

    private(set) var entries: [Entry] = []
    private var inFlight: Set<String> = []
    private let monitor = NWPathMonitor()
    private let service = ScanService()

    private init() {
        entries = Self.loadAll()
        monitor.pathUpdateHandler = { [weak self] path in
            guard path.status == .satisfied else { return }
            Task { @MainActor in self?.resumeAll() }
        }
        monitor.start(queue: DispatchQueue(label: "pendingscan.network"))
    }

    // MARK: - Public API

    /// Persist the photo + entry, then start advancing. Returns the entry id.
    @discardableResult
    func enqueue(image: UIImage, suggestedDate: Date?) -> String? {
        guard let jpeg = ImageCompressor.compressForUpload(image) else { return nil }
        let scanId = UUID().uuidString.lowercased()
        let dir = Self.dir(scanId)
        do {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            try jpeg.write(to: dir.appendingPathComponent("photo.jpg"))
        } catch {
            return nil
        }
        LocalPhotoStore.saveThumbnail(image, scanId: scanId)
        var entry = Entry(scanId: scanId, step: .needsCreate, createdAt: Date(), suggestedDate: suggestedDate)
        persist(&entry)
        entries.append(entry)
        advance(scanId)
        return scanId
    }

    func retry(_ scanId: String) {
        guard var entry = entry(scanId), entry.step == .failed else { return }
        // A FAILED scan is terminal server-side — restart as a NEW scan with
        // the same photo (quota was refunded for the failed one).
        let oldDir = Self.dir(scanId)
        guard let jpeg = try? Data(contentsOf: oldDir.appendingPathComponent("photo.jpg")),
              let image = UIImage(data: jpeg) else {
            discard(scanId)
            return
        }
        discard(scanId)
        _ = enqueue(image: image, suggestedDate: entry.suggestedDate)
        entry.failureMessage = nil
    }

    /// Remove an entry and its photo (after save, not-food dismissal, or
    /// user discard).
    func discard(_ scanId: String) {
        entries.removeAll { $0.scanId == scanId }
        try? FileManager.default.removeItem(at: Self.dir(scanId))
    }

    /// Called after the meal is saved: the full photo goes, the diary
    /// thumbnail stays.
    func completeSaved(_ scanId: String) {
        discard(scanId)
    }

    func resumeAll() {
        for e in entries where ![.awaitingSelection, .notFood, .failed].contains(e.step) {
            advance(e.scanId)
        }
    }

    func loadImage(_ scanId: String) -> UIImage? {
        guard let data = try? Data(contentsOf: Self.dir(scanId).appendingPathComponent("photo.jpg")) else { return nil }
        return UIImage(data: data)
    }

    // MARK: - Engine

    private func advance(_ scanId: String) {
        guard !inFlight.contains(scanId) else { return }
        inFlight.insert(scanId)
        Task {
            defer { inFlight.remove(scanId) }
            await run(scanId)
        }
    }

    private func run(_ scanId: String) async {
        guard var entry = entry(scanId) else { return }
        do {
            if entry.step == .needsCreate || entry.step == .needsUpload || entry.step == .needsConfirm {
                // Create is idempotent on scanId and re-issues a fresh
                // presigned URL — resume-safe even after URL expiry.
                let created = try await service.createScan(scanId: scanId)
                update(&entry, .needsUpload)
                if let url = created.uploadURL {
                    let jpeg = try Data(contentsOf: Self.dir(scanId).appendingPathComponent("photo.jpg"))
                    try await service.uploadPhoto(jpeg, to: url)
                }
                update(&entry, .needsConfirm)
                try await service.confirmUploaded(scanId: scanId)
                update(&entry, .awaitingResult)
            }
            if entry.step == .awaitingResult {
                let job = try await service.awaitResult(scanId: scanId)
                switch job.status {
                case .ready where job.result?.isFood == true:
                    update(&entry, .awaitingSelection)
                case .ready:
                    update(&entry, .notFood)
                case .failed:
                    entry.failureMessage = friendlyFailure(job.failureReason)
                    update(&entry, .failed)
                default:
                    break // poll timeout — stays awaitingResult; next resume re-polls
                }
            }
        } catch {
            // Transport/offline errors keep the current step (resume retries
            // it); only mark failed for explicit server rejections.
            if case let APIError.http(status, _, message) = error, status >= 400, status != 408, status != 429 {
                entry.failureMessage = message
                update(&entry, .failed)
            }
        }
    }

    private func friendlyFailure(_ reason: String?) -> String {
        switch reason {
        case "image_unreadable": return "That photo couldn't be processed."
        case "no_image": return "The photo didn't finish uploading."
        case "analysis_implausible": return "The analysis didn't look right."
        default: return "Something went wrong — this scan wasn't counted."
        }
    }

    // MARK: - State

    private func entry(_ scanId: String) -> Entry? {
        entries.first { $0.scanId == scanId }
    }

    private func update(_ entry: inout Entry, _ step: Step) {
        entry.step = step
        persist(&entry)
        if let i = entries.firstIndex(where: { $0.scanId == entry.scanId }) {
            entries[i] = entry
        }
    }

    private func persist(_ entry: inout Entry) {
        let url = Self.dir(entry.scanId).appendingPathComponent("state.json")
        if let data = try? JSONEncoder().encode(entry) {
            try? data.write(to: url)
        }
    }

    // MARK: - Disk

    private static var root: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        let d = base.appendingPathComponent("PendingScans", isDirectory: true)
        try? FileManager.default.createDirectory(at: d, withIntermediateDirectories: true)
        return d
    }

    private static func dir(_ scanId: String) -> URL {
        root.appendingPathComponent(scanId, isDirectory: true)
    }

    private static func loadAll() -> [Entry] {
        guard let dirs = try? FileManager.default.contentsOfDirectory(at: root, includingPropertiesForKeys: nil) else { return [] }
        return dirs.compactMap { dir in
            guard let data = try? Data(contentsOf: dir.appendingPathComponent("state.json")),
                  let entry = try? JSONDecoder().decode(Entry.self, from: data) else { return nil }
            return entry
        }
        .sorted { $0.createdAt < $1.createdAt }
    }
}

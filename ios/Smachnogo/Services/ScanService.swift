import Foundation
import UIKit

/// Drives the scan pipeline: create → S3 PUT → uploaded → poll. Polling
/// lives HERE (not in views) so it survives view dismissal.
struct ScanService: Sendable {
    var api = APIClient()

    struct CreateResult {
        let scanId: String
        let uploadURL: String
    }

    func createScan() async throws -> CreateResult {
        let scanId = UUID().uuidString.lowercased()
        let resp: ScanCreateResponse = try await api.post("/v1/scans", body: ["scan_id": scanId])
        guard let upload = resp.upload else {
            throw APIError.http(status: 409, code: "NO_UPLOAD_URL", message: "scan already past upload")
        }
        return CreateResult(scanId: resp.scanId, uploadURL: upload.url)
    }

    func uploadPhoto(_ jpeg: Data, to presignedURL: String) async throws {
        try await api.uploadToPresignedURL(presignedURL, data: jpeg, contentType: "image/jpeg")
    }

    func confirmUploaded(scanId: String) async throws {
        struct Resp: Codable { var status: ScanStatus }
        let _: Resp = try await api.post("/v1/scans/\(scanId)/uploaded", body: [String: String]())
    }

    /// Polls with backoff (1, 1.5, 2, 3, then 5s) until READY/FAILED.
    /// Budget 120s — the photo stays local, retry is always possible.
    func awaitResult(scanId: String) async throws -> ScanJob {
        let delays: [Double] = [1.0, 1.5, 2.0, 3.0]
        let deadline = Date().addingTimeInterval(120)
        var attempt = 0
        while Date() < deadline {
            let delay = attempt < delays.count ? delays[attempt] : 5.0
            try await Task.sleep(for: .seconds(delay))
            attempt += 1
            let job: ScanJob = try await api.get("/v1/scans/\(scanId)")
            if job.status == .ready || job.status == .failed {
                return job
            }
        }
        throw APIError.http(status: 408, code: "POLL_TIMEOUT", message: "analysis took too long — try again")
    }

    struct ConfirmDish: Codable {
        var index: Int
        var portionFactor: Double
        enum CodingKeys: String, CodingKey {
            case index
            case portionFactor = "portion_factor"
        }
    }

    struct ConfirmRequest: Codable {
        var dishes: [ConfirmDish]
        var date: String
        var consumedAt: String
        enum CodingKeys: String, CodingKey {
            case dishes, date
            case consumedAt = "consumed_at"
        }
    }

    func confirm(scanId: String, dishes: [ConfirmDish], date: String) async throws -> [Meal] {
        let req = ConfirmRequest(dishes: dishes, date: date,
                                 consumedAt: ISO8601DateFormatter().string(from: Date()))
        let resp: MealsResponse = try await api.post("/v1/scans/\(scanId)/confirm", body: req)
        return resp.meals
    }
}

/// Local photo storage: the compressed upload image is kept until the meal
/// is saved; a small thumbnail (keyed by scan id) backs the diary rows.
/// Photos never round-trip through the server for display in v1.
enum LocalPhotoStore {
    private static var dir: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        let d = base.appendingPathComponent("Photos", isDirectory: true)
        try? FileManager.default.createDirectory(at: d, withIntermediateDirectories: true)
        return d
    }

    static func saveThumbnail(_ image: UIImage, scanId: String) {
        guard let data = ImageCompressor.thumbnail(image) else { return }
        try? data.write(to: dir.appendingPathComponent("\(scanId)-thumb.jpg"))
    }

    static func thumbnail(scanId: String) -> UIImage? {
        guard let data = try? Data(contentsOf: dir.appendingPathComponent("\(scanId)-thumb.jpg")) else { return nil }
        return UIImage(data: data)
    }
}

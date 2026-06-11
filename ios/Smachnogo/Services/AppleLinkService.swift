import Foundation
import CryptoKit

/// Sign in with Apple → backend link/recover. One POST, the server decides:
/// first link backs the diary up; signing in on a new device pulls the
/// diary (and subscription) over to this install's identity.
struct AppleLinkService: Sendable {
    var api = APIClient()

    struct Response: Codable {
        var status: String // linked | already_linked | recovered
        var itemsCopied: Int?

        enum CodingKeys: String, CodingKey {
            case status
            case itemsCopied = "items_copied"
        }
    }

    func link(identityToken: String, rawNonce: String) async throws -> Response {
        try await api.post("/v1/users/apple", body: [
            "identity_token": identityToken,
            "nonce": rawNonce,
        ])
    }

    /// The SIWA request wants the SHA256 of the nonce; the raw value goes to
    /// our backend for comparison against the token's claim.
    static func sha256Hex(_ s: String) -> String {
        SHA256.hash(data: Data(s.utf8)).map { String(format: "%02x", $0) }.joined()
    }
}

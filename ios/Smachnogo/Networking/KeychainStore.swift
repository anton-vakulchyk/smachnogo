import Foundation
import Security

/// Device identity persistence. kSecAttrAccessibleAfterFirstUnlock — a
/// MIGRATING class, deliberately NOT ...ThisDeviceOnly: standard
/// iPhone-to-iPhone migration must carry the identity (and with it the
/// diary); and Keychain items survive app deletion, so a casual
/// delete/reinstall returns the same user with the same consumed allowance.
enum KeychainStore {
    private static let service = "app.smachnogo.identity"
    private static let account = "device-user"

    struct Identity: Codable {
        let username: String
        let password: String
    }

    static func loadIdentity() -> Identity? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var out: AnyObject?
        guard SecItemCopyMatching(query as CFDictionary, &out) == errSecSuccess,
              let data = out as? Data,
              let identity = try? JSONDecoder().decode(Identity.self, from: data) else {
            return nil
        }
        return identity
    }

    @discardableResult
    static func saveIdentity(_ identity: Identity) -> Bool {
        guard let data = try? JSONEncoder().encode(identity) else { return false }
        let base: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(base as CFDictionary)
        var add = base
        add[kSecValueData as String] = data
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        return SecItemAdd(add as CFDictionary, nil) == errSecSuccess
    }

    static func deleteIdentity() {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(query as CFDictionary)
    }
}

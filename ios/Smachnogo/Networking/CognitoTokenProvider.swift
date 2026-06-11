import Foundation

/// Anonymous-first Cognito auth via plain REST (no AWS SDK):
/// 1. Identity = device-generated random credentials in the Keychain
///    (minted via silent SignUp on first need — the user sees nothing).
/// 2. USER_PASSWORD_AUTH on first request, REFRESH_TOKEN_AUTH near expiry,
///    invalidate-and-retry on 401.
/// 3. If a stored identity stops authenticating (dev pool wiped), a new
///    one is minted — graceful, but on a real device this only happens if
///    the pool itself changed.
///
/// Identity-bootstrap failures are surfaced as TRANSPORT errors so the
/// pending-scan queue retries instead of terminally failing entries —
/// "identity creation is step 0 of the resume chain".
actor CognitoTokenProvider: TokenProvider {
    private let region: String
    private let clientID: String

    private var identity: KeychainStore.Identity?
    private var accessToken: String?
    private var accessExpiry: Date = .distantPast
    private var refreshToken: String?

    init(region: String, clientID: String) {
        self.region = region
        self.clientID = clientID
        self.identity = KeychainStore.loadIdentity()
    }

    func token() async throws -> String {
        if let t = accessToken, accessExpiry > Date().addingTimeInterval(120) {
            return t
        }
        if refreshToken != nil {
            if let t = try? await refresh() { return t }
            refreshToken = nil // expired/revoked — full login below
        }
        do {
            return try await login()
        } catch let APIError.http(_, code, _) where code == "UserNotFoundException" || code == "NotAuthorizedException" {
            // Stored identity no longer valid (pool reset) — mint a fresh one.
            identity = nil
            KeychainStore.deleteIdentity()
            return try await login()
        }
    }

    func invalidate() async {
        accessToken = nil
        accessExpiry = .distantPast
    }

    // MARK: - Identity

    private func ensureIdentity() async throws -> KeychainStore.Identity {
        if let identity { return identity }
        if let stored = KeychainStore.loadIdentity() {
            identity = stored
            return stored
        }
        // Mint: opaque username + 44-char random secret (pool minimum 20).
        let fresh = KeychainStore.Identity(
            username: "ios-" + UUID().uuidString.lowercased(),
            password: Self.randomSecret()
        )
        _ = try await cognito("SignUp", body: [
            "ClientId": clientID,
            "Username": fresh.username,
            "Password": fresh.password,
        ])
        // Persist IMMEDIATELY — losing these credentials orphans the account.
        KeychainStore.saveIdentity(fresh)
        identity = fresh
        return fresh
    }

    private static func randomSecret() -> String {
        var bytes = [UInt8](repeating: 0, count: 33)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        return Data(bytes).base64EncodedString()
    }

    // MARK: - Auth flows

    private struct AuthResult: Decodable {
        let AccessToken: String?
        let RefreshToken: String?
        let ExpiresIn: Int?
    }
    private struct AuthResponse: Decodable {
        let AuthenticationResult: AuthResult?
    }

    private func login() async throws -> String {
        let id = try await ensureIdentity()
        let data = try await cognito("InitiateAuth", body: [
            "AuthFlow": "USER_PASSWORD_AUTH",
            "ClientId": clientID,
            "AuthParameters": ["USERNAME": id.username, "PASSWORD": id.password],
        ])
        let result = try decodeAuth(data)
        if let rt = result.RefreshToken { refreshToken = rt }
        return try adopt(result)
    }

    private func refresh() async throws -> String {
        guard let rt = refreshToken else { throw APIError.noToken }
        let data = try await cognito("InitiateAuth", body: [
            "AuthFlow": "REFRESH_TOKEN_AUTH",
            "ClientId": clientID,
            "AuthParameters": ["REFRESH_TOKEN": rt],
        ])
        return try adopt(try decodeAuth(data)) // no new RefreshToken — keep ours
    }

    private func decodeAuth(_ data: Data) throws -> AuthResult {
        guard let decoded = try? JSONDecoder().decode(AuthResponse.self, from: data),
              let result = decoded.AuthenticationResult else {
            throw APIError.noToken
        }
        return result
    }

    private func adopt(_ result: AuthResult) throws -> String {
        guard let token = result.AccessToken else { throw APIError.noToken }
        accessToken = token
        accessExpiry = Date().addingTimeInterval(TimeInterval(result.ExpiresIn ?? 3600))
        return token
    }

    // MARK: - Cognito IDP REST

    private struct CognitoError: Decodable {
        let __type: String?
        let message: String?
    }

    private func cognito(_ target: String, body: [String: Any]) async throws -> Data {
        var req = URLRequest(url: URL(string: "https://cognito-idp.\(region).amazonaws.com/")!)
        req.httpMethod = "POST"
        req.setValue("application/x-amz-json-1.1", forHTTPHeaderField: "Content-Type")
        req.setValue("AWSCognitoIdentityProviderService.\(target)", forHTTPHeaderField: "X-Amz-Target")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)

        let (data, resp): (Data, URLResponse)
        do {
            (data, resp) = try await URLSession.shared.data(for: req)
        } catch {
            throw APIError.transport(error) // offline → callers retry later
        }
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard status == 200 else {
            let parsed = try? JSONDecoder().decode(CognitoError.self, from: data)
            let type = (parsed?.__type ?? "COGNITO_AUTH").components(separatedBy: "#").last ?? "COGNITO_AUTH"
            switch type {
            case "UserNotFoundException", "NotAuthorizedException":
                throw APIError.http(status: status, code: type, message: parsed?.message ?? "")
            default:
                // Throttling, service hiccups, etc. — treat as transient so
                // pending work retries rather than terminally failing.
                throw APIError.transport(NSError(domain: "Cognito." + type, code: status, userInfo: [
                    NSLocalizedDescriptionKey: parsed?.message ?? "Cognito \(type)",
                ]))
            }
        }
        return data
    }
}

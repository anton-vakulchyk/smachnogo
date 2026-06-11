import Foundation

/// Cognito auth via plain REST (no AWS SDK): silent USER_PASSWORD_AUTH on
/// first use, REFRESH_TOKEN_AUTH near expiry, invalidate-and-retry on 401.
/// M2 uses the hardcoded dev user from build config; M6 swaps the
/// credential source to per-install Keychain identities — same provider.
actor CognitoTokenProvider: TokenProvider {
    private let region: String
    private let clientID: String
    private let username: String
    private let password: String

    private var accessToken: String?
    private var accessExpiry: Date = .distantPast
    private var refreshToken: String?

    init(region: String, clientID: String, username: String, password: String) {
        self.region = region
        self.clientID = clientID
        self.username = username
        self.password = password
    }

    func token() async throws -> String {
        if let t = accessToken, accessExpiry > Date().addingTimeInterval(120) {
            return t
        }
        if refreshToken != nil {
            do {
                return try await refresh()
            } catch {
                // Refresh token revoked/expired — fall through to full login.
                refreshToken = nil
            }
        }
        return try await login()
    }

    func invalidate() async {
        accessToken = nil
        accessExpiry = .distantPast
    }

    // MARK: - Cognito IDP REST

    private struct AuthResult: Decodable {
        let AccessToken: String?
        let RefreshToken: String?
        let ExpiresIn: Int?
    }
    private struct AuthResponse: Decodable {
        let AuthenticationResult: AuthResult?
    }

    private func login() async throws -> String {
        let result = try await initiateAuth(flow: "USER_PASSWORD_AUTH", params: [
            "USERNAME": username, "PASSWORD": password,
        ])
        if let rt = result.RefreshToken { refreshToken = rt }
        return try adopt(result)
    }

    private func refresh() async throws -> String {
        guard let rt = refreshToken else { throw APIError.noToken }
        let result = try await initiateAuth(flow: "REFRESH_TOKEN_AUTH", params: [
            "REFRESH_TOKEN": rt,
        ])
        return try adopt(result) // refresh responses carry no new RefreshToken — keep ours
    }

    private func adopt(_ result: AuthResult) throws -> String {
        guard let token = result.AccessToken else { throw APIError.noToken }
        accessToken = token
        accessExpiry = Date().addingTimeInterval(TimeInterval(result.ExpiresIn ?? 3600))
        return token
    }

    private func initiateAuth(flow: String, params: [String: String]) async throws -> AuthResult {
        var req = URLRequest(url: URL(string: "https://cognito-idp.\(region).amazonaws.com/")!)
        req.httpMethod = "POST"
        req.setValue("application/x-amz-json-1.1", forHTTPHeaderField: "Content-Type")
        req.setValue("AWSCognitoIdentityProviderService.InitiateAuth", forHTTPHeaderField: "X-Amz-Target")
        let body: [String: Any] = [
            "AuthFlow": flow,
            "ClientId": clientID,
            "AuthParameters": params,
        ]
        req.httpBody = try JSONSerialization.data(withJSONObject: body)

        let (data, resp) = try await URLSession.shared.data(for: req)
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard status == 200 else {
            let msg = String(data: data, encoding: .utf8) ?? ""
            throw APIError.http(status: status, code: "COGNITO_AUTH", message: String(msg.prefix(200)))
        }
        let decoded = try JSONDecoder().decode(AuthResponse.self, from: data)
        guard let result = decoded.AuthenticationResult else { throw APIError.noToken }
        return result
    }
}

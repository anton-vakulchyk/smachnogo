import Foundation

protocol TokenProvider: Sendable {
    func token() async throws -> String
    func invalidate() async
}

extension TokenProvider {
    func invalidate() async {}
}

/// Dev static token from xcconfig (local API in static mode).
struct StaticTokenProvider: TokenProvider {
    func token() async throws -> String {
        let t = AppConfig.bearerToken
        guard !t.isEmpty else { throw APIError.noToken }
        return t
    }
}

/// One shared provider for the process: Cognito when build config carries
/// pool credentials, otherwise the static dev token.
let sharedTokenProvider: TokenProvider = {
    if let c = AppConfig.cognito {
        return CognitoTokenProvider(region: c.region, clientID: c.clientID,
                                    username: c.username, password: c.password)
    }
    return StaticTokenProvider()
}()

enum APIError: Error, LocalizedError {
    case noToken
    case http(status: Int, code: String, message: String)
    case transport(Error)
    case decoding(Error)

    var errorDescription: String? {
        switch self {
        case .noToken: return "Missing API token — fill Configs/Secrets.xcconfig."
        case let .http(_, code, message): return "\(code): \(message)"
        case let .transport(e): return e.localizedDescription
        case let .decoding(e): return "Bad response: \(e.localizedDescription)"
        }
    }
}

private struct ErrorEnvelope: Codable {
    struct Detail: Codable { var code: String; var message: String }
    var error: Detail
}

struct APIClient: Sendable {
    var baseURL: URL = AppConfig.apiBaseURL
    var tokenProvider: TokenProvider = sharedTokenProvider

    func get<T: Decodable>(_ path: String, query: [URLQueryItem] = []) async throws -> T {
        try await request(path, method: "GET", query: query, body: Optional<Int>.none)
    }

    func post<T: Decodable, B: Encodable>(_ path: String, body: B) async throws -> T {
        try await request(path, method: "POST", body: body)
    }

    func request<T: Decodable, B: Encodable>(
        _ path: String, method: String, query: [URLQueryItem] = [], body: B?
    ) async throws -> T {
        var encoded: Data?
        if let body { encoded = try JSONEncoder().encode(body) }
        let data = try await perform(path, method: method, query: query, body: encoded)
        do {
            return try JSONDecoder().decode(T.self, from: data)
        } catch {
            throw APIError.decoding(error)
        }
    }

    /// Request expecting no response body (204s).
    func requestVoid(_ path: String, method: String, query: [URLQueryItem] = []) async throws {
        _ = try await perform(path, method: method, query: query, body: nil)
    }

    /// Raw bytes (export download).
    func rawGet(_ path: String, query: [URLQueryItem] = []) async throws -> Data {
        try await perform(path, method: "GET", query: query, body: nil)
    }

    /// Core: builds the request, sends, maps the error envelope, and
    /// retries ONCE on 401 after invalidating the cached token.
    private func perform(_ path: String, method: String, query: [URLQueryItem], body: Data?) async throws -> Data {
        do {
            return try await performOnce(path, method: method, query: query, body: body)
        } catch let APIError.http(status, _, _) where status == 401 {
            await tokenProvider.invalidate()
            return try await performOnce(path, method: method, query: query, body: body)
        }
    }

    private func performOnce(_ path: String, method: String, query: [URLQueryItem], body: Data?) async throws -> Data {
        var comps = URLComponents(url: baseURL.appendingPathComponent(path), resolvingAgainstBaseURL: false)!
        if !query.isEmpty { comps.queryItems = query }
        var req = URLRequest(url: comps.url!)
        req.httpMethod = method
        req.setValue("Bearer \(try await tokenProvider.token())", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue(UUID().uuidString, forHTTPHeaderField: "X-Request-Id")
        req.httpBody = body

        let (data, resp): (Data, URLResponse)
        do {
            (data, resp) = try await URLSession.shared.data(for: req)
        } catch {
            throw APIError.transport(error)
        }
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            if let env = try? JSONDecoder().decode(ErrorEnvelope.self, from: data) {
                throw APIError.http(status: status, code: env.error.code, message: env.error.message)
            }
            throw APIError.http(status: status, code: "HTTP_\(status)", message: String(data: data, encoding: .utf8) ?? "")
        }
        return data
    }

    /// Raw PUT to a presigned S3 URL — no auth header (the signature IS the
    /// auth), Content-Type must match what the signature pinned.
    func uploadToPresignedURL(_ urlString: String, data: Data, contentType: String) async throws {
        guard let url = URL(string: urlString) else { throw APIError.transport(URLError(.badURL)) }
        var req = URLRequest(url: url)
        req.httpMethod = "PUT"
        req.setValue(contentType, forHTTPHeaderField: "Content-Type")
        let (respData, resp): (Data, URLResponse)
        do {
            (respData, resp) = try await URLSession.shared.upload(for: req, from: data)
        } catch {
            throw APIError.transport(error)
        }
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            throw APIError.http(status: status, code: "S3_PUT_FAILED",
                                message: String(data: respData, encoding: .utf8) ?? "")
        }
    }
}

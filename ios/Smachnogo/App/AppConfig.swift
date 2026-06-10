import Foundation

/// Build-time configuration injected via xcconfig → Info.plist.
/// Debug → local dev API + static token; Release → prod + Cognito (M2+).
enum AppConfig {
    static var apiBaseURL: URL {
        guard let s = Bundle.main.object(forInfoDictionaryKey: "APIBaseURL") as? String,
              let url = URL(string: s), !s.isEmpty else {
            fatalError("APIBaseURL missing — check Configs/*.xcconfig")
        }
        return url
    }

    /// Dev-only static bearer token (empty in Release).
    static var bearerToken: String {
        Bundle.main.object(forInfoDictionaryKey: "APIBearerToken") as? String ?? ""
    }
}

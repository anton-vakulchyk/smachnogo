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

    struct CognitoConfig {
        let region: String
        let clientID: String
    }

    /// Present when the build carries a Cognito app client. Identity is
    /// per-install (silent signup + Keychain) — no credentials in config
    /// since M6; the dev-user xcconfig keys are unused and ignored.
    static var cognito: CognitoConfig? {
        func plist(_ key: String) -> String {
            Bundle.main.object(forInfoDictionaryKey: key) as? String ?? ""
        }
        let clientID = plist("CognitoClientID")
        guard !clientID.isEmpty else { return nil }
        let region = plist("CognitoRegion").isEmpty ? "us-east-1" : plist("CognitoRegion")
        return CognitoConfig(region: region, clientID: clientID)
    }
}

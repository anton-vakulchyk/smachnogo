import SwiftUI
import AuthenticationServices

/// Custom-drawn Sign in with Apple button. iOS 26's system control renders
/// as a capsule and ignores its cornerRadius property; the HIG permits a
/// custom equivalent that follows Apple's spec — white field, Apple logo,
/// title-weight text — which lets us keep the standard rounded rectangle.
struct AppleSignInButton: View {
    var onTap: () -> Void

    var body: some View {
        Button(action: onTap) {
            HStack(spacing: 7) {
                Image(systemName: "apple.logo")
                    .font(.system(size: 18, weight: .medium))
                    .baselineOffset(1)
                Text("Continue with Apple")
                    .font(.system(size: 19, weight: .semibold))
            }
            .foregroundStyle(.black)
            .frame(maxWidth: .infinity, minHeight: 48)
            .background(.white, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Continue with Apple")
    }
}

/// Drives the ASAuthorizationController flow the SwiftUI button used to run
/// for us: request with the hashed nonce → delegate → Result callback.
@MainActor
final class AppleSignInRunner: NSObject, ASAuthorizationControllerDelegate, ASAuthorizationControllerPresentationContextProviding {
    private var completion: ((Result<ASAuthorization, Error>) -> Void)?

    func run(nonceHash: String, completion: @escaping (Result<ASAuthorization, Error>) -> Void) {
        self.completion = completion
        let request = ASAuthorizationAppleIDProvider().createRequest()
        request.nonce = nonceHash
        let controller = ASAuthorizationController(authorizationRequests: [request])
        controller.delegate = self
        controller.presentationContextProvider = self
        controller.performRequests()
    }

    func authorizationController(controller: ASAuthorizationController, didCompleteWithAuthorization authorization: ASAuthorization) {
        completion?(.success(authorization))
        completion = nil
    }

    func authorizationController(controller: ASAuthorizationController, didCompleteWithError error: Error) {
        completion?(.failure(error))
        completion = nil
    }

    func presentationAnchor(for controller: ASAuthorizationController) -> ASPresentationAnchor {
        UIApplication.shared.connectedScenes
            .compactMap { ($0 as? UIWindowScene)?.keyWindow }
            .first ?? ASPresentationAnchor()
    }
}

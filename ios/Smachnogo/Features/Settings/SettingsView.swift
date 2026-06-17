import SwiftUI
import AuthenticationServices

/// Settings: Apple backup/restore, subscription, data export, account
/// deletion (App Store 5.1.1(v)), the estimates disclaimer, legal links.
struct SettingsView: View {
    /// Fired when server-side data changed under us (Apple recovery) so the
    /// presenting view reloads the diary.
    var onDataChanged: (() -> Void)? = nil

    @Environment(\.dismiss) private var dismiss
    @State private var exportURL: URL?
    @State private var exporting = false
    @State private var confirmDelete = false
    @State private var deleting = false
    @State private var deleted = false
    @State private var errorText: String?
    @State private var showPaywall = false
    @State private var appleStatus: String?
    @State private var appleBusy = false
    @State private var rawNonce = ""
    @State private var showLimits = false
    @State private var signInRunner = AppleSignInRunner()

    private let service = MealService()

    var body: some View {
        NavigationStack {
            Form {
                Section("Your data") {
                    if exporting {
                        ProgressView()
                    } else if let exportURL {
                        ShareLink(item: exportURL) {
                            Label("Share exported data", systemImage: "square.and.arrow.up")
                        }
                    } else {
                        Button {
                            exportData()
                        } label: {
                            Label("Export my data", systemImage: "arrow.down.doc")
                        }
                    }
                    Button(role: .destructive) {
                        confirmDelete = true
                    } label: {
                        if deleting { ProgressView() } else { Label("Delete account & data", systemImage: "trash") }
                    }
                    .disabled(deleting)
                }

                Section {
                    if StoreService.shared.me?.appleLinked == true {
                        Label("Backed up with your Apple ID", systemImage: "checkmark.icloud")
                            .foregroundStyle(.secondary)
                    }
                    if appleBusy {
                        ProgressView()
                    } else {
                        AppleSignInButton {
                            rawNonce = UUID().uuidString + UUID().uuidString
                            signInRunner.run(nonceHash: AppleLinkService.sha256Hex(rawNonce)) { result in
                                handleApple(result)
                            }
                        }
                        .frame(height: 48)
                        // Inset past the section card's corner clip (iOS 26
                        // Forms clip rows to a large-radius card — anything
                        // touching the edges turns into a capsule).
                        .padding(.horizontal, 12)
                        .padding(.vertical, 2)
                        .listRowBackground(Color.clear)
                        .listRowInsets(EdgeInsets())
                    }
                    if let appleStatus {
                        Text(appleStatus).font(.footnote).foregroundStyle(.secondary)
                    }
                } header: {
                    Text("Back up & restore")
                } footer: {
                    Text("Link your Apple ID to restore your diary and subscription on a new iPhone. Without it, your data lives only on this device.")
                }

                Section("Subscription") {
                    if StoreService.shared.isSubscribed {
                        Link(destination: URL(string: "https://apps.apple.com/account/subscriptions")!) {
                            Label("Manage subscription", systemImage: "creditcard")
                        }
                    } else {
                        Button {
                            showPaywall = true
                        } label: {
                            Label("Go unlimited", systemImage: "camera.viewfinder")
                        }
                    }
                    Button {
                        Task { await StoreService.shared.restore() }
                    } label: {
                        Label("Restore purchases", systemImage: "arrow.clockwise")
                    }
                }

                Section {
                    Button {
                        showLimits = true
                    } label: {
                        HStack {
                            Label("Daily limits", systemImage: "gauge.with.needle")
                            Spacer()
                            Text(limitsSummary).foregroundStyle(.secondary).font(.footnote)
                        }
                    }
                } footer: {
                    Text("Set caps on calories, sugar, sodium and more — calendar days turn green or red against them.")
                }

                Section("About the numbers") {
                    Text("Calories, macros and scores are AI estimates from your photos and descriptions — useful for tracking trends, not medical or dietary advice. Estimates can be off, especially for hidden ingredients. You can adjust any meal after saving.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }

                Section("Legal") {
                    Link("Privacy policy", destination: URL(string: "https://smachnogo.app/privacy.html")!)
                    Link("Terms of use", destination: URL(string: "https://smachnogo.app/terms.html")!)
                    Link("Support", destination: URL(string: "https://smachnogo.app/support.html")!)
                }

                if let errorText {
                    Section { Text(errorText).font(.footnote).foregroundStyle(.red) }
                }
            }
            .sheet(isPresented: $showPaywall) { PaywallView(reason: nil) }
            .sheet(isPresented: $showLimits) { LimitsEditorSheet(onSaved: onDataChanged) }
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Done") { dismiss() } }
            }
            .confirmationDialog(
                "Delete your account?",
                isPresented: $confirmDelete,
                titleVisibility: .visible
            ) {
                Button("Delete everything", role: .destructive) { deleteAccount() }
            } message: {
                Text("All meals and photos are permanently removed. An active subscription is NOT cancelled here — manage it in your Apple ID settings.")
            }
            .alert("Account deleted", isPresented: $deleted) {
                Button("OK") { dismiss() }
            } message: {
                Text("Your data is gone. The app starts fresh on next launch.")
            }
        }
    }

    private var limitsSummary: String {
        let n = (StoreService.shared.me?.limits ?? [:]).count
        return n == 0 ? "None set" : "\(n) set"
    }

    fileprivate func handleApple(_ result: Result<ASAuthorization, Error>) {
        switch result {
        case .failure(let err):
            // Includes cancellation and the simulator/no-team cases —
            // surface gently, never block anything else.
            if (err as? ASAuthorizationError)?.code == .canceled { return }
            appleStatus = "Apple sign-in didn't complete: \(err.localizedDescription)"
        case .success(let auth):
            guard let cred = auth.credential as? ASAuthorizationAppleIDCredential,
                  let tokenData = cred.identityToken,
                  let token = String(data: tokenData, encoding: .utf8) else {
                appleStatus = "Apple didn't return an identity token."
                return
            }
            appleBusy = true
            Task {
                defer { appleBusy = false }
                do {
                    let resp = try await AppleLinkService().link(identityToken: token, rawNonce: rawNonce)
                    switch resp.status {
                    case "recovered":
                        appleStatus = "Diary restored — \(resp.itemsCopied ?? 0) items moved to this iPhone."
                        onDataChanged?()
                        await StoreService.shared.refresh()
                    default:
                        appleStatus = "Backed up with your Apple ID."
                        await StoreService.shared.refreshServerState()
                    }
                } catch let APIError.http(_, code, message) where code == "APPLE_MISMATCH" {
                    appleStatus = message
                } catch {
                    appleStatus = error.localizedDescription
                }
            }
        }
    }

    private func exportData() {
        exporting = true
        errorText = nil
        Task {
            do {
                let data = try await service.exportData()
                let url = FileManager.default.temporaryDirectory
                    .appendingPathComponent("smachnogo-export.json")
                try data.write(to: url)
                exportURL = url
            } catch {
                errorText = error.localizedDescription
            }
            exporting = false
        }
    }

    private func deleteAccount() {
        deleting = true
        errorText = nil
        Task {
            do {
                try await service.deleteAccount()
                // Account wiped — clear the first-run onboarding flag so the
                // diary empty state returns to "Add your first meal".
                UserDefaults.standard.removeObject(forKey: "hasLoggedAnyMeal")
                deleted = true
            } catch {
                errorText = error.localizedDescription
            }
            deleting = false
        }
    }
}

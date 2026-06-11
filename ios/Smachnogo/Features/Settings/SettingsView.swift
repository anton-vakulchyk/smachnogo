import SwiftUI

/// Settings: data export, account deletion (App Store 5.1.1(v)), the
/// estimates disclaimer, legal links. M7 adds manage-subscription.
struct SettingsView: View {
    @Environment(\.dismiss) private var dismiss
    @State private var exportURL: URL?
    @State private var exporting = false
    @State private var confirmDelete = false
    @State private var deleting = false
    @State private var deleted = false
    @State private var errorText: String?

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

                Section("About the numbers") {
                    Text("Calories, macros and scores are AI estimates from your photos and descriptions — useful for tracking trends, not medical or dietary advice. Estimates can be off, especially for hidden ingredients. You can adjust any meal after saving.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }

                Section("Legal") {
                    Link("Privacy policy", destination: URL(string: "https://smachnogo.app/privacy")!)
                    Link("Terms of use", destination: URL(string: "https://smachnogo.app/terms")!)
                    Link("Support", destination: URL(string: "https://smachnogo.app/support")!)
                }

                if let errorText {
                    Section { Text(errorText).font(.footnote).foregroundStyle(.red) }
                }
            }
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
                deleted = true
            } catch {
                errorText = error.localizedDescription
            }
            deleting = false
        }
    }
}

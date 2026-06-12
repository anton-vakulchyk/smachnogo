import SwiftUI

@main
struct SmachnogoApp: App {
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            RootTabView()
                .task { StoreService.shared.start() }
                .onChange(of: scenePhase) { _, phase in
                    // Returning to the app re-syncs billing state (scans
                    // remaining, entitlement) — it changes server-side via
                    // scans, webhooks, and restores on other devices.
                    if phase == .active {
                        Task { await StoreService.shared.refreshServerState() }
                    }
                }
        }
    }
}

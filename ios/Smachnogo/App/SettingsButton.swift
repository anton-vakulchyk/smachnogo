import SwiftUI

/// Gear button + Settings sheet, reused by the Diary and Stats toolbars so
/// Settings is reachable from both tabs with identical placement.
struct SettingsButton: View {
    /// Called after the Settings sheet reports a data change (e.g. a wipe),
    /// so the host tab can refresh.
    let onDataChanged: () -> Void
    @State private var show = false

    var body: some View {
        Button { show = true } label: {
            Image(systemName: "gearshape")
        }
        .accessibilityLabel("Settings")
        .sheet(isPresented: $show) {
            SettingsView(onDataChanged: onDataChanged)
        }
    }
}

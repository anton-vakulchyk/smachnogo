import SwiftUI

struct RootTabView: View {
    var body: some View {
        TabView {
            DayView()
                .tabItem { Label("Diary", systemImage: "book") }
            StatsPlaceholderView()
                .tabItem { Label("Stats", systemImage: "chart.bar") }
        }
    }
}

struct StatsPlaceholderView: View {
    var body: some View {
        NavigationStack {
            Text("Log a few meals to see trends.")
                .foregroundStyle(.secondary)
                .navigationTitle("Stats")
        }
    }
}

#Preview {
    RootTabView()
}

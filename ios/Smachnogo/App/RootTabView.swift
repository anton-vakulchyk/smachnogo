import SwiftUI

struct RootTabView: View {
    var body: some View {
        TabView {
            DiaryPlaceholderView()
                .tabItem { Label("Diary", systemImage: "book") }
            StatsPlaceholderView()
                .tabItem { Label("Stats", systemImage: "chart.bar") }
        }
    }
}

// M0 placeholders — M1 replaces Diary with DayView + the scan flow.
struct DiaryPlaceholderView: View {
    var body: some View {
        NavigationStack {
            VStack(spacing: 12) {
                Image(systemName: "camera.viewfinder")
                    .font(.system(size: 56))
                    .foregroundStyle(.secondary)
                Text("Scan your first meal")
                    .font(.title3.weight(.semibold))
                Text("Point the camera at your plate — calories, macros and nutrition appear in seconds.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 32)
            }
            .navigationTitle("Today")
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

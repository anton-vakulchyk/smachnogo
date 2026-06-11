import SwiftUI

struct RootTabView: View {
    var body: some View {
        TabView {
            DayView()
                .tabItem { Label("Diary", systemImage: "book") }
            StatsView()
                .tabItem { Label("Stats", systemImage: "chart.bar") }
        }
    }
}

#Preview {
    RootTabView()
}

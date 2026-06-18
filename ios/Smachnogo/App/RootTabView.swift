import SwiftUI

/// The app shell: a native TabView (Diary · Stats) so it renders the iOS 26
/// Liquid-Glass bar for free and degrades to the standard bar on 17–25. The
/// meal-add action lives in a bottom accessory (system-synced on 26, a
/// pinned material pill on 17–25). Because the accessory sits outside
/// DayView — which owns the scan/queue flow — taps switch to the Diary tab
/// and hand DayView an AddMealAction to perform.
struct RootTabView: View {
    enum Tab: Hashable { case diary, stats }

    @State private var selected: Tab = .diary
    @State private var addAction: AddMealAction?

    var body: some View {
        if #available(iOS 26, *) {
            glassTabView
        } else {
            fallbackTabView
        }
    }

    @available(iOS 26, *)
    private var glassTabView: some View {
        TabView(selection: $selected) {
            SwiftUI.Tab("Diary", systemImage: "book", value: Tab.diary) {
                DayView(addAction: $addAction)
            }
            SwiftUI.Tab("Stats", systemImage: "chart.bar", value: Tab.stats) {
                StatsView()
            }
        }
        .tabBarMinimizeBehavior(.onScrollDown)
        .tabViewBottomAccessory {
            ScanAccessory { trigger($0) }
        }
    }

    private var fallbackTabView: some View {
        TabView(selection: $selected) {
            DayView(addAction: $addAction)
                .modifier(ScanAccessoryPill { trigger($0) })
                .tabItem { Label("Diary", systemImage: "book") }
                .tag(Tab.diary)
            StatsView()
                .modifier(ScanAccessoryPill { trigger($0) })
                .tabItem { Label("Stats", systemImage: "chart.bar") }
                .tag(Tab.stats)
        }
    }

    /// Adds always route through the Diary tab (it owns the scan/queue flow).
    private func trigger(_ action: AddMealAction) {
        selected = .diary
        addAction = action
    }
}

/// iOS 17–25 fallback: pins the "Scan a meal" pill above the bottom edge of a
/// tab's CONTENT. Applied to each tab's content (NOT the TabView) — applying a
/// bottom `safeAreaInset` to the TabView places the pill inside the standard
/// tab bar's own safe-area region, overlapping and blocking the tabs.
private struct ScanAccessoryPill: ViewModifier {
    let onAction: (AddMealAction) -> Void
    func body(content: Content) -> some View {
        content.safeAreaInset(edge: .bottom) {
            ScanAccessory(onAction: onAction)
                .padding(.vertical, 8)
                .padding(.horizontal, 14)
                .background(.regularMaterial, in: Capsule())
                .shadow(color: .black.opacity(0.15), radius: 8, y: 3)
                .padding(.horizontal, 24)
                .padding(.bottom, 4)
        }
    }
}

#Preview {
    RootTabView()
}

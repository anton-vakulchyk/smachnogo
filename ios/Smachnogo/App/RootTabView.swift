import SwiftUI

/// Photos-style floating bottom bar: a pill with the Diary/Stats tabs on
/// the left, a detached circular Scan button on the right. Custom (not the
/// system tab bar) so it renders identically from iOS 17 up and the right
/// circle can be an ACTION — it jumps to the diary and opens the camera.
struct RootTabView: View {
    enum Tab { case diary, stats }

    @State private var selected: Tab = .diary
    /// Incremented by the Scan button; DayView reacts by opening the camera.
    @State private var scanRequests = 0

    var body: some View {
        ZStack(alignment: .bottom) {
            // Both views stay alive (opacity switch) so tab flips don't
            // reset scroll position or in-flight state.
            DayView(scanRequests: $scanRequests)
                .opacity(selected == .diary ? 1 : 0)
                .allowsHitTesting(selected == .diary)
            StatsView()
                .opacity(selected == .stats ? 1 : 0)
                .allowsHitTesting(selected == .stats)

            bottomBar
        }
    }

    private var bottomBar: some View {
        HStack {
            HStack(spacing: 0) {
                tabSegment(.diary, "Diary", "book")
                tabSegment(.stats, "Stats", "chart.bar")
            }
            .padding(4)
            .background(.regularMaterial, in: Capsule())
            .shadow(color: .black.opacity(0.18), radius: 10, y: 4)

            Spacer()

            Button {
                selected = .diary
                scanRequests += 1
            } label: {
                Image(systemName: "camera.fill")
                    .font(.system(size: 21, weight: .medium))
                    .foregroundStyle(.primary)
                    .frame(width: 62, height: 62)
                    .background(.regularMaterial, in: Circle())
                    .shadow(color: .black.opacity(0.18), radius: 10, y: 4)
            }
            .accessibilityLabel("Scan a meal")
        }
        .padding(.horizontal, 18)
        .padding(.bottom, 4)
    }

    private func tabSegment(_ tab: Tab, _ title: String, _ icon: String) -> some View {
        let isSelected = selected == tab
        return Button {
            selected = tab
        } label: {
            VStack(spacing: 3) {
                Image(systemName: isSelected ? icon + ".fill" : icon)
                    .font(.system(size: 19, weight: .medium))
                Text(title).font(.caption2.weight(.medium))
            }
            .foregroundStyle(isSelected ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))
            .frame(width: 86, height: 54)
            .background(isSelected ? AnyShapeStyle(.quaternary) : AnyShapeStyle(.clear), in: Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(title)
        .accessibilityAddTraits(isSelected ? .isSelected : [])
    }
}

#Preview {
    RootTabView()
}

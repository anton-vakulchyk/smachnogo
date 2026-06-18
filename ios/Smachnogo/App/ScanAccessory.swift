import SwiftUI

/// A meal-add method requested from OUTSIDE DayView (the bottom-bar
/// accessory). DayView observes it, routes to the matching flow, then
/// clears it so the same action can fire again.
enum AddMealAction: Equatable {
    case camera, library, describe
}

/// Bottom-bar accessory content: a primary "Scan a meal" button (opens the
/// live camera) plus a compact, fully-labelled menu for the no-photo
/// methods. Shared by the iOS 26 `tabViewBottomAccessory` and the iOS 17–25
/// pinned fallback. Glass/material is supplied by the host (system on 26,
/// the fallback wrapper on 17–25) — this view stays plain.
struct ScanAccessory: View {
    let onAction: (AddMealAction) -> Void

    var body: some View {
        HStack(spacing: 8) {
            Button { onAction(.camera) } label: {
                Label("Scan a meal", systemImage: "camera.fill")
                    .font(.headline)
                    .frame(maxWidth: .infinity, minHeight: 36)
            }
            .accessibilityLabel("Scan a meal with the camera")

            Menu {
                Button { onAction(.describe) } label: {
                    Label("Describe a meal", systemImage: "square.and.pencil")
                }
                Button { onAction(.library) } label: {
                    Label("Choose from library", systemImage: "photo.on.rectangle")
                }
            } label: {
                Image(systemName: "ellipsis")
                    .font(.headline)
                    .frame(width: 40, height: 36)
            }
            .accessibilityLabel("More ways to add a meal")
        }
    }
}

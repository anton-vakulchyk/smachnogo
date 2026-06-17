import SwiftUI
import StoreKit

/// The "free taste, paid camera" paywall. Shown on 402 PAYWALL, from the
/// scans-remaining chip, and from paywalled pending scans. Copy varies by
/// the server's reason; prices ALWAYS render from Product.displayPrice
/// (hardcoded numbers are wrong in every non-USD storefront). The free
/// diary stays one tap away — this screen never dead-ends.
struct PaywallView: View {
    var reason: String?

    @State private var store = StoreService.shared
    @State private var errorMessage: String?
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 20) {
                    Image("Logo")
                        .resizable()
                        .scaledToFit()
                        .frame(width: 84, height: 84)
                        .padding(.top, 24)

                    VStack(spacing: 6) {
                        Text(title).font(.title2.bold()).multilineTextAlignment(.center)
                        Text(subtitle)
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                    }
                    .padding(.horizontal)

                    productSection

                    if let errorMessage {
                        Text(errorMessage).font(.footnote).foregroundStyle(.red)
                            .multilineTextAlignment(.center).padding(.horizontal)
                    }

                    Button("Restore Purchases") {
                        Task {
                            await store.restore()
                            if store.isSubscribed { dismiss() }
                        }
                    }
                    .font(.subheadline)

                    Text("The text diary stays free forever — describe meals, edit history, see stats.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal)

                    disclosure
                }
                .padding(.bottom, 24)
            }
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Not now") { dismiss() }
                }
            }
        }
    }

    private var title: String {
        switch reason {
        case "window_expired": return "Your free week ended"
        case "scans_exhausted": return "You've used your free scans"
        case "device_already_used": return "This device used its free scans"
        default: return "Scan meals with your camera"
        }
    }

    private var subtitle: String {
        switch reason {
        case "window_expired": return "Your diary is intact. Subscribe to point the camera at your plate again."
        default: return "Point the camera at any meal — calories, macros and nutrition in seconds."
        }
    }

    @ViewBuilder
    private var productSection: some View {
        if store.products.isEmpty {
            if let err = store.productsError {
                VStack(spacing: 8) {
                    Text(err).font(.footnote).foregroundStyle(.secondary)
                    Button("Try again") { Task { await store.loadProducts() } }
                }
                .padding()
            } else {
                ProgressView().padding()
            }
        } else {
            VStack(spacing: 10) {
                ForEach(store.products, id: \.id) { product in
                    productButton(product)
                }
            }
            .padding(.horizontal)
        }
    }

    private func productButton(_ product: Product) -> some View {
        Button {
            Task {
                errorMessage = nil
                do {
                    try await store.purchase(product)
                    if store.isSubscribed { dismiss() }
                } catch {
                    errorMessage = error.localizedDescription
                }
            }
        } label: {
            VStack(spacing: 2) {
                HStack {
                    Text(product.displayName).fontWeight(.semibold)
                    Spacer()
                    Text("\(product.displayPrice)\(periodSuffix(product))")
                }
                if let trial = trialBadge(product) {
                    HStack {
                        Text(trial).font(.caption).foregroundStyle(.tint)
                        Spacer()
                    }
                }
            }
            .padding(.vertical, 6)
        }
        .buttonStyle(.bordered)
        .controlSize(.large)
        .disabled(store.purchasing)
    }

    private func periodSuffix(_ product: Product) -> String {
        switch product.subscription?.subscriptionPeriod.unit {
        case .year: return "/year"
        case .month: return "/month"
        default: return ""
        }
    }

    private func trialBadge(_ product: Product) -> String? {
        guard let intro = product.subscription?.introductoryOffer,
              intro.paymentMode == .freeTrial else { return nil }
        let p = intro.period
        let days: Int
        switch p.unit {
        case .day: days = p.value
        case .week: days = p.value * 7
        case .month: days = p.value * 30
        case .year: days = p.value * 365
        @unknown default: days = p.value
        }
        return "\(days)-day free trial"
    }

    private var disclosure: some View {
        VStack(spacing: 8) {
            Text("Subscriptions renew automatically until cancelled at least 24 hours before the current period ends. Manage or cancel anytime in Settings → Apple ID → Subscriptions. A free trial converts to a paid subscription unless cancelled before it ends.")
                .font(.caption2)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            HStack(spacing: 16) {
                Link("Terms of Use", destination: URL(string: "https://smachnogo.app/terms.html")!)
                Link("Privacy Policy", destination: URL(string: "https://smachnogo.app/privacy.html")!)
            }
            .font(.caption)
        }
        .padding(.horizontal)
    }
}

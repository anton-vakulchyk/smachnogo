import SwiftUI
import StoreKit

/// The "free taste, paid camera" paywall. Shown on 402 PAYWALL, from the
/// scans-remaining chip, and from paywalled pending scans. Copy varies by
/// the server's reason; prices ALWAYS render from the Product (displayPrice /
/// priceFormatStyle) — hardcoded numbers are wrong in every non-USD
/// storefront. The free diary stays one tap away — this screen never
/// dead-ends.
struct PaywallView: View {
    var reason: String?

    @State private var store = StoreService.shared
    @State private var errorMessage: String?
    /// The plan the primary CTA will buy. Defaults to annual (the value
    /// pick) once products load; falls back to whatever single plan exists.
    @State private var selectedID: String?
    /// Flips on a verified purchase to fire the success haptic.
    @State private var purchaseSucceeded = false
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

                    benefits

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
            .sensoryFeedback(.success, trigger: purchaseSucceeded)
            .onChange(of: store.products.map(\.id)) { _, _ in selectDefault() }
            .onAppear { selectDefault() }
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

    // MARK: - Benefits

    /// Why the price is worth it. Calm, honest, on-brand — no exclamation
    /// marks, no growth-hack urgency.
    private var benefits: some View {
        VStack(alignment: .leading, spacing: 12) {
            benefitRow("camera.fill", "Unlimited meal scans")
            benefitRow("chart.pie.fill", "Full calorie & macro breakdown")
            benefitRow("bolt.fill", "Results in seconds")
            benefitRow("xmark.circle", "Cancel anytime")
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 28)
    }

    private func benefitRow(_ symbol: String, _ text: String) -> some View {
        HStack(spacing: 12) {
            Image(systemName: symbol)
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(.tint)
                .frame(width: 24)
            Text(text)
                .font(.subheadline)
            Spacer(minLength: 0)
        }
    }

    // MARK: - Products

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
            VStack(spacing: 12) {
                ForEach(store.products, id: \.id) { product in
                    planCard(product)
                }

                Button(action: buySelected) {
                    Text(ctaTitle)
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(store.purchasing || selectedProduct == nil)
                .padding(.top, 4)
            }
            .padding(.horizontal)
        }
    }

    /// A tappable plan card. Selection (not purchase) is the tap action; the
    /// single prominent CTA below commits. The selected card reads as a
    /// filled control, the other as a quiet bordered row.
    private func planCard(_ product: Product) -> some View {
        let selected = product.id == selectedID
        return Button {
            selectedID = product.id
        } label: {
            HStack(alignment: .center, spacing: 12) {
                Image(systemName: selected ? "largecircle.fill.circle" : "circle")
                    .font(.title3)
                    .foregroundStyle(selected ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))

                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 8) {
                        Text(product.displayName).fontWeight(.semibold)
                        if isAnnual(product) {
                            Text("Best value")
                                .font(.caption2.weight(.bold))
                                .padding(.horizontal, 7)
                                .padding(.vertical, 3)
                                .background(.tint, in: Capsule())
                                .foregroundStyle(.white)
                        }
                    }
                    if let sub = secondaryLine(product) {
                        Text(sub).font(.caption).foregroundStyle(.secondary)
                    }
                }

                Spacer(minLength: 8)

                VStack(alignment: .trailing, spacing: 2) {
                    Text("\(product.displayPrice)\(periodSuffix(product))")
                        .fontWeight(.semibold)
                    if let trial = trialBadge(product) {
                        Text(trial).font(.caption2).foregroundStyle(.tint)
                    }
                }
            }
            .padding(.vertical, 12)
            .padding(.horizontal, 14)
            .frame(maxWidth: .infinity)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .background {
            RoundedRectangle(cornerRadius: 14, style: .continuous)
                .fill(selected ? AnyShapeStyle(Color.accentColor.opacity(0.12)) : AnyShapeStyle(Color(.secondarySystemBackground)))
        }
        .overlay {
            RoundedRectangle(cornerRadius: 14, style: .continuous)
                .strokeBorder(selected ? AnyShapeStyle(.tint) : AnyShapeStyle(Color.clear), lineWidth: 2)
        }
        .disabled(store.purchasing)
    }

    private func buySelected() {
        guard let product = selectedProduct else { return }
        Task {
            errorMessage = nil
            do {
                try await store.purchase(product)
                if store.isSubscribed {
                    purchaseSucceeded.toggle() // fire success haptic
                    dismiss()
                }
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }

    private var ctaTitle: String {
        if let p = selectedProduct, trialBadge(p) != nil { return "Start free trial" }
        return "Continue"
    }

    // MARK: - Selection helpers

    private var selectedProduct: Product? {
        store.products.first { $0.id == selectedID }
    }

    /// Prefer annual (the value pick); otherwise the first loaded plan. Keeps
    /// a valid selection if the list reloads and the old id vanished.
    private func selectDefault() {
        if let id = selectedID, store.products.contains(where: { $0.id == id }) { return }
        let annual = store.products.first(where: isAnnual)
        selectedID = (annual ?? store.products.first)?.id
    }

    // MARK: - Value framing

    private func isAnnual(_ product: Product) -> Bool {
        product.subscription?.subscriptionPeriod.unit == .year
    }

    private func isMonthly(_ product: Product) -> Bool {
        product.subscription?.subscriptionPeriod.unit == .month
    }

    /// Per-plan secondary line. Annual carries the relative-value framing:
    /// the per-month equivalent (annual price ÷ 12) plus a "Save N%" vs
    /// paying monthly×12. Both are computed from Product.price and rendered
    /// in the storefront currency via priceFormatStyle — never hardcoded. If
    /// the monthly plan didn't load, the % is hidden (we can't compute it).
    private func secondaryLine(_ product: Product) -> String? {
        guard isAnnual(product) else { return nil }
        let perMonth = product.price / 12
        let perMonthText = perMonth.formatted(product.priceFormatStyle)
        var line = "\(perMonthText)/mo"
        if let pct = annualSavingsPercent(product) {
            line += " · Save \(pct)%"
        }
        return line
    }

    /// N% saved by buying annual instead of 12× monthly:
    /// 1 − (annualPrice / (monthlyPrice × 12)), rounded to a whole percent.
    /// Returns nil unless both plans loaded and the saving is positive.
    private func annualSavingsPercent(_ annual: Product) -> Int? {
        guard let monthly = store.products.first(where: isMonthly) else { return nil }
        let twelveMonths = monthly.price * 12
        guard twelveMonths > 0, annual.price < twelveMonths else { return nil }
        let saved = (twelveMonths - annual.price) / twelveMonths
        let pct = Int((NSDecimalNumber(decimal: saved).doubleValue * 100).rounded())
        return pct > 0 ? pct : nil
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

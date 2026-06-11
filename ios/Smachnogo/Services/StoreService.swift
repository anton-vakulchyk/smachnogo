import Foundation
import StoreKit

/// StoreKit 2 client: products, purchase, restore, and the server handshake.
/// Trust model: a locally VERIFIED transaction unlocks the camera
/// immediately; the signed JWS is queued to the backend with retry (a paying
/// user must never see a paywall because one POST failed), and
/// currentEntitlements reconcile on every launch.
@MainActor
@Observable
final class StoreService {
    static let shared = StoreService()

    static let monthlyID = "smachnogo.premium.monthly"
    static let annualID = "smachnogo.premium.annual"
    static let productIDs = [monthlyID, annualID]

    private(set) var products: [Product] = []
    private(set) var productsError: String?
    /// True when StoreKit holds a verified, unexpired, unrevoked
    /// subscription transaction on this device.
    private(set) var purchasedLocally = false
    /// Server's view (GET /v1/users/me) — drives the scans-remaining chip.
    private(set) var me: UserMe?
    private(set) var purchasing = false

    /// Camera entitled? Local verification wins immediately; server state
    /// covers restored-on-another-device + webhook-driven transitions.
    var isSubscribed: Bool { purchasedLocally || (me?.subscribed ?? false) }

    private let api = APIClient()
    private var updatesTask: Task<Void, Never>?

    private init() {}

    /// Call once at app launch.
    func start() {
        guard updatesTask == nil else { return }
        updatesTask = Task { [weak self] in
            // Renewals, ask-to-buy completions, revocations — arrive any time.
            for await update in Transaction.updates {
                await self?.handle(update)
            }
        }
        Task { await refresh() }
    }

    func refresh() async {
        await loadProducts()
        await reconcileLocalEntitlements()
        await flushPendingReceipts()
        await refreshServerState()
    }

    func loadProducts() async {
        do {
            products = try await Product.products(for: Self.productIDs).sorted { $0.price < $1.price }
            productsError = products.isEmpty ? "Subscriptions unavailable right now." : nil
        } catch {
            productsError = error.localizedDescription
        }
    }

    func refreshServerState() async {
        me = try? await api.get("/v1/users/me")
    }

    func purchase(_ product: Product) async throws {
        purchasing = true
        defer { purchasing = false }
        // appAccountToken binds the Apple transaction to our user id so
        // server notifications attribute without guesswork.
        var options: Set<Product.PurchaseOption> = []
        if let uid = await sharedTokenProvider.userID(), let uuid = UUID(uuidString: uid) {
            options.insert(.appAccountToken(uuid))
        }
        let result = try await product.purchase(options: options)
        switch result {
        case .success(let verification):
            await handle(verification)
        case .pending, .userCancelled:
            break
        @unknown default:
            break
        }
    }

    func restore() async {
        try? await AppStore.sync()
        await reconcileLocalEntitlements()
        await flushPendingReceipts()
        await refreshServerState()
    }

    private func handle(_ verification: VerificationResult<Transaction>) async {
        guard case .verified(let txn) = verification else { return }
        guard Self.productIDs.contains(txn.productID) else { await txn.finish(); return }
        if txn.revocationDate == nil, (txn.expirationDate ?? .distantFuture) > Date() {
            purchasedLocally = true // unlock NOW — server sync follows
            PendingScanQueue.shared.retryPaywalled()
        } else {
            purchasedLocally = false
        }
        enqueueReceipt(verification.jwsRepresentation)
        await txn.finish()
        await flushPendingReceipts()
        await refreshServerState()
    }

    private func reconcileLocalEntitlements() async {
        var entitled = false
        for await result in Transaction.currentEntitlements {
            guard case .verified(let txn) = result,
                  Self.productIDs.contains(txn.productID),
                  txn.revocationDate == nil,
                  (txn.expirationDate ?? .distantFuture) > Date() else { continue }
            entitled = true
            enqueueReceipt(result.jwsRepresentation)
        }
        purchasedLocally = entitled
    }

    // MARK: - Pending receipt queue (same crash-safety idea as PendingScanQueue)

    private static var receiptsDir: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        let d = base.appendingPathComponent("PendingReceipts", isDirectory: true)
        try? FileManager.default.createDirectory(at: d, withIntermediateDirectories: true)
        return d
    }

    private func enqueueReceipt(_ jws: String) {
        // Filename = hash of contents: the same transaction re-enqueued on
        // every launch reconcile stays ONE pending file.
        let name = String(format: "%08x", jws.hashValue) + ".jws"
        let url = Self.receiptsDir.appendingPathComponent(name)
        try? jws.data(using: .utf8)?.write(to: url)
    }

    private struct ReceiptResponse: Codable { var entitlement: String }

    func flushPendingReceipts() async {
        guard let files = try? FileManager.default.contentsOfDirectory(at: Self.receiptsDir, includingPropertiesForKeys: nil) else { return }
        for file in files {
            guard let jws = try? String(contentsOf: file, encoding: .utf8) else {
                try? FileManager.default.removeItem(at: file)
                continue
            }
            do {
                let _: ReceiptResponse = try await api.post("/v1/subscriptions/receipt", body: ["jws_representation": jws])
                try? FileManager.default.removeItem(at: file)
            } catch {
                if case APIError.transport = error {
                    return // offline — keep the file, retry next launch/refresh
                }
                // Server rejected (bad/foreign JWS) — dropping prevents an
                // infinite re-post loop; local entitlement is unaffected.
                try? FileManager.default.removeItem(at: file)
            }
        }
    }
}

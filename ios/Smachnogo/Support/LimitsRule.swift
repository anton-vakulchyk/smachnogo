import SwiftUI

/// M9 limit-coloring rules — the ONE place for thresholds (plan: "one
/// tunable constant, documented here"):
///   day    → red iff ANY set limit is exceeded, green iff all respected,
///            neutral when nothing logged (or no limits set)
///   period → over LOGGED days only: green when ≥80% are green, red when
///            <50% are green, neutral between (and when nothing logged)
enum LimitsRule {
    static let periodGreenShare = 0.8
    static let periodRedShare = 0.5

    enum Status {
        case green, red, neutral
    }

    /// One editable spec per cap-sensible field. Wire keys match the
    /// backend's LimitableFields / summary bucket field names exactly.
    struct Spec: Identifiable {
        let key: String
        let name: String
        let unit: String
        let typical: Double // prefilled suggestion when enabling
        let value: (Nutrients) -> Double
        var id: String { key }
    }

    /// UI catalog: caps only ("at least N protein" floors are a later
    /// enhancement). Order = display order.
    static let specs: [Spec] = [
        Spec(key: "calories_kcal", name: "Calories", unit: "kcal", typical: 2000) { Double($0.caloriesKcal) },
        Spec(key: "sugar_g", name: "Sugar", unit: "g", typical: 50) { $0.sugarG },
        Spec(key: "sodium_mg", name: "Sodium", unit: "mg", typical: 2300) { $0.sodiumMg },
        Spec(key: "saturated_fat_g", name: "Sat. fat", unit: "g", typical: 20) { $0.saturatedFatG },
        Spec(key: "carbs_g", name: "Carbs", unit: "g", typical: 250) { $0.carbsG },
        Spec(key: "fat_g", name: "Fat", unit: "g", typical: 80) { $0.fatG },
    ]

    private static let valueFor: [String: (Nutrients) -> Double] = {
        var m: [String: (Nutrients) -> Double] = [:]
        for s in specs { m[s.key] = s.value }
        // Limitable server-side but not in the editor UI — still colorable
        // if ever set (e.g. via a future floor editor).
        m["protein_g"] = { $0.proteinG }
        m["fiber_g"] = { $0.fiberG }
        m["iron_mg"] = { $0.ironMg }
        m["calcium_mg"] = { $0.calciumMg }
        m["omega3_g"] = { $0.omega3G }
        return m
    }()

    static func dayStatus(_ bucket: SummaryBucket?, limits: [String: Double]) -> Status {
        guard let bucket, bucket.mealCount > 0, !limits.isEmpty else { return .neutral }
        for (key, cap) in limits {
            if let value = valueFor[key]?(bucket.nutrients), value > cap {
                return .red
            }
        }
        return .green
    }

    static func periodStatus(_ days: [Status]) -> Status {
        let colored = days.filter { $0 != .neutral }
        guard !colored.isEmpty else { return .neutral }
        let greenShare = Double(colored.filter { $0 == .green }.count) / Double(colored.count)
        if greenShare >= periodGreenShare { return .green }
        if greenShare < periodRedShare { return .red }
        return .neutral
    }
}

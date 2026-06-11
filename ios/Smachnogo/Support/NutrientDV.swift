import SwiftUI

/// Fixed adult daily-reference values (FDA DVs; omega-3 uses the common
/// ~1.5g AI). Mirror of backend pkg/models/nutrients.go. We collect no
/// profile data by design — these are general references shown as
/// low/ok/high bands, never lab-precise targets.
enum NutrientDV {
    enum Direction {
        case moreIsBetter // fiber, iron, calcium, omega-3 (target)
        case lessIsBetter // sugar, sodium, saturated fat (ceiling)
    }

    enum Band {
        case low, ok, high

        var color: Color {
            switch self {
            case .ok: return .green
            case .low: return .orange
            case .high: return .red
            }
        }
    }

    struct Spec: Identifiable {
        let name: String
        let unit: String
        let dv: Double
        let direction: Direction
        let value: (Nutrients) -> Double

        var id: String { name }
    }

    static let all: [Spec] = [
        Spec(name: "Fiber", unit: "g", dv: 28, direction: .moreIsBetter) { $0.fiberG },
        Spec(name: "Sugar", unit: "g", dv: 50, direction: .lessIsBetter) { $0.sugarG },
        Spec(name: "Sodium", unit: "mg", dv: 2300, direction: .lessIsBetter) { $0.sodiumMg },
        Spec(name: "Sat. fat", unit: "g", dv: 20, direction: .lessIsBetter) { $0.saturatedFatG },
        Spec(name: "Iron", unit: "mg", dv: 18, direction: .moreIsBetter) { $0.ironMg },
        Spec(name: "Calcium", unit: "mg", dv: 1300, direction: .moreIsBetter) { $0.calciumMg },
        Spec(name: "Omega-3", unit: "g", dv: 1.5, direction: .moreIsBetter) { $0.omega3G },
    ]

    /// Band for a PER-DAY average intake vs the daily reference.
    static func band(_ spec: Spec, perDay: Double) -> Band {
        let ratio = perDay / spec.dv
        switch spec.direction {
        case .moreIsBetter:
            if ratio >= 0.7 { return .ok }
            return .low
        case .lessIsBetter:
            if ratio <= 1.0 { return .ok }
            return .high
        }
    }
}

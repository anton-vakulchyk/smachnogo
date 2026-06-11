import Foundation

struct SummaryBucket: Codable, Identifiable {
    var key: String
    var nutrients: Nutrients
    var nutritionScore: Int
    var dietQualityScore: Int
    var mealCount: Int
    var daysLogged: Int

    var id: String { key }

    enum CodingKeys: String, CodingKey {
        case key
        case nutritionScore = "nutrition_score"
        case dietQualityScore = "diet_quality_score"
        case mealCount = "meal_count"
        case daysLogged = "days_logged"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        key = try c.decode(String.self, forKey: .key)
        nutrients = try Nutrients(from: decoder)
        nutritionScore = try c.decode(Int.self, forKey: .nutritionScore)
        dietQualityScore = try c.decode(Int.self, forKey: .dietQualityScore)
        mealCount = try c.decode(Int.self, forKey: .mealCount)
        daysLogged = try c.decode(Int.self, forKey: .daysLogged)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(key, forKey: .key)
        try nutrients.encode(to: encoder)
        try c.encode(nutritionScore, forKey: .nutritionScore)
        try c.encode(dietQualityScore, forKey: .dietQualityScore)
        try c.encode(mealCount, forKey: .mealCount)
        try c.encode(daysLogged, forKey: .daysLogged)
    }
}

struct SummaryResult: Codable {
    var granularity: String
    var buckets: [SummaryBucket]
    var totals: SummaryBucket
}

struct MealService: Sendable {
    var api = APIClient()

    func meals(from: String, to: String) async throws -> [Meal] {
        let resp: MealsResponse = try await api.get("/v1/meals", query: [
            URLQueryItem(name: "from", value: from),
            URLQueryItem(name: "to", value: to),
        ])
        return resp.meals
    }

    func meals(on date: String) async throws -> [Meal] {
        try await meals(from: date, to: date)
    }

    struct PatchRequest: Codable {
        var label: String?
        var state: String?
        var consumedAt: String?
        var portionFactor: Double?
        var newDate: String?
        enum CodingKeys: String, CodingKey {
            case label, state
            case consumedAt = "consumed_at"
            case portionFactor = "portion_factor"
            case newDate = "new_date"
        }
    }

    struct MealEnvelope: Codable { var meal: Meal }

    func patch(mealId: String, date: String, _ req: PatchRequest) async throws -> Meal {
        let resp: MealEnvelope = try await api.request(
            "/v1/meals/\(mealId)", method: "PATCH",
            query: [URLQueryItem(name: "date", value: date)], body: req)
        return resp.meal
    }

    func delete(mealId: String, date: String) async throws {
        try await api.requestVoid("/v1/meals/\(mealId)", method: "DELETE",
                                  query: [URLQueryItem(name: "date", value: date)])
    }

    func summary(granularity: String, from: String, to: String) async throws -> SummaryResult {
        try await api.get("/v1/summary", query: [
            URLQueryItem(name: "granularity", value: granularity),
            URLQueryItem(name: "from", value: from),
            URLQueryItem(name: "to", value: to),
        ])
    }

    func exportData() async throws -> Data {
        try await api.rawGet("/v1/export")
    }

    func deleteAccount() async throws {
        try await api.requestVoid("/v1/users/me", method: "DELETE", query: [])
    }
}

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
        var variantIndex: Int?
        var newDate: String?
        enum CodingKeys: String, CodingKey {
            case label, state
            case consumedAt = "consumed_at"
            case portionFactor = "portion_factor"
            case variantIndex = "variant_index"
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

    struct LimitsEnvelope: Codable { var limits: [String: Double] }

    /// Replace-semantics: always sends the full map; {} clears everything.
    func updateLimits(_ limits: [String: Double]) async throws {
        let _: LimitsEnvelope = try await api.request(
            "/v1/users/me", method: "PATCH", body: ["limits": limits])
    }

    func estimate(text: String) async throws -> EstimateResponse {
        try await api.post("/v1/meals/estimate", body: ["text": text])
    }

    func recent(limit: Int = 20) async throws -> [Meal] {
        let resp: MealsResponse = try await api.get("/v1/meals/recent", query: [
            URLQueryItem(name: "limit", value: String(limit)),
        ])
        return resp.meals
    }

    struct CreateMealRequest: Encodable {
        var mealId: String
        var date: String
        var state: String
        var consumedAt: String
        var label: String
        var source: String
        var nutrients: Nutrients
        var nutritionScore: Int
        var dietQualityScore: Int
        var components: [EstimateItem]

        enum CodingKeys: String, CodingKey {
            case mealId = "meal_id"
            case date, state, label, source, components
            case consumedAt = "consumed_at"
            case nutritionScore = "nutrition_score"
            case dietQualityScore = "diet_quality_score"
        }

        func encode(to encoder: Encoder) throws {
            var c = encoder.container(keyedBy: CodingKeys.self)
            try c.encode(mealId, forKey: .mealId)
            try c.encode(date, forKey: .date)
            try c.encode(state, forKey: .state)
            try c.encode(consumedAt, forKey: .consumedAt)
            try c.encode(label, forKey: .label)
            try c.encode(source, forKey: .source)
            try nutrients.encode(to: encoder)
            try c.encode(nutritionScore, forKey: .nutritionScore)
            try c.encode(dietQualityScore, forKey: .dietQualityScore)
            try c.encode(components, forKey: .components)
        }
    }

    @discardableResult
    func create(_ req: CreateMealRequest) async throws -> Meal {
        let resp: MealEnvelope = try await api.post("/v1/meals", body: req)
        return resp.meal
    }

    /// One-gesture re-log: copy a past meal to today with a fresh id.
    @discardableResult
    func logAgainToday(_ meal: Meal) async throws -> Meal {
        try await create(CreateMealRequest(
            mealId: UUID().uuidString.lowercased(),
            date: DateUtil.dayString(),
            state: "logged",
            consumedAt: ISO8601DateFormatter().string(from: Date()),
            label: meal.label,
            source: "readd",
            nutrients: meal.nutrients,
            nutritionScore: meal.nutritionScore,
            dietQualityScore: meal.dietQualityScore,
            components: []
        ))
    }
}

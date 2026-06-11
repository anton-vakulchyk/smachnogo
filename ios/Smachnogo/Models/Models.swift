import Foundation

// Wire models matching the Go backend exactly. Explicit CodingKeys, no
// convertFromSnakeCase magic. Numerics are NON-optional — the backend never
// omits them (no omitempty on numerics, by contract).

struct Nutrients: Codable, Equatable {
    var caloriesKcal: Int
    var proteinG: Double
    var fatG: Double
    var carbsG: Double
    var fiberG: Double
    var sugarG: Double
    var sodiumMg: Double
    var saturatedFatG: Double
    var ironMg: Double
    var calciumMg: Double
    var omega3G: Double

    enum CodingKeys: String, CodingKey {
        case caloriesKcal = "calories_kcal"
        case proteinG = "protein_g"
        case fatG = "fat_g"
        case carbsG = "carbs_g"
        case fiberG = "fiber_g"
        case sugarG = "sugar_g"
        case sodiumMg = "sodium_mg"
        case saturatedFatG = "saturated_fat_g"
        case ironMg = "iron_mg"
        case calciumMg = "calcium_mg"
        case omega3G = "omega3_g"
    }

    static let zero = Nutrients(caloriesKcal: 0, proteinG: 0, fatG: 0, carbsG: 0, fiberG: 0,
                                sugarG: 0, sodiumMg: 0, saturatedFatG: 0, ironMg: 0,
                                calciumMg: 0, omega3G: 0)

    static func + (a: Nutrients, b: Nutrients) -> Nutrients {
        Nutrients(caloriesKcal: a.caloriesKcal + b.caloriesKcal,
                  proteinG: a.proteinG + b.proteinG,
                  fatG: a.fatG + b.fatG,
                  carbsG: a.carbsG + b.carbsG,
                  fiberG: a.fiberG + b.fiberG,
                  sugarG: a.sugarG + b.sugarG,
                  sodiumMg: a.sodiumMg + b.sodiumMg,
                  saturatedFatG: a.saturatedFatG + b.saturatedFatG,
                  ironMg: a.ironMg + b.ironMg,
                  calciumMg: a.calciumMg + b.calciumMg,
                  omega3G: a.omega3G + b.omega3G)
    }

    func scaled(_ f: Double) -> Nutrients {
        Nutrients(caloriesKcal: Int((Double(caloriesKcal) * f).rounded()),
                  proteinG: proteinG * f, fatG: fatG * f, carbsG: carbsG * f,
                  fiberG: fiberG * f, sugarG: sugarG * f, sodiumMg: sodiumMg * f,
                  saturatedFatG: saturatedFatG * f, ironMg: ironMg * f,
                  calciumMg: calciumMg * f, omega3G: omega3G * f)
    }
}

struct Dish: Codable, Equatable {
    var label: String
    var description: String
    var portionDesc: String
    var portionG: Int
    var nutrients: Nutrients
    var nutritionScore: Int
    var dietQualityScore: Int
    var confidence: Double
    var needsClarification: Bool
    var clarificationQuestion: String
    var clarificationOptions: [String]

    enum CodingKeys: String, CodingKey {
        case label, description
        case portionDesc = "portion_desc"
        case portionG = "portion_g"
        case nutritionScore = "nutrition_score"
        case dietQualityScore = "diet_quality_score"
        case confidence
        case needsClarification = "needs_clarification"
        case clarificationQuestion = "clarification_question"
        case clarificationOptions = "clarification_options"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        label = try c.decode(String.self, forKey: .label)
        description = try c.decode(String.self, forKey: .description)
        portionDesc = try c.decode(String.self, forKey: .portionDesc)
        portionG = try c.decode(Int.self, forKey: .portionG)
        nutrients = try Nutrients(from: decoder) // flat in JSON, nested in Swift
        nutritionScore = try c.decode(Int.self, forKey: .nutritionScore)
        dietQualityScore = try c.decode(Int.self, forKey: .dietQualityScore)
        confidence = try c.decode(Double.self, forKey: .confidence)
        needsClarification = try c.decode(Bool.self, forKey: .needsClarification)
        clarificationQuestion = try c.decode(String.self, forKey: .clarificationQuestion)
        clarificationOptions = try c.decode([String].self, forKey: .clarificationOptions)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(label, forKey: .label)
        try c.encode(description, forKey: .description)
        try c.encode(portionDesc, forKey: .portionDesc)
        try c.encode(portionG, forKey: .portionG)
        try nutrients.encode(to: encoder)
        try c.encode(nutritionScore, forKey: .nutritionScore)
        try c.encode(dietQualityScore, forKey: .dietQualityScore)
        try c.encode(confidence, forKey: .confidence)
        try c.encode(needsClarification, forKey: .needsClarification)
        try c.encode(clarificationQuestion, forKey: .clarificationQuestion)
        try c.encode(clarificationOptions, forKey: .clarificationOptions)
    }
}

struct PhotoAnalysis: Codable, Equatable {
    var isFood: Bool
    var imageQuality: String
    var dishes: [Dish]

    enum CodingKeys: String, CodingKey {
        case isFood = "is_food"
        case imageQuality = "image_quality"
        case dishes
    }
}

enum ScanStatus: String, Codable {
    case pendingUpload = "PENDING_UPLOAD"
    case queued = "QUEUED"
    case processing = "PROCESSING"
    case ready = "READY"
    case failed = "FAILED"
}

struct ScanJob: Codable {
    var scanId: String
    var status: ScanStatus
    var result: PhotoAnalysis?
    var failureReason: String?

    enum CodingKeys: String, CodingKey {
        case scanId = "scan_id"
        case status, result
        case failureReason = "failure_reason"
    }
}

struct UploadInfo: Codable {
    var url: String
    var method: String
    var headers: [String: String]
}

struct ScanCreateResponse: Codable {
    var scanId: String
    var status: ScanStatus
    var upload: UploadInfo?

    enum CodingKeys: String, CodingKey {
        case scanId = "scan_id"
        case status, upload
    }
}

struct Meal: Codable, Identifiable, Equatable {
    var mealId: String
    var date: String
    var state: String
    var consumedAt: String
    var label: String
    var source: String
    var nutrients: Nutrients
    var nutritionScore: Int
    var dietQualityScore: Int
    var portionFactor: Double
    var refined: Bool
    var refinementAnswer: String?
    var scanId: String?
    var dishIndex: Int?

    var id: String { mealId }

    enum CodingKeys: String, CodingKey {
        case mealId = "meal_id"
        case date, state, label, source, refined
        case consumedAt = "consumed_at"
        case nutritionScore = "nutrition_score"
        case dietQualityScore = "diet_quality_score"
        case portionFactor = "portion_factor"
        case refinementAnswer = "refinement_answer"
        case scanId = "scan_id"
        case dishIndex = "dish_index"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        mealId = try c.decode(String.self, forKey: .mealId)
        date = try c.decode(String.self, forKey: .date)
        state = try c.decode(String.self, forKey: .state)
        consumedAt = try c.decode(String.self, forKey: .consumedAt)
        label = try c.decode(String.self, forKey: .label)
        source = try c.decode(String.self, forKey: .source)
        nutrients = try Nutrients(from: decoder)
        nutritionScore = try c.decode(Int.self, forKey: .nutritionScore)
        dietQualityScore = try c.decode(Int.self, forKey: .dietQualityScore)
        portionFactor = try c.decode(Double.self, forKey: .portionFactor)
        refined = try c.decodeIfPresent(Bool.self, forKey: .refined) ?? false
        refinementAnswer = try c.decodeIfPresent(String.self, forKey: .refinementAnswer)
        scanId = try c.decodeIfPresent(String.self, forKey: .scanId)
        dishIndex = try c.decodeIfPresent(Int.self, forKey: .dishIndex)
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
        try c.encode(portionFactor, forKey: .portionFactor)
        try c.encode(refined, forKey: .refined)
        try c.encodeIfPresent(refinementAnswer, forKey: .refinementAnswer)
        try c.encodeIfPresent(scanId, forKey: .scanId)
        try c.encodeIfPresent(dishIndex, forKey: .dishIndex)
    }
}

struct MealsResponse: Codable {
    var meals: [Meal]
}

struct EstimateItem: Codable, Identifiable {
    var name: String
    var quantityDesc: String
    var nutrients: Nutrients
    var nutritionScore: Int
    var dietQualityScore: Int
    var confidence: Double

    var id: String { name + quantityDesc }

    enum CodingKeys: String, CodingKey {
        case name
        case quantityDesc = "quantity_desc"
        case nutritionScore = "nutrition_score"
        case dietQualityScore = "diet_quality_score"
        case confidence
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        quantityDesc = try c.decode(String.self, forKey: .quantityDesc)
        nutrients = try Nutrients(from: decoder)
        nutritionScore = try c.decode(Int.self, forKey: .nutritionScore)
        dietQualityScore = try c.decode(Int.self, forKey: .dietQualityScore)
        confidence = try c.decode(Double.self, forKey: .confidence)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(name, forKey: .name)
        try c.encode(quantityDesc, forKey: .quantityDesc)
        try nutrients.encode(to: encoder)
        try c.encode(nutritionScore, forKey: .nutritionScore)
        try c.encode(dietQualityScore, forKey: .dietQualityScore)
        try c.encode(confidence, forKey: .confidence)
    }
}

struct EstimateTotals: Codable {
    var nutrients: Nutrients
    var nutritionScore: Int
    var dietQualityScore: Int

    enum CodingKeys: String, CodingKey {
        case nutritionScore = "nutrition_score"
        case dietQualityScore = "diet_quality_score"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        nutrients = try Nutrients(from: decoder)
        nutritionScore = try c.decode(Int.self, forKey: .nutritionScore)
        dietQualityScore = try c.decode(Int.self, forKey: .dietQualityScore)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try nutrients.encode(to: encoder)
        try c.encode(nutritionScore, forKey: .nutritionScore)
        try c.encode(dietQualityScore, forKey: .dietQualityScore)
    }
}

struct EstimateResponse: Codable {
    var isFood: Bool
    var label: String
    var assumptions: String
    var items: [EstimateItem]
    var totals: EstimateTotals

    enum CodingKeys: String, CodingKey {
        case isFood = "is_food"
        case label, assumptions, items, totals
    }
}

/// GET /v1/users/me — billing state behind the scans-remaining indicator.
struct UserMe: Codable {
    var entitlement: String
    var scansRemaining: Int
    var allowanceEndsAt: String?
    var appleLinked: Bool
    var limits: [String: Double]

    enum CodingKeys: String, CodingKey {
        case entitlement
        case scansRemaining = "scans_remaining"
        case allowanceEndsAt = "allowance_ends_at"
        case appleLinked = "apple_linked"
        case limits
    }

    var subscribed: Bool {
        ["trialing", "active", "grace", "billing_retry"].contains(entitlement)
    }

    var allowanceEnds: Date? {
        allowanceEndsAt.flatMap { ISO8601DateFormatter().date(from: $0) }
    }
}

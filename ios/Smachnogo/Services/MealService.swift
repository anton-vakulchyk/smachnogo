import Foundation

struct MealService: Sendable {
    var api = APIClient()

    func meals(on date: String) async throws -> [Meal] {
        let resp: MealsResponse = try await api.get("/v1/meals", query: [
            URLQueryItem(name: "from", value: date),
            URLQueryItem(name: "to", value: date),
        ])
        return resp.meals
    }
}

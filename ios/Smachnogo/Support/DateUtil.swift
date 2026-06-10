import Foundation

/// THE one place that maps Date → "YYYY-MM-DD". Device-local timezone at
/// logging time; the server treats the value as opaque. Changing this rule
/// is a product decision (see plan: deferred "day starts at 4am" setting).
enum DateUtil {
    private static let formatter: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd"
        f.calendar = Calendar.current
        f.timeZone = TimeZone.current
        return f
    }()

    static func dayString(_ date: Date = Date()) -> String {
        formatter.string(from: date)
    }

    static func yesterdayString(relativeTo date: Date = Date()) -> String {
        dayString(Calendar.current.date(byAdding: .day, value: -1, to: date) ?? date)
    }
}

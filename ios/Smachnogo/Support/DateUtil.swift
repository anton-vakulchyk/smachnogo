import Foundation

/// THE one place that maps Date → "YYYY-MM-DD". Device-local timezone at
/// logging time; the server treats the value as opaque. Changing this rule
/// is a product decision (see plan: deferred "day starts at 4am" setting).
enum DateUtil {
    // Use the auto-updating calendar/timezone so the formatter always reflects
    // the LIVE device timezone. A frozen TimeZone.current/Calendar.current
    // snapshot would stamp meals on the wrong calendar day after the user
    // crosses a timezone (until the app is force-killed).
    private static let formatter: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd"
        f.calendar = Calendar.autoupdatingCurrent
        f.timeZone = TimeZone.autoupdatingCurrent
        return f
    }()

    static func dayString(_ date: Date = Date()) -> String {
        formatter.string(from: date)
    }

    static func yesterdayString(relativeTo date: Date = Date()) -> String {
        dayString(Calendar.current.date(byAdding: .day, value: -1, to: date) ?? date)
    }
}

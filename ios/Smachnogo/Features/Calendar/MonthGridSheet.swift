import SwiftUI

/// Full month calendar as the any-day picker. Dots mark days with logged
/// meals (from the day-granularity summary); M9 recolors them from limits.
struct MonthGridSheet: View {
    @Binding var selectedDate: Date
    @Environment(\.dismiss) private var dismiss

    @State private var visibleMonth: Date = Date()
    @State private var dayBuckets: [String: SummaryBucket] = [:]

    private let service = MealService()
    private let cal = Calendar.current
    private var limits: [String: Double] { StoreService.shared.me?.limits ?? [:] }

    var body: some View {
        NavigationStack {
            VStack(spacing: 12) {
                monthHeader
                weekdayHeader
                grid
                legend
                Spacer()
            }
            .padding()
            .navigationTitle("Pick a day")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close") { dismiss() }
                }
            }
        }
        .task(id: monthKey(visibleMonth)) { await loadDots() }
        .onAppear { visibleMonth = selectedDate }
    }

    private var monthHeader: some View {
        HStack {
            Button { shiftMonth(-1) } label: { Image(systemName: "chevron.left") }
            Spacer()
            Text(visibleMonth.formatted(.dateTime.month(.wide).year()))
                .font(.headline)
            Spacer()
            Button { shiftMonth(1) } label: { Image(systemName: "chevron.right") }
        }
        .buttonStyle(.plain)
    }

    private var weekdayHeader: some View {
        // ISO Mon–Sun, matching the backend's week buckets.
        HStack {
            ForEach(["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"], id: \.self) { d in
                Text(d).font(.caption2).foregroundStyle(.secondary).frame(maxWidth: .infinity)
            }
        }
    }

    /// Tiny key for the day markers. When limits are set the dots take a
    /// within/over verdict (shape + color); otherwise a plain dot just marks
    /// a logged day. Spelled out so the marks aren't a mystery.
    @ViewBuilder
    private var legend: some View {
        Group {
            if limits.isEmpty {
                HStack(spacing: 6) {
                    Circle().fill(Color.accentColor).frame(width: 7, height: 7)
                    Text("Day with a logged meal")
                    Text("· Set limits in Settings to color days")
                        .foregroundStyle(.tertiary)
                }
            } else {
                HStack(spacing: 12) {
                    legendItem(systemImage: "checkmark.circle.fill", color: .green, text: "Within limits")
                    legendItem(systemImage: "exclamationmark.circle.fill", color: .red, text: "Over a limit")
                    HStack(spacing: 5) {
                        Circle().fill(Color.accentColor).frame(width: 7, height: 7)
                        Text("Logged")
                    }
                }
            }
        }
        .font(.caption2)
        .foregroundStyle(.secondary)
        .frame(maxWidth: .infinity)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(limits.isEmpty
            ? "Legend: a dot marks a day with a logged meal. Set limits in Settings to color days within or over your limits."
            : "Legend: a check means a logged day within your limits, an exclamation mark means a day over a limit, and a plain dot means a logged day.")
    }

    private func legendItem(systemImage: String, color: Color, text: String) -> some View {
        HStack(spacing: 5) {
            Image(systemName: systemImage).foregroundStyle(color)
            Text(text)
        }
    }

    private var grid: some View {
        let days = monthDays()
        return LazyVGrid(columns: Array(repeating: GridItem(.flexible()), count: 7), spacing: 8) {
            ForEach(days.indices, id: \.self) { i in
                if let day = days[i] {
                    dayCell(day)
                } else {
                    Color.clear.frame(height: 40)
                }
            }
        }
    }

    @ViewBuilder
    private func dayCell(_ day: Date) -> some View {
        let key = DateUtil.dayString(day)
        let isSelected = cal.isDate(day, inSameDayAs: selectedDate)
        let isToday = cal.isDateInToday(day)
        let logged = dayBuckets[key] != nil
        let status = dayStatus(key)
        Button {
            selectedDate = day
            dismiss()
        } label: {
            VStack(spacing: 3) {
                Text("\(cal.component(.day, from: day))")
                    .font(.callout.weight(isToday ? .bold : .regular))
                    .foregroundStyle(isSelected ? Color.white : (isToday ? Color.accentColor : .primary))
                dayMarker(logged: logged, status: status)
            }
            .frame(maxWidth: .infinity, minHeight: 40)
            .background(isSelected ? Color.accentColor : Color.clear, in: RoundedRectangle(cornerRadius: 10))
        }
        .buttonStyle(.plain)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(dayAccessibilityLabel(day, logged: logged, status: status))
        .accessibilityAddTraits(isSelected ? [.isButton, .isSelected] : .isButton)
    }

    /// Logged-day marker. Symbol-coded (not hue alone) when limits color the
    /// day: a check for within-limits, an alert glyph for over. A plain dot
    /// marks a logged day with no limits set. Slightly larger than before for
    /// visibility. Empty (clear) when nothing was logged.
    @ViewBuilder
    private func dayMarker(logged: Bool, status: LimitsRule.Status) -> some View {
        switch status {
        case .green:
            Image(systemName: "checkmark.circle.fill")
                .font(.system(size: 9))
                .foregroundStyle(.green)
        case .red:
            Image(systemName: "exclamationmark.circle.fill")
                .font(.system(size: 9))
                .foregroundStyle(.red)
        case .neutral:
            Circle()
                .fill(logged ? Color.accentColor : Color.clear)
                .frame(width: 7, height: 7)
        }
    }

    /// No log → neutral (no dot). Logged → neutral (accent) unless the user's
    /// limits color it green/red (M9: client-side over the same day buckets).
    private func dayStatus(_ key: String) -> LimitsRule.Status {
        guard let bucket = dayBuckets[key] else { return .neutral }
        return LimitsRule.dayStatus(bucket, limits: limits)
    }

    /// VoiceOver sentence for a day cell, e.g. "June 12, logged, within limits".
    private func dayAccessibilityLabel(_ day: Date, logged: Bool, status: LimitsRule.Status) -> String {
        let date = day.formatted(.dateTime.month(.wide).day())
        guard logged else { return "\(date), not logged" }
        switch status {
        case .green: return "\(date), logged, within limits"
        case .red: return "\(date), logged, over a limit"
        case .neutral: return "\(date), logged"
        }
    }

    // MARK: - Data

    private func loadDots() async {
        guard let interval = cal.dateInterval(of: .month, for: visibleMonth) else { return }
        let from = DateUtil.dayString(interval.start)
        let to = DateUtil.dayString(interval.end.addingTimeInterval(-1))
        if let result = try? await service.summary(granularity: "day", from: from, to: to) {
            dayBuckets = Dictionary(uniqueKeysWithValues: result.buckets.map { ($0.key, $0) })
        }
    }

    // MARK: - Layout math

    private func shiftMonth(_ delta: Int) {
        visibleMonth = cal.date(byAdding: .month, value: delta, to: visibleMonth) ?? visibleMonth
    }

    private func monthKey(_ d: Date) -> String {
        d.formatted(.dateTime.year().month(.twoDigits))
    }

    /// Month days padded with nils so column 0 is always Monday.
    private func monthDays() -> [Date?] {
        guard let interval = cal.dateInterval(of: .month, for: visibleMonth) else { return [] }
        let first = interval.start
        let count = cal.range(of: .day, in: .month, for: visibleMonth)?.count ?? 30
        let firstWeekday = cal.component(.weekday, from: first) // Sunday=1
        let leadingBlanks = (firstWeekday + 5) % 7              // days since Monday
        var out: [Date?] = Array(repeating: nil, count: leadingBlanks)
        for d in 0..<count {
            out.append(cal.date(byAdding: .day, value: d, to: first))
        }
        return out
    }
}

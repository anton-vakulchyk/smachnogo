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
        Button {
            selectedDate = day
            dismiss()
        } label: {
            VStack(spacing: 3) {
                Text("\(cal.component(.day, from: day))")
                    .font(.callout.weight(isToday ? .bold : .regular))
                    .foregroundStyle(isSelected ? Color.white : (isToday ? Color.accentColor : .primary))
                Circle()
                    .fill(dotColor(key))
                    .frame(width: 5, height: 5)
            }
            .frame(maxWidth: .infinity, minHeight: 40)
            .background(isSelected ? Color.accentColor : Color.clear, in: RoundedRectangle(cornerRadius: 10))
        }
        .buttonStyle(.plain)
    }

    /// No log → no dot. Logged → accent, or green/red against the user's
    /// limits (M9: pure client-side mapping over the same day buckets).
    private func dotColor(_ key: String) -> Color {
        guard let bucket = dayBuckets[key] else { return .clear }
        switch LimitsRule.dayStatus(bucket, limits: limits) {
        case .green: return .green
        case .red: return .red
        case .neutral: return .accentColor
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

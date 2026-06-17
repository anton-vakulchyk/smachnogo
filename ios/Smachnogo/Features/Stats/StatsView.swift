import SwiftUI
import Charts

/// The aggregate home: Day | Week | Month with a period pager. Day is
/// instant (single-day summary); week/month fold on the server (brief
/// first-load spinner is fine by design).
struct StatsView: View {
    enum Granularity: String, CaseIterable, Identifiable {
        case day = "Day", week = "Week", month = "Month"
        var id: String { rawValue }
    }

    @State private var granularity: Granularity = .week
    @State private var anchor = Date() // any date inside the visible period
    @State private var result: SummaryResult?
    @State private var loading = false
    @State private var errorText: String?

    private let service = MealService()
    private let cal = Calendar.current
    private var limits: [String: Double] { StoreService.shared.me?.limits ?? [:] }

    /// Per-day limit status for the loaded buckets (empty when no limits).
    private var dayStatuses: [String: LimitsRule.Status] {
        guard !limits.isEmpty, let result else { return [:] }
        return Dictionary(uniqueKeysWithValues: result.buckets.map {
            ($0.key, LimitsRule.dayStatus($0, limits: limits))
        })
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 16) {
                    Picker("Period", selection: $granularity) {
                        ForEach(Granularity.allCases) { g in Text(g.rawValue).tag(g) }
                    }
                    .pickerStyle(.segmented)

                    periodPager

                    if loading && result == nil {
                        ProgressView().padding(.vertical, 40)
                    } else if let result {
                        if result.totals.mealCount == 0 {
                            ContentUnavailableView {
                                Label("Nothing logged", systemImage: "chart.bar")
                            } description: {
                                Text("Meals you log in this period show up here.")
                            }
                            .frame(minHeight: 240)
                        } else {
                            totalsCard(result)
                            if granularity != .day {
                                chartCard(result)
                            }
                            scoresCard(result)
                            nutritionCard(result)
                        }
                    }
                    if let errorText {
                        Text(errorText).font(.footnote).foregroundStyle(.red)
                    }
                }
                .padding()
            }
            .navigationTitle("Stats")
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    SettingsButton { Task { await load() } }
                }
            }
        }
        .task(id: "\(granularity.rawValue)-\(DateUtil.dayString(anchor))") { await load() }
    }

    // MARK: - Period navigation

    private var periodPager: some View {
        HStack {
            Button { shift(-1) } label: { Image(systemName: "chevron.left") }
            Spacer()
            HStack(spacing: 6) {
                Text(periodTitle).font(.headline)
                if granularity != .day, periodLimitStatus != .neutral {
                    Circle()
                        .fill(periodLimitStatus == .green ? Color.green : Color.red)
                        .frame(width: 8, height: 8)
                }
            }
            Spacer()
            Button { shift(1) } label: { Image(systemName: "chevron.right") }
                .disabled(periodRange.to >= DateUtil.dayString())
        }
        .buttonStyle(.plain)
        .padding(.horizontal, 4)
    }

    /// Week/month verdict against limits (≥80% of logged days green → green,
    /// <50% → red — LimitsRule owns the thresholds).
    private var periodLimitStatus: LimitsRule.Status {
        LimitsRule.periodStatus(Array(dayStatuses.values))
    }

    private func shift(_ delta: Int) {
        switch granularity {
        case .day: anchor = cal.date(byAdding: .day, value: delta, to: anchor) ?? anchor
        case .week: anchor = cal.date(byAdding: .day, value: 7 * delta, to: anchor) ?? anchor
        case .month: anchor = cal.date(byAdding: .month, value: delta, to: anchor) ?? anchor
        }
    }

    private var periodTitle: String {
        switch granularity {
        case .day:
            return cal.isDateInToday(anchor) ? "Today" : anchor.formatted(.dateTime.weekday().day().month())
        case .week:
            let r = periodRange
            return "\(r.from.suffix(5)) – \(r.to.suffix(5))"
        case .month:
            return anchor.formatted(.dateTime.month(.wide).year())
        }
    }

    /// from/to (YYYY-MM-DD) for the visible period. Weeks are ISO Mon–Sun,
    /// matching the backend's buckets.
    private var periodRange: (from: String, to: String) {
        switch granularity {
        case .day:
            let d = DateUtil.dayString(anchor)
            return (d, d)
        case .week:
            let wd = (cal.component(.weekday, from: anchor) + 5) % 7 // days since Monday
            let monday = cal.date(byAdding: .day, value: -wd, to: anchor) ?? anchor
            let sunday = cal.date(byAdding: .day, value: 6, to: monday) ?? anchor
            return (DateUtil.dayString(monday), DateUtil.dayString(sunday))
        case .month:
            let interval = cal.dateInterval(of: .month, for: anchor)!
            return (DateUtil.dayString(interval.start),
                    DateUtil.dayString(interval.end.addingTimeInterval(-1)))
        }
    }

    private func load() async {
        loading = true
        defer { loading = false }
        let r = periodRange
        do {
            // Stats always fold by day; week/month grouping is visual.
            result = try await service.summary(granularity: "day", from: r.from, to: r.to)
            errorText = nil
        } catch {
            errorText = error.localizedDescription
        }
    }

    // MARK: - Cards

    private func card<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(title).font(.headline)
            content()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding()
        .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 14))
    }

    private func totalsCard(_ r: SummaryResult) -> some View {
        let days = max(r.totals.daysLogged, 1)
        let avg = granularity == .day ? 1 : days
        return card(granularity == .day ? "Totals" : "Daily average (\(days) day\(days == 1 ? "" : "s") logged)") {
            HStack {
                stat("\(r.totals.nutrients.caloriesKcal / avg)", "kcal")
                stat("\(Int(r.totals.nutrients.proteinG) / avg)g", "protein")
                stat("\(Int(r.totals.nutrients.fatG) / avg)g", "fat")
                stat("\(Int(r.totals.nutrients.carbsG) / avg)g", "carbs")
            }
        }
    }

    private func chartCard(_ r: SummaryResult) -> some View {
        card("Calories per day") {
            Chart(r.buckets) { bucket in
                BarMark(
                    x: .value("Day", String(bucket.key.suffix(5))),
                    y: .value("kcal", bucket.nutrients.caloriesKcal)
                )
                .foregroundStyle(barColor(bucket).gradient)
            }
            .frame(height: 180)
            if !limits.isEmpty {
                limitsCaption
            }
        }
    }

    /// Bars take the day's limit verdict when limits exist; plain accent
    /// otherwise — the chart IS the calendar coloring at week/month zoom.
    private func barColor(_ bucket: SummaryBucket) -> Color {
        switch dayStatuses[bucket.key] {
        case .green: return .green
        case .red: return .red
        default: return .accentColor
        }
    }

    private var limitsCaption: some View {
        let statuses = Array(dayStatuses.values).filter { $0 != .neutral }
        let green = statuses.filter { $0 == .green }.count
        return Text("\(green) of \(statuses.count) logged day\(statuses.count == 1 ? "" : "s") within limits")
            .font(.caption)
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, alignment: .center)
    }

    private func scoresCard(_ r: SummaryResult) -> some View {
        card("Quality") {
            HStack(spacing: 24) {
                scoreRing("Nutrition", r.totals.nutritionScore)
                scoreRing("Diet quality", r.totals.dietQualityScore)
            }
            .frame(maxWidth: .infinity)
        }
    }

    private func scoreRing(_ name: String, _ score: Int) -> some View {
        VStack(spacing: 6) {
            ZStack {
                Circle().stroke(.quaternary, lineWidth: 7)
                Circle()
                    .trim(from: 0, to: Double(score) / 100)
                    .stroke(scoreColor(score), style: StrokeStyle(lineWidth: 7, lineCap: .round))
                    .rotationEffect(.degrees(-90))
                Text("\(score)").font(.headline)
            }
            .frame(width: 64, height: 64)
            Text(name).font(.caption).foregroundStyle(.secondary)
        }
    }

    private func scoreColor(_ s: Int) -> Color {
        s >= 70 ? .green : (s >= 45 ? .orange : .red)
    }

    /// The 7 curated micros vs daily reference, as low/ok/high bands on the
    /// PER-DAY average. Directional honesty: these are AI estimates.
    private func nutritionCard(_ r: SummaryResult) -> some View {
        let days = Double(max(r.totals.daysLogged, 1))
        return card("Nutrition vs daily reference") {
            VStack(spacing: 8) {
                ForEach(NutrientDV.all) { spec in
                    let perDay = spec.value(r.totals.nutrients) / days
                    let band = NutrientDV.band(spec, perDay: perDay)
                    HStack {
                        Text(spec.name).font(.subheadline).frame(width: 70, alignment: .leading)
                        GeometryReader { geo in
                            ZStack(alignment: .leading) {
                                Capsule().fill(.quaternary).frame(height: 6)
                                Capsule().fill(band.color)
                                    .frame(width: min(1.0, perDay / spec.dv) * geo.size.width, height: 6)
                            }
                            .frame(maxHeight: .infinity)
                        }
                        .frame(height: 14)
                        Text(valueLabel(perDay, spec))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .frame(width: 92, alignment: .trailing)
                    }
                }
                Text("General adult reference values · AI estimates, not medical advice")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                    .frame(maxWidth: .infinity, alignment: .center)
                    .padding(.top, 4)
            }
        }
    }

    private func valueLabel(_ v: Double, _ spec: NutrientDV.Spec) -> String {
        let val = v < 10 ? String(format: "%.1f", v) : String(format: "%.0f", v)
        let dv = spec.dv < 10 ? String(format: "%.1f", spec.dv) : String(format: "%.0f", spec.dv)
        return "\(val)/\(dv)\(spec.unit)"
    }

    private func stat(_ value: String, _ name: String) -> some View {
        VStack {
            Text(value).font(.headline)
            Text(name).font(.caption2).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
    }
}

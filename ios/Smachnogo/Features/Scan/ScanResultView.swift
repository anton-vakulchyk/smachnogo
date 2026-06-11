import SwiftUI
import UIKit

/// Dish selection + portion + date → confirm. All dishes pre-selected;
/// the estimate is always saveable as-is (clarification chips arrive M4 —
/// fields are parsed but not rendered; the assumption shows via description).
struct ScanResultView: View {
    let scanId: String
    let analysis: PhotoAnalysis
    let image: UIImage
    let onSaved: ([Meal]) -> Void

    @State private var dishes: [Dish] // refined dishes replace entries in place
    @State private var selected: Set<Int>
    @State private var portions: [Int: Double] = [:]
    @State private var date: Date
    @State private var saving = false
    @State private var saveError: String?
    @State private var refining: Set<Int> = []
    @State private var freeTextAnswer: [Int: String] = [:]
    @State private var lastTimeAnswers: [String: String] = [:] // lowercased label → past refinement answer

    private static let portionChoices: [(String, Double)] = [
        ("¼", 0.25), ("⅓", 1.0 / 3.0), ("½", 0.5), ("¾", 0.75), ("1", 1.0), ("1½", 1.5), ("2", 2.0),
    ]

    init(scanId: String, analysis: PhotoAnalysis, image: UIImage, suggestedDate: Date? = nil, onSaved: @escaping ([Meal]) -> Void) {
        self.scanId = scanId
        self.analysis = analysis
        self.image = image
        self.onSaved = onSaved
        _dishes = State(initialValue: analysis.dishes)
        _selected = State(initialValue: Set(analysis.dishes.indices))
        _date = State(initialValue: suggestedDate ?? Date())
    }

    var body: some View {
        List {
            if analysis.imageQuality != "good" {
                Section {
                    Label(retakeHint, systemImage: "camera.badge.ellipsis")
                        .font(.footnote)
                        .foregroundStyle(.orange)
                }
            }

            Section {
                ForEach(dishes.indices, id: \.self) { i in
                    dishRow(i)
                }
            } header: {
                Text(dishes.count > 1 ? "Which dishes are yours?" : "Your meal")
            }

            Section {
                DatePicker("When", selection: $date, displayedComponents: [.date, .hourAndMinute])
                Button("Yesterday evening") {
                    let cal = Calendar.current
                    if let y = cal.date(byAdding: .day, value: -1, to: Date()) {
                        date = cal.date(bySettingHour: 19, minute: 0, second: 0, of: y) ?? y
                    }
                }
                .font(.footnote)
            }

            Section {
                totalsRow
            }
        }
        .task {
            // "Same as last time": map past refined meals' labels to their
            // recorded answers (meals are the durable copy — scans TTL out).
            guard dishes.contains(where: { $0.needsClarification }) else { return }
            if let recents = try? await MealService().recent(limit: 50) {
                for m in recents where m.refined && !(m.refinementAnswer ?? "").isEmpty {
                    lastTimeAnswers[m.label.lowercased()] = m.refinementAnswer
                }
            }
        }
        .safeAreaInset(edge: .bottom) {
            VStack(spacing: 8) {
                if let saveError {
                    Text(saveError).font(.footnote).foregroundStyle(.red)
                }
                Button(action: save) {
                    if saving {
                        ProgressView().frame(maxWidth: .infinity)
                    } else {
                        Text(selected.isEmpty ? "Select a dish" : "Save \(selected.count == 1 ? "meal" : "\(selected.count) meals")")
                            .frame(maxWidth: .infinity)
                    }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(selected.isEmpty || saving)
            }
            .padding()
            .background(.bar)
        }
    }

    private var retakeHint: String {
        switch analysis.imageQuality {
        case "blurry": return "The photo is blurry — estimates may be rough. Consider a retake."
        case "dark": return "The photo is dark — estimates may be rough. Consider a retake."
        case "partial": return "Some food looks cut off at the edge — totals may miss part of it."
        default: return ""
        }
    }

    @ViewBuilder
    private func dishRow(_ i: Int) -> some View {
        let dish = dishes[i]
        let factor = portions[i] ?? 1.0
        VStack(alignment: .leading, spacing: 8) {
            Button {
                if selected.contains(i) { selected.remove(i) } else { selected.insert(i) }
            } label: {
                HStack(alignment: .top) {
                    Image(systemName: selected.contains(i) ? "checkmark.circle.fill" : "circle")
                        .foregroundStyle(selected.contains(i) ? Color.accentColor : .secondary)
                        .font(.title3)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(dish.label).font(.headline)
                        Text(dish.description).font(.footnote).foregroundStyle(.secondary)
                        Text("\(dish.portionDesc) · \(scaledKcal(dish, factor)) kcal")
                            .font(.subheadline)
                        if dish.confidence < 0.6 {
                            Label("Rough estimate", systemImage: "questionmark.circle")
                                .font(.caption2).foregroundStyle(.orange)
                        }
                    }
                    Spacer()
                }
            }
            .buttonStyle(.plain)

            if selected.contains(i) {
                HStack(spacing: 6) {
                    Text("Ate").font(.caption).foregroundStyle(.secondary)
                    ForEach(Self.portionChoices, id: \.0) { (label, value) in
                        Button(label) { portions[i] = value }
                            .font(.caption.weight(abs(factor - value) < 0.01 ? .bold : .regular))
                            .buttonStyle(.bordered)
                            .tint(abs(factor - value) < 0.01 ? .accentColor : .secondary)
                            .controlSize(.mini)
                    }
                }

                if dish.needsClarification && dish.confidence < 0.6 {
                    clarificationRow(i, dish)
                }

                DisclosureGroup("Nutrients") {
                    nutrientsGrid(dish.nutrients.scaled(factor))
                }
                .font(.footnote)
            }
        }
        .padding(.vertical, 2)
    }

    /// The refine affordance — never blocks saving; one question, tappable
    /// chips, optional free text. "Same as last time" reuses a past answer.
    @ViewBuilder
    private func clarificationRow(_ i: Int, _ dish: Dish) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            if refining.contains(i) {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Updating estimate…").font(.caption).foregroundStyle(.secondary)
                }
            } else {
                Text(dish.clarificationQuestion)
                    .font(.footnote.weight(.medium))
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 6) {
                        if let last = lastTimeAnswers[dish.label.lowercased()] {
                            Button("Same as last time") { refine(i, answer: last) }
                                .buttonStyle(.borderedProminent)
                                .controlSize(.mini)
                        }
                        ForEach(dish.clarificationOptions, id: \.self) { option in
                            Button(option) { refine(i, answer: option) }
                                .buttonStyle(.bordered)
                                .controlSize(.mini)
                        }
                    }
                }
                HStack {
                    TextField("Or type what's in it…", text: Binding(
                        get: { freeTextAnswer[i] ?? "" },
                        set: { freeTextAnswer[i] = $0 }
                    ))
                    .font(.caption)
                    .textFieldStyle(.roundedBorder)
                    Button {
                        if let a = freeTextAnswer[i], !a.isEmpty { refine(i, answer: a) }
                    } label: {
                        Image(systemName: "arrow.up.circle.fill")
                    }
                    .disabled((freeTextAnswer[i] ?? "").isEmpty)
                }
            }
        }
        .padding(8)
        .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 10))
    }

    private func refine(_ i: Int, answer: String) {
        refining.insert(i)
        Task {
            do {
                let revised = try await ScanService().refine(scanId: scanId, dishIndex: i, answer: answer)
                dishes[i] = revised
            } catch {
                saveError = error.localizedDescription
            }
            refining.remove(i)
        }
    }

    private func scaledKcal(_ dish: Dish, _ factor: Double) -> Int {
        Int((Double(dish.nutrients.caloriesKcal) * factor).rounded())
    }

    private var selectedTotals: Nutrients {
        selected.reduce(Nutrients.zero) { acc, i in
            acc + dishes[i].nutrients.scaled(portions[i] ?? 1.0)
        }
    }

    private var totalsRow: some View {
        let t = selectedTotals
        return HStack {
            macro("kcal", "\(t.caloriesKcal)")
            macro("Protein", String(format: "%.0fg", t.proteinG))
            macro("Fat", String(format: "%.0fg", t.fatG))
            macro("Carbs", String(format: "%.0fg", t.carbsG))
        }
    }

    private func macro(_ name: String, _ value: String) -> some View {
        VStack {
            Text(value).font(.headline)
            Text(name).font(.caption2).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
    }

    private func nutrientsGrid(_ n: Nutrients) -> some View {
        Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 4) {
            GridRow { nutrient("Fiber", n.fiberG, "g"); nutrient("Sugar", n.sugarG, "g") }
            GridRow { nutrient("Sodium", n.sodiumMg, "mg"); nutrient("Sat. fat", n.saturatedFatG, "g") }
            GridRow { nutrient("Iron", n.ironMg, "mg"); nutrient("Calcium", n.calciumMg, "mg") }
            GridRow { nutrient("Omega-3", n.omega3G, "g"); Color.clear.gridCellUnsizedAxes([.horizontal, .vertical]) }
        }
        .padding(.top, 4)
    }

    private func nutrient(_ name: String, _ value: Double, _ unit: String) -> some View {
        HStack(spacing: 4) {
            Text(name).foregroundStyle(.secondary)
            Text(String(format: value < 10 ? "%.1f%@" : "%.0f%@", value, unit))
        }
        .font(.caption)
    }

    private func save() {
        saving = true
        saveError = nil
        let dishes = selected.sorted().map {
            ScanService.ConfirmDish(index: $0, portionFactor: portions[$0] ?? 1.0)
        }
        let day = DateUtil.dayString(date)
        Task {
            do {
                let meals = try await ScanService().confirm(scanId: scanId, dishes: dishes, date: day)
                onSaved(meals)
            } catch {
                saveError = error.localizedDescription
                saving = false
            }
        }
    }
}

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
    @State private var selectedVariant: [Int: Int] = [:] // dish index → chosen variant (regular/diet)
    @State private var date: Date
    @State private var saving = false
    @State private var saveError: String?
    @State private var refining: Set<Int> = []
    @State private var freeTextAnswer: [Int: String] = [:]
    @State private var lastTimeAnswers: [String: String] = [:] // lowercased label → past refinement answer
    // Haptics (iOS 17 `.sensoryFeedback`): `selectionTick` bumps on any
    // portion/variant chip tap; `savedTick` fires once the meal is saved.
    @State private var selectionTick = 0
    @State private var savedTick = 0
    // App Store health-app guideline: the AI-estimates disclaimer must be shown at the
    // first scan result (and lives permanently in Settings). Once seen, never again here.
    @AppStorage("hasSeenEstimateDisclaimer") private var hasSeenEstimateDisclaimer = false

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
            } footer: {
                if !hasSeenEstimateDisclaimer {
                    Text("Calories and nutrition are AI estimates, not medical or dietary advice. You can adjust anything after saving.")
                }
            }
        }
        .onDisappear { hasSeenEstimateDisclaimer = true }
        .sensoryFeedback(.selection, trigger: selectionTick)
        .sensoryFeedback(.success, trigger: savedTick)
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
                        Text("\(dish.portionDesc) · \(scaledKcal(i, factor)) kcal")
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
                VStack(alignment: .leading, spacing: 6) {
                    Text("Ate").font(.caption).foregroundStyle(.secondary)
                    WrapLayout(spacing: 8, lineSpacing: 8) {
                        ForEach(Self.portionChoices, id: \.0) { (label, value) in
                            let picked = abs(factor - value) < 0.01
                            ChipButton(title: label, selected: picked) {
                                portions[i] = value
                                selectionTick &+= 1
                            }
                        }
                    }
                }

                // Variants and open-ended clarification are independent: a
                // forkable dish can ALSO be ambiguous. Show the fork whenever
                // variants exist; additionally expose the refine path when the
                // dish needs clarification. When both are present the variant
                // chips already ARE the fork (the common cola case), so we
                // suppress the clarification question/chips and keep only the
                // free-text "or type what's in it" escape hatch — no double-ask.
                if !dish.variants.isEmpty {
                    variantRow(i, dish)
                }
                if dish.needsClarification {
                    clarificationRow(i, dish, variantsPresent: !dish.variants.isEmpty)
                }

                DisclosureGroup("Nutrients") {
                    nutrientsGrid(baseNutrients(i).scaled(factor))
                }
                .font(.footnote)
            }
        }
        .padding(.vertical, 2)
    }

    /// The refine affordance — never blocks saving; one question, tappable
    /// chips, optional free text. "Same as last time" reuses a past answer.
    ///
    /// When `variantsPresent`, the variant fork above already poses the
    /// question and offers the choices, so we drop the redundant question +
    /// chips and show only the free-text escape hatch (for contents the fork
    /// doesn't cover) — keeping the common cola case from double-asking.
    @ViewBuilder
    private func clarificationRow(_ i: Int, _ dish: Dish, variantsPresent: Bool = false) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            if refining.contains(i) {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Updating estimate…").font(.caption).foregroundStyle(.secondary)
                }
            } else {
                if !variantsPresent {
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
                }
                HStack {
                    TextField(variantsPresent ? "Something else? Type what's in it…" : "Or type what's in it…", text: Binding(
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

    /// Instant regular/diet fork picker — mirrors the portion chips, no
    /// network. Each variant carries its own precomputed nutrients.
    @ViewBuilder
    private func variantRow(_ i: Int, _ dish: Dish) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(dish.clarificationQuestion.isEmpty ? "Which one is it?" : dish.clarificationQuestion)
                .font(.footnote.weight(.medium))
            WrapLayout(spacing: 8, lineSpacing: 8) {
                ForEach(dish.variants.indices, id: \.self) { v in
                    let picked = (selectedVariant[i] ?? 0) == v
                    ChipButton(selected: picked) {
                        selectedVariant[i] = v
                        selectionTick &+= 1
                    } label: {
                        VStack(spacing: 1) {
                            Text(dish.variants[v].label)
                            Text("\(dish.variants[v].nutrients.caloriesKcal) kcal").font(.caption2)
                        }
                    }
                }
            }
        }
        .padding(8)
        .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 10))
    }

    /// Base (unscaled) nutrients for a dish: the chosen variant when it's a
    /// regular/diet fork, else the dish's own estimate.
    private func baseNutrients(_ i: Int) -> Nutrients {
        let d = dishes[i]
        guard !d.variants.isEmpty else { return d.nutrients }
        let s = min(max(selectedVariant[i] ?? 0, 0), d.variants.count - 1)
        return d.variants[s].nutrients
    }

    private func scaledKcal(_ i: Int, _ factor: Double) -> Int {
        Int((Double(baseNutrients(i).caloriesKcal) * factor).rounded())
    }

    private var selectedTotals: Nutrients {
        selected.reduce(Nutrients.zero) { acc, i in
            acc + baseNutrients(i).scaled(portions[i] ?? 1.0)
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
        let confirmDishes = selected.sorted().map { i in
            ScanService.ConfirmDish(
                index: i,
                portionFactor: portions[i] ?? 1.0,
                variantIndex: dishes[i].variants.isEmpty ? nil : (selectedVariant[i] ?? 0))
        }
        let day = DateUtil.dayString(date)
        Task {
            do {
                let meals = try await ScanService().confirm(scanId: scanId, dishes: confirmDishes, date: day)
                savedTick &+= 1
                onSaved(meals)
            } catch {
                saveError = error.localizedDescription
                saving = false
            }
        }
    }
}

/// A selectable chip with a ≥44pt hit target (HIG), a filled/tinted selected
/// state (not bold-only, so it survives reduced contrast), and an
/// `.isSelected` accessibility trait so VoiceOver announces the active choice.
/// Used for the portion and variant pickers.
fileprivate struct ChipButton<Label: View>: View {
    let selected: Bool
    let action: () -> Void
    @ViewBuilder let label: Label

    init(selected: Bool, action: @escaping () -> Void, @ViewBuilder label: () -> Label) {
        self.selected = selected
        self.action = action
        self.label = label()
    }

    var body: some View {
        Button(action: action) {
            label.font(.subheadline.weight(selected ? .semibold : .regular))
        }
        .buttonStyle(ChipButtonStyle(selected: selected))
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

extension ChipButton where Label == Text {
    init(title: String, selected: Bool, action: @escaping () -> Void) {
        self.init(selected: selected, action: action) { Text(title) }
    }
}

/// Chip look: pill background, accent fill when selected, ≥44pt tall hit
/// target, subtle press dim. Selection is conveyed by fill + tinted text/border
/// so it doesn't rely on weight alone.
fileprivate struct ChipButtonStyle: ButtonStyle {
    let selected: Bool

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .padding(.horizontal, 14)
            .frame(minHeight: 44)
            .foregroundStyle(selected ? Color.white : Color.accentColor)
            .background(
                Capsule().fill(selected ? Color.accentColor : Color.accentColor.opacity(0.12))
            )
            .overlay(
                Capsule().strokeBorder(Color.accentColor.opacity(selected ? 0 : 0.4), lineWidth: 1)
            )
            .contentShape(Capsule())
            .opacity(configuration.isPressed ? 0.6 : 1)
    }
}

/// Minimal flow layout (iOS 16+): lays children left-to-right and wraps to a
/// new line when the row overflows, so chip rows grow taller at large Dynamic
/// Type instead of clipping or needing a horizontal scroll.
fileprivate struct WrapLayout: Layout {
    var spacing: CGFloat = 8
    var lineSpacing: CGFloat = 8

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let maxWidth = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, rowHeight: CGFloat = 0, widest: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x > 0 && x + s.width > maxWidth {
                widest = max(widest, x - spacing)
                y += rowHeight + lineSpacing
                x = 0; rowHeight = 0
            }
            x += s.width + spacing
            rowHeight = max(rowHeight, s.height)
        }
        widest = max(widest, x - spacing)
        return CGSize(width: maxWidth.isFinite ? maxWidth : widest, height: y + rowHeight)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let maxWidth = bounds.width
        var x: CGFloat = 0, y: CGFloat = 0, rowHeight: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x > 0 && x + s.width > maxWidth {
                y += rowHeight + lineSpacing
                x = 0; rowHeight = 0
            }
            v.place(at: CGPoint(x: bounds.minX + x, y: bounds.minY + y), proposal: ProposedViewSize(s))
            x += s.width + spacing
            rowHeight = max(rowHeight, s.height)
        }
    }
}

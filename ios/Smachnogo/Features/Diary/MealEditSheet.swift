import SwiftUI

/// Edit a saved meal: label, portion (rescaled server-side from the BASE
/// estimate — never compounding), date move, planned↔logged, delete.
struct MealEditSheet: View {
    let meal: Meal
    let onChanged: () -> Void
    @Environment(\.dismiss) private var dismiss

    @State private var label: String
    @State private var portionFactor: Double
    @State private var variantIndex: Int
    @State private var date: Date
    @State private var isPlanned: Bool
    @State private var working = false
    @State private var errorText: String?
    @State private var confirmDelete = false
    // Haptics (iOS 17 `.sensoryFeedback`): `selectionTick` bumps on any
    // portion/variant chip tap; `deletedTick` fires when the meal is deleted.
    @State private var selectionTick = 0
    @State private var deletedTick = 0

    private let service = MealService()

    private static let portionChoices: [(String, Double)] = [
        ("¼", 0.25), ("⅓", 1.0 / 3.0), ("½", 0.5), ("¾", 0.75), ("1", 1.0), ("1½", 1.5), ("2", 2.0),
    ]

    init(meal: Meal, onChanged: @escaping () -> Void) {
        self.meal = meal
        self.onChanged = onChanged
        _label = State(initialValue: meal.label)
        _portionFactor = State(initialValue: meal.portionFactor)
        _variantIndex = State(initialValue: meal.variantIndex ?? 0)
        _date = State(initialValue: Self.parse(meal.date))
        _isPlanned = State(initialValue: meal.state == "planned")
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Meal") {
                    TextField("Name", text: $label)
                    if meal.variants.count > 1 {
                        WrapLayout(spacing: 8, lineSpacing: 8) {
                            ForEach(meal.variants.indices, id: \.self) { v in
                                ChipButton(title: meal.variants[v].label, selected: variantIndex == v) {
                                    variantIndex = v
                                    selectionTick &+= 1
                                }
                            }
                        }
                    }
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Ate").font(.caption).foregroundStyle(.secondary)
                        WrapLayout(spacing: 8, lineSpacing: 8) {
                            ForEach(Self.portionChoices, id: \.0) { (chip, value) in
                                ChipButton(title: chip, selected: abs(portionFactor - value) < 0.01) {
                                    portionFactor = value
                                    selectionTick &+= 1
                                }
                            }
                        }
                    }
                    Text("\(scaledPreviewKcal) kcal at this portion")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                Section("When") {
                    DatePicker("Day", selection: $date, displayedComponents: [.date])
                    Toggle("Planned (not eaten yet)", isOn: $isPlanned)
                }
                if let errorText {
                    Section { Text(errorText).font(.footnote).foregroundStyle(.red) }
                }
                Section {
                    Button("Delete meal", role: .destructive) { confirmDelete = true }
                }
            }
            .navigationTitle("Edit meal")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if working {
                        ProgressView()
                    } else {
                        Button("Save") { save() }.disabled(label.isEmpty)
                    }
                }
            }
            .confirmationDialog("Delete this meal?", isPresented: $confirmDelete, titleVisibility: .visible) {
                Button("Delete", role: .destructive) { deleteMeal() }
            }
            .sensoryFeedback(.selection, trigger: selectionTick)
            .sensoryFeedback(.warning, trigger: deletedTick)
        }
    }

    private var scaledPreviewKcal: Int {
        // Client preview only — the server rescales from the true base.
        let base: Double
        if !meal.variants.isEmpty {
            let v = min(max(variantIndex, 0), meal.variants.count - 1)
            base = Double(meal.variants[v].nutrients.caloriesKcal)
        } else {
            base = meal.portionFactor > 0 ? Double(meal.nutrients.caloriesKcal) / meal.portionFactor : 0
        }
        return Int((base * portionFactor).rounded())
    }

    private func save() {
        working = true
        errorText = nil
        var req = MealService.PatchRequest()
        if label != meal.label { req.label = label }
        if abs(portionFactor - meal.portionFactor) > 0.001 { req.portionFactor = portionFactor }
        if variantIndex != (meal.variantIndex ?? 0) { req.variantIndex = variantIndex }
        let newDay = DateUtil.dayString(date)
        if newDay != meal.date { req.newDate = newDay }
        let newState = isPlanned ? "planned" : "logged"
        if newState != meal.state { req.state = newState }

        Task {
            do {
                _ = try await service.patch(mealId: meal.mealId, date: meal.date, req)
                onChanged()
                dismiss()
            } catch {
                errorText = error.localizedDescription
                working = false
            }
        }
    }

    private func deleteMeal() {
        working = true
        Task {
            do {
                try await service.delete(mealId: meal.mealId, date: meal.date)
                deletedTick &+= 1
                onChanged()
                dismiss()
            } catch {
                errorText = error.localizedDescription
                working = false
            }
        }
    }

    private static func parse(_ day: String) -> Date {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd"
        return f.date(from: day) ?? Date()
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

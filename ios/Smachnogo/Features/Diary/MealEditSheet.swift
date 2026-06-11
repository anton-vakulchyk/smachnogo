import SwiftUI

/// Edit a saved meal: label, portion (rescaled server-side from the BASE
/// estimate — never compounding), date move, planned↔logged, delete.
struct MealEditSheet: View {
    let meal: Meal
    let onChanged: () -> Void
    @Environment(\.dismiss) private var dismiss

    @State private var label: String
    @State private var portionFactor: Double
    @State private var date: Date
    @State private var isPlanned: Bool
    @State private var working = false
    @State private var errorText: String?
    @State private var confirmDelete = false

    private let service = MealService()

    private static let portionChoices: [(String, Double)] = [
        ("¼", 0.25), ("⅓", 1.0 / 3.0), ("½", 0.5), ("¾", 0.75), ("1", 1.0), ("1½", 1.5), ("2", 2.0),
    ]

    init(meal: Meal, onChanged: @escaping () -> Void) {
        self.meal = meal
        self.onChanged = onChanged
        _label = State(initialValue: meal.label)
        _portionFactor = State(initialValue: meal.portionFactor)
        _date = State(initialValue: Self.parse(meal.date))
        _isPlanned = State(initialValue: meal.state == "planned")
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Meal") {
                    TextField("Name", text: $label)
                    HStack(spacing: 6) {
                        Text("Ate").font(.caption).foregroundStyle(.secondary)
                        ForEach(Self.portionChoices, id: \.0) { (chip, value) in
                            Button(chip) { portionFactor = value }
                                .font(.caption.weight(abs(portionFactor - value) < 0.01 ? .bold : .regular))
                                .buttonStyle(.bordered)
                                .tint(abs(portionFactor - value) < 0.01 ? .accentColor : .secondary)
                                .controlSize(.mini)
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
        }
    }

    private var scaledPreviewKcal: Int {
        // Client preview only — the server rescales from the true base.
        let base = meal.portionFactor > 0 ? Double(meal.nutrients.caloriesKcal) / meal.portionFactor : 0
        return Int((base * portionFactor).rounded())
    }

    private func save() {
        working = true
        errorText = nil
        var req = MealService.PatchRequest()
        if label != meal.label { req.label = label }
        if abs(portionFactor - meal.portionFactor) > 0.001 { req.portionFactor = portionFactor }
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

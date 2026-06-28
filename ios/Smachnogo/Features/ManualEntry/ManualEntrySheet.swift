import SwiftUI

/// Fast no-photo logging: recents (one tap re-add) or free text → AI
/// estimate → save. Saving with a future date creates a planned meal.
struct ManualEntrySheet: View {
    let onSaved: () -> Void
    @Environment(\.dismiss) private var dismiss

    @State private var text = ""
    @State private var estimating = false
    @State private var estimate: EstimateResponse?
    @State private var date = Date()
    @State private var saving = false
    @State private var errorText: String?
    @State private var recents: [Meal] = []
    @State private var reAddedLabel: String?
    @State private var reAddHaptic = false

    private let service = MealService()
    @FocusState private var textFocused: Bool

    var body: some View {
        NavigationStack {
            Form {
                Section("Describe what you ate") {
                    TextField("e.g. 2 eggs and toast with butter", text: $text, axis: .vertical)
                        .lineLimit(2...4)
                        .focused($textFocused)
                        .submitLabel(.done)
                    Button {
                        runEstimate()
                    } label: {
                        if estimating { ProgressView() } else { Label("Estimate", systemImage: "sparkles") }
                    }
                    .disabled(text.trimmingCharacters(in: .whitespaces).isEmpty || estimating)
                }

                if let est = estimate {
                    estimateSection(est)
                }

                if estimate == nil && !recents.isEmpty {
                    Section("Recent meals — tap to log again") {
                        ForEach(recents.prefix(8)) { meal in
                            Button {
                                reAdd(meal)
                            } label: {
                                HStack {
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(meal.label).foregroundStyle(.primary)
                                        Text("\(meal.nutrients.caloriesKcal) kcal · P \(Int(meal.nutrients.proteinG))")
                                            .font(.caption).foregroundStyle(.secondary)
                                    }
                                    Spacer()
                                    if reAddedLabel == meal.label {
                                        Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                                    } else {
                                        Image(systemName: "plus.circle")
                                    }
                                }
                            }
                        }
                    }
                }

                if let errorText {
                    Section { Text(errorText).font(.footnote).foregroundStyle(.red) }
                }
            }
            .navigationTitle("Add meal")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Close") { dismiss() } }
            }
        }
        .sensoryFeedback(.success, trigger: reAddHaptic)
        .task { recents = (try? await service.recent()) ?? [] }
    }

    @ViewBuilder
    private func estimateSection(_ est: EstimateResponse) -> some View {
        Section {
            Button(role: .destructive) {
                estimate = nil
                errorText = nil
            } label: {
                Label("Start over", systemImage: "arrow.uturn.backward")
            }
        }
        if !est.isFood {
            Section {
                Label("That doesn't sound like food — try describing the meal.", systemImage: "questionmark.circle")
                    .font(.footnote)
            }
        } else {
            Section(est.label) {
                if !est.assumptions.isEmpty {
                    Text(est.assumptions).font(.footnote).foregroundStyle(.secondary)
                }
                ForEach(est.items) { item in
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(item.name)
                            Text(item.quantityDesc).font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        Text("\(item.nutrients.caloriesKcal) kcal").font(.callout)
                    }
                }
                HStack {
                    Text("Total").font(.headline)
                    Spacer()
                    Text("\(est.totals.nutrients.caloriesKcal) kcal · P \(Int(est.totals.nutrients.proteinG)) · F \(Int(est.totals.nutrients.fatG)) · C \(Int(est.totals.nutrients.carbsG))")
                        .font(.callout)
                }
            }
            Section {
                DatePicker("When", selection: $date, displayedComponents: [.date, .hourAndMinute])
                if DateUtil.dayString(date) > DateUtil.dayString() {
                    Label("Future date — saves as a planned meal", systemImage: "calendar.badge.clock")
                        .font(.footnote).foregroundStyle(.orange)
                }
                Button {
                    save(est)
                } label: {
                    if saving { ProgressView().frame(maxWidth: .infinity) }
                    else { Text("Save meal").frame(maxWidth: .infinity) }
                }
                .buttonStyle(.borderedProminent)
                .disabled(saving)
            }
        }
    }

    private func runEstimate() {
        estimating = true
        errorText = nil
        textFocused = false
        Task {
            do {
                estimate = try await service.estimate(text: text.trimmingCharacters(in: .whitespaces))
            } catch {
                errorText = error.localizedDescription
            }
            estimating = false
        }
    }

    private func save(_ est: EstimateResponse) {
        saving = true
        errorText = nil
        let day = DateUtil.dayString(date)
        let state = day > DateUtil.dayString() ? "planned" : "logged"
        Task {
            do {
                try await service.create(MealService.CreateMealRequest(
                    mealId: UUID().uuidString.lowercased(),
                    date: day,
                    state: state,
                    consumedAt: ISO8601DateFormatter().string(from: date),
                    label: est.label,
                    source: "text",
                    nutrients: est.totals.nutrients,
                    nutritionScore: est.totals.nutritionScore,
                    dietQualityScore: est.totals.dietQualityScore,
                    components: est.items
                ))
                onSaved()
                dismiss()
            } catch {
                errorText = error.localizedDescription
                saving = false
            }
        }
    }

    private func reAdd(_ meal: Meal) {
        Task {
            do {
                try await service.logAgainToday(meal)
                reAddedLabel = meal.label
                reAddHaptic.toggle()
                onSaved()
                try? await Task.sleep(for: .seconds(0.6))
                dismiss()
            } catch {
                errorText = error.localizedDescription
            }
        }
    }
}

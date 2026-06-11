import SwiftUI

/// Daily limits editor: toggle a cap on, type the number. Saving sends the
/// FULL map (replace semantics). Calendar days and stats recolor from these
/// instantly — green when respected, red when any cap is blown.
struct LimitsEditorSheet: View {
    var onSaved: (() -> Void)? = nil

    @Environment(\.dismiss) private var dismiss
    @State private var enabled: [String: Bool] = [:]
    @State private var values: [String: String] = [:]
    @State private var saving = false
    @State private var errorText: String?

    private let service = MealService()

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    ForEach(LimitsRule.specs) { spec in
                        limitRow(spec)
                    }
                } footer: {
                    Text("A day turns red when any limit is exceeded, green when all are respected. Limits apply per day.")
                }
                if let errorText {
                    Section { Text(errorText).font(.footnote).foregroundStyle(.red) }
                }
            }
            .navigationTitle("Daily limits")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if saving {
                        ProgressView()
                    } else {
                        Button("Save") { save() }
                    }
                }
            }
            .onAppear { seed() }
        }
    }

    private func limitRow(_ spec: LimitsRule.Spec) -> some View {
        HStack {
            Toggle(isOn: binding(for: spec)) {
                Text(spec.name)
            }
            if enabled[spec.key] == true {
                TextField("\(Int(spec.typical))", text: valueBinding(spec.key))
                    .keyboardType(.numberPad)
                    .multilineTextAlignment(.trailing)
                    .frame(width: 80)
                Text(spec.unit).foregroundStyle(.secondary).font(.footnote)
            }
        }
    }

    private func binding(for spec: LimitsRule.Spec) -> Binding<Bool> {
        Binding(
            get: { enabled[spec.key] ?? false },
            set: { on in
                enabled[spec.key] = on
                if on && (values[spec.key] ?? "").isEmpty {
                    values[spec.key] = String(Int(spec.typical))
                }
            }
        )
    }

    private func valueBinding(_ key: String) -> Binding<String> {
        Binding(get: { values[key] ?? "" }, set: { values[key] = $0 })
    }

    private func seed() {
        let current = StoreService.shared.me?.limits ?? [:]
        for spec in LimitsRule.specs {
            if let v = current[spec.key] {
                enabled[spec.key] = true
                values[spec.key] = String(Int(v))
            }
        }
    }

    private func save() {
        var limits: [String: Double] = [:]
        for spec in LimitsRule.specs where enabled[spec.key] == true {
            guard let v = Double(values[spec.key] ?? ""), v > 0 else {
                errorText = "\(spec.name): enter a number above zero."
                return
            }
            limits[spec.key] = v
        }
        saving = true
        errorText = nil
        Task {
            defer { saving = false }
            do {
                try await service.updateLimits(limits)
                await StoreService.shared.refreshServerState()
                onSaved?()
                dismiss()
            } catch {
                errorText = error.localizedDescription
            }
        }
    }
}

import SwiftUI

/// Day navigation: ±1 chevrons, tappable title (opens the month grid), and
/// a "Today" anchor when away from today.
struct CalendarStrip: View {
    @Binding var selectedDate: Date
    @State private var showMonthGrid = false

    private var isToday: Bool {
        Calendar.current.isDateInToday(selectedDate)
    }

    var body: some View {
        HStack {
            Button {
                shift(-1)
            } label: {
                Image(systemName: "chevron.left")
            }
            .accessibilityLabel("Previous day")

            Spacer()
            Button {
                showMonthGrid = true
            } label: {
                Text(title)
                    .font(.headline)
            }
            .accessibilityLabel("Pick a date")
            Spacer()

            if !isToday {
                Button("Today") { selectedDate = Date() }
                    .font(.subheadline)
            }
            Button {
                shift(1)
            } label: {
                Image(systemName: "chevron.right")
            }
            .accessibilityLabel("Next day")
        }
        .buttonStyle(.plain)
        .padding(.horizontal)
        .sheet(isPresented: $showMonthGrid) {
            MonthGridSheet(selectedDate: $selectedDate)
                .presentationDetents([.medium, .large])
        }
    }

    private var title: String {
        if isToday { return "Today" }
        if Calendar.current.isDateInYesterday(selectedDate) { return "Yesterday" }
        if Calendar.current.isDateInTomorrow(selectedDate) { return "Tomorrow" }
        return selectedDate.formatted(.dateTime.weekday(.abbreviated).day().month(.abbreviated))
    }

    private func shift(_ days: Int) {
        selectedDate = Calendar.current.date(byAdding: .day, value: days, to: selectedDate) ?? selectedDate
    }
}

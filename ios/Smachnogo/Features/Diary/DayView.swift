import SwiftUI
import PhotosUI
import AVFoundation

/// M1 diary: today's meals + the scan entry points. The empty state IS the
/// onboarding. Calendar navigation arrives in M3.
struct DayView: View {
    @State private var meals: [Meal] = []
    @State private var loading = false
    @State private var loadError: String?

    @State private var showCamera = false
    @State private var cameraDenied = false
    @State private var photoItem: PhotosPickerItem?
    @State private var scanImage: UIImage?

    private let mealService = MealService()
    private var today: String { DateUtil.dayString() }

    var body: some View {
        NavigationStack {
            Group {
                if meals.isEmpty && !loading {
                    emptyState
                } else {
                    mealList
                }
            }
            .navigationTitle("Today")
            .toolbar {
                ToolbarItemGroup(placement: .topBarTrailing) {
                    PhotosPicker(selection: $photoItem, matching: .images) {
                        Image(systemName: "photo.on.rectangle")
                    }
                    .accessibilityLabel("Scan from photo library")
                    .accessibilityIdentifier("scan.library")
                    Button { openCamera() } label: {
                        Image(systemName: "camera")
                    }
                    .accessibilityLabel("Scan with camera")
                    .accessibilityIdentifier("scan.camera")
                }
            }
            .refreshable { await load() }
        }
        .task { await load() }
        .onChange(of: photoItem) { _, item in
            guard let item else { return }
            Task {
                if let data = try? await item.loadTransferable(type: Data.self),
                   let image = UIImage(data: data) {
                    scanImage = image
                }
                photoItem = nil
            }
        }
        .fullScreenCover(isPresented: $showCamera) {
            CameraPicker { image in scanImage = image }
                .ignoresSafeArea()
        }
        .sheet(item: $scanImage) { image in
            ScanFlowView(image: image) { _ in
                Task { await load() }
            }
        }
        .alert("Camera access is off", isPresented: $cameraDenied) {
            Button("Open Settings") {
                if let url = URL(string: UIApplication.openSettingsURLString) {
                    UIApplication.shared.open(url)
                }
            }
            Button("Use photo library instead", role: .cancel) {}
        } message: {
            Text("Enable camera access in Settings, or pick a photo from your library — scanning works either way.")
        }
    }

    private func openCamera() {
        guard UIImagePickerController.isSourceTypeAvailable(.camera) else {
            cameraDenied = false
            return // simulator: no camera; library button covers it
        }
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .denied, .restricted:
            cameraDenied = true
        default:
            showCamera = true
        }
    }

    private func load() async {
        loading = true
        defer { loading = false }
        do {
            meals = try await mealService.meals(on: today)
            loadError = nil
        } catch {
            loadError = error.localizedDescription
        }
    }

    private var emptyState: some View {
        VStack(spacing: 16) {
            Spacer()
            Image(systemName: "camera.viewfinder")
                .font(.system(size: 56))
                .foregroundStyle(.secondary)
            Text("Scan your first meal")
                .font(.title3.weight(.semibold))
            Text("Point the camera at your plate — calories, macros and nutrition appear in seconds.\n\nTip: for packaged food, include the label in the shot.")
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 32)
            Button { openCamera() } label: {
                Label("Scan a meal", systemImage: "camera")
                    .frame(maxWidth: 220)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            if let loadError {
                Text(loadError).font(.footnote).foregroundStyle(.red).padding(.horizontal)
            }
            Spacer()
            Spacer()
        }
    }

    private var mealList: some View {
        List {
            Section {
                totalsHeader
            }
            Section("Meals") {
                ForEach(meals) { meal in
                    MealRow(meal: meal)
                }
            }
        }
    }

    private var totalsHeader: some View {
        let logged = meals.filter { $0.state == "logged" }
        let totals = logged.reduce(Nutrients.zero) { $0 + $1.nutrients }
        return HStack {
            stat("\(totals.caloriesKcal)", "kcal")
            stat(String(format: "%.0fg", totals.proteinG), "protein")
            stat(String(format: "%.0fg", totals.fatG), "fat")
            stat(String(format: "%.0fg", totals.carbsG), "carbs")
        }
    }

    private func stat(_ value: String, _ name: String) -> some View {
        VStack {
            Text(value).font(.headline)
            Text(name).font(.caption2).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
    }
}

struct MealRow: View {
    let meal: Meal

    var body: some View {
        HStack(spacing: 12) {
            if let scanId = meal.scanId, let thumb = LocalPhotoStore.thumbnail(scanId: scanId) {
                Image(uiImage: thumb)
                    .resizable().scaledToFill()
                    .frame(width: 48, height: 48)
                    .clipShape(RoundedRectangle(cornerRadius: 8))
            } else {
                RoundedRectangle(cornerRadius: 8)
                    .fill(.quaternary)
                    .frame(width: 48, height: 48)
                    .overlay(Image(systemName: "fork.knife").foregroundStyle(.secondary))
            }
            VStack(alignment: .leading, spacing: 2) {
                Text(meal.label).font(.body)
                Text("\(meal.nutrients.caloriesKcal) kcal · P \(Int(meal.nutrients.proteinG)) · F \(Int(meal.nutrients.fatG)) · C \(Int(meal.nutrients.carbsG))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if meal.portionFactor != 1.0 {
                Text("×\(meal.portionFactor, specifier: "%.2g")")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
        }
    }
}

// Allows .sheet(item:) on a UIImage payload.
extension UIImage: @retroactive Identifiable {
    public var id: ObjectIdentifier { ObjectIdentifier(self) }
}

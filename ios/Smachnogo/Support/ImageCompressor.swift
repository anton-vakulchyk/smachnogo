import UIKit

/// Pre-scales photos to fit a 768×768 box (aspect preserved, never cropped)
/// and re-encodes as JPEG q0.7 via a RENDER-based resize:
/// - bakes EXIF orientation into pixels (library/HEIC photos otherwise reach
///   the model sideways and degrade portion estimates)
/// - strips ALL metadata, including GPS (library photos carry home/work
///   coordinates — they must never reach S3 or the AI provider)
enum ImageCompressor {
    static let maxDimension: CGFloat = 768
    static let jpegQuality: CGFloat = 0.7

    static func compressForUpload(_ image: UIImage) -> Data? {
        let size = image.size
        guard size.width > 0, size.height > 0 else { return nil }

        let scale = min(1.0, maxDimension / max(size.width, size.height))
        let target = CGSize(width: (size.width * scale).rounded(.down),
                            height: (size.height * scale).rounded(.down))

        let format = UIGraphicsImageRendererFormat()
        format.scale = 1 // pixel-exact, not point-scaled
        let renderer = UIGraphicsImageRenderer(size: target, format: format)
        let rendered = renderer.image { _ in
            image.draw(in: CGRect(origin: .zero, size: target))
        }
        return rendered.jpegData(compressionQuality: jpegQuality)
    }

    /// Small diary thumbnail (~200px long edge) stored locally per meal.
    static func thumbnail(_ image: UIImage, maxDim: CGFloat = 200) -> Data? {
        let size = image.size
        guard size.width > 0, size.height > 0 else { return nil }
        let scale = min(1.0, maxDim / max(size.width, size.height))
        let target = CGSize(width: size.width * scale, height: size.height * scale)
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        let renderer = UIGraphicsImageRenderer(size: target, format: format)
        let rendered = renderer.image { _ in
            image.draw(in: CGRect(origin: .zero, size: target))
        }
        return rendered.jpegData(compressionQuality: 0.7)
    }
}

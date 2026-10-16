import AVFoundation
import Flutter
import UIKit

enum LocalVideoTools {
    static func inspect(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
        guard let arguments = call.arguments as? [String: Any],
              let source = arguments["videoPath"] as? String,
              let destination = arguments["thumbnailPath"] as? String else {
            result(FlutterError(code: "media_invalid", message: "Unable to process this video", details: nil))
            return
        }
        DispatchQueue.global(qos: .userInitiated).async {
            do {
                let video = URL(fileURLWithPath: source).resolvingSymlinksInPath()
                let thumbnail = URL(fileURLWithPath: destination).resolvingSymlinksInPath()
                let container = URL(fileURLWithPath: NSHomeDirectory()).resolvingSymlinksInPath().path + "/"
                guard thumbnail.path.hasPrefix(container),
                      FileManager.default.fileExists(atPath: video.path) else {
                    throw NSError(domain: "luobo.media", code: 1)
                }
                let asset = AVURLAsset(url: video)
                let duration = CMTimeGetSeconds(asset.duration)
                guard duration.isFinite && duration > 0 else {
                    throw NSError(domain: "luobo.media", code: 2)
                }
                let generator = AVAssetImageGenerator(asset: asset)
                generator.appliesPreferredTrackTransform = true
                generator.maximumSize = CGSize(width: 720, height: 720)
                let frame = try generator.copyCGImage(at: .zero, actualTime: nil)
                guard let data = UIImage(cgImage: frame).jpegData(compressionQuality: 0.8) else {
                    throw NSError(domain: "luobo.media", code: 3)
                }
                try FileManager.default.createDirectory(at: thumbnail.deletingLastPathComponent(), withIntermediateDirectories: true)
                try data.write(to: thumbnail, options: .atomic)
                DispatchQueue.main.async {
                    result(["durationMilliseconds": Int64(duration * 1000),
                            "width": frame.width, "height": frame.height,
                            "thumbnailPath": thumbnail.path])
                }
            } catch {
                DispatchQueue.main.async {
                    result(FlutterError(code: "media_invalid", message: "Unable to process this video", details: nil))
                }
            }
        }
    }
}

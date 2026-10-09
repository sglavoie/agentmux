// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import Foundation
import GhosttyKit
import os

/// Namespace for the libghostty embedding glue, as upstream spells it.
enum Ghostty {
    static let logger = Logger(subsystem: "io.github.sglavoie.agentmux.ghostty-harness", category: "ghostty")
}

/// Whatever owns a `ghostty_surface_t`. A surface's userdata is an unretained
/// pointer to its host object, and the runtime callbacks that carry surface
/// userdata (clipboard, close) resolve it back through this protocol.
protocol GhosttySurfaceHost: AnyObject {
    var surface: ghostty_surface_t? { get }
}

extension Ghostty {
    /// The host behind a surface callback's userdata, or nil when the pointer
    /// is null or its object does not host a surface.
    static func surfaceHost(from userdata: UnsafeMutableRawPointer?) -> GhosttySurfaceHost? {
        guard let userdata else { return nil }
        return Unmanaged<AnyObject>.fromOpaque(userdata).takeUnretainedValue() as? GhosttySurfaceHost
    }
}

extension Ghostty {
    /// The embedded Ghostty's version and build mode, e.g. "1.3.1 (ReleaseFast)".
    static var versionDescription: String {
        let info = ghostty_info()
        let version: String
        if let ptr = info.version {
            let data = Data(bytes: ptr, count: Int(info.version_len))
            version = String(decoding: data, as: UTF8.self)
        } else {
            version = "unknown"
        }
        let mode: String
        switch info.build_mode {
        case GHOSTTY_BUILD_MODE_DEBUG: mode = "Debug"
        case GHOSTTY_BUILD_MODE_RELEASE_SAFE: mode = "ReleaseSafe"
        case GHOSTTY_BUILD_MODE_RELEASE_FAST: mode = "ReleaseFast"
        case GHOSTTY_BUILD_MODE_RELEASE_SMALL: mode = "ReleaseSmall"
        default: mode = "unknown build"
        }
        return "\(version) (\(mode))"
    }
}

import CoreGraphics
import Foundation

/// Where an answer panel opens, worked out from frames alone so that it can
/// be tested without a window or a display. All rects are in global screen
/// coordinates, as `NSScreen` and `NSEvent.mouseLocation` give them.
struct PanelPlacement {
    /// A connected display: its whole frame, and the part of it not taken by
    /// the menu bar or the Dock.
    struct Screen: Equatable {
        let frame: CGRect
        let visibleFrame: CGRect
    }

    let screens: [Screen]

    /// The screen a panel belongs on: the one most of the workspace window
    /// lies on while that window is open, else the one under the pointer,
    /// else the first.
    func screen(workspace: CGRect?, pointer: CGPoint) -> Screen? {
        if let workspace {
            let best = screens
                .map { ($0, Self.area($0.frame.intersection(workspace))) }
                .max { $0.1 < $1.1 }
            if let best, best.1 > 0 { return best.0 }
        }
        // A screen's frame excludes its top and right edges; the pointer can
        // rest on them.
        if let under = screens.first(where: { $0.frame.insetBy(dx: -1, dy: -1).contains(pointer) }) {
            return under
        }
        return screens.first
    }

    /// The frame to show a panel of `size` in. A frame the operator dragged
    /// the panel to is kept while its middle still lies on a connected
    /// screen; otherwise the panel is centred on the chosen screen, and no
    /// larger than it.
    func frame(size: CGSize, saved: CGRect?, workspace: CGRect?, pointer: CGPoint) -> CGRect? {
        if let saved, isOnScreen(saved) { return saved }
        guard let screen = screen(workspace: workspace, pointer: pointer) else { return nil }
        let visible = screen.visibleFrame
        let width = min(size.width, visible.width)
        let height = min(size.height, visible.height)
        return CGRect(
            x: (visible.midX - width / 2).rounded(),
            y: (visible.midY - height / 2).rounded(),
            width: width,
            height: height
        )
    }

    /// Whether a frame can be seen and dragged again: its middle is on some
    /// screen's usable area.
    func isOnScreen(_ frame: CGRect) -> Bool {
        guard frame.width > 0, frame.height > 0 else { return false }
        let middle = CGPoint(x: frame.midX, y: frame.midY)
        return screens.contains { $0.visibleFrame.contains(middle) }
    }

    private static func area(_ rect: CGRect) -> CGFloat {
        rect.isNull ? 0 : rect.width * rect.height
    }
}

/// A panel's dragged frame, kept between openings and launches.
struct PanelFrameStore {
    let key: String
    let defaults: UserDefaults?

    func load() -> CGRect? {
        guard let text = defaults?.string(forKey: key) else { return nil }
        let rect = NSRectFromString(text)
        return rect.width > 0 && rect.height > 0 ? rect : nil
    }

    func save(_ frame: CGRect) {
        defaults?.set(NSStringFromRect(frame), forKey: key)
    }
}

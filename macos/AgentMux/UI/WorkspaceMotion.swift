import AppKit
import QuartzCore

/// Presentation only: focus and the final view state change synchronously.
@MainActor
enum WorkspaceMotion {
    nonisolated static let feedback: TimeInterval = 0.1
    nonisolated static let entrance: TimeInterval = 0.14
    nonisolated static let disclosure: TimeInterval = 0.18
    static var enabled: Bool {
        !AppDelegate.isRunningTests && !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
    }

    static func reveal(_ view: NSView) {
        view.isHidden = false
        fade(view, from: 0, to: 1, duration: entrance)
    }

    static func fade(_ view: NSView, from: CGFloat? = nil, to value: CGFloat, duration: TimeInterval = feedback) {
        let previous = from ?? CGFloat(view.layer?.presentation()?.opacity ?? Float(view.alphaValue))
        view.wantsLayer = true
        view.alphaValue = value
        view.layer?.removeAnimation(forKey: "workspace.opacity")
        guard enabled, previous != value else { return }
        let animation = CABasicAnimation(keyPath: "opacity")
        animation.fromValue = previous
        animation.toValue = value
        animation.duration = duration
        animation.timingFunction = CAMediaTimingFunction(name: .easeOut)
        view.layer?.add(animation, forKey: "workspace.opacity")
    }

    static func collapse(_ item: NSSplitViewItem, to collapsed: Bool, animated: Bool) {
        guard animated, enabled else { item.isCollapsed = collapsed; return }
        NSAnimationContext.runAnimationGroup { context in
            context.duration = disclosure
            context.timingFunction = CAMediaTimingFunction(name: .easeInEaseOut)
            item.animator().isCollapsed = collapsed
        }
    }
}

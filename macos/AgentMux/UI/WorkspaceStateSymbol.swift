import AppKit
import SwiftUI

/// Workspace state presentation. Text glyphs remain available to menus and titles.
enum WorkspaceStateSymbol {
    static func name(for state: String) -> String? {
        switch state {
        case "bolt": return "bolt.fill"
        case "stop": return "checkmark.circle.fill"
        case "key": return "key.fill"
        case "question": return "questionmark.bubble.fill"
        case "warning": return "exclamationmark.triangle.fill"
        case "error": return "xmark.octagon.fill"
        case "subagent": return "person.2.fill"
        case "compact": return "arrow.triangle.2.circlepath"
        default: return nil
        }
    }

    static func isWorking(_ state: String) -> Bool {
        ["bolt", "subagent", "compact"].contains(state)
    }

    /// Keep the completed-turn green legible on both sidebar appearances.
    static let completedColor = NSColor(name: nil) { appearance in
        if appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua {
            return NSColor(srgbRed: 0x61 / 255.0, green: 0xd9 / 255.0, blue: 0x85 / 255.0, alpha: 1)
        }
        return NSColor(srgbRed: 0x16 / 255.0, green: 0x70 / 255.0, blue: 0x38 / 255.0, alpha: 1)
    }

    static func color(for state: String) -> NSColor {
        switch state {
        case "key", "question": return .systemOrange
        case "bolt", "subagent", "compact": return .systemBlue
        case "stop": return completedColor
        case "error": return .systemRed
        default: return .secondaryLabelColor
        }
    }
}

@MainActor
final class WorkspaceStateImageView: NSImageView {
    private(set) var iconState = ""
    private(set) var isPulsing = false

    init() {
        super.init(frame: .zero)
        imageScaling = .scaleProportionallyDown
        NSLayoutConstraint.activate([
            widthAnchor.constraint(equalToConstant: 20),
            heightAnchor.constraint(equalToConstant: 20),
        ])
        NSWorkspace.shared.notificationCenter.addObserver(
            self, selector: #selector(updateMotion),
            name: NSWorkspace.accessibilityDisplayOptionsDidChangeNotification, object: nil)
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) is not supported") }

    func show(_ state: String) {
        let changed = iconState != state
        iconState = state
        let words = WorkspaceInspector.word(for: state)
        image = WorkspaceStateSymbol.name(for: state).flatMap {
            NSImage(systemSymbolName: $0, accessibilityDescription: words)?
                .withSymbolConfiguration(.init(pointSize: 14, weight: .regular))
        }
        contentTintColor = WorkspaceStateSymbol.color(for: state)
        setAccessibilityLabel(words)
        if changed, #available(macOS 14.0, *) {
            removeAllSymbolEffects()
            isPulsing = false
        }
        updateMotion()
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        updateMotion()
    }

    @objc private func updateMotion() {
        guard #available(macOS 14.0, *) else { return }
        let pulse = window != nil && WorkspaceMotion.enabled && WorkspaceStateSymbol.isWorking(iconState)
        guard pulse != isPulsing else { return }
        removeAllSymbolEffects()
        if pulse { addSymbolEffect(.pulse, options: .repeating) }
        isPulsing = pulse
    }
}

@MainActor
struct WorkspaceStateSymbolView: View {
    let state: String
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        if let name = WorkspaceStateSymbol.name(for: state) {
            if #available(macOS 14.0, *) {
                symbol(name).symbolEffect(.pulse, options: .repeating,
                    isActive: WorkspaceMotion.enabled && !reduceMotion && WorkspaceStateSymbol.isWorking(state))
            } else {
                symbol(name)
            }
        }
    }

    private func symbol(_ name: String) -> some View {
        Image(systemName: name)
            .foregroundStyle(Color(nsColor: WorkspaceStateSymbol.color(for: state)))
            .accessibilityLabel(WorkspaceInspector.word(for: state))
    }
}

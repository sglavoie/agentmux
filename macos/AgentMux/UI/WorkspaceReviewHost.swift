import AppKit
import SwiftUI

/// Review shortcuts belong to the focused review pane. A Changes pane beside
/// an agent must not consume that agent's Find, Refresh, or editor shortcuts.
final class WorkspaceReviewHost<Content: View>: NSHostingView<Content> {
    var closesWindow = false

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        guard ownsKeyboard else { return false }
        if closesWindow, event.charactersIgnoringModifiers == "w",
           event.modifierFlags.intersection([.command, .shift, .option, .control]) == .command {
            window?.performClose(nil)
            return true
        }
        // Resolve modifiers explicitly before SwiftUI's hosting shortcut table.
        // Paired commands (resume/fork, next/previous) must never alias.
        if let receiver = shortcutReceiver(in: self),
           receiver.handle(event.charactersIgnoringModifiers?.lowercased() ?? "",
                           event.modifierFlags.intersection([.command, .shift, .option, .control])) {
            return true
        }
        return super.performKeyEquivalent(with: event)
    }

    private func shortcutReceiver(in view: NSView) -> WorkspaceReviewShortcutReceiver? {
        if let receiver = view as? WorkspaceReviewShortcutReceiver { return receiver }
        for child in view.subviews {
            if let receiver = shortcutReceiver(in: child) { return receiver }
        }
        return nil
    }

    var ownsKeyboard: Bool {
        guard let responder = window?.firstResponder as? NSView else { return false }
        if responder === self || responder.isDescendant(of: self) { return true }
        // AppKit's shared field editor is outside the hosting view's subtree.
        if let text = responder as? NSTextView, text.isFieldEditor,
           let owner = text.delegate as? NSView {
            return owner.isDescendant(of: self)
        }
        return false
    }
}

/// Keeps handlers attached to the current SwiftUI state, including FocusState.
struct WorkspaceReviewShortcuts: NSViewRepresentable {
    let handle: (String, NSEvent.ModifierFlags) -> Bool
    func makeNSView(context: Context) -> WorkspaceReviewShortcutReceiver {
        WorkspaceReviewShortcutReceiver(handle: handle)
    }
    func updateNSView(_ view: WorkspaceReviewShortcutReceiver, context: Context) { view.handle = handle }
}

final class WorkspaceReviewShortcutReceiver: NSView {
    var handle: (String, NSEvent.ModifierFlags) -> Bool
    init(handle: @escaping (String, NSEvent.ModifierFlags) -> Bool) {
        self.handle = handle
        super.init(frame: .zero)
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }
}

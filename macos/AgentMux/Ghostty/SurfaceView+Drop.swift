// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import AppKit
import GhosttyKit

extension Ghostty.SurfaceView {
    // MARK: Drag and drop

    /// Files arrive as shell-escaped paths, other URLs shell-escaped
    /// too, and text as text.
    static let dropTypes: [NSPasteboard.PasteboardType] = [.fileURL, .URL, .string]

    /// What a drop types into the terminal. A URL is a word for the
    /// shell, so `&` or `?` in it never start anything.
    static func dropText(from pasteboard: NSPasteboard) -> String? {
        if let urls = pasteboard.readObjects(forClasses: [NSURL.self]) as? [URL], !urls.isEmpty {
            return urls
                .map { Ghostty.App.shellEscape($0.isFileURL ? $0.path : $0.absoluteString) }
                .joined(separator: " ")
        }
        return pasteboard.string(forType: .string).flatMap { $0.isEmpty ? nil : $0 }
    }

    /// Dropped text of more than one line is pasted, so Ghostty's paste
    /// protection asks about it first and a bracketed paste stays one.
    static func dropIsPaste(_ text: String) -> Bool {
        text.contains(where: \.isNewline)
    }

    /// Types dropped text into the terminal, or pastes it when it spans
    /// lines. Returns whether Ghostty took it.
    @discardableResult
    func drop(_ text: String) -> Bool {
        guard let surface else { return false }
        if Self.dropIsPaste(text) { return paste(text) }
        let len = text.utf8CString.count
        text.withCString { ptr in
            ghostty_surface_text(surface, ptr, UInt(len - 1))
        }
        return true
    }

    /// Runs Ghostty's own paste on `text` instead of the clipboard's: the
    /// request that paste makes is answered from `pendingPaste`
    /// (`Ghostty.App.readClipboard`), and the clipboard is left alone.
    @discardableResult
    func paste(_ text: String) -> Bool {
        pendingPaste = text
        defer { pendingPaste = nil }
        return performBindingAction("paste_from_clipboard")
    }

    override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
        guard surface != nil else { return [] }
        return Self.dropText(from: sender.draggingPasteboard) == nil ? [] : .copy
    }

    override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
        guard surface != nil, let text = Self.dropText(from: sender.draggingPasteboard), drop(text) else { return false }
        // A drop from Finder leaves Finder in front; keep typing here.
        NSApp.activate()
        window?.makeKeyAndOrderFront(nil)
        window?.makeFirstResponder(self)
        return true
    }
}

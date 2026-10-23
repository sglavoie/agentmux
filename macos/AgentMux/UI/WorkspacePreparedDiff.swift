import AppKit

/// A private, detached TextKit graph. Construct and fully lay it out on one
/// worker queue, then transfer ownership to the main thread. No NSTextView is
/// attached during background work and automatic background layout is disabled.
/// https://developer.apple.com/library/archive/documentation/Cocoa/Conceptual/TextLayout/Concepts/LayoutManager.html
final class WorkspacePreparedDiff {
    let storage: NSTextStorage
    let layout: NSLayoutManager
    let container: NSTextContainer
    let height: CGFloat

    init(text: NSAttributedString, width: CGFloat) {
        storage = NSTextStorage(attributedString: text)
        layout = NSLayoutManager()
        layout.backgroundLayoutEnabled = false
        container = NSTextContainer(containerSize: NSSize(width: width, height: CGFloat.greatestFiniteMagnitude))
        container.widthTracksTextView = false
        storage.addLayoutManager(layout)
        layout.addTextContainer(container)
        layout.ensureLayout(forCharacterRange: NSRange(location: 0, length: storage.length))
        height = layout.usedRect(for: container).maxY
    }
}

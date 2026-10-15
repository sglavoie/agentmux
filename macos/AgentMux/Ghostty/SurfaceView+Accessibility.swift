// Ported from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import AppKit
import GhosttyKit

// What VoiceOver and other assistive technologies read of a terminal: a text
// area holding the screen's text, with the selection. A session's terminal
// only views the tmux server, so its screen is the session's visible pane.
extension Ghostty.SurfaceView {
    /// How long a reading of the screen is reused: an assistive technology
    /// asks for the value, its length, and its ranges in quick succession.
    static let accessibilityContentsLifetime: Duration = .milliseconds(500)

    /// The text of the whole screen, reread at most once per
    /// `accessibilityContentsLifetime`.
    var screenContents: String {
        let now = ContinuousClock.now
        if let cached = accessibilityContents, now - cached.read < Self.accessibilityContentsLifetime {
            return cached.text
        }
        let text = readScreen()
        accessibilityContents = (text, now)
        return text
    }

    private func readScreen() -> String {
        readScreen(to: ghostty_point_s(tag: GHOSTTY_POINT_SCREEN, coord: GHOSTTY_POINT_COORD_BOTTOM_RIGHT, x: 0, y: 0))
            ?? ""
    }

    /// The screen's text from its first cell through `end`, serialized as
    /// `screenContents` is, so it is always a prefix of that text.
    private func readScreen(to end: ghostty_point_s) -> String? {
        guard let surface else { return nil }
        var text = ghostty_text_s()
        let selection = ghostty_selection_s(
            top_left: ghostty_point_s(tag: GHOSTTY_POINT_SCREEN, coord: GHOSTTY_POINT_COORD_TOP_LEFT, x: 0, y: 0),
            bottom_right: end,
            rectangle: false)
        guard ghostty_surface_read_text(surface, selection, &text) else { return nil }
        defer { ghostty_surface_free_text(surface, &text) }
        return String(cString: text.text)
    }

    /// The selection as a UTF-16 range of `screenContents`, or nil when it
    /// is not a run of that text: nothing selected, a selection reaching
    /// outside the viewport, or a rectangle.
    ///
    /// Ghostty reports a selection's place as grid cells (row × columns +
    /// column), which line breaks, short rows, wide characters, and
    /// multi-unit characters all keep from being string offsets. The
    /// selection's last cell is a viewport point, though, and the text from
    /// the screen's first cell through it ends where the selection does.
    private func selectionInScreenContents() -> NSRange? {
        guard let surface else { return nil }
        var text = ghostty_text_s()
        guard ghostty_surface_read_selection(surface, &text) else { return nil }
        let selected = String(cString: text.text)
        let inViewport = text.tl_px_x >= 0
        let last = Int(text.offset_start) + Int(text.offset_len)
        ghostty_surface_free_text(surface, &text)
        let columns = Int(ghostty_surface_size(surface).columns)
        guard inViewport, columns > 0, !selected.isEmpty else { return nil }

        let end = ghostty_point_s(
            tag: GHOSTTY_POINT_VIEWPORT, coord: GHOSTTY_POINT_COORD_EXACT,
            x: UInt32(last % columns), y: UInt32(last / columns))
        guard let prefix = readScreen(to: end) else { return nil }
        let length = selected.utf16.count
        let range = NSRange(location: prefix.utf16.count - length, length: length)
        guard range.location >= 0 else { return nil }
        // The screen may have changed since `screenContents` was read; read
        // it again once rather than name a range of other text.
        for attempt in 0..<2 {
            if attempt == 1 { accessibilityContents = nil }
            let content = screenContents as NSString
            if NSMaxRange(range) <= content.length, content.substring(with: range) == selected {
                return range
            }
        }
        return nil
    }

    override func isAccessibilityElement() -> Bool {
        true
    }

    /// A text area: the terminal is where commands are typed and their
    /// output read.
    override func accessibilityRole() -> NSAccessibility.Role? {
        .textArea
    }

    override func accessibilityHelp() -> String? {
        "Terminal content area"
    }

    override func accessibilityValue() -> Any? {
        screenContents
    }

    /// Empty at the start of the screen when no range of its text is
    /// selected; never `selectedRange()`'s grid-cell offsets.
    override func accessibilitySelectedTextRange() -> NSRange {
        selectionInScreenContents() ?? NSRange(location: 0, length: 0)
    }

    override func accessibilitySelectedText() -> String? {
        guard let surface else { return nil }
        var text = ghostty_text_s()
        guard ghostty_surface_read_selection(surface, &text) else { return nil }
        defer { ghostty_surface_free_text(surface, &text) }
        let selected = String(cString: text.text)
        return selected.isEmpty ? nil : selected
    }

    // Accessibility counts and ranges are in UTF-16 units, as NSRange's
    // are everywhere in AppKit.

    override func accessibilityNumberOfCharacters() -> Int {
        screenContents.utf16.count
    }

    /// The whole screen is visible.
    override func accessibilityVisibleCharacterRange() -> NSRange {
        NSRange(location: 0, length: screenContents.utf16.count)
    }

    /// The line holding the character at `index`, from zero.
    override func accessibilityLine(for index: Int) -> Int {
        let content = screenContents as NSString
        let before = content.substring(to: max(0, min(index, content.length)))
        return before.components(separatedBy: .newlines).count - 1
    }

    override func accessibilityString(for range: NSRange) -> String? {
        let content = screenContents
        guard let swiftRange = Range(range, in: content) else { return nil }
        return String(content[swiftRange])
    }

    /// The text in `range` in the terminal's font; Ghostty exposes no other
    /// styling.
    override func accessibilityAttributedString(for range: NSRange) -> NSAttributedString? {
        guard let surface, let plain = accessibilityString(for: range) else { return nil }
        var attributes: [NSAttributedString.Key: Any] = [:]
        if let fontRaw = ghostty_surface_quicklook_font(surface) {
            // Ghostty hands over a retained copy of its CTFont; the
            // dictionary retains its own, so the copy is released here.
            let font = Unmanaged<CTFont>.fromOpaque(fontRaw)
            attributes[.font] = font.takeUnretainedValue()
            font.release()
        }
        return NSAttributedString(string: plain, attributes: attributes)
    }
}

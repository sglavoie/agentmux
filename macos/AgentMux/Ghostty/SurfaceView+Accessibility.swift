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
        guard let surface else { return "" }
        var text = ghostty_text_s()
        let selection = ghostty_selection_s(
            top_left: ghostty_point_s(tag: GHOSTTY_POINT_SCREEN, coord: GHOSTTY_POINT_COORD_TOP_LEFT, x: 0, y: 0),
            bottom_right: ghostty_point_s(tag: GHOSTTY_POINT_SCREEN, coord: GHOSTTY_POINT_COORD_BOTTOM_RIGHT, x: 0, y: 0),
            rectangle: false)
        guard ghostty_surface_read_text(surface, selection, &text) else { return "" }
        defer { ghostty_surface_free_text(surface, &text) }
        return String(cString: text.text)
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

    override func accessibilitySelectedTextRange() -> NSRange {
        selectedRange()
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

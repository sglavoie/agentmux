import AppKit

/// The find bar over a utility view: a search through the terminal's own
/// buffer, which Ghostty runs. A session's terminal searches the tmux
/// server's history instead and never shows it.
///
/// Return goes to the next match and Shift-Return to the previous one; Esc
/// closes the bar. ⌘G and ⇧⌘G reach it through the responder chain while it
/// has the keyboard, as they reach the terminal while that has it.
@MainActor
final class WorkspaceFindBar: NSView, NSSearchFieldDelegate {
    let field = NSSearchField()
    let countLabel = NSTextField(labelWithString: "")
    let previousButton = NSButton()
    let nextButton = NSButton()
    let doneButton = NSButton()

    /// The needle to search for, once typing paused (`delay(for:)`).
    var onNeedle: ((String) -> Void)?
    var onNext: (() -> Void)?
    var onPrevious: (() -> Void)?
    var onClose: (() -> Void)?

    /// Counts edits, so a needle overtaken by later typing is never sent.
    private var edits = 0

    /// The search the field types into: which view, and which time Find
    /// was opened there. A needle still waiting when it changes is dropped,
    /// so it can never reach another view or a search opened again.
    var context: AnyHashable? {
        didSet { if context != oldValue { edits += 1 } }
    }

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
        layer?.cornerRadius = 6
        layer?.backgroundColor = NSColor.windowBackgroundColor.cgColor
        layer?.borderColor = NSColor.separatorColor.cgColor
        layer?.borderWidth = 1

        field.placeholderString = "Find"
        field.delegate = self
        field.sendsSearchStringImmediately = true
        field.sendsWholeSearchString = false
        field.target = self
        field.action = #selector(fieldChanged(_:))
        field.setAccessibilityLabel("Find in terminal")
        field.translatesAutoresizingMaskIntoConstraints = false

        countLabel.font = .monospacedDigitSystemFont(ofSize: NSFont.smallSystemFontSize, weight: .regular)
        countLabel.textColor = .secondaryLabelColor
        countLabel.setContentCompressionResistancePriority(.required, for: .horizontal)

        for (button, symbol, label, action) in [
            (previousButton, "chevron.up", "Previous Match", #selector(findPrevious(_:))),
            (nextButton, "chevron.down", "Next Match", #selector(findNext(_:))),
            (doneButton, "xmark", "Close Find", #selector(close(_:))),
        ] {
            button.bezelStyle = .accessoryBarAction
            button.isBordered = false
            button.image = NSImage(systemSymbolName: symbol, accessibilityDescription: label)
            button.toolTip = label
            button.setAccessibilityLabel(label)
            button.target = self
            button.action = action
            button.refusesFirstResponder = true
        }

        let stack = NSStackView(views: [field, countLabel, previousButton, nextButton, doneButton])
        stack.orientation = .horizontal
        stack.spacing = 4
        stack.edgeInsets = NSEdgeInsets(top: 4, left: 6, bottom: 4, right: 4)
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor),
            stack.topAnchor.constraint(equalTo: topAnchor),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor),
            field.widthAnchor.constraint(equalToConstant: 200),
        ])
        setAccessibilityElement(true)
        setAccessibilityRole(.group)
        setAccessibilityLabel("Find")
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    /// Shows a search: its needle, unless it is being typed, and its count.
    func show(_ search: Ghostty.Search) {
        if field.stringValue != search.needle, field.currentEditor() == nil {
            field.stringValue = search.needle
        }
        countLabel.stringValue = Self.countText(search)
        let hasMatches = (search.total ?? 0) > 0
        previousButton.isEnabled = hasMatches
        nextButton.isEnabled = hasMatches
    }

    /// Gives the field the keyboard, its text selected to type over.
    func focus() {
        guard let window else { return }
        window.makeFirstResponder(field)
        field.currentEditor()?.selectAll(nil)
    }

    /// Whether the field is being typed in.
    var hasKeyboard: Bool { field.currentEditor() != nil }

    /// What the bar says of the matches: nothing until there is a needle
    /// and they are counted.
    nonisolated static func countText(_ search: Ghostty.Search) -> String {
        guard !search.needle.isEmpty, let total = search.total else { return "" }
        if total == 0 { return "No matches" }
        if let selected = search.selected { return "\(selected + 1) of \(total)" }
        return total == 1 ? "1 match" : "\(total) matches"
    }

    /// How long typing must pause before a needle is searched for. Short
    /// needles match nearly everywhere, so they wait, as upstream does;
    /// nil sends at once.
    nonisolated static func delay(for needle: String) -> TimeInterval? {
        needle.isEmpty || needle.count >= 3 ? nil : 0.3
    }

    @objc private func fieldChanged(_ sender: Any?) {
        needleChanged(field.stringValue)
    }

    func needleChanged(_ needle: String) {
        edits += 1
        guard let delay = Self.delay(for: needle) else {
            onNeedle?(needle)
            return
        }
        let edit = edits
        DispatchQueue.main.asyncAfter(deadline: .now() + delay) { [weak self] in
            guard let self, self.edits == edit else { return }
            self.onNeedle?(needle)
        }
    }

    func control(_ control: NSControl, textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
        switch commandSelector {
        case #selector(NSResponder.cancelOperation(_:)):
            onClose?()
            return true
        case #selector(NSResponder.insertNewline(_:)):
            if NSApp.currentEvent?.modifierFlags.contains(.shift) == true { onPrevious?() } else { onNext?() }
            return true
        default:
            return false
        }
    }

    // The Edit menu's Find items, while the bar has the keyboard.
    @objc func find(_ sender: Any?) { focus() }

    @objc func findNext(_ sender: Any?) { onNext?() }

    @objc func findPrevious(_ sender: Any?) { onPrevious?() }

    @objc private func close(_ sender: Any?) { onClose?() }
}

/// The scrollbar over a utility view, drawn from where Ghostty says the
/// viewport sits in the terminal's own buffer. Dragging the knob, or
/// clicking the track, scrolls the terminal there (`scroll_to_row`).
@MainActor
final class WorkspaceScrollbarView: NSView {
    static let width: CGFloat = 10
    static let minimumKnob: CGFloat = 24

    var scrollbar: Ghostty.Scrollbar? {
        didSet {
            guard scrollbar != oldValue else { return }
            isHidden = !(scrollbar?.scrolls ?? false)
            needsDisplay = true
        }
    }

    /// The first row to show, as the knob is moved.
    var onScroll: ((UInt64) -> Void)?
    /// The terminal under the bar, which takes the wheel turned over it.
    weak var terminal: NSView?

    /// Where in the knob the pointer took it, from its middle.
    private var grab: CGFloat = 0
    private var lastRow: UInt64?

    override init(frame: NSRect) {
        super.init(frame: frame)
        isHidden = true
        setAccessibilityElement(true)
        setAccessibilityRole(.scrollBar)
        setAccessibilityLabel("Terminal history")
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    override var isFlipped: Bool { false }

    /// The knob's frame for a scrollbar within `bounds`, its top at the
    /// top for the top of history.
    nonisolated static func knobRect(_ scrollbar: Ghostty.Scrollbar, in bounds: NSRect) -> NSRect {
        let height = min(bounds.height, max(minimumKnob, bounds.height * scrollbar.knobProportion))
        let travel = bounds.height - height
        let y = bounds.maxY - height - travel * scrollbar.position
        return NSRect(x: bounds.minX, y: y, width: bounds.width, height: height)
    }

    /// The position (0 at the top of history, 1 at the bottom) of a knob
    /// whose middle is at `midY`.
    nonisolated static func position(knobMidY midY: CGFloat, knobHeight: CGFloat, in bounds: NSRect) -> Double {
        let travel = bounds.height - knobHeight
        guard travel > 0 else { return 1 }
        let top = bounds.maxY - knobHeight / 2
        return min(1, max(0, Double((top - midY) / travel)))
    }

    override func draw(_ dirtyRect: NSRect) {
        guard let scrollbar, scrollbar.scrolls else { return }
        let knob = Self.knobRect(scrollbar, in: bounds).insetBy(dx: 2, dy: 2)
        NSColor.gray.withAlphaComponent(0.6).setFill()
        NSBezierPath(roundedRect: knob, xRadius: knob.width / 2, yRadius: knob.width / 2).fill()
    }

    override func mouseDown(with event: NSEvent) {
        guard let scrollbar else { return }
        let point = convert(event.locationInWindow, from: nil)
        let knob = Self.knobRect(scrollbar, in: bounds)
        // A click on the track brings the knob's middle there.
        grab = knob.contains(point) ? point.y - knob.midY : 0
        lastRow = nil
        drag(to: point)
    }

    override func mouseDragged(with event: NSEvent) {
        drag(to: convert(event.locationInWindow, from: nil))
    }

    private func drag(to point: NSPoint) {
        guard let scrollbar else { return }
        let knob = Self.knobRect(scrollbar, in: bounds)
        let position = Self.position(knobMidY: point.y - grab, knobHeight: knob.height, in: bounds)
        let row = scrollbar.row(at: position)
        guard row != lastRow else { return }
        lastRow = row
        onScroll?(row)
    }

    override func scrollWheel(with event: NSEvent) {
        if let terminal { terminal.scrollWheel(with: event) } else { super.scrollWheel(with: event) }
    }
}

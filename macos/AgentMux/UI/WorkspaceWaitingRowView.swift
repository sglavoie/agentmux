import AppKit
import SwiftUI
import Combine

/// One waiting session: a click goes to it, as a click on its line in the
/// session list does. One that can be answered from here has an Answer…
/// button, which opens a panel and leaves the pane where it is.
@MainActor
final class WorkspaceWaitingRowView: NSBox {
    static let answerTitle = "Answer…"

    private let flashView = WorkspaceRowFlashView()

    func flash() {
        guard let contentView else { return }
        if flashView.superview !== contentView {
            flashView.frame = contentView.bounds
            flashView.autoresizingMask = [.width, .height]
            contentView.addSubview(flashView, positioned: .above, relativeTo: nil)
        }
        flashView.flash()
        flashView.layer?.cornerRadius = cornerRadius
    }

    var isFlashing: Bool { flashView.layer?.animation(forKey: WorkspaceRowView.flashKey) != nil }

    private(set) var row: WorkspaceWaitingRow
    var onPress: (() -> Void)?
    var onAnswer: ((WorkspaceWaitingAnswer) -> Void)?
    var onContext: (() -> Void)?
    var onRefreshContext: (() -> Void)?
    let contextButton = NSButton(title: "Context", target: nil, action: nil)
    let refreshContextButton = NSButton(title: "Refresh", target: nil, action: nil)
    let contextStatus = NSTextField(wrappingLabelWithString: "")
    private let contextHeader = NSStackView()
    private var proseView: NSHostingView<WorkspaceTranscriptText>?
    private var readingPreferences = WorkspaceReadingPreferences.shared
    private var shownReading: WorkspaceWaitingContext.Reading?
    let contextLabel = NSTextField(wrappingLabelWithString: "")
    let contextScroll = NSScrollView()
    private var contextHeight: NSLayoutConstraint!
    /// Nil on a row with nothing to answer from here.
    private(set) var answerButton: NSButton?
    let titleLabel = NSTextField(labelWithString: "")
    let detailLabel = NSTextField(labelWithString: "")
    /// Whether the pointer is over the line, and whether it is held down on
    /// it: each draws the line darker, as a button's would be.
    var isHovered = false {
        didSet { syncFill() }
    }
    var isPressed = false {
        didSet { syncFill() }
    }

    static let restingFill = NSColor.labelColor.withAlphaComponent(0.035)
    static let hoveredFill = NSColor.labelColor.withAlphaComponent(0.07)
    static let pressedFill = NSColor.labelColor.withAlphaComponent(0.12)

    private func syncFill() {
        fillColor = isPressed ? Self.pressedFill : isHovered ? Self.hoveredFill : Self.restingFill
    }

    init(_ row: WorkspaceWaitingRow) {
        self.row = row
        super.init(frame: .zero)
        boxType = .custom
        titlePosition = .noTitle
        borderWidth = 0
        cornerRadius = 6
        fillColor = Self.restingFill
        contentViewMargins = .zero
        addTrackingArea(NSTrackingArea(rect: .zero, options: [.mouseEnteredAndExited, .activeInActiveApp, .inVisibleRect],
                                       owner: self, userInfo: nil))

        titleLabel.font = .systemFont(ofSize: NSFont.systemFontSize, weight: .medium)
        titleLabel.lineBreakMode = .byWordWrapping
        titleLabel.maximumNumberOfLines = 2
        titleLabel.cell?.wraps = true
        detailLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        detailLabel.textColor = .secondaryLabelColor
        detailLabel.lineBreakMode = .byWordWrapping
        detailLabel.maximumNumberOfLines = 2
        detailLabel.cell?.wraps = true

        let text = NSStackView(views: [titleLabel, detailLabel])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 1
        let actions = NSStackView()
        actions.orientation = .horizontal
        actions.spacing = 8
        let stack = NSStackView(views: [text])
        stack.orientation = .horizontal
        stack.alignment = .centerY
        stack.spacing = 6
        stack.translatesAutoresizingMaskIntoConstraints = false
        if row.answer != nil {
            let button = NSButton(title: Self.answerTitle, target: self, action: #selector(answerPressed(_:)))
            button.controlSize = .small
            button.bezelStyle = .rounded
            button.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
            // The terminal that has the keyboard keeps it.
            button.refusesFirstResponder = true
            button.setContentHuggingPriority(.required, for: .horizontal)
            button.setContentCompressionResistancePriority(.required, for: .horizontal)
            button.bezelColor = .controlAccentColor
            actions.addArrangedSubview(button)
            answerButton = button
        }
        text.setContentHuggingPriority(.defaultLow, for: .horizontal)
        contextButton.target = self
        contextButton.action = #selector(contextPressed(_:))
        contextButton.controlSize = .small
        contextButton.bezelStyle = .rounded
        contextButton.isBordered = false
        contextButton.refusesFirstResponder = true
        contextLabel.font = .monospacedSystemFont(ofSize: 11, weight: .regular)
        contextLabel.isSelectable = true
        contextLabel.translatesAutoresizingMaskIntoConstraints = false
        contextScroll.documentView = contextLabel
        contextScroll.hasVerticalScroller = true
        contextScroll.drawsBackground = false
        contextScroll.isHidden = true
        actions.addArrangedSubview(contextButton)
        refreshContextButton.target = self
        refreshContextButton.action = #selector(refreshContextPressed(_:))
        refreshContextButton.controlSize = .small
        refreshContextButton.bezelStyle = .rounded
        refreshContextButton.refusesFirstResponder = true
        refreshContextButton.setAccessibilityLabel("Refresh context for \(row.title)")
        contextStatus.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        contextStatus.isSelectable = true
        contextStatus.maximumNumberOfLines = 3
        contextStatus.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        contextHeader.addArrangedSubview(contextStatus)
        actions.addArrangedSubview(refreshContextButton)
        refreshContextButton.isBordered = false
        refreshContextButton.image = NSImage(systemSymbolName: "arrow.clockwise", accessibilityDescription: "Refresh context")
        refreshContextButton.imagePosition = .imageOnly
        refreshContextButton.toolTip = "Refresh context"
        refreshContextButton.isHidden = true
        contextHeader.alignment = .top
        contextHeader.spacing = 4
        contextHeader.isHidden = true
        let column = NSStackView(views: [stack, actions, contextScroll, contextHeader])
        column.orientation = .vertical
        column.alignment = .leading
        column.spacing = 4
        column.translatesAutoresizingMaskIntoConstraints = false
        let content = NSView()
        content.addSubview(column)
        contentView = content
        contextHeight = contextScroll.heightAnchor.constraint(equalToConstant: 160)
        NSLayoutConstraint.activate([
            stack.widthAnchor.constraint(equalTo: column.widthAnchor),
            contextHeader.widthAnchor.constraint(equalTo: column.widthAnchor),
            contextScroll.widthAnchor.constraint(equalTo: column.widthAnchor),
            contextHeight,
            contextLabel.widthAnchor.constraint(equalTo: contextScroll.contentView.widthAnchor),
            column.topAnchor.constraint(equalTo: content.topAnchor, constant: 6),
            column.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -6),
            column.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 8),
            column.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -8),
        ])
        for label in [titleLabel, detailLabel] {
            label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
            // The line is said as one; its labels are not said again. Its
            // Answer… button stays an element of its own.
            label.setAccessibilityElement(false)
        }
        setAccessibilityElement(true)
        setAccessibilityRole(.button)
        update(row)
    }

    var readingHeight: CGFloat { contextHeight.constant }

    func configureReading(preferences: WorkspaceReadingPreferences, height: CGFloat) {
        readingPreferences = preferences
        let isProse: Bool
        if case .asl? = row.answer { isProse = true } else { isProse = row.key.runtimeSessionID.isEmpty }
        contextLabel.font = isProse ? .systemFont(ofSize: preferences.effectiveProseSize) : .monospacedSystemFont(ofSize: preferences.effectiveFontSize, weight: .regular)
        if contextHeight.constant != height { contextHeight.constant = height }
    }

    func showContext(_ reading: WorkspaceWaitingContext.Reading?) {
        contextHeader.isHidden = reading == nil
        refreshContextButton.isHidden = reading == nil
        detailLabel.isHidden = reading != nil
        contextScroll.isHidden = reading == nil
        contextButton.title = reading == nil ? "Context" : "Hide context"
        contextButton.setAccessibilityLabel("\(contextButton.title) for \(row.title)")
        contextButton.cell?.setAccessibilityLabel("\(contextButton.title) for \(row.title)")
        defer { shownReading = reading }
        guard let reading else {
            contextLabel.stringValue = ""
            proseView = nil; contextScroll.documentView = contextLabel
            return
        }
        let age = reading.refreshedAt.map { "Read " + $0.formatted(date: .omitted, time: .standard) }
        var status = reading.loading ? (reading.content == nil ? "Loading context…" : "Refreshing context…")
            : reading.unavailable ? "Context unavailable" : reading.failed ? "Context read failed" : reading.problem != nil ? "Partial context" : age ?? "Not read yet"
        if let problem = reading.problem { status += ": " + problem.reveal() }
        if reading.content != nil && (reading.loading || reading.failed) {
            status += " · Previous context remains visible" + (age.map { " · " + $0 } ?? "")
        }
        contextStatus.stringValue = status
        contextStatus.toolTip = status + (reading.refreshedAt.map { "\n" + $0.formatted(.dateTime.year().month().day().hour().minute().second().timeZone()) } ?? "")
        contextStatus.textColor = reading.problem == nil ? .secondaryLabelColor : .systemOrange
        refreshContextButton.title = reading.loading ? "Reading…" : reading.problem == nil ? "Refresh" : "Retry"
        refreshContextButton.imagePosition = reading.problem == nil ? .imageOnly : .imageLeading
        refreshContextButton.isEnabled = !reading.loading
        refreshContextButton.toolTip = reading.loading ? "Reading context…" : reading.problem == nil ? "Refresh context" : "Retry context read"
        refreshContextButton.setAccessibilityLabel("\(refreshContextButton.toolTip!) for \(row.title)")
        refreshContextButton.cell?.setAccessibilityLabel("\(refreshContextButton.toolTip!) for \(row.title)")
        guard reading.content != shownReading?.content else { return }
        let text = reading.content?.reveal() ?? ""
        let origin = contextScroll.contentView.bounds.origin
        contextLabel.stringValue = text
        if case .asl? = row.answer, reading.content != nil {
            let body = WorkspaceTranscriptText(text: text, terms: [], preferences: readingPreferences)
            if let proseView { proseView.rootView = body }
            else {
                let host = NSHostingView(rootView: body)
                host.translatesAutoresizingMaskIntoConstraints = false
                contextScroll.documentView = host
                host.widthAnchor.constraint(equalTo: contextScroll.contentView.widthAnchor).isActive = true
                proseView = host
            }
        } else {
            proseView = nil
            contextScroll.documentView = contextLabel
        }
        contextScroll.layoutSubtreeIfNeeded()
        contextScroll.contentView.scroll(to: origin)
        contextScroll.reflectScrolledClipView(contextScroll.contentView)
    }

    @objc private func contextPressed(_ sender: Any?) { onContext?() }
    @objc private func refreshContextPressed(_ sender: Any?) { onRefreshContext?() }

    /// Shows `row` on this line: the same session, said anew. Whether it has
    /// an Answer… button is the line's own and does not change.
    func update(_ row: WorkspaceWaitingRow) {
        self.row = row
        titleLabel.stringValue = [row.glyph, row.title].filter { !$0.isEmpty }.joined(separator: " ")
        detailLabel.stringValue = row.detail
        // The button's cell is the element VoiceOver reads.
        answerButton?.setAccessibilityLabel("Answer \(row.title)")
        answerButton?.cell?.setAccessibilityLabel("Answer \(row.title)")
        contextButton.setAccessibilityLabel("\(contextButton.title) for \(row.title)")
        contextButton.cell?.setAccessibilityLabel("\(contextButton.title) for \(row.title)")
        toolTip = row.toolTip
        setAccessibilityLabel("\(row.title), \(row.detail)")
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    // The whole line is one target; its labels take no clicks of their own.
    // Its button does.
    override func hitTest(_ point: NSPoint) -> NSView? {
        guard let hit = super.hitTest(point) else { return nil }
        if let answerButton, hit.isDescendant(of: answerButton) { return hit }
        if hit.isDescendant(of: contextButton) || hit.isDescendant(of: refreshContextButton)
            || hit.isDescendant(of: contextHeader) || hit.isDescendant(of: contextScroll) { return hit }
        return self
    }

    @objc private func answerPressed(_ sender: Any?) {
        if let answer = row.answer { onAnswer?(answer) }
    }

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func mouseEntered(with event: NSEvent) { isHovered = true }
    override func mouseExited(with event: NSEvent) { isHovered = false }

    override func mouseDown(with event: NSEvent) { isPressed = true }

    override func mouseDragged(with event: NSEvent) {
        isPressed = bounds.contains(convert(event.locationInWindow, from: nil))
    }

    override func mouseUp(with event: NSEvent) {
        isPressed = false
        guard bounds.contains(convert(event.locationInWindow, from: nil)) else { return }
        press()
    }

    func press() { onPress?() }

    override func accessibilityPerformPress() -> Bool {
        press()
        return true
    }

    /// Expose the actions and expanded context without repeating the row labels.
    override func accessibilityChildren() -> [Any]? {
        var children: [NSView] = answerButton.map { [$0] } ?? []
        children.append(contextButton)
        if !contextScroll.isHidden { children.append(contentsOf: [refreshContextButton, contextScroll, contextHeader]) }
        return NSAccessibility.unignoredChildren(from: children)
    }
}

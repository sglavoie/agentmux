import AppKit

/// A session's line: its state glyph, its profile, and a line of detail,
/// with a × in the top right corner while the pointer is over a line that can
/// be closed. A session
/// that was given a name has it on a line of its own above the profile, and
/// one of the first ten has the key that goes to it in the bottom right.
@MainActor
final class WorkspaceRowCellView: NSTableCellView {
    static let identifier = NSUserInterfaceItemIdentifier("workspace.row")
    /// A deeper green on light sidebars and a brighter green on dark ones.
    /// The state glyph sits outside the selection pill, on the sidebar surface.
    static let completedGlyphColor = NSColor(name: nil) { appearance in
        if appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua {
            return NSColor(srgbRed: 0x61 / 255.0, green: 0xd9 / 255.0, blue: 0x85 / 255.0, alpha: 1)
        }
        return NSColor(srgbRed: 0x16 / 255.0, green: 0x70 / 255.0, blue: 0x38 / 255.0, alpha: 1)
    }
    private var hasCompletedGlyph = false
    let glyphLabel = WorkspaceSidebarLabel(labelWithString: "")
    let nameLabel = WorkspaceSidebarLabel(labelWithString: "")
    let titleLabel = WorkspaceSidebarLabel(labelWithString: "")
    let nameReviewIcon = NSImageView()
    let titleReviewIcon = NSImageView()
    private let nameHeading = NSStackView()
    private let titleHeading = NSStackView()
    let detailLabel = WorkspaceSidebarLabel(labelWithString: "")
    let closeButton = NSButton(title: "", target: nil, action: nil)
    /// `⌘1`: the key that goes to the session, empty when it has none.
    let keyLabel = WorkspaceSidebarLabel(labelWithString: "")
    /// What the × does for the line shown now.
    var onClose: (() -> Void)?
    /// Whether the pointer is over the line; the × is drawn only then.
    var isHovered = false {
        didSet { WorkspaceMotion.fade(closeButton, to: isHovered ? 1 : 0) }
    }
    /// The side of the ×'s target, larger than its glyph.
    static let closeTarget: CGFloat = 18
    /// Keep the text clear of the × and the key label, each only while it
    /// is there: a hidden one leaves the title the whole line.
    private var clearOfClose: NSLayoutConstraint!
    private var clearOfKey: NSLayoutConstraint!
    private var compactContentWidth: NSLayoutConstraint!
    private var terminalTabsLeading: NSLayoutConstraint!
    private var terminalTabsTrailing: NSLayoutConstraint!
    private let contentStack = NSStackView()
    private let terminalTabs = NSStackView()
    private(set) var compactTerminalButtons: [WorkspaceCompactTerminalButton] = []
    var onSelectTerminal: ((String) -> Void)?
    var onCloseTerminal: ((String) -> Void)?
    var onTerminalMenu: ((String) -> NSMenu?)?

    init() {
        super.init(frame: .zero)
        identifier = Self.identifier
        glyphLabel.alignment = .center
        nameLabel.font = .systemFont(ofSize: 12, weight: .semibold)
        nameLabel.textColor = .labelColor
        nameLabel.lineBreakMode = .byTruncatingTail
        nameLabel.isHidden = true
        titleLabel.lineBreakMode = .byTruncatingTail
        for (heading, icon, label) in [(nameHeading, nameReviewIcon, nameLabel), (titleHeading, titleReviewIcon, titleLabel)] {
            label.maximumNumberOfLines = 1
            label.cell?.usesSingleLineMode = true
            icon.image = NSImage(systemSymbolName: "eye.fill", accessibilityDescription: "Accounted for")?
                .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: 12, weight: .bold))
            icon.contentTintColor = .labelColor
            icon.toolTip = "Accounted for"
            icon.isHidden = true
            icon.widthAnchor.constraint(equalToConstant: 16).isActive = true
            icon.heightAnchor.constraint(equalToConstant: 16).isActive = true
            heading.orientation = .horizontal
            heading.alignment = .centerY
            heading.spacing = 4
            heading.addArrangedSubview(icon)
            heading.addArrangedSubview(label)
        }
        nameHeading.isHidden = true
        detailLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        detailLabel.textColor = .secondaryLabelColor
        detailLabel.lineBreakMode = .byTruncatingTail
        // Not the cell's textField: the source list restyles that outlet,
        // drawing it dark on the selected line's pill in a dark appearance
        // and faded on a session's color, whatever color the line sets.
        let text = NSStackView(views: [nameHeading, titleHeading, detailLabel])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 1
        contentStack.addArrangedSubview(glyphLabel)
        contentStack.addArrangedSubview(text)
        contentStack.orientation = .horizontal
        contentStack.spacing = 6
        contentStack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(contentStack)
        terminalTabs.orientation = .horizontal
        terminalTabs.distribution = .fillEqually
        terminalTabs.spacing = 2
        terminalTabs.translatesAutoresizingMaskIntoConstraints = false
        terminalTabs.isHidden = true
        addSubview(terminalTabs)
        closeButton.image = NSImage(systemSymbolName: "xmark", accessibilityDescription: "Close")?
            .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: 8, weight: .bold))
        closeButton.imagePosition = .imageOnly
        closeButton.isBordered = false
        closeButton.contentTintColor = .tertiaryLabelColor
        closeButton.refusesFirstResponder = true
        closeButton.target = self
        closeButton.action = #selector(closePressed(_:))
        closeButton.translatesAutoresizingMaskIntoConstraints = false
        addSubview(closeButton)
        keyLabel.font = .monospacedDigitSystemFont(ofSize: NSFont.smallSystemFontSize, weight: .regular)
        keyLabel.textColor = .tertiaryLabelColor
        keyLabel.setContentCompressionResistancePriority(.required, for: .horizontal)
        keyLabel.translatesAutoresizingMaskIntoConstraints = false
        addSubview(keyLabel)
        NSLayoutConstraint.activate([
            glyphLabel.widthAnchor.constraint(equalToConstant: 20),
            contentStack.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2),
            contentStack.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -6),
            keyLabel.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -4),
            keyLabel.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -6),
            contentStack.centerYAnchor.constraint(equalTo: centerYAnchor),
            terminalTabs.centerYAnchor.constraint(equalTo: centerYAnchor),
            terminalTabs.heightAnchor.constraint(equalToConstant: 34),
            closeButton.topAnchor.constraint(equalTo: topAnchor, constant: 1),
            closeButton.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -2),
            closeButton.widthAnchor.constraint(equalToConstant: Self.closeTarget),
            closeButton.heightAnchor.constraint(equalToConstant: Self.closeTarget),
        ])
        clearOfClose = contentStack.trailingAnchor.constraint(lessThanOrEqualTo: closeButton.leadingAnchor, constant: -2)
        clearOfKey = contentStack.trailingAnchor.constraint(lessThanOrEqualTo: keyLabel.leadingAnchor, constant: -4)
        terminalTabsLeading = terminalTabs.leadingAnchor.constraint(equalTo: contentStack.trailingAnchor, constant: 4)
        terminalTabsTrailing = terminalTabs.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -28)
        compactContentWidth = contentStack.widthAnchor.constraint(equalTo: widthAnchor, multiplier: 0.45)
        closeButton.alphaValue = 0
        addTrackingArea(NSTrackingArea(rect: .zero, options: [.mouseEnteredAndExited, .activeInActiveApp, .inVisibleRect],
                                       owner: self, userInfo: nil))
    }

    @objc private func closePressed(_ sender: Any?) { onClose?() }

    /// A session and its attached terminals share one visible line in compact mode.
    func showCompactTerminals(_ terminals: [WorkspaceUtilityView], selectedID: String?, ringing: Set<String> = [],
                              details: [String: String] = [:]) {
        let titles = terminals.map { $0.displayName + (ringing.contains($0.id) ? " \(Self.bellGlyph)" : "") }
        if compactTerminalButtons.map(\.terminalID) == terminals.map(\.id),
           compactTerminalButtons.map(\.title) == titles,
           compactTerminalButtons.map(\.detail) == terminals.map({ details[$0.id] ?? "" }) {
            selectCompactTerminal(selectedID)
            return
        }
        for button in compactTerminalButtons {
            terminalTabs.removeArrangedSubview(button)
            button.removeFromSuperview()
        }
        compactTerminalButtons = terminals.enumerated().map { index, terminal in
            let button = WorkspaceCompactTerminalButton(terminalID: terminal.id)
            button.title = titles[index]
            button.shortTitle = "T\(max(terminal.ordinal, 1))"
            button.detail = details[terminal.id] ?? ""
            button.toolTip = button.detail.isEmpty ? terminal.displayName : "\(terminal.displayName) — \(button.detail)"
            button.setAccessibilityLabel(button.detail.isEmpty
                ? "Terminal: \(terminal.displayName)" : "Terminal: \(terminal.displayName), \(button.detail)")
            button.closeButton.toolTip = "Close Terminal: \(terminal.displayName)"
            button.closeButton.setAccessibilityLabel("Close Terminal: \(terminal.displayName)")
            button.onSelect = { [weak self, id = terminal.id] in self?.onSelectTerminal?(id) }
            button.onClose = { [weak self, id = terminal.id] in self?.onCloseTerminal?(id) }
            button.onMenu = { [weak self, id = terminal.id] in self?.onTerminalMenu?(id) }
            terminalTabs.addArrangedSubview(button)
            return button
        }
        terminalTabs.isHidden = terminals.isEmpty
        terminalTabsLeading.isActive = !terminals.isEmpty
        terminalTabsTrailing.isActive = !terminals.isEmpty
        compactContentWidth.isActive = !terminals.isEmpty
        selectCompactTerminal(selectedID)
    }

    func selectCompactTerminal(_ id: String?) {
        for button in compactTerminalButtons { button.isCurrent = button.terminalID == id }
    }

    func setStateGlyphOutsidePill(_ outside: Bool) {
        contentStack.spacing = outside ? 10 : 6
        if outside { glyphLabel.textColor = hasCompletedGlyph ? Self.completedGlyphColor : .labelColor }
    }

    private func showStateGlyph(_ glyph: String, completed: Bool = false) {
        let changed = !glyphLabel.stringValue.isEmpty && glyphLabel.stringValue != glyph
        hasCompletedGlyph = completed
        glyphLabel.stringValue = glyph
        glyphLabel.font = .systemFont(ofSize: NSFont.systemFontSize, weight: completed ? .bold : .regular)
        glyphLabel.textColor = completed ? Self.completedGlyphColor : .labelColor
        if changed { WorkspaceMotion.fade(glyphLabel, from: 0.35, to: 1, duration: WorkspaceMotion.entrance) }
    }

    /// Whether the title may run under the × or the key label: only where
    /// that one is not there.
    var titleClearsClose: Bool { clearOfClose.isActive }
    var titleClearsKey: Bool { clearOfKey.isActive }

    private func syncTrailingRoom() {
        clearOfClose.isActive = !closeButton.isHidden
        clearOfKey.isActive = !keyLabel.stringValue.isEmpty
    }

    /// Whether the × can be seen now: the line can be closed and the
    /// pointer is over it.
    var showsCloseButton: Bool { !closeButton.isHidden && isHovered }

    override func mouseEntered(with event: NSEvent) { isHovered = true }
    override func mouseExited(with event: NSEvent) { isHovered = false }

    override func prepareForReuse() {
        super.prepareForReuse()
        isHovered = false
        setStateGlyphOutsidePill(false)
        showCompactTerminals([], selectedID: nil)
        onSelectTerminal = nil
        onCloseTerminal = nil
        onTerminalMenu = nil
    }

    /// After the title of a line whose terminal rang the bell unseen.
    static let bellGlyph = "🔔"

    /// The detail, key, and × on the selected line's opaque pill: the label
    /// color, a little muted. Built per appearance, because muting
    /// `labelColor` fixes it to whichever appearance was current then,
    /// which drew it black on the dark pill.
    static let selectedDetailColor = NSColor(name: nil) { appearance in
        (appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua ? NSColor.white : .black)
            .withAlphaComponent(0.8)
    }

    /// The detail and key on a chip: a dark gray, where the list's own
    /// grays fade into it.
    static let highlightedDetailColor = NSColor(white: 0.25, alpha: 1)
    /// Draws the text in `color`, for a session's color behind it, or as the
    /// list draws it when there is none. On the `selected` line's opaque pill
    /// the list's secondary and tertiary grays are too faint to read, so the
    /// detail, key, and × there are the label color, a little muted.
    /// `chip` puts the text on that color, the detail and key dark gray.
    func setTabText(_ color: NSColor?, selected: Bool = false, chip: NSColor? = nil) {
        [glyphLabel, nameLabel, titleLabel, detailLabel, keyLabel].forEach { $0.chip = chip }
        nameLabel.textColor = color ?? .labelColor
        glyphLabel.textColor = hasCompletedGlyph ? Self.completedGlyphColor : (color ?? .labelColor)
        titleLabel.textColor = color ?? .labelColor
        let muted = color?.withAlphaComponent(0.8) ?? (chip != nil ? Self.highlightedDetailColor : nil)
            ?? (selected ? Self.selectedDetailColor : nil)
        detailLabel.textColor = muted ?? .secondaryLabelColor
        closeButton.contentTintColor = muted ?? .tertiaryLabelColor
        keyLabel.textColor = muted ?? .secondaryLabelColor
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    /// A utility view's line: what it is and what it is about. It shows no
    /// session state, because it has none.
    /// `program` is the title its program set, shown under the view's own;
    /// `bell` marks a bell rung while another was shown.
    /// `progress` is what its program last reported of its progress.
    func show(_ view: WorkspaceUtilityView, session: String? = nil, program: String? = nil, bell: Bool = false,
              progress: Ghostty.Progress? = nil) {
        nameLabel.stringValue = ""
        nameLabel.isHidden = true
        nameHeading.isHidden = true
        nameReviewIcon.isHidden = true
        titleReviewIcon.isHidden = true
        titleLabel.lineBreakMode = .byTruncatingTail
        showStateGlyph(view.glyph)
        titleLabel.stringValue = bell ? "\(view.displayName) \(Self.bellGlyph)" : view.displayName
        titleLabel.font = .systemFont(ofSize: NSFont.systemFontSize, weight: .bold)
        let detail = view.detail(session: session, program: program)
        let program = program.flatMap { $0.isEmpty ? nil : $0 }
        let progressText = progress.map(Self.progressText)
        detailLabel.stringValue = progressText.map { "\($0) · \(detail)" } ?? detail
        toolTip = view.toolTip(session: session)
        var spoken = ["\(view.displayName), \(view.kind.rawValue) view"]
        if let program { spoken.append(program) }
        if let progressText { spoken.append(progressText) }
        if bell { spoken.append("bell") }
        setAccessibilityLabel(spoken.joined(separator: ", "))
        keyLabel.stringValue = ""
        closeButton.isHidden = view.isPermanent
        closeButton.toolTip = "Close View: \(view.displayName) — ends its program"
        closeButton.setAccessibilityLabel("Close View: \(view.displayName). Ends its program.")
        syncTrailingRoom()
    }

    /// Only an embedded session can be closed from here; `closing` is set
    /// once it was asked to be. `sessionKey` is the digit that goes to the
    /// session with ⌘, when it has one.
    /// `away` says the line is listed under Pinned or Needs attention rather
    /// than its project; nil reads it from the row, as where it would be listed.
    func show(_ row: WorkspaceRow, muteGlyph: String, closing: Bool = false, sessionKey: String? = nil, bell: Bool = false,
              away: Bool? = nil, projectFirstLine: Bool = false) {
        nameLabel.lineBreakMode = row.qualifier.isEmpty ? .byTruncatingTail : .byTruncatingMiddle
        titleLabel.lineBreakMode = row.qualifier.isEmpty ? .byTruncatingTail : .byTruncatingMiddle
        nameLabel.stringValue = row.namedLabel
        nameLabel.font = .systemFont(ofSize: 12, weight: .semibold)
        nameLabel.isHidden = row.name.isEmpty
        nameHeading.isHidden = row.name.isEmpty
        nameReviewIcon.isHidden = !row.review.accounted || row.name.isEmpty
        titleReviewIcon.isHidden = !row.review.accounted || !row.name.isEmpty
        showStateGlyph(row.glyph, completed: row.iconState == "stop")
        // A pinned or promoted session names the project it is away from.
        let namesProject = projectFirstLine ? row.name.isEmpty : (away ?? (row.pinned || row.needsAttention))
        let place = !row.name.isEmpty ? row.taskMetadata : namesProject ? "\(row.project) — \(row.sessionLabel)" : row.sessionLabel
        let badge = row.pinned ? "★ " : ""
        let label = badge + (row.harnessIcon.isEmpty ? place : "\(row.harnessIcon) \(place)")
        let muted = row.muted ? "\(label) \(muteGlyph)" : label
        titleLabel.stringValue = bell ? "\(muted) \(Self.bellGlyph)" : muted
        titleLabel.font = .systemFont(ofSize: 12, weight: row.name.isEmpty ? .semibold : .regular)
        detailLabel.stringValue = closing ? Self.closingDetail : row.detail
        toolTip = row.displayName + " · " + row.toolTip
        keyLabel.stringValue = sessionKey.map { "⌘\($0)" } ?? ""
        setAccessibilityLabel(Self.accessibilityLabel(row, closing: closing, sessionKey: sessionKey, bell: bell))
        closeButton.isHidden = row.embeddedRuntimeSessionID == nil || closing
        closeButton.toolTip = "End \(row.displayName)…"
        closeButton.setAccessibilityLabel("End session \(row.displayName)")
        syncTrailingRoom()
    }

    nonisolated static let closingDetail = "ending…"

    /// What a utility view's line says of its program's progress (OSC 9;4).
    nonisolated static func progressText(_ progress: Ghostty.Progress) -> String {
        let percent = progress.percent.map { "\($0)%" }
        switch progress.state {
        case .normal: return percent ?? "in progress"
        case .indeterminate: return "in progress"
        case .paused: return percent.map { "paused at \($0)" } ?? "paused"
        case .error: return percent.map { "failed at \($0)" } ?? "failed"
        }
    }

    /// What VoiceOver says of a session's line: what the line shows, in
    /// words. A session asked to close is said to be closing instead of its
    /// detail, as the line shows it.
    nonisolated static func accessibilityLabel(_ row: WorkspaceRow, closing: Bool = false, sessionKey: String? = nil,
                                   bell: Bool = false) -> String {
        var spoken = (row.name.isEmpty ? [] : [row.namedLabel]) + [row.project, row.sessionLabel]
        if row.pinned { spoken.append("pinned") }
        if row.muted { spoken.append("muted") }
        if row.attention { spoken.append("needs attention") } else if row.working { spoken.append("working") }
        if row.review.accounted { spoken.append("Accounted for") }
        if bell { spoken.append("bell") }
        if closing { spoken.append("ending session") } else if !row.detail.isEmpty { spoken.append(row.detail) }
        if let sessionKey { spoken.append("Command \(sessionKey)") }
        return spoken.joined(separator: ", ")
    }
}

/// A terminal tab in a compact session row, with its own close button and
/// right-click menu so both actions address the terminal rather than its session.
@MainActor
final class WorkspaceCompactTerminalButton: NSView {
    let terminalID: String
    let closeButton = NSButton(title: "", target: nil, action: nil)
    private let titleLabel = NSTextField(labelWithString: "")
    private let detailLabel = NSTextField(labelWithString: "")
    private var titleCenterY: NSLayoutConstraint!
    private var titleTop: NSLayoutConstraint!
    private var fullTitle = ""
    var title: String {
        get { fullTitle }
        set { fullTitle = newValue; needsLayout = true }
    }
    var shortTitle = "" { didSet { needsLayout = true } }
    var detail = "" {
        didSet {
            detailLabel.stringValue = detail
            detailLabel.isHidden = detail.isEmpty
            if detail.isEmpty {
                titleTop.isActive = false
                titleCenterY.isActive = true
            } else {
                titleCenterY.isActive = false
                titleTop.isActive = true
            }
        }
    }
    var displayedTitle: String { titleLabel.stringValue }
    var displayedDetail: String { detailLabel.stringValue }
    var onSelect: (() -> Void)?
    var onClose: (() -> Void)?
    var onMenu: (() -> NSMenu?)?
    var isCurrent = false {
        didSet { needsDisplay = true }
    }

    init(terminalID: String) {
        self.terminalID = terminalID
        super.init(frame: .zero)
        wantsLayer = true
        layer?.cornerRadius = 5
        layer?.cornerCurve = .continuous
        setAccessibilityRole(.button)
        titleLabel.font = .systemFont(ofSize: 11, weight: .semibold)
        titleLabel.lineBreakMode = .byTruncatingTail
        titleLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        titleLabel.setAccessibilityElement(false)
        titleLabel.translatesAutoresizingMaskIntoConstraints = false
        addSubview(titleLabel)
        detailLabel.font = .systemFont(ofSize: 10)
        detailLabel.textColor = .secondaryLabelColor
        detailLabel.lineBreakMode = .byTruncatingTail
        detailLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        detailLabel.setAccessibilityElement(false)
        detailLabel.isHidden = true
        detailLabel.translatesAutoresizingMaskIntoConstraints = false
        addSubview(detailLabel)
        closeButton.image = NSImage(systemSymbolName: "xmark", accessibilityDescription: "Close Terminal")?
            .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: 8, weight: .bold))
        closeButton.imagePosition = .imageOnly
        closeButton.isBordered = false
        closeButton.contentTintColor = .secondaryLabelColor
        closeButton.refusesFirstResponder = true
        closeButton.target = self
        closeButton.action = #selector(closePressed(_:))
        closeButton.translatesAutoresizingMaskIntoConstraints = false
        addSubview(closeButton)
        titleCenterY = titleLabel.centerYAnchor.constraint(equalTo: centerYAnchor)
        titleTop = titleLabel.topAnchor.constraint(equalTo: topAnchor, constant: 2)
        NSLayoutConstraint.activate([
            titleLabel.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 3),
            titleLabel.trailingAnchor.constraint(lessThanOrEqualTo: closeButton.leadingAnchor, constant: -1),
            titleCenterY,
            detailLabel.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 3),
            detailLabel.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -2),
            detailLabel.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -2),
            closeButton.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -2),
            closeButton.centerYAnchor.constraint(equalTo: titleLabel.centerYAnchor),
            closeButton.widthAnchor.constraint(equalToConstant: 14),
            closeButton.heightAnchor.constraint(equalToConstant: 16),
        ])
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) is not supported for this view") }

    override func layout() {
        super.layout()
        let available = max(0, bounds.width - 3 - 1 - 14 - 2)
        let font = titleLabel.font ?? .systemFont(ofSize: 11, weight: .semibold)
        let width = (fullTitle as NSString).size(withAttributes: [.font: font]).width
        let wanted = width <= available || shortTitle.isEmpty ? fullTitle : shortTitle
        if titleLabel.stringValue != wanted { titleLabel.stringValue = wanted }
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.borderWidth = 1.5
        layer?.borderColor = (isCurrent ? NSColor.controlAccentColor : .secondaryLabelColor).cgColor
        layer?.backgroundColor = (isCurrent ? NSColor.controlAccentColor.withAlphaComponent(0.15) : NSColor.clear).cgColor
    }

    @objc private func closePressed(_ sender: Any?) { onClose?() }

    func performClick(_ sender: Any?) { onSelect?() }

    override func mouseDown(with event: NSEvent) { onSelect?() }

    override func hitTest(_ point: NSPoint) -> NSView? {
        let local = convert(point, from: superview)
        guard bounds.contains(local) else { return nil }
        return closeButton.frame.contains(local) ? closeButton : self
    }

    override func menu(for event: NSEvent) -> NSMenu? { onMenu?() }
}

/// A sidebar label that can sit on a chip of color, as if highlighted. The
/// chip is drawn only behind text, and the label opts out of vibrancy while
/// it is shown: blended into the list, a light chip draws as black.
@MainActor
final class WorkspaceSidebarLabel: NSTextField {
    var chip: NSColor? {
        didSet { if chip != oldValue { needsDisplay = true } }
    }

    override var allowsVibrancy: Bool { chip == nil && super.allowsVibrancy }

    override func draw(_ dirtyRect: NSRect) {
        if let chip, !stringValue.isEmpty {
            chip.setFill()
            NSBezierPath(roundedRect: bounds, xRadius: 3, yRadius: 3).fill()
        }
        super.draw(dirtyRect)
    }
}

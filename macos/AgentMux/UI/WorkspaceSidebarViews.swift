import AppKit

/// The session list. It keeps the keys a list needs — the arrows, Return
/// to go into the selected row, and ← and → to collapse and expand its
/// project — and nothing typed here reaches a program.
@MainActor
final class WorkspaceSidebarView: NSTableView {
    private(set) var pointerInside = false
    var onPointerChanged: (() -> Void)?
    private var pointerTracking: NSTrackingArea?

    override func updateTrackingAreas() {
        if let pointerTracking { removeTrackingArea(pointerTracking) }
        let area = NSTrackingArea(rect: .zero, options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect], owner: self, userInfo: nil)
        addTrackingArea(area)
        pointerTracking = area
        super.updateTrackingAreas()
    }
    override func mouseEntered(with event: NSEvent) {
        pointerInside = true
        onPointerChanged?()
    }
    override func mouseExited(with event: NSEvent) {
        pointerInside = false
        onPointerChanged?()
    }
    var onReturn: (() -> Void)?
    /// ← (true) or → (false).
    var onCollapse: ((Bool) -> Void)?
    /// ↑ (-1) or ↓ (1) while no line is selected; false leaves the key to
    /// the list.
    var onStep: ((Int) -> Bool)?
    /// Whether the line at an index has anything for its right-click menu;
    /// one that has nothing shows no menu rather than an empty one.
    var hasMenu: ((Int) -> Bool)?

    override func menu(for event: NSEvent) -> NSMenu? {
        let index = row(at: convert(event.locationInWindow, from: nil))
        guard hasMenu?(index) ?? true else { return nil }
        return super.menu(for: event)
    }

    override func keyDown(with event: NSEvent) {
        let returnKeys: Set<UInt16> = [36, 76]
        if returnKeys.contains(event.keyCode),
           event.modifierFlags.isDisjoint(with: [.command, .control, .option]) {
            onReturn?()
            return
        }
        let arrows: [UInt16: Bool] = [123: true, 124: false]
        if let collapsed = arrows[event.keyCode],
           event.modifierFlags.isDisjoint(with: [.command, .control, .option, .shift]) {
            onCollapse?(collapsed)
            return
        }
        // With no line selected — the pane's session was collapsed away —
        // ↑ and ↓ go from that session, not from the top of the list.
        let steps: [UInt16: Int] = [126: -1, 125: 1]
        if selectedRow < 0, let offset = steps[event.keyCode],
           event.modifierFlags.isDisjoint(with: [.command, .control, .option, .shift]),
           onStep?(offset) == true {
            return
        }
        super.keyDown(with: event)
    }
}

/// A line of the session list, drawn on its session's color when unselected.
/// Selection contrasts the space around its rounded pill in either theme.
@MainActor
class WorkspaceRowView: NSTableRowView {
    /// The text on a session's color. The daemon publishes colors bright
    /// enough for it.
    static let tabText = NSColor.black

    var tabColor: NSColor? {
        didSet {
            selectionBorder.color = tabColor ?? .controlAccentColor
            needsDisplay = true
            syncCell()
        }
    }

    override var isSelected: Bool {
        didSet {
            needsDisplay = true
            needsLayout = true
            syncCell()
        }
    }

    /// Whether the color is what the line shows now.
    var showsTabColor: Bool { tabColor != nil && !isSelected }

    /// Tells the line's cell what it is drawn on, so its text stays legible.
    func syncCell() {
        guard numberOfColumns > 0 else { return }
        guard let cell = view(atColumn: 0) as? WorkspaceRowCellView else { return }
        cell.setTabText(showsTabColor ? Self.tabText : nil)
        cell.setStateGlyphOutsidePill((self as? WorkspaceSessionRowView)?.hasStateGlyph == true)
    }

    override func drawBackground(in dirtyRect: NSRect) {
        if isSelected {
            let dark = effectiveAppearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
            (dark ? NSColor.white : NSColor.black).setFill()
            bounds.fill()

            NSColor.windowBackgroundColor.setFill()
            outline().fill()
            return
        }
        guard showsTabColor, let tabColor else {
            super.drawBackground(in: dirtyRect)
            return
        }
        tabColor.setFill()
        outline().fill()
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        needsDisplay = true
    }

    /// The selected line keeps its session's color as a border around the
    /// pill, falling back to the accent color for uncolored rows.
    /// It is a view over the line so background drawing cannot cover it.
    let selectionBorder = WorkspaceSelectionBorderView()

    static let selectionBorderWidth: CGFloat = 3
    /// How far the list's selection is inset from the row's sides, and how
    /// round its corners are.
    static let selectionInset: CGFloat = 10
    static let selectionRadius: CGFloat = 10
    var pillLeadingInset: CGFloat { Self.selectionInset }

    override func layout() {
        super.layout()
        if subviews.last !== selectionBorder {
            addSubview(selectionBorder, positioned: .above, relativeTo: nil)
        }
        selectionBorder.frame = pillRect(verticalInset: 0)
        selectionBorder.isHidden = !isSelected
    }

    /// The shape a line's color follows: the selection's, a point shorter so
    /// two colored lines do not run together.
    private func outline() -> NSBezierPath {
        NSBezierPath(roundedRect: pillRect(verticalInset: 1),
                     xRadius: Self.selectionRadius, yRadius: Self.selectionRadius)
    }

    private func pillRect(verticalInset: CGFloat) -> NSRect {
        let left = bounds.minX + pillLeadingInset
        return NSRect(x: left, y: bounds.minY + verticalInset,
                      width: max(0, bounds.maxX - Self.selectionInset - left),
                      height: max(0, bounds.height - 2 * verticalInset))
    }
}

/// A session's state emoji always sits to the left of its pill.
/// The session text and border stay in the pill.
@MainActor
final class WorkspaceSessionRowView: WorkspaceRowView {
    var hasStateGlyph = false {
        didSet {
            needsDisplay = true
            needsLayout = true
            syncCell()
        }
    }

    override var pillLeadingInset: CGFloat { hasStateGlyph ? 40 : Self.selectionInset }

    override var selectionHighlightStyle: NSTableView.SelectionHighlightStyle {
        get { .none }
        set { super.selectionHighlightStyle = .none }
    }

    override var interiorBackgroundStyle: NSView.BackgroundStyle { .normal }
}

/// An attached terminal uses only the session-colored border for selection.
/// AppKit reapplies the table's highlight style when focus changes, so keep
/// the override on this row rather than changing the whole sidebar.
@MainActor
final class WorkspaceTerminalRowView: WorkspaceRowView {
    override var selectionHighlightStyle: NSTableView.SelectionHighlightStyle {
        get { .none }
        set { super.selectionHighlightStyle = .none }
    }

    override var interiorBackgroundStyle: NSView.BackgroundStyle { .normal }
}

/// The border of the selected line. It takes no clicks.
@MainActor
final class WorkspaceSelectionBorderView: NSView {
    var color: NSColor = .controlAccentColor {
        didSet { needsDisplay = true }
    }

    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
        layer?.borderWidth = WorkspaceRowView.selectionBorderWidth
        layer?.cornerRadius = WorkspaceRowView.selectionRadius
        layer?.cornerCurve = .continuous
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    override var wantsUpdateLayer: Bool { true }

    /// Runs again when the appearance or the accent color changes.
    override func updateLayer() {
        layer?.borderColor = color.cgColor
    }

    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}

/// A project's header line: its name, how many of its sessions wait, and
/// whether it is collapsed. A click on it collapses or expands it, and so
/// does its accessibility action.
@MainActor
final class WorkspaceGroupCellView: NSTableCellView {
    static let identifier = NSUserInterfaceItemIdentifier("workspace.group")
    static let collapsedDisclosure = "▸"
    static let expandedDisclosure = "▾"
    static let expandAction = "Expand"
    static let collapseAction = "Collapse"
    let nameLabel = NSTextField(labelWithString: "")
    let countLabel = NSTextField(labelWithString: "")
    /// ▸ while the project is collapsed to this line, ▾ while it is not; a
    /// heading that is not a project has none.
    let disclosureLabel = NSTextField(labelWithString: "")
    /// Collapses or expands the project.
    var onToggle: (() -> Void)?

    init() {
        super.init(frame: .zero)
        identifier = Self.identifier
        disclosureLabel.textColor = .secondaryLabelColor
        disclosureLabel.isHidden = true
        nameLabel.font = .systemFont(ofSize: 16, weight: .semibold)
        nameLabel.textColor = .secondaryLabelColor
        nameLabel.lineBreakMode = .byTruncatingTail
        nameLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        countLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize, weight: .semibold)
        countLabel.textColor = .systemOrange
        // Source-list group rows replace textField's attributed text with
        // an 11-point heading style. Keep this custom-sized label independent
        // of that outlet; the cell exposes its own accessibility label below.
        let stack = NSStackView(views: [nameLabel, NSView(), countLabel, disclosureLabel])
        stack.orientation = .horizontal
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -8),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    /// A heading that is not a project: it counts nothing.
    func show(section title: String) {
        nameLabel.stringValue = title
        countLabel.stringValue = ""
        countLabel.toolTip = nil
        disclosureLabel.isHidden = true
        onToggle = nil
        setAccessibilityLabel(title)
        setAccessibilityDisclosed(false)
        setAccessibilityCustomActions(nil)
    }

    func show(_ group: WorkspaceGroup) {
        if group.needsAttention {
            show(section: group.project)
            countLabel.stringValue = String(group.rows.count)
            countLabel.toolTip = "\(group.rows.count) waiting on you"
            setAccessibilityLabel("\(group.project), \(group.rows.count) waiting")
            return
        }
        nameLabel.stringValue = group.project
        let waiting = group.attention
        countLabel.stringValue = waiting > 0 ? String(waiting) : ""
        countLabel.toolTip = waiting > 0 ? "\(waiting) waiting on you" : nil
        disclosureLabel.isHidden = false
        disclosureLabel.stringValue = group.collapsed ? Self.collapsedDisclosure : Self.expandedDisclosure
        disclosureLabel.toolTip = group.collapsed ? "Collapsed: click to expand" : "Click to collapse"
        disclosureLabel.setAccessibilityLabel(group.collapsed ? "collapsed" : "expanded")
        var label = waiting > 0 ? "\(group.project), \(waiting) waiting" : group.project
        label += group.collapsed ? ", collapsed" : ", expanded"
        setAccessibilityLabel(label)
        setAccessibilityDisclosed(!group.collapsed)
        let action = NSAccessibilityCustomAction(name: group.collapsed ? Self.expandAction : Self.collapseAction) { [weak self] in
            guard let onToggle = self?.onToggle else { return false }
            onToggle()
            return true
        }
        setAccessibilityCustomActions([action])
    }
}

/// A session's line: its state glyph, its profile, and a line of detail,
/// with a × in the top right corner while the pointer is over a line that can
/// be closed. A session
/// that was given a name has it on a line of its own above the profile, and
/// one of the first ten has the key that goes to it in the bottom right.
@MainActor
final class WorkspaceRowCellView: NSTableCellView {
    static let identifier = NSUserInterfaceItemIdentifier("workspace.row")
    let glyphLabel = NSTextField(labelWithString: "")
    let nameLabel = NSTextField(labelWithString: "")
    let titleLabel = NSTextField(labelWithString: "")
    let detailLabel = NSTextField(labelWithString: "")
    let closeButton = NSButton(title: "", target: nil, action: nil)
    /// `⌘1`: the key that goes to the session, empty when it has none.
    let keyLabel = NSTextField(labelWithString: "")
    /// What the × does for the line shown now.
    var onClose: (() -> Void)?
    /// Whether the pointer is over the line; the × is drawn only then.
    var isHovered = false {
        didSet { closeButton.alphaValue = isHovered ? 1 : 0 }
    }
    /// The side of the ×'s target, larger than its glyph.
    static let closeTarget: CGFloat = 18
    /// Keep the text clear of the × and the key label, each only while it
    /// is there: a hidden one leaves the title the whole line.
    private var clearOfClose: NSLayoutConstraint!
    private var clearOfKey: NSLayoutConstraint!
    private let contentStack = NSStackView()

    init() {
        super.init(frame: .zero)
        identifier = Self.identifier
        glyphLabel.alignment = .center
        nameLabel.font = .systemFont(ofSize: 12, weight: .bold)
        nameLabel.textColor = Self.nameColor
        nameLabel.lineBreakMode = .byTruncatingTail
        nameLabel.isHidden = true
        titleLabel.lineBreakMode = .byTruncatingTail
        detailLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        detailLabel.textColor = .secondaryLabelColor
        detailLabel.lineBreakMode = .byTruncatingTail
        textField = titleLabel
        let text = NSStackView(views: [nameLabel, titleLabel, detailLabel])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 1
        contentStack.addArrangedSubview(glyphLabel)
        contentStack.addArrangedSubview(text)
        contentStack.orientation = .horizontal
        contentStack.spacing = 6
        contentStack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(contentStack)
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
            closeButton.topAnchor.constraint(equalTo: topAnchor, constant: 1),
            closeButton.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -2),
            closeButton.widthAnchor.constraint(equalToConstant: Self.closeTarget),
            closeButton.heightAnchor.constraint(equalToConstant: Self.closeTarget),
        ])
        clearOfClose = contentStack.trailingAnchor.constraint(lessThanOrEqualTo: closeButton.leadingAnchor, constant: -2)
        clearOfKey = contentStack.trailingAnchor.constraint(lessThanOrEqualTo: keyLabel.leadingAnchor, constant: -4)
        closeButton.alphaValue = 0
        addTrackingArea(NSTrackingArea(rect: .zero, options: [.mouseEnteredAndExited, .activeInActiveApp, .inVisibleRect],
                                       owner: self, userInfo: nil))
    }

    @objc private func closePressed(_ sender: Any?) { onClose?() }

    func setStateGlyphOutsidePill(_ outside: Bool) {
        contentStack.spacing = outside ? 10 : 6
        if outside { glyphLabel.textColor = .labelColor }
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
    }

    /// After the title of a line whose terminal rang the bell unseen.
    static let bellGlyph = "🔔"

    /// A session's name on its color: a dark blue, which reads on every
    /// color the daemon publishes, as black does.
    static let nameOnTabColor = NSColor(srgbRed: 0, green: 0.16, blue: 0.55, alpha: 1)
    /// A session's name on the list's own background: the same dark blue in
    /// a light appearance, and a blue light enough to read in a dark one.
    static let nameColor = NSColor(name: nil) { appearance in
        appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
            ? NSColor(srgbRed: 0.45, green: 0.68, blue: 1, alpha: 1) : nameOnTabColor
    }

    /// Draws the text in `color`, for a session's color behind it, or as the
    /// list draws it when there is none.
    func setTabText(_ color: NSColor?) {
        nameLabel.textColor = color == nil ? Self.nameColor : Self.nameOnTabColor
        glyphLabel.textColor = color ?? .labelColor
        titleLabel.textColor = color ?? .labelColor
        detailLabel.textColor = color?.withAlphaComponent(0.8) ?? .secondaryLabelColor
        closeButton.contentTintColor = color?.withAlphaComponent(0.8) ?? .tertiaryLabelColor
        keyLabel.textColor = color?.withAlphaComponent(0.8) ?? .tertiaryLabelColor
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
        glyphLabel.stringValue = view.glyph
        titleLabel.stringValue = bell ? "\(view.displayName) \(Self.bellGlyph)" : view.displayName
        titleLabel.font = .systemFont(ofSize: NSFont.systemFontSize, weight: .regular)
        let program = program.flatMap { $0.isEmpty ? nil : $0 }
        let detail = view.detail(session: session, program: program)
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
    func show(_ row: WorkspaceRow, muteGlyph: String, closing: Bool = false, sessionKey: String? = nil, bell: Bool = false) {
        nameLabel.stringValue = row.name
        nameLabel.isHidden = row.name.isEmpty
        glyphLabel.stringValue = row.glyph
        // A pinned or promoted session names the project it is away from.
        let place = row.pinned || row.needsAttention ? "\(row.project) — \(row.label)" : row.label
        let label = row.harnessIcon.isEmpty ? place : "\(row.harnessIcon) \(place)"
        let muted = row.muted ? "\(label) \(muteGlyph)" : label
        titleLabel.stringValue = bell ? "\(muted) \(Self.bellGlyph)" : muted
        titleLabel.font = .systemFont(ofSize: 12, weight: row.attention ? .semibold : .regular)
        detailLabel.stringValue = closing ? Self.closingDetail : row.detail
        toolTip = row.toolTip
        keyLabel.stringValue = sessionKey.map { "⌘\($0)" } ?? ""
        setAccessibilityLabel(Self.accessibilityLabel(row, closing: closing, sessionKey: sessionKey, bell: bell))
        closeButton.isHidden = row.embeddedRuntimeSessionID == nil || closing
        closeButton.toolTip = "Detach \(row.displayName); agent keeps running"
        closeButton.setAccessibilityLabel("Detach \(row.displayName); agent keeps running")
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
        var spoken = (row.name.isEmpty ? [] : [row.name]) + [row.project, row.label]
        if row.pinned { spoken.append("pinned") }
        if row.muted { spoken.append("muted") }
        if row.attention { spoken.append("needs attention") } else if row.working { spoken.append("working") }
        if bell { spoken.append("bell") }
        if closing { spoken.append("ending session") } else if !row.detail.isEmpty { spoken.append(row.detail) }
        if let sessionKey { spoken.append("Command \(sessionKey)") }
        return spoken.joined(separator: ", ")
    }
}

import AppKit

/// The session list. It keeps the keys a list needs — the arrows, Return
/// to go into the selected row, and ← and → to collapse and expand its
/// project — and nothing typed here reaches a program.
@MainActor
final class WorkspaceSidebarView: NSTableView {
    private(set) var pointerInside = false
    /// The sections drawn on a background of their own, behind their lines.
    var bands: [(lines: Range<Int>, band: WorkspaceSidebarBand)] = [] {
        didSet {
            needsDisplay = true
            enumerateAvailableRowViews { rowView, row in self.syncBandAppearance(rowView, row: row) }
        }
    }

    /// A line on a band is drawn in the light appearance, dark text on a
    /// bright hue, whichever appearance the sidebar has, and its text sits
    /// on a lighter shade of the band, as if highlighted.
    func syncBandAppearance(_ rowView: NSTableRowView, row: Int) {
        let band = bands.first { $0.lines.contains(row) }?.band
        rowView.appearance = band == nil ? nil : NSAppearance(named: .aqua)
        let dark = effectiveAppearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
        (rowView as? WorkspaceRowView)?.chipColor = band?.chipColor(dark: dark)
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        enumerateAvailableRowViews { rowView, row in self.syncBandAppearance(rowView, row: row) }
    }

    /// How wide a band's strip is; a line with a profile color fills its
    /// stretch of it with that color.
    static let stripWidth: CGFloat = 10

    override func drawBackground(inClipRect clipRect: NSRect) {
        super.drawBackground(inClipRect: clipRect)
        for (lines, band) in bands where !lines.isEmpty && lines.upperBound <= numberOfRows {
            let rect = rect(ofRow: lines.lowerBound).union(rect(ofRow: lines.upperBound - 1))
            guard rect.intersects(clipRect) else { continue }
            let gap: CGFloat = band.isSection ? 0 : 1
            let fill = NSRect(x: bounds.minX, y: rect.minY + gap, width: bounds.width, height: rect.height - 2 * gap)
            band.color.setFill()
            fill.fill()
            band.stripColor.setFill()
            NSRect(x: fill.minX, y: fill.minY, width: Self.stripWidth, height: fill.height).fill()
        }
    }

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

/// A session row's profile color takes over its stretch of the band's strip.
/// Selection uses the system accent independently of the profile's color.
@MainActor
class WorkspaceRowView: NSTableRowView {
    var tabColor: NSColor? {
        didSet {
            selectionBorder.color = .controlAccentColor
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

    /// What the line's text sits on: a lighter shade of its band, or nothing.
    var chipColor: NSColor? {
        didSet { if chipColor != oldValue { syncCell() } }
    }

    /// Profile color fills the line's strip, including on the selected row.
    var showsTabColor: Bool { tabColor != nil }

    /// Tells the line's cell what it is drawn on, so its text stays legible.
    func syncCell() {
        guard numberOfColumns > 0 else { return }
        if let group = view(atColumn: 0) as? WorkspaceGroupCellView {
            group.chipColor = chipColor
        }
        guard let cell = view(atColumn: 0) as? WorkspaceRowCellView else { return }
        cell.setTabText(nil, selected: isSelected, chip: chipColor)
        cell.setStateGlyphOutsidePill((self as? WorkspaceSessionRowView)?.hasStateGlyph == true)
    }

    /// A cell the list puts in the line, new or reused, learns what it is on.
    override func didAddSubview(_ subview: NSView) {
        super.didAddSubview(subview)
        if subview is NSTableCellView { syncCell() }
    }

    override func drawBackground(in dirtyRect: NSRect) {
        if isSelected {
            NSColor.controlAccentColor.withAlphaComponent(0.12).setFill()
            outline().fill()
        }
        if let tabColor {
            tabColor.setFill()
            NSRect(x: bounds.minX, y: bounds.minY,
                   width: WorkspaceSidebarView.stripWidth, height: bounds.height).fill()
        }
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        needsDisplay = true
    }

    /// System-accent selection stays distinct from the profile strip.
    /// Draw over the cell so its contents cannot cover the border.
    let selectionBorder = WorkspaceSelectionBorderView()

    static let selectionBorderWidth: CGFloat = 2
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
        flashView.frame = pillRect(verticalInset: 1)
    }

    /// The accent drawn over the line while it flashes, under its border.
    private let flashView: NSView = {
        let view = NSView()
        view.wantsLayer = true
        view.layer?.opacity = 0
        view.layer?.cornerRadius = WorkspaceRowView.selectionRadius
        return view
    }()

    static let flashKey = "amx.row.flash"

    /// Pulses the accent over the line twice, so the eye finds it (once with
    /// Reduce Motion), then leaves it as it was.
    func flash() {
        if flashView.superview !== self { addSubview(flashView, positioned: .below, relativeTo: selectionBorder) }
        flashView.frame = pillRect(verticalInset: 1)
        effectiveAppearance.performAsCurrentDrawingAppearance {
            flashView.layer?.backgroundColor = NSColor.controlAccentColor.withAlphaComponent(0.35).cgColor
        }
        let pulses = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion ? 1 : 2
        let animation = CAKeyframeAnimation(keyPath: "opacity")
        animation.values = [0] + Array(repeating: [1, 0], count: pulses).flatMap { $0 }
        animation.duration = 0.4 * Double(pulses)
        flashView.layer?.add(animation, forKey: Self.flashKey)
    }

    /// Whether the line is flashing now.
    var isFlashing: Bool { flashView.layer?.animation(forKey: Self.flashKey) != nil }

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

/// Session state sits to the left of the selected row's outline.
/// Unselected rows stay neutral in either appearance.
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

    override func layout() {
        super.layout()
        selectionBorder.usesAppearanceColor = false
        selectionBorder.isHidden = !isSelected
    }

    override var selectionHighlightStyle: NSTableView.SelectionHighlightStyle {
        get { .none }
        set { super.selectionHighlightStyle = .none }
    }

    override var interiorBackgroundStyle: NSView.BackgroundStyle { .normal }
}

/// An attached terminal uses the same accent selection as a session.
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

/// The border of a selected line.
/// It takes no clicks.
@MainActor
final class WorkspaceSelectionBorderView: NSView {
    var color: NSColor = .controlAccentColor {
        didSet { needsDisplay = true }
    }

    var usesAppearanceColor = false {
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
        let dark = effectiveAppearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
        layer?.borderColor = (usesAppearanceColor ? (dark ? NSColor.white : .black) : color).cgColor
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
    let nameLabel = WorkspaceSidebarLabel(labelWithString: "")
    let countLabel = WorkspaceSidebarLabel(labelWithString: "")
    /// ▸ while the project is collapsed to this line, ▾ while it is not; a
    /// heading that is not a project has none.
    let disclosureLabel = WorkspaceSidebarLabel(labelWithString: "")
    /// Collapses or expands the project.
    var onToggle: (() -> Void)?
    private var labelLeading: NSLayoutConstraint!
    /// What the heading's text sits on (`WorkspaceRowView.chipColor`).
    var chipColor: NSColor? {
        didSet { [nameLabel, countLabel, disclosureLabel].forEach { $0.chip = chipColor } }
    }

    init() {
        super.init(frame: .zero)
        identifier = Self.identifier
        wantsLayer = true
        layer?.cornerRadius = 6
        disclosureLabel.textColor = .labelColor
        disclosureLabel.isHidden = true
        nameLabel.font = .systemFont(ofSize: 13, weight: .semibold)
        nameLabel.textColor = .labelColor
        nameLabel.lineBreakMode = .byTruncatingTail
        nameLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        countLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize, weight: .semibold)
        countLabel.textColor = .labelColor
        // Source-list group rows replace textField's attributed text with
        // an 11-point heading style. Keep this custom-sized label independent
        // of that outlet; the cell exposes its own accessibility label below.
        let stack = NSStackView(views: [nameLabel, NSView(), countLabel, disclosureLabel])
        stack.orientation = .horizontal
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        labelLeading = stack.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2)
        NSLayoutConstraint.activate([
            labelLeading,
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
        var label: String
        if group.isSectionHeading {
            // A state section counts all the sessions it holds.
            nameLabel.stringValue = group.project
            countLabel.stringValue = String(group.sessions)
            countLabel.toolTip = group.sessions == 1 ? "1 session" : "\(group.sessions) sessions"
            label = "\(group.project), \(countLabel.toolTip ?? "")"
        } else {
            nameLabel.stringValue = group.project
            let waiting = group.attention
            countLabel.stringValue = waiting > 0 ? String(waiting) : ""
            countLabel.toolTip = waiting > 0 ? "\(waiting) waiting on you" : nil
            label = waiting > 0 ? "\(group.project), \(waiting) waiting" : group.project
        }
        disclosureLabel.isHidden = false
        disclosureLabel.stringValue = group.collapsed ? Self.collapsedDisclosure : Self.expandedDisclosure
        disclosureLabel.toolTip = group.collapsed ? "Collapsed: click to expand" : "Click to collapse"
        disclosureLabel.setAccessibilityLabel(group.collapsed ? "collapsed" : "expanded")
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

/// The line above the sessions: one chip per section listed below it, with
/// how many sessions it holds. A click on a chip goes to the section's first
/// session. The chips wrap onto more rows when the sidebar is narrow.
@MainActor
final class WorkspaceOverviewCellView: NSTableCellView {
    static let identifier = NSUserInterfaceItemIdentifier("workspace.overview")
    static let chipHeight: CGFloat = 20
    static let spacing: CGFloat = 4
    static let inset = NSEdgeInsets(top: 6, left: 2, bottom: 6, right: 8)
    static let font = NSFont.systemFont(ofSize: NSFont.smallSystemFontSize, weight: .semibold)
    /// Goes to the first session of the section a chip counts.
    var onSelect: ((WorkspaceSectionCount) -> Void)?
    private(set) var chips: [WorkspaceOverviewChip] = []
    private var counts: [WorkspaceSectionCount] = []

    init() {
        super.init(frame: .zero)
        identifier = Self.identifier
        setAccessibilityRole(.group)
        setAccessibilityLabel("Sections")
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    static func title(_ count: WorkspaceSectionCount) -> String { "\(count.title) \(count.count)" }

    /// Where each chip goes in a line `width` wide.
    static func frames(for counts: [WorkspaceSectionCount], width: CGFloat) -> [NSRect] {
        let available = max(width - inset.left - inset.right, 0)
        var frames: [NSRect] = []
        var x: CGFloat = 0, y: CGFloat = 0
        for count in counts {
            let text = (title(count) as NSString).size(withAttributes: [.font: font]).width
            let chip = min(ceil(text) + 12, available)
            if x > 0, x + chip > available {
                x = 0
                y += chipHeight + spacing
            }
            frames.append(NSRect(x: inset.left + x, y: inset.top + y, width: chip, height: chipHeight))
            x += chip + spacing
        }
        return frames
    }

    /// How tall the line is with its chips wrapped to `width`.
    static func height(for counts: [WorkspaceSectionCount], width: CGFloat) -> CGFloat {
        let bottom = frames(for: counts, width: width).map(\.maxY).max() ?? inset.top + chipHeight
        return bottom + inset.bottom
    }

    override var isFlipped: Bool { true }

    func show(_ counts: [WorkspaceSectionCount]) {
        self.counts = counts
        while chips.count > counts.count { chips.removeLast().removeFromSuperview() }
        while chips.count < counts.count {
            let chip = WorkspaceOverviewChip()
            chip.target = self
            chip.action = #selector(chipClicked(_:))
            addSubview(chip)
            chips.append(chip)
        }
        for (chip, count) in zip(chips, counts) { chip.show(count) }
        needsLayout = true
    }

    override func layout() {
        super.layout()
        for (chip, frame) in zip(chips, Self.frames(for: counts, width: bounds.width)) { chip.frame = frame }
    }

    @objc private func chipClicked(_ sender: WorkspaceOverviewChip) {
        guard let index = chips.firstIndex(of: sender), counts.indices.contains(index) else { return }
        onSelect?(counts[index])
    }
}

/// One section's chip on the overview line, on its band's hue.
@MainActor
final class WorkspaceOverviewChip: NSButton {
    private(set) var band: WorkspaceSidebarBand = .idle

    init() {
        super.init(frame: .zero)
        isBordered = false
        bezelStyle = .regularSquare
        focusRingType = .none
        // Dark text on a bright hue, as on the sidebar's bands.
        appearance = NSAppearance(named: .aqua)
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    func show(_ count: WorkspaceSectionCount) {
        switch count.kind {
        case .attention: band = .attention
        case .pinned: band = .pinned
        case let .state(section):
            switch section {
            case .waiting: band = .attention
            case .done: band = .stopped
            case .working: band = .running
            case .idle: band = .idle
            }
        }
        attributedTitle = NSAttributedString(string: WorkspaceOverviewCellView.title(count), attributes: [
            .font: WorkspaceOverviewCellView.font, .foregroundColor: NSColor.labelColor,
        ])
        let sessions = count.count == 1 ? "1 session" : "\(count.count) sessions"
        toolTip = "\(count.title): \(sessions). Click to go to the first."
        setAccessibilityLabel("\(count.title), \(sessions)")
        needsDisplay = true
    }

    override func draw(_ dirtyRect: NSRect) {
        band.stripColor.setFill()
        NSBezierPath(roundedRect: bounds, xRadius: 5, yRadius: 5).fill()
        if isHighlighted {
            NSColor.black.withAlphaComponent(0.12).setFill()
            NSBezierPath(roundedRect: bounds, xRadius: 5, yRadius: 5).fill()
        }
        let title = attributedTitle
        let size = title.size()
        title.draw(at: NSPoint(x: bounds.midX - size.width / 2, y: bounds.midY - size.height / 2))
    }

    override func resetCursorRects() {
        addCursorRect(bounds, cursor: .pointingHand)
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
    let glyphLabel = WorkspaceSidebarLabel(labelWithString: "")
    let nameLabel = WorkspaceSidebarLabel(labelWithString: "")
    let titleLabel = WorkspaceSidebarLabel(labelWithString: "")
    let detailLabel = WorkspaceSidebarLabel(labelWithString: "")
    let closeButton = NSButton(title: "", target: nil, action: nil)
    /// `⌘1`: the key that goes to the session, empty when it has none.
    let keyLabel = WorkspaceSidebarLabel(labelWithString: "")
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
        detailLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize, weight: .bold)
        detailLabel.textColor = .secondaryLabelColor
        detailLabel.lineBreakMode = .byTruncatingTail
        // Not the cell's textField: the source list restyles that outlet,
        // drawing it dark on the selected line's pill in a dark appearance
        // and faded on a session's color, whatever color the line sets.
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
        keyLabel.font = .monospacedDigitSystemFont(ofSize: NSFont.smallSystemFontSize, weight: .bold)
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
    /// The detail and key of a line on no band, such as the Dashboard's and
    /// the Overview's: #D0D0D0 dark, #333333 light, firmer than the list's
    /// own grays.
    static let detailColor = NSColor(name: nil) { appearance in
        NSColor(white: appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua ? 0xD0 / 255.0 : 0x33 / 255.0, alpha: 1)
    }

    /// Draws the text in `color`, for a session's color behind it, or as the
    /// list draws it when there is none. On the `selected` line's opaque pill
    /// the list's secondary and tertiary grays are too faint to read, so the
    /// detail, key, and × there are the label color, a little muted.
    /// `chip` puts the text on that color, the detail and key dark gray.
    func setTabText(_ color: NSColor?, selected: Bool = false, chip: NSColor? = nil) {
        [glyphLabel, nameLabel, titleLabel, detailLabel, keyLabel].forEach { $0.chip = chip }
        nameLabel.textColor = color == nil ? Self.nameColor : Self.nameOnTabColor
        glyphLabel.textColor = color ?? .labelColor
        titleLabel.textColor = color ?? .labelColor
        let muted = color?.withAlphaComponent(0.8) ?? (chip != nil ? Self.highlightedDetailColor : nil)
            ?? (selected ? Self.selectedDetailColor : nil)
        detailLabel.textColor = muted ?? Self.detailColor
        closeButton.contentTintColor = muted ?? .tertiaryLabelColor
        keyLabel.textColor = muted ?? Self.detailColor
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
        titleLabel.lineBreakMode = .byTruncatingTail
        glyphLabel.stringValue = view.glyph
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
              away: Bool? = nil) {
        nameLabel.lineBreakMode = row.qualifier.isEmpty ? .byTruncatingTail : .byTruncatingMiddle
        titleLabel.lineBreakMode = row.qualifier.isEmpty ? .byTruncatingTail : .byTruncatingMiddle
        nameLabel.stringValue = row.namedLabel
        nameLabel.isHidden = row.name.isEmpty
        glyphLabel.stringValue = row.glyph
        // A pinned or promoted session names the project it is away from.
        let place = away ?? (row.pinned || row.needsAttention) ? "\(row.project) — \(row.sessionLabel)" : row.sessionLabel
        let label = row.harnessIcon.isEmpty ? place : "\(row.harnessIcon) \(place)"
        let muted = row.muted ? "\(label) \(muteGlyph)" : label
        titleLabel.stringValue = bell ? "\(muted) \(Self.bellGlyph)" : muted
        titleLabel.font = .systemFont(ofSize: 12, weight: .bold)
        detailLabel.stringValue = closing ? Self.closingDetail : row.detail
        toolTip = row.displayName + " · " + row.toolTip
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
        var spoken = (row.name.isEmpty ? [] : [row.namedLabel]) + [row.project, row.sessionLabel]
        if row.pinned { spoken.append("pinned") }
        if row.muted { spoken.append("muted") }
        if row.attention { spoken.append("needs attention") } else if row.working { spoken.append("working") }
        if bell { spoken.append("bell") }
        if closing { spoken.append("ending session") } else if !row.detail.isEmpty { spoken.append(row.detail) }
        if let sessionKey { spoken.append("Command \(sessionKey)") }
        return spoken.joined(separator: ", ")
    }
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

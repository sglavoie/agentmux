import AppKit

/// The session list. It keeps the keys a list needs — the arrows, Return
/// to go into the selected row, and ← and → to collapse and expand its
/// project — and nothing typed here reaches a program.
@MainActor
final class WorkspaceSidebarView: NSTableView {
    var onWidthChanged: (() -> Void)?
    var projectDividers = IndexSet() {
        didSet {
            enumerateAvailableRowViews { rowView, row in self.syncProjectDivider(rowView, row: row) }
        }
    }
    var sessionDividers = IndexSet() {
        didSet {
            enumerateAvailableRowViews { rowView, row in self.syncProjectDivider(rowView, row: row) }
        }
    }

    func syncProjectDivider(_ rowView: NSTableRowView, row: Int) {
        let existing = rowView.subviews.first { $0 is WorkspaceProjectDividerView } as? WorkspaceProjectDividerView
        let highContrast = projectDividers.contains(row + 1)
        guard highContrast || sessionDividers.contains(row + 1) else {
            existing?.removeFromSuperview()
            return
        }
        let divider = existing ?? WorkspaceProjectDividerView(frame: rowView.bounds)
        divider.highContrast = highContrast
        divider.autoresizingMask = [.width, .height]
        rowView.addSubview(divider, positioned: .above, relativeTo: nil)
    }

    override func setFrameSize(_ newSize: NSSize) {
        let changed = newSize.width != frame.width
        super.setFrameSize(newSize)
        if changed {
            // Wait until AppKit has also resized the table column.
            DispatchQueue.main.async { [weak self] in self?.onWidthChanged?() }
        }
    }
    private(set) var pointerInside = false
    /// Section headings with state-colored backgrounds.
    var bands: [(lines: Range<Int>, band: WorkspaceSidebarBand)] = [] {
        didSet {
            needsDisplay = true
            enumerateAvailableRowViews { rowView, row in self.syncBandAppearance(rowView, row: row) }
        }
    }

    /// Keep native text contrast in both appearances. Status tint belongs to
    /// the background; labels do not need a second colored surface.
    func syncBandAppearance(_ rowView: NSTableRowView, row: Int) {
        rowView.appearance = nil
        (rowView as? WorkspaceRowView)?.chipColor = nil
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        enumerateAvailableRowViews { rowView, row in self.syncBandAppearance(rowView, row: row) }
    }

    /// Width of the profile-color strip along the right edge of a row.
    static let stripWidth: CGFloat = 6

    override func drawBackground(inClipRect clipRect: NSRect) {
        super.drawBackground(inClipRect: clipRect)
        for (lines, band) in bands where !lines.isEmpty && lines.upperBound <= numberOfRows {
            let rect = rect(ofRow: lines.lowerBound).union(rect(ofRow: lines.upperBound - 1))
            guard rect.intersects(clipRect) else { continue }
            let fill = NSRect(x: bounds.minX + 6, y: rect.minY + 2,
                              width: max(0, bounds.width - 12), height: max(0, rect.height - 4))
            band.color.setFill()
            NSBezierPath(roundedRect: fill, xRadius: 6, yRadius: 6).fill()
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

/// A decorative rule above the row's contents, without adding a keyboard or
/// mouse target. Each rule sits at the bottom of the preceding row.
@MainActor
private final class WorkspaceProjectDividerView: NSView {
    var highContrast = false {
        didSet { needsDisplay = true }
    }

    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        needsDisplay = true
    }

    override func draw(_ dirtyRect: NSRect) {
        let thickness = 1 / (window?.backingScaleFactor ?? 2)
        let dark = effectiveAppearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
        let color = highContrast ? NSColor(white: dark ? 0.9 : 0.1, alpha: 1) : .separatorColor
        color.setFill()
        NSRect(x: 10, y: 0, width: max(0, bounds.width - 20), height: thickness).fill()
    }
}

/// Navigation buttons and compact terminal tabs draw their own selection.
/// Their backing rows must not draw an additional full-width highlight.
@MainActor
final class WorkspaceUnhighlightedRowView: NSTableRowView {
    override var selectionHighlightStyle: NSTableView.SelectionHighlightStyle {
        get { .none }
        set { super.selectionHighlightStyle = .none }
    }
}

@MainActor
final class WorkspaceNavigationCellView: NSTableCellView {
    static let identifier = NSUserInterfaceItemIdentifier("workspace.navigation")
    static let spacing: CGFloat = 8
    // Include the source list's cell insets when deciding whether both fit.
    static var minimumWidth: CGFloat {
        32 + spacing + 2 * max(WorkspaceNavigationButton.width(for: .dashboard), WorkspaceNavigationButton.width(for: .overview))
    }
    let buttons = [WorkspaceNavigationButton(view: .dashboard), WorkspaceNavigationButton(view: .overview)]
    var onSelect: ((String) -> Void)?
    var onMenu: ((WorkspaceUtilityView) -> NSMenu?)?

    init() {
        super.init(frame: .zero)
        identifier = Self.identifier
        setAccessibilityRole(.group)
        setAccessibilityLabel("Workspace navigation")
        for button in buttons {
            button.target = self
            button.action = #selector(clicked(_:))
            button.onMenu = { [weak self, view = button.view] in self?.onMenu?(view) }
            addSubview(button)
        }
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) is not supported") }

    func select(_ id: String?) {
        for button in buttons {
            button.state = button.view.id == id ? .on : .off
            button.needsDisplay = true
        }
    }

    override func layout() {
        let width = max(0, (bounds.width - Self.spacing) / 2)
        for (index, button) in buttons.enumerated() {
            button.frame = NSRect(x: CGFloat(index) * (width + Self.spacing), y: 0, width: width, height: bounds.height)
        }
        super.layout()
    }

    @objc private func clicked(_ sender: WorkspaceNavigationButton) { onSelect?(sender.view.id) }
}

@MainActor
final class WorkspaceNavigationButton: NSButton {
    static let titleFont = NSFont.systemFont(ofSize: 13, weight: .bold)
    static let detailFont = NSFont.systemFont(ofSize: NSFont.smallSystemFontSize, weight: .bold)
    let view: WorkspaceUtilityView
    let content = WorkspaceRowCellView()
    var onMenu: (() -> NSMenu?)?

    static func width(for view: WorkspaceUtilityView) -> CGFloat {
        48 + ceil(max((view.displayName as NSString).size(withAttributes: [.font: titleFont]).width,
                      (view.detail as NSString).size(withAttributes: [.font: detailFont]).width))
    }

    init(view: WorkspaceUtilityView) {
        self.view = view
        super.init(frame: NSRect(x: 0, y: 0, width: Self.width(for: view), height: 38))
        title = view.displayName
        isBordered = false
        setButtonType(.pushOnPushOff)
        content.frame = bounds.insetBy(dx: 8, dy: 0)
        addSubview(content)
        content.setAccessibilityElement(false)
        show()
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) is not supported") }
    override var isFlipped: Bool { true }
    override func menu(for event: NSEvent) -> NSMenu? { onMenu?() }

    func show(program: String? = nil, bell: Bool = false, progress: Ghostty.Progress? = nil) {
        content.show(view, program: program, bell: bell, progress: progress)
        toolTip = content.toolTip
        setAccessibilityLabel(content.accessibilityLabel())
    }

    override func layout() {
        content.frame = bounds.insetBy(dx: 8, dy: 0)
        super.layout()
    }

    override func hitTest(_ point: NSPoint) -> NSView? { super.hitTest(point) == nil ? nil : self }

    override func draw(_ dirtyRect: NSRect) {
        if state == .on || isHighlighted {
            let pill = NSBezierPath(roundedRect: bounds.insetBy(dx: 1, dy: 1), xRadius: 10, yRadius: 10)
            NSColor.controlAccentColor.withAlphaComponent(0.12).setFill()
            pill.fill()
            if state == .on {
                NSColor.controlAccentColor.setStroke()
                pill.lineWidth = 2
                pill.stroke()
            }
        }
    }
}

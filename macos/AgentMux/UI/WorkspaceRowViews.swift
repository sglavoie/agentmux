import AppKit

/// A session row's profile color fills a strip along its right edge.
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
            NSRect(x: bounds.maxX - WorkspaceSidebarView.stripWidth, y: bounds.minY,
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
    private let flashView: WorkspaceRowFlashView = {
        let view = WorkspaceRowFlashView()
        view.wantsLayer = true
        view.layer?.opacity = 0
        view.layer?.cornerRadius = WorkspaceRowView.selectionRadius
        return view
    }()

    static let flashKey = "amx.row.flash"

    /// Briefly highlights the line so the eye finds it, then leaves it as it was.
    func flash(color: NSColor = .controlAccentColor.withAlphaComponent(0.35), pulseDuration: TimeInterval = 0.32, pulses: Int = 1) {
        if flashView.superview !== self { addSubview(flashView, positioned: .below, relativeTo: selectionBorder) }
        flashView.frame = pillRect(verticalInset: 1)
        flashView.flash(color: color, pulseDuration: pulseDuration, pulses: pulses)
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

/// The flash remains installed after its layer fades; it must never cover
/// the row's terminal tabs or close buttons for mouse hit testing.
@MainActor
final class WorkspaceRowFlashView: NSView {
    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    func flash(color: NSColor = .controlAccentColor.withAlphaComponent(0.35), pulseDuration: TimeInterval = 0.32, pulses: Int = 1) {
        wantsLayer = true
        layer?.opacity = 0
        effectiveAppearance.performAsCurrentDrawingAppearance {
            layer?.backgroundColor = color.cgColor
        }
        let pulses = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion ? 1 : pulses
        let animation = CAKeyframeAnimation(keyPath: "opacity")
        animation.values = [0] + Array(repeating: [1, 0], count: pulses).flatMap { $0 }
        animation.duration = pulseDuration * Double(pulses)
        layer?.add(animation, forKey: WorkspaceRowView.flashKey)
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

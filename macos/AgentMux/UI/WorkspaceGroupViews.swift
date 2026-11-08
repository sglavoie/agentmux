import AppKit

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
        labelLeading.constant = 2
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
        labelLeading.constant = group.isNested ? 14 : 2
        if group.needsAttention {
            show(section: group.title)
            countLabel.stringValue = String(group.rows.count)
            countLabel.toolTip = "\(group.rows.count) waiting on you"
            setAccessibilityLabel("\(group.title), \(group.rows.count) waiting")
            return
        }
        if group.isFlat {
            show(section: group.title)
            countLabel.stringValue = String(group.rows.count)
            countLabel.toolTip = "\(group.rows.count) sessions"
            setAccessibilityLabel("\(group.title), \(group.rows.count) sessions")
            return
        }
        var label: String
        if group.isSectionHeading || group.isProjectHeading {
            // A heading counts all the sessions it holds.
            nameLabel.stringValue = group.title
            countLabel.stringValue = String(group.sessions)
            countLabel.toolTip = group.sessions == 1 ? "1 session" : "\(group.sessions) sessions"
            label = "\(group.title), \(countLabel.toolTip ?? "")"
            if group.isProjectHeading, group.attention > 0 {
                countLabel.stringValue += " · \(group.attention) waiting"
                countLabel.toolTip = "\(group.sessions) sessions, \(group.attention) waiting on you"
                label = "\(group.title), \(countLabel.toolTip ?? "")"
            }
        } else {
            nameLabel.stringValue = group.title
            let waiting = group.attention
            countLabel.stringValue = waiting > 0 ? String(waiting) : ""
            countLabel.toolTip = waiting > 0 ? "\(waiting) waiting on you" : nil
            label = waiting > 0 ? "\(group.title), \(waiting) waiting" : group.title
        }
        disclosureLabel.isHidden = false
        disclosureLabel.stringValue = group.collapsed ? Self.collapsedDisclosure : Self.expandedDisclosure
        disclosureLabel.toolTip = group.collapsed ? "Collapsed: click to expand" : "Click to collapse"
        disclosureLabel.setAccessibilityLabel(group.collapsed ? "collapsed" : "expanded")
        if !group.reviewLabel.isEmpty {
            countLabel.stringValue += (countLabel.stringValue.isEmpty ? "" : " · ") + group.reviewLabel
            label += ", " + group.reviewLabel
        }
        toolTip = [group.title, group.reviewLabel, group.projectNote].filter { !$0.isEmpty }.joined(separator: "\n")
        if !group.projectNote.isEmpty { label += ", " + group.projectNote }
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

/// One section's chip on a neutral native surface.
@MainActor
final class WorkspaceOverviewChip: NSButton {
    private(set) var band: WorkspaceSidebarBand = .idle

    init() {
        super.init(frame: .zero)
        isBordered = false
        bezelStyle = .regularSquare
        focusRingType = .none
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
        NSColor.controlBackgroundColor.setFill()
        NSBezierPath(roundedRect: bounds, xRadius: 5, yRadius: 5).fill()
        if isHighlighted {
            NSColor.labelColor.withAlphaComponent(0.12).setFill()
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

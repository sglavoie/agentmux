import AppKit

/// Configured group tags, wrapped to the sidebar's current width.
@MainActor
final class WorkspaceGroupFilter: NSView {
    var onSelect: ((String?) -> Void)?
    private(set) var pills: [WorkspaceGroupPill] = []
    private var groups: [String] = []

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        setContentHuggingPriority(.required, for: .vertical)
        setContentCompressionResistancePriority(.required, for: .vertical)
        setAccessibilityRole(.group)
        setAccessibilityLabel("Group scope")
        show(groups: [], selected: nil)
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) is not supported") }
    override var isFlipped: Bool { true }

    func show(groups: [String], selected: String?, waiting: [String: Int] = [:], totalWaiting: Int? = nil) {
        if self.groups != groups || pills.isEmpty {
            self.groups = groups
            pills.forEach { $0.removeFromSuperview() }
            pills = ([nil] + groups.map(Optional.some)).map { group in
                let pill = WorkspaceGroupPill(group: group)
                pill.target = self
                pill.action = #selector(clicked(_:))
                addSubview(pill)
                return pill
            }
            invalidateIntrinsicContentSize()
            needsLayout = true
        }
        for pill in pills {
            let previousTitle = pill.title
            pill.showWaiting(pill.group.map { waiting[$0] } ?? totalWaiting)
            if previousTitle != pill.title { invalidateIntrinsicContentSize(); needsLayout = true }
            pill.state = pill.group == selected ? .on : .off
            pill.needsDisplay = true
        }
    }

    @objc private func clicked(_ sender: WorkspaceGroupPill) { onSelect?(sender.group) }

    private func frames(width: CGFloat) -> [NSRect] {
        let available = max(0, width - 20)
        var x: CGFloat = 0, y: CGFloat = 6
        return pills.map { pill in
            let textWidth = (pill.title as NSString).size(withAttributes: [.font: WorkspaceGroupPill.labelFont]).width
            let width = min(ceil(textWidth) + 20, available)
            if x > 0, x + width > available { x = 0; y += 30 }
            let frame = NSRect(x: 10 + x, y: y, width: width, height: 24)
            x += width + 6
            return frame
        }
    }

    override var intrinsicContentSize: NSSize {
        NSSize(width: NSView.noIntrinsicMetric, height: (frames(width: bounds.width).last?.maxY ?? 30) + 6)
    }

    override func setFrameSize(_ newSize: NSSize) {
        let changed = newSize.width != frame.width
        super.setFrameSize(newSize)
        if changed { invalidateIntrinsicContentSize(); needsLayout = true }
    }

    override func layout() {
        super.layout()
        for (pill, frame) in zip(pills, frames(width: bounds.width)) { pill.frame = frame }
    }
}

@MainActor
final class WorkspaceGroupPill: NSButton {
    static let labelFont = NSFont.systemFont(ofSize: NSFont.smallSystemFontSize, weight: .medium)
    let group: String?
    private(set) var waitingCount: Int?

    func showWaiting(_ count: Int?) {
        waitingCount = count
        let name = group ?? "All"
        title = count.map { $0 > 0 ? "\(name) · \($0)" : name } ?? name
        let label = group.map { "Group: \($0)" } ?? "All groups"
        setAccessibilityLabel(count.map { "\(label), \($0) waiting" } ?? "\(label), waiting count unavailable")
        let scope = group.map { "Show sessions in \($0). Click again to show all groups." } ?? "Show all groups."
        toolTip = scope + (count.map { " \($0) waiting." } ?? " Waiting count unavailable.")
    }

    init(group: String?) {
        self.group = group
        super.init(frame: .zero)
        title = group ?? "All"
        setButtonType(.pushOnPushOff)
        isBordered = false
        font = Self.labelFont
        toolTip = group.map { "Show sessions in \($0). Click again to show all groups." } ?? "Show all groups"
        setAccessibilityLabel(group.map { "Group: \($0)" } ?? "All groups")
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) is not supported") }

    override func draw(_ dirtyRect: NSRect) {
        let selected = state == .on
        (selected ? NSColor.controlAccentColor : NSColor.quaternaryLabelColor).setFill()
        let shape = NSBezierPath(roundedRect: bounds, xRadius: bounds.height / 2, yRadius: bounds.height / 2)
        shape.fill()
        if isHighlighted { NSColor.labelColor.withAlphaComponent(0.12).setFill(); shape.fill() }
        let paragraph = NSMutableParagraphStyle()
        paragraph.alignment = .center
        paragraph.lineBreakMode = .byTruncatingTail
        let text = NSAttributedString(string: title, attributes: [
            .font: Self.labelFont,
            .foregroundColor: selected ? NSColor.white : NSColor.labelColor,
            .paragraphStyle: paragraph,
        ])
        let height = text.size().height
        text.draw(in: NSRect(x: 10, y: (bounds.height - height) / 2, width: max(0, bounds.width - 20), height: height))
    }
}

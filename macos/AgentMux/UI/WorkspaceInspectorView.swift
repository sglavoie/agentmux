import AppKit

/// The workspace's right sidebar: the sessions waiting on the operator, or
/// what there is to say about the session the pane is on.
///
/// Nothing in it takes the keyboard. A click on a waiting session goes to
/// it, Answer… on one opens the panel that answers it without going there,
/// a click on an identifier copies it, and the buttons do what the
/// session function keys do; the terminal that had the keyboard keeps it.
@MainActor
final class WorkspaceInspectorView: NSView {
    /// Everything the sidebar shows.
    struct Model: Equatable {
        var tab = WorkspaceInspectorTab.waiting
        var waiting: [WorkspaceWaitingRow] = []
        /// Nil when the pane is on no session.
        var inspection: WorkspaceInspection?
        /// False while the rows are held from the last live snapshot.
        var current = true
        /// What is wrong with the feed, said instead of "nothing waiting":
        /// a daemon that is down cannot say that nothing is.
        var feedNotice: String?
    }

    static let nothingWaiting = "Nothing is waiting on you."
    static let noSession = "No session is shown."
    static let kittyOnly = "A Kitty session has this in Kitty."

    var onTab: ((WorkspaceInspectorTab) -> Void)?
    var onWaiting: ((AttentionCycler.Key) -> Void)?
    var onAnswer: ((WorkspaceWaitingAnswer) -> Void)?
    var onAction: ((WorkspaceInspectorAction) -> Void)?
    /// Takes a copied identifier; the general pasteboard unless a test says
    /// otherwise.
    var onCopy: (String) -> Void = { value in
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(value, forType: .string)
    }

    private(set) var model = Model()
    let tabs = NSSegmentedControl(
        labels: WorkspaceInspectorTab.allCases.map(\.title), trackingMode: .selectOne, target: nil, action: nil
    )
    /// The lines under the tabs, top to bottom.
    private(set) var lines: [NSView] = []
    private let stack = WorkspaceInspectorStack()
    private static let inset: CGFloat = 12

    init() {
        super.init(frame: .zero)
        tabs.target = self
        tabs.action = #selector(tabChosen(_:))
        tabs.controlSize = .small
        tabs.segmentDistribution = .fillEqually
        tabs.refusesFirstResponder = true
        tabs.translatesAutoresizingMaskIntoConstraints = false
        tabs.setAccessibilityLabel("Inspector")

        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 6
        stack.edgeInsets = NSEdgeInsets(top: 4, left: Self.inset, bottom: Self.inset, right: Self.inset)
        stack.translatesAutoresizingMaskIntoConstraints = false

        let scroll = NSScrollView()
        scroll.documentView = stack
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.drawsBackground = false
        scroll.translatesAutoresizingMaskIntoConstraints = false

        addSubview(tabs)
        addSubview(scroll)
        NSLayoutConstraint.activate([
            tabs.topAnchor.constraint(equalTo: topAnchor, constant: 8),
            tabs.leadingAnchor.constraint(equalTo: leadingAnchor, constant: Self.inset),
            tabs.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -Self.inset),
            scroll.topAnchor.constraint(equalTo: tabs.bottomAnchor, constant: 6),
            scroll.leadingAnchor.constraint(equalTo: leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: bottomAnchor),
            stack.topAnchor.constraint(equalTo: scroll.contentView.topAnchor),
            stack.leadingAnchor.constraint(equalTo: scroll.contentView.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: scroll.contentView.trailingAnchor),
        ])
        setAccessibilityLabel("Inspector")
        rebuild()
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    /// Shows `model`; nothing is rebuilt when it is what is shown already.
    func show(_ model: Model) {
        guard model != self.model else { return }
        self.model = model
        rebuild()
    }

    private func rebuild() {
        for (index, tab) in WorkspaceInspectorTab.allCases.enumerated() {
            let count = tab == .waiting && !model.waiting.isEmpty ? " \(model.waiting.count)" : ""
            tabs.setLabel(tab.title + count, forSegment: index)
            if tab == model.tab { tabs.selectedSegment = index }
        }
        alphaValue = model.current ? 1 : 0.5

        lines.forEach { $0.removeFromSuperview() }
        lines = []
        switch model.tab {
        case .waiting:
            if model.waiting.isEmpty { add(Self.note(model.feedNotice ?? Self.nothingWaiting)) }
            for row in model.waiting {
                let line = WorkspaceWaitingRowView(row)
                line.onPress = { [weak self] in self?.onWaiting?(row.key) }
                line.onAnswer = { [weak self] answer in self?.onAnswer?(answer) }
                add(line)
            }
        case .session:
            guard let inspection = model.inspection else { return add(Self.note(Self.noSession)) }
            let title = NSTextField(labelWithString: [inspection.glyph, inspection.title].filter { !$0.isEmpty }.joined(separator: " "))
            title.font = .systemFont(ofSize: NSFont.systemFontSize, weight: .semibold)
            title.lineBreakMode = .byTruncatingTail
            title.toolTip = inspection.title
            add(title)
            for section in inspection.sections {
                add(Self.heading(section.title))
                for field in section.fields {
                    let line = WorkspaceInspectorFieldView(field)
                    line.onCopy = { [weak self] value in self?.onCopy(value) }
                    add(line)
                }
            }
            add(Self.heading("Actions"))
            add(actions(for: inspection))
        }
    }

    /// Adds a line as wide as the sidebar.
    private func add(_ line: NSView) {
        line.translatesAutoresizingMaskIntoConstraints = false
        stack.addArrangedSubview(line)
        line.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -2 * Self.inset).isActive = true
        lines.append(line)
    }

    private static func note(_ text: String) -> NSTextField {
        let label = NSTextField(wrappingLabelWithString: text)
        label.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        label.textColor = .secondaryLabelColor
        return label
    }

    private static func heading(_ text: String) -> NSView {
        let label = NSTextField(labelWithString: text.uppercased())
        label.font = .systemFont(ofSize: 10, weight: .semibold)
        label.textColor = .tertiaryLabelColor
        label.translatesAutoresizingMaskIntoConstraints = false
        // The room above a heading sets its section apart.
        let holder = NSView()
        holder.addSubview(label)
        NSLayoutConstraint.activate([
            label.topAnchor.constraint(equalTo: holder.topAnchor, constant: 8),
            label.leadingAnchor.constraint(equalTo: holder.leadingAnchor),
            label.trailingAnchor.constraint(lessThanOrEqualTo: holder.trailingAnchor),
            label.bottomAnchor.constraint(equalTo: holder.bottomAnchor),
        ])
        return holder
    }

    /// The buttons, two to a line. One a Kitty session has in Kitty is
    /// disabled and says so.
    private func actions(for inspection: WorkspaceInspection) -> NSView {
        let buttons = WorkspaceInspectorAction.allCases.enumerated().map { index, action in
            let button = NSButton(title: action.title(muted: inspection.muted), target: self, action: #selector(actionPressed(_:)))
            button.tag = index
            button.bezelStyle = .rounded
            button.controlSize = .small
            button.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
            button.refusesFirstResponder = true
            button.isEnabled = inspection.embedded || !action.needsEmbedded
            button.toolTip = button.isEnabled ? action.shortcut : Self.kittyOnly
            return button
        }
        let rows = stride(from: 0, to: buttons.count, by: 2).map { Array(buttons[$0..<min($0 + 2, buttons.count)]) }
        let grid = NSGridView(views: rows)
        grid.rowSpacing = 4
        grid.columnSpacing = 6
        for column in 0..<grid.numberOfColumns { grid.column(at: column).xPlacement = .fill }
        if buttons.count > 1 { buttons[0].widthAnchor.constraint(equalTo: buttons[1].widthAnchor).isActive = true }
        return grid
    }

    /// The buttons of the session tab, in `WorkspaceInspectorAction` order;
    /// empty on the other tab and with no session shown.
    var actionButtons: [NSButton] {
        (lines.last as? NSGridView)?.subviews.compactMap { $0 as? NSButton }.sorted { $0.tag < $1.tag } ?? []
    }

    @objc private func tabChosen(_ sender: Any?) {
        let all = WorkspaceInspectorTab.allCases
        guard all.indices.contains(tabs.selectedSegment) else { return }
        onTab?(all[tabs.selectedSegment])
    }

    @objc private func actionPressed(_ sender: NSButton) {
        let all = WorkspaceInspectorAction.allCases
        guard all.indices.contains(sender.tag) else { return }
        onAction?(all[sender.tag])
    }
}

/// The inspector's lines, laid out from the top down so a short list sits
/// at the top of the sidebar.
@MainActor
final class WorkspaceInspectorStack: NSStackView {
    override var isFlipped: Bool { true }
}

/// One waiting session: a click goes to it, as a click on its line in the
/// session list does. One that can be answered from here has an Answer…
/// button, which opens a panel and leaves the pane where it is.
@MainActor
final class WorkspaceWaitingRowView: NSBox {
    static let answerTitle = "Answer…"

    let row: WorkspaceWaitingRow
    var onPress: (() -> Void)?
    var onAnswer: ((WorkspaceWaitingAnswer) -> Void)?
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

    static let restingFill = NSColor.quaternaryLabelColor
    static let hoveredFill = NSColor.labelColor.withAlphaComponent(0.16)
    static let pressedFill = NSColor.labelColor.withAlphaComponent(0.24)

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

        titleLabel.stringValue = [row.glyph, row.title].filter { !$0.isEmpty }.joined(separator: " ")
        titleLabel.font = .systemFont(ofSize: NSFont.systemFontSize, weight: .medium)
        titleLabel.lineBreakMode = .byTruncatingTail
        detailLabel.stringValue = row.detail
        detailLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        detailLabel.textColor = .secondaryLabelColor
        detailLabel.lineBreakMode = .byTruncatingTail

        let text = NSStackView(views: [titleLabel, detailLabel])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 1
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
            button.setAccessibilityLabel("Answer \(row.title)")
            stack.addArrangedSubview(button)
            answerButton = button
        }
        text.setContentHuggingPriority(.defaultLow, for: .horizontal)
        let content = NSView()
        content.addSubview(stack)
        contentView = content
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: content.topAnchor, constant: 6),
            stack.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -6),
            stack.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 8),
            stack.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -8),
        ])
        for label in [titleLabel, detailLabel] {
            label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        }
        toolTip = row.toolTip
        setAccessibilityRole(.button)
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
}

/// One thing known about the shown session: what it is, and its value. An
/// identifier is copied by a click on its line.
@MainActor
final class WorkspaceInspectorFieldView: NSView {
    static let labelWidth: CGFloat = 84
    static let copied = "Copied"

    let field: WorkspaceInspection.Field
    var onCopy: ((String) -> Void)?
    let nameLabel = NSTextField(labelWithString: "")
    let valueLabel = NSTextField(labelWithString: "")

    init(_ field: WorkspaceInspection.Field) {
        self.field = field
        super.init(frame: .zero)
        nameLabel.stringValue = field.label
        nameLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        nameLabel.textColor = .secondaryLabelColor
        valueLabel.stringValue = field.value
        valueLabel.font = field.copies
            ? .monospacedSystemFont(ofSize: NSFont.smallSystemFontSize, weight: .regular)
            : .systemFont(ofSize: NSFont.smallSystemFontSize)
        valueLabel.lineBreakMode = .byTruncatingMiddle
        valueLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        for label in [nameLabel, valueLabel] {
            label.translatesAutoresizingMaskIntoConstraints = false
            addSubview(label)
        }
        NSLayoutConstraint.activate([
            nameLabel.leadingAnchor.constraint(equalTo: leadingAnchor),
            nameLabel.widthAnchor.constraint(equalToConstant: Self.labelWidth),
            nameLabel.firstBaselineAnchor.constraint(equalTo: valueLabel.firstBaselineAnchor),
            valueLabel.leadingAnchor.constraint(equalTo: nameLabel.trailingAnchor, constant: 6),
            valueLabel.trailingAnchor.constraint(equalTo: trailingAnchor),
            valueLabel.topAnchor.constraint(equalTo: topAnchor),
            valueLabel.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])
        toolTip = field.copies ? "\(field.value) — click to copy" : field.value
        setAccessibilityLabel("\(field.label): \(field.value)")
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    override func hitTest(_ point: NSPoint) -> NSView? {
        super.hitTest(point) == nil ? nil : self
    }

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func mouseDown(with event: NSEvent) {}

    override func mouseUp(with event: NSEvent) {
        guard bounds.contains(convert(event.locationInWindow, from: nil)) else { return }
        copyValue()
    }

    /// Copies the value and says so for a moment; nothing for a line that is
    /// not an identifier.
    func copyValue() {
        guard field.copies else { return }
        onCopy?(field.value)
        valueLabel.stringValue = Self.copied
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) { [weak self] in
            MainActor.assumeIsolated { self?.valueLabel.stringValue = self?.field.value ?? "" }
        }
    }
}

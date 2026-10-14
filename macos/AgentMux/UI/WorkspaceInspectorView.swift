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

        /// The lines under the tabs, top to bottom. The session tab with no
        /// session says what is wrong with the feed when something is: the
        /// pane may still be on a session nobody can list.
        var lines: [WorkspaceInspectorLine] {
            switch tab {
            case .waiting:
                if waiting.isEmpty { return [.note(feedNotice ?? WorkspaceInspectorView.nothingWaiting)] }
                return waiting.map(WorkspaceInspectorLine.waiting)
            case .session:
                guard let inspection else { return [.note(feedNotice ?? WorkspaceInspectorView.noSession)] }
                let title = [inspection.glyph, inspection.title].filter { !$0.isEmpty }.joined(separator: " ")
                return [.title(title, toolTip: inspection.title)]
                    + inspection.sections.flatMap { section in
                        [.heading(section.title)] + section.fields.map(WorkspaceInspectorLine.field)
                    }
                    + [.heading("Actions"), .actions(muted: inspection.muted, embedded: inspection.embedded)]
            }
        }
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

    /// Shows `model`. Lines that are the same lines as before are updated
    /// where they are, so a line under the pointer keeps its tint and a
    /// copied identifier keeps saying so while ages tick; the lines are
    /// built again only when they are different lines.
    func show(_ model: Model) {
        guard model != self.model else { return }
        let old = self.model.lines
        self.model = model
        rebuild(keeping: old)
    }

    private func rebuild(keeping old: [WorkspaceInspectorLine]? = nil) {
        for (index, tab) in WorkspaceInspectorTab.allCases.enumerated() {
            let count = tab == .waiting && !model.waiting.isEmpty ? " \(model.waiting.count)" : ""
            tabs.setLabel(tab.title + count, forSegment: index)
            if tab == model.tab { tabs.selectedSegment = index }
        }
        alphaValue = model.current ? 1 : 0.5

        let wanted = model.lines
        if let old, lines.count == old.count, WorkspaceInspectorLine.sameLines(old, wanted) {
            for (view, line) in zip(lines, wanted) { update(view, to: line) }
            return
        }
        lines.forEach { $0.removeFromSuperview() }
        lines = []
        wanted.forEach { add(view(for: $0)) }
    }

    private func view(for line: WorkspaceInspectorLine) -> NSView {
        switch line {
        case let .note(text):
            return Self.note(text)
        case let .title(text, toolTip):
            let title = NSTextField(labelWithString: text)
            title.font = .systemFont(ofSize: NSFont.systemFontSize, weight: .semibold)
            title.lineBreakMode = .byTruncatingTail
            title.toolTip = toolTip
            return title
        case let .heading(text):
            return Self.heading(text)
        case let .field(field):
            let view = WorkspaceInspectorFieldView(field)
            view.onCopy = { [weak self] value in self?.onCopy(value) }
            return view
        case let .waiting(row):
            let view = WorkspaceWaitingRowView(row)
            view.onPress = { [weak self, weak view] in
                if let key = view?.row.key { self?.onWaiting?(key) }
            }
            view.onAnswer = { [weak self] answer in self?.onAnswer?(answer) }
            return view
        case let .actions(muted, embedded):
            return actions(muted: muted, embedded: embedded)
        }
    }

    /// Shows `line` on the view that showed the same line before.
    private func update(_ view: NSView, to line: WorkspaceInspectorLine) {
        switch line {
        case let .note(text):
            (view as? NSTextField)?.stringValue = text
        case let .title(text, toolTip):
            (view as? NSTextField)?.stringValue = text
            view.toolTip = toolTip
        case .heading:
            break
        case let .field(field):
            (view as? WorkspaceInspectorFieldView)?.update(field)
        case let .waiting(row):
            (view as? WorkspaceWaitingRowView)?.update(row)
        case let .actions(muted, embedded):
            for (button, action) in zip(actionButtons, WorkspaceInspectorAction.allCases) {
                Self.configure(button, action, muted: muted, embedded: embedded)
            }
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
    private func actions(muted: Bool, embedded: Bool) -> NSView {
        let buttons = WorkspaceInspectorAction.allCases.enumerated().map { index, action in
            let button = NSButton(title: "", target: self, action: #selector(actionPressed(_:)))
            button.tag = index
            button.bezelStyle = .rounded
            button.controlSize = .small
            button.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
            button.refusesFirstResponder = true
            Self.configure(button, action, muted: muted, embedded: embedded)
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

    private static func configure(_ button: NSButton, _ action: WorkspaceInspectorAction, muted: Bool, embedded: Bool) {
        button.title = action.title(muted: muted)
        button.isEnabled = embedded || !action.needsEmbedded
        button.toolTip = button.isEnabled ? action.shortcut : kittyOnly
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

    private(set) var row: WorkspaceWaitingRow
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

        titleLabel.font = .systemFont(ofSize: NSFont.systemFontSize, weight: .medium)
        titleLabel.lineBreakMode = .byTruncatingTail
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
        setAccessibilityRole(.button)
        update(row)
    }

    /// Shows `row` on this line: the same session, said anew. Whether it has
    /// an Answer… button is the line's own and does not change.
    func update(_ row: WorkspaceWaitingRow) {
        self.row = row
        titleLabel.stringValue = [row.glyph, row.title].filter { !$0.isEmpty }.joined(separator: " ")
        detailLabel.stringValue = row.detail
        answerButton?.setAccessibilityLabel("Answer \(row.title)")
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

    private(set) var field: WorkspaceInspection.Field
    var onCopy: ((String) -> Void)?
    let nameLabel = NSTextField(labelWithString: "")
    let valueLabel = NSTextField(labelWithString: "")
    /// Counts copies, so only the last one's moment ends the "Copied".
    private var copies = 0
    /// Whether the value says "Copied" for now.
    var showsCopied: Bool { copies > 0 }

    init(_ field: WorkspaceInspection.Field) {
        self.field = field
        super.init(frame: .zero)
        nameLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        nameLabel.textColor = .secondaryLabelColor
        valueLabel.font = field.copies
            ? .monospacedSystemFont(ofSize: NSFont.smallSystemFontSize, weight: .regular)
            : .systemFont(ofSize: NSFont.smallSystemFontSize)
        valueLabel.lineBreakMode = .byTruncatingMiddle
        valueLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        for label in [nameLabel, valueLabel] {
            label.translatesAutoresizingMaskIntoConstraints = false
            // The line is said as one; its labels are not said again.
            label.setAccessibilityElement(false)
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
        update(field)
    }

    /// Shows `field` on this line: the same field with its value now. A
    /// value that says "Copied" goes on saying so for its moment.
    func update(_ field: WorkspaceInspection.Field) {
        self.field = field
        nameLabel.stringValue = field.label
        if !showsCopied { valueLabel.stringValue = field.value }
        toolTip = field.copies ? "\(field.value) — click to copy" : field.value
        // A view is not an accessibility element until it says so; an
        // identifier's line is a button that copies it.
        setAccessibilityElement(true)
        setAccessibilityRole(field.copies ? .button : .staticText)
        setAccessibilityLabel("\(field.label): \(field.value)")
        setAccessibilityHelp(field.copies ? "Copies the \(field.label.lowercased())" : nil)
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
        NSAccessibility.post(element: self, notification: .announcementRequested,
                             userInfo: [.announcement: Self.copied,
                                        .priority: NSAccessibilityPriorityLevel.high.rawValue])
        copies += 1
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) { [weak self] in
            MainActor.assumeIsolated {
                guard let self else { return }
                self.copies -= 1
                if !self.showsCopied { self.valueLabel.stringValue = self.field.value }
            }
        }
    }

    override func accessibilityPerformPress() -> Bool {
        guard field.copies else { return false }
        copyValue()
        return true
    }
}

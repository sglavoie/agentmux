import AppKit
import SwiftUI
import Combine

/// The workspace's right sidebar: the sessions waiting on the operator, or
/// what there is to say about the session the pane is on.
///
/// Pointer actions leave terminal focus alone. Window › Waiting explicitly
/// gives the reader the keyboard for context, answer and next-item navigation.
/// A click on a waiting session goes to it, Answer… on one opens the panel that answers it without going there,
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
        /// pane may still be on a session nobody can list. Held lines say it
        /// above them, so what they show is not taken for what is so now.
        var lines: [WorkspaceInspectorLine] {
            let held: [WorkspaceInspectorLine] = !current ? feedNotice.map { [.note($0)] } ?? [] : []
            switch tab {
            case .waiting:
                if waiting.isEmpty { return [.note(feedNotice ?? WorkspaceInspectorView.nothingWaiting)] }
                return held + waiting.map(WorkspaceInspectorLine.waiting)
            case .session:
                guard let inspection else { return [.note(feedNotice ?? WorkspaceInspectorView.noSession)] }
                let title = [inspection.glyph, inspection.title].filter { !$0.isEmpty }.joined(separator: " ")
                return held + [.title(title, toolTip: inspection.title)]
                    + inspection.sections.flatMap { section in
                        [.heading(section.title)] + section.fields.map(WorkspaceInspectorLine.field)
                    }
                    + [.heading("Actions"), .actions(muted: inspection.muted, embedded: inspection.embedded)]
            }
        }
    }

    /// Whether a line is drawn dimmed: every held line but a note.
    static func dims(_ line: WorkspaceInspectorLine, current: Bool) -> Bool {
        guard !current else { return false }
        if case .note = line { return false }
        return true
    }

    nonisolated static let nothingWaiting = "Nothing is waiting on you."
    nonisolated static let noSession = "No session is shown."
    nonisolated static let kittyOnly = "A Kitty session has this in Kitty."
    nonisolated static let waitForDaemon = "Available once the daemon answers again."

    var onTab: ((WorkspaceInspectorTab) -> Void)?
    var onWaiting: ((AttentionCycler.Key) -> Void)?
    var onAnswer: ((WorkspaceWaitingAnswer) -> Void)?
    var onAction: ((WorkspaceInspectorAction) -> Void)?
    var onContext: ((WorkspaceWaitingRow, @escaping (WorkspaceWaitingContext.Result) -> Void) -> Void)?
    let filterPicker = NSPopUpButton()
    let oldestButton = NSButton(checkboxWithTitle: "Oldest first", target: nil, action: nil)
    let nextButton = NSButton(title: "Next Waiting", target: nil, action: nil)
    let updatesButton = NSButton(title: "Update List", target: nil, action: nil)
    private let inboxControls = NSStackView()
    private(set) var waitingFilter = WorkspaceWaitingFilter.all
    private(set) var oldestFirst = false
    private var expanded: WorkspaceWaitingRow?
    private var preview = WorkspaceWaitingContext.Reading()
    private var contextSequence = 0
    private var lastWaiting: AttentionCycler.Key?
    private var heldWaiting: [WorkspaceWaitingRow]?
    private var pointerInside = false
    private let readingPreferences: WorkspaceReadingPreferences
    private var readingSubscription: AnyCancellable?
    private var readerIndex = 0
    var onLeaveReader: (() -> Void)?
    private(set) var readingKey: AttentionCycler.Key?
    let readerHint = NSTextField(wrappingLabelWithString: "↑↓ read · Return answer · N next · Esc return")

    var visibleWaiting: [WorkspaceWaitingRow] { heldWaiting ?? waitingFilter.apply(model.waiting, oldestFirst: oldestFirst) }
    private var displayedLines: [WorkspaceInspectorLine] {
        guard model.tab == .waiting else { return model.lines }
        let rows = visibleWaiting
        let notice = model.current ? [] : model.feedNotice.map { [WorkspaceInspectorLine.note($0)] } ?? []
        guard !rows.isEmpty else { return notice + [.note(model.waiting.isEmpty ? Self.nothingWaiting : "No waiting items match this filter.")] }
        guard waitingFilter == .all else { return notice + [.heading("\(waitingFilter.rawValue) (\(rows.count))")] + rows.map(WorkspaceInspectorLine.waiting) }
        return notice + [0, 1, 2].flatMap { priority -> [WorkspaceInspectorLine] in
            let group = rows.filter { $0.category.priority == priority }
            guard let first = group.first else { return [] }
            return [.heading("\(first.category.groupTitle) (\(group.count))")] + group.map(WorkspaceInspectorLine.waiting)
        }
    }

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
    private let scroll = NSScrollView()
    private static let inset: CGFloat = 12

    init(preferences: WorkspaceReadingPreferences? = nil) {
        let preferences = preferences ?? .shared
        readingPreferences = preferences
        super.init(frame: .zero)
        addTrackingArea(NSTrackingArea(rect: .zero, options: [.mouseEnteredAndExited, .activeInActiveApp, .inVisibleRect], owner: self, userInfo: nil))
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

        scroll.documentView = stack
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.drawsBackground = false
        scroll.translatesAutoresizingMaskIntoConstraints = false

        filterPicker.addItems(withTitles: WorkspaceWaitingFilter.allCases.map(\.rawValue))
        filterPicker.target = self
        filterPicker.action = #selector(filterChanged(_:))
        filterPicker.setAccessibilityLabel("Waiting category")
        oldestButton.target = self
        oldestButton.action = #selector(filterChanged(_:))
        nextButton.target = self
        nextButton.action = #selector(nextWaiting(_:))
        updatesButton.target = self
        updatesButton.action = #selector(applyWaitingUpdates(_:))
        for control in [filterPicker, oldestButton, nextButton, updatesButton] {
            control.controlSize = .small
            control.refusesFirstResponder = true
        }
        inboxControls.orientation = .vertical
        inboxControls.alignment = .leading
        inboxControls.spacing = 4
        let filters = NSStackView(views: [filterPicker, oldestButton])
        filters.spacing = 6
        filterPicker.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let navigation = NSStackView(views: [nextButton, updatesButton])
        navigation.spacing = 6
        updatesButton.lineBreakMode = .byTruncatingTail
        updatesButton.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        for row in [filters, navigation] {
            inboxControls.addArrangedSubview(row)
            row.widthAnchor.constraint(equalTo: inboxControls.widthAnchor).isActive = true
        }
        let readingMenu = NSHostingView(rootView: WorkspaceReadingMenu(preferences: preferences))
        readingMenu.translatesAutoresizingMaskIntoConstraints = false
        readingMenu.widthAnchor.constraint(equalToConstant: 28).isActive = true
        readingMenu.heightAnchor.constraint(equalToConstant: 24).isActive = true
        let tabRow = NSStackView(views: [tabs, readingMenu])
        readerHint.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        readerHint.textColor = .secondaryLabelColor
        readerHint.isHidden = true
        let header = NSStackView(views: [tabRow, inboxControls, readerHint])
        header.orientation = .vertical
        header.alignment = .leading
        header.spacing = 6
        header.translatesAutoresizingMaskIntoConstraints = false
        addSubview(header)
        addSubview(scroll)
        NSLayoutConstraint.activate([
            header.topAnchor.constraint(equalTo: topAnchor, constant: 8),
            header.leadingAnchor.constraint(equalTo: leadingAnchor, constant: Self.inset),
            header.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -Self.inset),
            tabRow.widthAnchor.constraint(equalTo: header.widthAnchor),
            inboxControls.widthAnchor.constraint(equalTo: header.widthAnchor),
            scroll.topAnchor.constraint(equalTo: header.bottomAnchor, constant: 6),
            scroll.leadingAnchor.constraint(equalTo: leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: bottomAnchor),
            stack.topAnchor.constraint(equalTo: scroll.contentView.topAnchor),
            stack.leadingAnchor.constraint(equalTo: scroll.contentView.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: scroll.contentView.trailingAnchor),
        ])
        setAccessibilityLabel("Inspector")
        rebuild()
        readingSubscription = preferences.objectWillChange.sink { [weak self] _ in
            DispatchQueue.main.async { self?.updateReadingLayout() }
        }
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
        let old = displayedLines
        let oldWaiting = visibleWaiting
        let newWaiting = waitingFilter.apply(model.waiting, oldestFirst: oldestFirst)
        let changed = oldWaiting.count != newWaiting.count || zip(oldWaiting, newWaiting).contains { !$0.sameContext(as: $1) }
        if model.tab == .waiting && (pointerInside || expanded != nil), changed {
            // Do not move a target under a pointer or an open context. The live
            // snapshot still invalidates its answer/context buttons immediately.
            heldWaiting = oldWaiting.map { old in newWaiting.first { $0.sameContext(as: old) } ?? old }
        } else { heldWaiting = nil }
        self.model = model
        if let expanded, !model.current || model.tab != .waiting || !model.waiting.contains(where: { $0.sameContext(as: expanded) }) {
            clearContext()
        }
        rebuild(keeping: old)
    }

    private func rebuild(keeping old: [WorkspaceInspectorLine]? = nil) {
        for (index, tab) in WorkspaceInspectorTab.allCases.enumerated() {
            let count = tab == .waiting && !model.waiting.isEmpty ? " \(model.waiting.count)" : ""
            tabs.setLabel(tab.title + count, forSegment: index)
            if tab == model.tab { tabs.selectedSegment = index }
        }
        inboxControls.isHidden = model.tab != .waiting
        readerHint.isHidden = model.tab != .waiting || window?.firstResponder !== self
        updatesButton.isEnabled = heldWaiting != nil
        updatesButton.isHidden = heldWaiting == nil
        updatesButton.title = "Update List (\(model.waiting.count))"
        updatesButton.toolTip = "Apply the updated waiting list. Items stay in place while you read."
        nextButton.isEnabled = model.current && !visibleWaiting.isEmpty
        let wanted = displayedLines
        if let old, lines.count == old.count, WorkspaceInspectorLine.sameLines(old, wanted) {
            for (view, line) in zip(lines, wanted) { update(view, to: line) }
        } else {
            lines.forEach { $0.removeFromSuperview() }
            lines = []
            wanted.forEach { add(view(for: $0)) }
        }
        // Held lines are dimmed, as the sidebar's are; the note saying why,
        // and the tabs, are not, or the warning would be the hardest thing
        // to read.
        for (view, line) in zip(lines, wanted) {
            view.alphaValue = Self.dims(line, current: model.current) ? 0.5 : 1
        }
        for case let row as WorkspaceWaitingRowView in lines {
            row.contextButton.isEnabled = rowIsCurrent(row.row)
            row.configureReading(preferences: readingPreferences, height: row.readingHeight)
            row.showContext(expanded?.key == row.row.key ? preview : nil)
            row.refreshContextButton.isEnabled = rowIsCurrent(row.row) && !preview.loading
        }
        holdWhileNotCurrent()
        updateReadingLayout()
    }

    /// Held lines can still be clicked to go to their session, which is
    /// listed, but nothing is answered or changed from them: what they show
    /// may no longer be so, and the daemon is not there to say.
    private func holdWhileNotCurrent() {
        for case let row as WorkspaceWaitingRowView in lines {
            let current = rowIsCurrent(row.row)
            row.answerButton?.isEnabled = current
            row.answerButton?.toolTip = current ? nil : (model.current ? "This item changed. Update the waiting list." : Self.waitForDaemon)
            if !current && model.current { row.detailLabel.stringValue = "Changed · update the waiting list" }
        }
        guard !model.current else { return }
        for button in actionButtons {
            button.isEnabled = false
            button.toolTip = Self.waitForDaemon
        }
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
            view.onAnswer = { [weak self, weak view] answer in
                guard let self, let view, self.rowIsCurrent(view.row) else { return }
                self.lastWaiting = view.row.key
                self.onAnswer?(answer)
            }
            view.onRefreshContext = { [weak self, weak view] in
                if let row = view?.row { self?.refreshContext(row) }
            }
            view.onContext = { [weak self, weak view] in
                if let row = view?.row { self?.toggleContext(row) }
            }
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
        let label = NSTextField(labelWithString: text)
        label.font = .systemFont(ofSize: NSFont.smallSystemFontSize, weight: .semibold)
        label.textColor = .secondaryLabelColor
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

    func setWaitingFilter(_ filter: WorkspaceWaitingFilter, oldestFirst: Bool) {
        heldWaiting = nil
        waitingFilter = filter
        self.oldestFirst = oldestFirst
        filterPicker.selectItem(withTitle: filter.rawValue)
        oldestButton.state = oldestFirst ? .on : .off
        if let expanded, !visibleWaiting.contains(where: { $0.key == expanded.key }) { clearContext() }
        rebuild()
    }

    @objc private func filterChanged(_ sender: Any?) {
        let filter = WorkspaceWaitingFilter(rawValue: filterPicker.titleOfSelectedItem ?? "") ?? .all
        setWaitingFilter(filter, oldestFirst: oldestButton.state == .on)
    }

    @objc func nextWaiting(_ sender: Any? = nil) {
        applyWaitingUpdates(nil)
        guard model.current, !visibleWaiting.isEmpty else { return }
        let rows = visibleWaiting
        let next = lastWaiting.flatMap { key in rows.firstIndex { $0.key == key } }.map { ($0 + 1) % rows.count } ?? 0
        lastWaiting = rows[next].key
        onWaiting?(rows[next].key)
    }

    /// Explicit keyboard entry; ordinary inspector clicks still preserve terminal focus.
    func focusReader() {
        guard model.tab == .waiting else { return }
        window?.makeFirstResponder(self)
        if let key = readingKey, let row = visibleWaiting.first(where: { $0.key == key }), rowIsCurrent(row) {
            if expanded?.sameContext(as: row) != true { toggleContext(row) }
        } else { moveReader(0) }
    }

    override var acceptsFirstResponder: Bool { true }
    override func becomeFirstResponder() -> Bool {
        guard super.becomeFirstResponder() else { return false }
        readerHint.isHidden = model.tab != .waiting
        updateReadingLayout()
        return true
    }

    override func resignFirstResponder() -> Bool {
        guard super.resignFirstResponder() else { return false }
        readerHint.isHidden = true
        updateReadingLayout()
        return true
    }
    override func keyDown(with event: NSEvent) {
        guard event.modifierFlags.intersection([.command, .control, .option]).isEmpty else { super.keyDown(with: event); return }
        switch event.keyCode {
        case 125: moveReader(1)
        case 126: moveReader(-1)
        case 53: readerHint.isHidden = true; updateReadingLayout(); onLeaveReader?()
        case 36, 76:
            guard let row = visibleWaiting.first(where: { $0.key == readingKey }), rowIsCurrent(row), let answer = row.answer else { NSSound.beep(); return }
            onAnswer?(answer)
        case 116, 121:
            guard let row = lines.compactMap({ $0 as? WorkspaceWaitingRowView }).first(where: { $0.row.key == readingKey }),
                  !row.contextScroll.isHidden else { return }
            let scroll = row.contextScroll
            let direction: CGFloat = event.keyCode == 121 ? 1 : -1
            let clip = scroll.contentView
            let proposed = NSRect(origin: NSPoint(x: clip.bounds.minX, y: clip.bounds.minY + direction * clip.bounds.height * 0.85), size: clip.bounds.size)
            clip.scroll(to: clip.constrainBoundsRect(proposed).origin)
            scroll.reflectScrolledClipView(clip)
        case 49:
            if let row = visibleWaiting.first(where: { $0.key == readingKey }) { toggleContext(row) }
        default:
            if event.charactersIgnoringModifiers?.lowercased() == "n" { moveReader(1) }
            else { super.keyDown(with: event) }
        }
    }

    func moveReader(_ delta: Int) {
        let prior = readingKey.flatMap { key in visibleWaiting.firstIndex { $0.key == key } }
        applyWaitingUpdates(nil)
        let rows = visibleWaiting
        guard model.current, !rows.isEmpty else { return }
        if let index = rows.firstIndex(where: { $0.key == readingKey }) {
            readerIndex = (index + delta + rows.count) % rows.count
        } else { readerIndex = min(prior ?? readerIndex, rows.count - 1) }
        readingKey = rows[readerIndex].key
        if expanded?.sameContext(as: rows[readerIndex]) != true { toggleContext(rows[readerIndex]) }
        updateReadingLayout()
        if let row = lines.compactMap({ $0 as? WorkspaceWaitingRowView }).first(where: { $0.row.key == readingKey }) {
            stack.scrollToVisible(row.frame)
        }
    }

    override func layout() {
        super.layout()
        updateReadingLayout()
    }

    private func updateReadingLayout() {
        for case let row as WorkspaceWaitingRowView in lines {
            row.borderWidth = row.row.key == readingKey && !readerHint.isHidden ? 2 : 0
            row.borderColor = .controlAccentColor
            let otherHeight = max(0, stack.fittingSize.height - row.readingHeight)
            let height = row.contextScroll.isHidden ? row.readingHeight : max(120, scroll.contentSize.height - otherHeight)
            row.configureReading(preferences: readingPreferences, height: height)
        }
    }

    func discardContext() {
        clearContext()
        heldWaiting = nil
        rebuild()
    }

    private func rowIsCurrent(_ row: WorkspaceWaitingRow) -> Bool {
        model.current && model.waiting.contains { $0.sameContext(as: row) }
    }

    override func mouseEntered(with event: NSEvent) { pointerInside = true }
    override func mouseExited(with event: NSEvent) {
        pointerInside = false
        if expanded == nil { applyWaitingUpdates(nil) }
    }

    @objc func applyWaitingUpdates(_ sender: Any?) {
        heldWaiting = nil
        rebuild()
    }

    private func clearContext() {
        contextSequence += 1
        expanded = nil
        preview = WorkspaceWaitingContext.Reading()
    }

    func toggleContext(_ row: WorkspaceWaitingRow) {
        guard rowIsCurrent(row) else { return }
        let closing = expanded?.key == row.key
        clearContext()
        if closing { rebuild(); return }
        expanded = row
        readContext(row)
    }

    func refreshContext(_ row: WorkspaceWaitingRow) {
        guard rowIsCurrent(row), expanded?.sameContext(as: row) == true, !preview.loading else { return }
        readContext(row)
    }

    private func readContext(_ row: WorkspaceWaitingRow) {
        contextSequence += 1
        preview.begin()
        let sequence = contextSequence
        rebuild(keeping: displayedLines)
        guard let onContext else {
            preview.finish(.unavailable(SecretText("Context is unavailable."))); rebuild(keeping: displayedLines); return
        }
        onContext(row) { [weak self] text in
            guard let self, self.contextSequence == sequence, self.model.current,
                  self.expanded?.sameContext(as: row) == true else { return }
            self.preview.finish(text)
            self.rebuild(keeping: self.displayedLines)
        }
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
        if ["Reason", "Evidence", "Observed"].contains(field.label) {
            valueLabel.lineBreakMode = .byWordWrapping
            valueLabel.usesSingleLineMode = false
            valueLabel.cell?.wraps = true
            valueLabel.maximumNumberOfLines = 0
        }
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
        WorkspaceCopyNotice.announce(Self.copied, from: self)
        copies += 1
        DispatchQueue.main.asyncAfter(deadline: .now() + WorkspaceCopyNotice.seconds) { [weak self] in
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

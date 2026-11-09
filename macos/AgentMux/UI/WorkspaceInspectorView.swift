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
final class WorkspaceInspectorView: NSView, WorkspaceSidebarZoomAware {
    var onTab: ((WorkspaceInspectorTab) -> Void)?
    var onWaiting: ((AttentionCycler.Key) -> Void)?
    var onAllWaitingGroups: ((Bool) -> Void)?
    var onAnswer: ((WorkspaceWaitingAnswer) -> Void)?
    var onSource: ((ConversationIdentity) -> Void)?
    var onAction: ((WorkspaceInspectorAction) -> Void)?
    var onContext: ((WorkspaceWaitingRow, @escaping (WorkspaceWaitingContext.Result) -> Void) -> Void)?
    let filterPicker = NSPopUpButton()
    let oldestButton = NSButton(checkboxWithTitle: "Oldest first", target: nil, action: nil)
    let allGroupsButton = NSButton(checkboxWithTitle: "Show all groups", target: nil, action: nil)
    let nextButton = NSButton(title: "Next Waiting", target: nil, action: nil)
    let updatesButton = NSButton(title: "Update List", target: nil, action: nil)
    private let inboxControls = NSStackView()
    private(set) var waitingFilter = WorkspaceWaitingFilter.all
    private(set) var oldestFirst = false
    private var expanded: WorkspaceWaitingRow?
    private var preview = WorkspaceWaitingContext.Reading()
    private var contextSequence = 0
    private(set) var sentAnswers: [AttentionCycler.Key: WorkspaceWaitingRow] = [:]
    func answerWasSent(_ row: WorkspaceWaitingRow) -> Bool { sentAnswers[row.key]?.sameContext(as: row) == true }
    func markAnswerSent(_ row: WorkspaceWaitingRow) {
        sentAnswers[row.key] = row
        lastWaiting = row.key
        rebuild()
    }
    func reconcileSentAnswers(_ waiting: [WorkspaceWaitingRow], current: Bool) {
        guard current else { return }
        sentAnswers = sentAnswers.filter { _, sent in waiting.contains { $0.sameContext(as: sent) } }
    }
    var remainingWaiting: [WorkspaceWaitingRow] { visibleWaiting.filter { !answerWasSent($0) && rowIsCurrent($0) } }
    var lastWaiting: AttentionCycler.Key?
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
    let recap = WorkspaceRecapModel(command: { nil })
    let threads = WorkspaceThreadsModel(command: { nil })
    private lazy var threadsHost = NSHostingView(rootView: WorkspaceThreadsView(model: threads, onOpen: { [weak self] identity in self?.onSource?(identity) }))
    private lazy var recapHost = WorkspaceSidebarHostingView(rootView: WorkspaceInspectorRecapView(model: recap))
    private let sourceButton = NSButton(title: "Read Source Conversation", target: nil, action: nil)
    private let scroll = NSScrollView()
    private static let inset: CGFloat = 12

    init(preferences: WorkspaceReadingPreferences? = nil, welcome: NSView? = nil) {
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

        recapHost.translatesAutoresizingMaskIntoConstraints = false
        stack.addArrangedSubview(recapHost)
        recapHost.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -2 * Self.inset).isActive = true
        recapHost.isHidden = true
        threadsHost.translatesAutoresizingMaskIntoConstraints = false
        stack.addArrangedSubview(threadsHost)
        threadsHost.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -2 * Self.inset).isActive = true
        threadsHost.isHidden = true
        sourceButton.target = self; sourceButton.action = #selector(readSourceConversation(_:))
        sourceButton.bezelStyle = .rounded
        sourceButton.refusesFirstResponder = true
        stack.addArrangedSubview(sourceButton)
        sourceButton.isHidden = true

        filterPicker.addItems(withTitles: WorkspaceWaitingFilter.allCases.map(\.rawValue))
        filterPicker.target = self
        filterPicker.action = #selector(filterChanged(_:))
        filterPicker.setAccessibilityLabel("Waiting category")
        oldestButton.target = self
        oldestButton.action = #selector(filterChanged(_:))
        allGroupsButton.target = self
        allGroupsButton.action = #selector(allWaitingGroupsChanged(_:))
        allGroupsButton.toolTip = "Show waiting sessions from every group. Selecting a hidden session switches the sidebar to All."
        nextButton.cell?.lineBreakMode = .byTruncatingTail
        nextButton.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        nextButton.target = self
        nextButton.action = #selector(nextWaiting(_:))
        // The pane is narrow, so the button keeps the short form of the
        // command's title; its tooltip and accessibility label say it whole.
        nextButton.toolTip = AttentionCycler.commandTitle
        nextButton.setAccessibilityLabel(AttentionCycler.commandTitle)
        updatesButton.target = self
        updatesButton.action = #selector(applyWaitingUpdates(_:))
        for control in [filterPicker, oldestButton, allGroupsButton, nextButton, updatesButton] {
            control.controlSize = .small
            control.refusesFirstResponder = true
        }
        inboxControls.orientation = .vertical
        inboxControls.alignment = .leading
        inboxControls.spacing = 4
        inboxControls.addArrangedSubview(allGroupsButton)
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
        readerHint.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        readerHint.textColor = .secondaryLabelColor
        readerHint.isHidden = true
        let header = NSStackView(views: [tabs, inboxControls, readerHint])
        header.orientation = .vertical
        header.alignment = .leading
        header.spacing = 6
        header.translatesAutoresizingMaskIntoConstraints = false
        addSubview(header)
        addSubview(scroll)
        if let welcome {
            welcome.translatesAutoresizingMaskIntoConstraints = false
            addSubview(welcome)
            NSLayoutConstraint.activate([
                welcome.leadingAnchor.constraint(equalTo: leadingAnchor),
                welcome.trailingAnchor.constraint(equalTo: trailingAnchor),
                welcome.bottomAnchor.constraint(equalTo: bottomAnchor),
            ])
        }
        NSLayoutConstraint.activate([
            header.topAnchor.constraint(equalTo: topAnchor, constant: 8),
            header.leadingAnchor.constraint(equalTo: leadingAnchor, constant: Self.inset),
            header.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -Self.inset),
            tabs.widthAnchor.constraint(equalTo: header.widthAnchor),
            inboxControls.widthAnchor.constraint(equalTo: header.widthAnchor),
            scroll.topAnchor.constraint(equalTo: header.bottomAnchor, constant: 6),
            scroll.leadingAnchor.constraint(equalTo: leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: welcome?.topAnchor ?? bottomAnchor),
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
        let scopeChanged = model.waitingGroup != self.model.waitingGroup
            || model.showsAllWaitingGroups != self.model.showsAllWaitingGroups
        if !scopeChanged, model.tab == .waiting && (pointerInside || expanded != nil), changed {
            // Do not move a target under a pointer or an open context. The live
            // snapshot still invalidates its answer/context buttons immediately.
            heldWaiting = oldWaiting.map { old in newWaiting.first { $0.sameContext(as: old) } ?? old }
        } else { heldWaiting = nil }
        self.model = model
        if model.current {
            sentAnswers = sentAnswers.filter { key, sent in
                model.waiting.first(where: { $0.key == key }).map { $0.sameContext(as: sent) } ?? true
            }
        }
        if let expanded, !model.current || model.tab != .waiting || !model.waiting.contains(where: { $0.sameContext(as: expanded) }) {
            clearContext()
        }
        rebuild(keeping: old)
    }

    private func rebuild(keeping old: [WorkspaceInspectorLine]? = nil) {
        recapHost.isHidden = model.tab != .session || model.inspection == nil
        threadsHost.isHidden = recapHost.isHidden
        sourceButton.isHidden = model.tab != .session || model.sourceConversation == nil
        sourceButton.toolTip = model.sourceConversation?.encoded
        for (index, tab) in WorkspaceInspectorTab.allCases.enumerated() {
            let count = tab == .waiting && !model.waiting.isEmpty ? " \(model.waiting.count)" : ""
            tabs.setLabel(tab.title + count, forSegment: index)
            if tab == model.tab { tabs.selectedSegment = index }
        }
        inboxControls.isHidden = model.tab != .waiting
        allGroupsButton.state = model.showsAllWaitingGroups ? .on : .off
        readerHint.isHidden = model.tab != .waiting || window?.firstResponder !== self
        updatesButton.isEnabled = heldWaiting != nil
        updatesButton.isHidden = heldWaiting == nil
        updatesButton.title = "Update List (\(model.waiting.count))"
        updatesButton.toolTip = "Apply the updated waiting list. Items stay in place while you read."
        nextButton.isEnabled = model.current && !remainingWaiting.isEmpty
        nextButton.title = remainingWaiting.isEmpty ? "Next Waiting" : "Next waiting · \(remainingWaiting.count) remaining"
        nextButton.setAccessibilityLabel(nextButton.title)
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
            let sent = answerWasSent(row.row)
            row.answerButton?.title = sent ? "Answer sent" : WorkspaceWaitingRowView.answerTitle
            row.answerButton?.setAccessibilityLabel(sent ? "Answer sent for " + row.row.title : "Answer " + row.row.title)
            row.answerButton?.isEnabled = current && !sent
            if sent { row.detailLabel.stringValue = "Answer sent · waiting for agent" }
            row.answerButton?.toolTip = current ? nil : (model.current ? "This item changed. Update the waiting list." : Self.waitForDaemon)
            if !current && model.current { row.detailLabel.stringValue = "Changed · update the waiting list" }
        }
        guard !model.current else { return }
        for button in actionButtons {
            button.isEnabled = false
            button.toolTip = Self.waitForDaemon
        }
    }

    @objc private func readSourceConversation(_ sender: Any?) {
        if let source = model.sourceConversation { onSource?(source) }
    }

    /// Adds a line as wide as the sidebar.
    private func add(_ line: NSView) {
        line.translatesAutoresizingMaskIntoConstraints = false
        stack.addArrangedSubview(line)
        line.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -2 * Self.inset).isActive = true
        lines.append(line)
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

    @objc private func allWaitingGroupsChanged(_ sender: NSButton) {
        onAllWaitingGroups?(sender.state == .on)
    }

    @objc func nextWaiting(_ sender: Any? = nil) {
        applyWaitingUpdates(nil)
        guard model.current, !remainingWaiting.isEmpty else { return }
        let rows = remainingWaiting
        let next = lastWaiting.flatMap { key in rows.firstIndex { $0.key == key } }.map { ($0 + 1) % rows.count } ?? 0
        lastWaiting = rows[next].key
        onWaiting?(rows[next].key)
    }

    /// Finds a waiting row without changing the tab, filters, or keyboard focus.
    func revealWaiting(_ key: AttentionCycler.Key) {
        guard model.tab == .waiting,
              let row = lines.compactMap({ $0 as? WorkspaceWaitingRowView }).first(where: { $0.row.key == key }) else { return }
        layoutSubtreeIfNeeded()
        stack.scrollToVisible(row.frame)
        row.flash()
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
            guard let row = visibleWaiting.first(where: { $0.key == readingKey }), rowIsCurrent(row), !answerWasSent(row), let answer = row.answer else { NSSound.beep(); return }
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
            if event.charactersIgnoringModifiers?.lowercased() == "n" { nextUnansweredInReader() }
            else { super.keyDown(with: event) }
        }
    }

    func nextUnansweredInReader() {
        applyWaitingUpdates(nil)
        let rows = remainingWaiting
        guard !rows.isEmpty else { return }
        let next = readingKey.flatMap { key in rows.firstIndex { $0.key == key } }.map { ($0 + 1) % rows.count } ?? 0
        readingKey = rows[next].key
        if expanded?.sameContext(as: rows[next]) != true { toggleContext(rows[next]) }
        updateReadingLayout()
        if let view = lines.compactMap({ $0 as? WorkspaceWaitingRowView }).first(where: { $0.row.key == readingKey }) { stack.scrollToVisible(view.frame) }
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

    func setSidebarZoom(_ zoom: CGFloat) {
        recapHost.zoom = zoom
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

    func rowIsCurrent(_ row: WorkspaceWaitingRow) -> Bool {
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

    @objc func actionPressed(_ sender: NSButton) {
        let all = WorkspaceInspectorAction.allCases
        guard all.indices.contains(sender.tag) else { return }
        onAction?(all[sender.tag])
    }
}

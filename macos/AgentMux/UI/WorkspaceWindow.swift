import AppKit
import os

/// One terminal the workspace embeds: the view, and how to let go of it.
/// Closing only detaches; the session lives on in its backend.
struct WorkspaceTerminal {
    let view: NSView
    let close: () -> Void
}

/// Why a terminal could not be shown.
struct WorkspaceTerminalProblem: Error, Equatable {
    let message: String
}

/// What the workspace asks of the app.
@MainActor
protocol WorkspaceDelegate: AnyObject {
    /// The live sessions and the feed that delivers them.
    var store: SessionStore { get }
    /// A terminal attached to a runtime session.
    func makeTerminal(runtimeSessionID: String) -> Result<WorkspaceTerminal, WorkspaceTerminalProblem>
    /// Focuses a session that is a window of another app and brings that app
    /// forward.
    func focusExternally(_ row: WorkspaceRow)
    /// The window was shown or closed.
    func workspaceVisibilityChanged(_ visible: Bool)
}

/// The workspace window: a native sidebar of the live sessions, grouped by
/// project, beside the terminal of the selected one.
///
/// Selection and focus are separate. Selecting a row shows what there is to
/// show for it — a tmux session's embedded terminal, or a note that the
/// session is a Kitty window — and never leaves the app. Focusing a row, by a
/// click or from the menu bar, a shortcut, or a banner, goes where the session
/// is: keyboard focus moves into the embedded terminal, or Kitty comes forward
/// on its window.
///
/// Each embedded session has at most one terminal, made the first time it is
/// selected and kept, hidden, while another is shown. A terminal is let go
/// when its `amx attach` ends, when a live snapshot no longer lists its
/// session, or when the window closes. None of those ends the agent.
@MainActor
final class WorkspaceWindowController: NSObject, NSWindowDelegate, NSTableViewDataSource, NSTableViewDelegate {
    private nonisolated static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "workspace")

    /// One line of the sidebar.
    enum Item: Equatable {
        case group(WorkspaceGroup)
        case row(WorkspaceRow)
    }

    /// What the pane beside the sidebar shows.
    enum Content: Equatable {
        /// Nothing is selected.
        case empty
        case terminal(runtimeSessionID: String)
        /// The selected session is a Kitty window.
        case external(AttentionCycler.Key)
        /// The selected session's terminal ended while its session is still
        /// listed: it was attached elsewhere, or detached with F12.
        case detached(runtimeSessionID: String)
        case unavailable(runtimeSessionID: String, String)
    }

    private weak var delegate: WorkspaceDelegate?
    private(set) var state = WorkspaceState()
    private(set) var items: [Item] = []
    private(set) var content: Content = .empty
    /// Whether the window is on screen; terminals exist only while it is.
    private(set) var isOpen = false

    let sidebar = NSTableView()
    let feedLabel = NSTextField(labelWithString: "")
    let placeholder = WorkspacePlaceholderView()
    private let pane = NSView()
    private(set) lazy var window: NSWindow = makeWindow()

    private var terminals: [String: WorkspaceTerminal] = [:]
    /// Sessions whose terminal ended and is not remade until asked for.
    private var detached: Set<String> = []
    /// Set while the table is brought in line with `state`, so its selection
    /// callbacks are not taken for the operator's.
    private var applying = false

    private let glyphs: Glyphs
    private let now: () -> Date

    init(delegate: WorkspaceDelegate, glyphs: Glyphs = .bundled, now: @escaping () -> Date = Date.init) {
        self.delegate = delegate
        self.glyphs = glyphs
        self.now = now
        super.init()
    }

    /// The runtime sessions that have a terminal right now.
    var attachedRuntimeSessionIDs: Set<String> { Set(terminals.keys) }

    func terminalView(for runtimeSessionID: String) -> NSView? { terminals[runtimeSessionID]?.view }

    // MARK: Showing

    /// Brings the window forward, opening it if it is closed.
    func show() {
        if !isOpen {
            isOpen = true
            Self.logger.notice("workspace: opened")
            delegate?.workspaceVisibilityChanged(true)
        }
        NSApp.activate(ignoringOtherApps: true)
        window.makeKeyAndOrderFront(nil)
        refresh()
    }

    /// Opens the window without activating the app or ordering it in, for
    /// tests: everything else behaves as it does on screen.
    func openForTesting() {
        _ = window
        isOpen = true
        refresh()
    }

    func windowWillClose(_ notification: Notification) {
        guard isOpen else { return }
        isOpen = false
        Self.logger.notice("workspace: closed, detaching \(self.terminals.count) terminal(s)")
        for id in Array(terminals.keys) { discardTerminal(id) }
        detached.removeAll()
        content = .empty
        delegate?.workspaceVisibilityChanged(false)
    }

    // MARK: State

    /// Brings the sidebar and the pane in line with the store.
    func refresh() {
        guard let store = delegate?.store else { return }
        var live: [WorkspaceGroup]?
        if store.feed == .live, let snapshot = store.snapshot {
            live = WorkspaceSidebar.groups(store.sessions, snapshotSocket: snapshot.kitty.socket, now: now(), glyphs: glyphs)
        }
        let wasCurrent = state.current
        let previous = state.selection
        let lost = state.apply(live)
        if state.current != wasCurrent {
            Self.logger.notice("workspace: rows \(self.state.current ? "current" : "held from the last snapshot", privacy: .public) (\(self.state.rows.count) session(s), selection \(Self.describe(self.state.selection), privacy: .public))")
        }
        if lost {
            Self.logger.notice("workspace: selected session \(Self.describe(previous), privacy: .public) is no longer listed")
        }
        if state.current {
            // Only a live snapshot says a session is gone.
            let listed = state.embeddedRuntimeSessionIDs
            for id in terminals.keys where !listed.contains(id) {
                Self.logger.notice("workspace: \(id, privacy: .public) is no longer listed; detaching its terminal")
                discardTerminal(id)
            }
            detached.formIntersection(listed)
        }
        reloadSidebar()
        let notice = WorkspaceSidebar.feedNotice(store.feed)
        feedLabel.stringValue = notice ?? ""
        feedLabel.isHidden = notice == nil
        updateContent(focus: false)
    }

    /// Selects a listed session and shows what there is to show for it. With
    /// `focus`, keyboard focus moves into its terminal when it has one.
    @discardableResult
    func select(_ key: AttentionCycler.Key?, focus: Bool = false) -> Bool {
        guard state.select(key) else { return false }
        syncSidebarSelection()
        updateContent(focus: focus)
        return true
    }

    /// Focuses a row the way a click does: an embedded session takes the
    /// keyboard here, a Kitty session comes forward in Kitty.
    func activate(_ row: WorkspaceRow) {
        switch row.target {
        case let .embedded(id):
            Self.logger.notice("workspace: focus embedded \(id, privacy: .public)")
            select(row.key, focus: true)
        case let .kitty(_, windowID):
            Self.logger.notice("workspace: focus Kitty window \(windowID) externally")
            select(row.key)
            delegate?.focusExternally(row)
        }
    }

    /// Shows the window on an embedded session and gives it the keyboard:
    /// what the menu bar, the next-attention shortcut, and a banner do.
    func focus(runtimeSessionID: String) {
        show()
        guard let row = state.row(.embedded(runtimeSessionID)) else {
            Self.logger.notice("workspace: \(runtimeSessionID, privacy: .public) is not listed; nothing to focus")
            return
        }
        activate(row)
    }

    /// Moves the selection `offset` rows, wrapping. A Kitty row is selected,
    /// not raised: stepping through the sidebar never leaves the app.
    func selectNeighbor(_ offset: Int) {
        guard let row = state.neighbor(offset) else { return }
        select(row.key, focus: row.embeddedRuntimeSessionID != nil)
    }

    /// A terminal's program ended, or it asked to be closed: its attachment
    /// is over. The session is not: it stays listed until the daemon drops it.
    func terminalEnded(_ view: NSView) {
        guard let id = terminals.first(where: { $0.value.view === view })?.key else { return }
        Self.logger.notice("workspace: the terminal for \(id, privacy: .public) ended; detached")
        discardTerminal(id)
        detached.insert(id)
        updateContent(focus: false)
    }

    private func discardTerminal(_ id: String) {
        guard let terminal = terminals.removeValue(forKey: id) else { return }
        if isOpen, let responder = window.firstResponder as? NSView, responder.isDescendant(of: terminal.view) {
            window.makeFirstResponder(sidebar)
        }
        terminal.view.removeFromSuperview()
        terminal.close()
    }

    private static func describe(_ key: AttentionCycler.Key?) -> String {
        guard let key else { return "none" }
        return key.runtimeSessionID.isEmpty ? "kitty window \(key.windowID)" : key.runtimeSessionID
    }

    // MARK: Pane

    private func wantedContent(retry: Bool) -> Content {
        guard let row = state.selectedRow else { return .empty }
        guard let id = row.embeddedRuntimeSessionID else { return .external(row.key) }
        if detached.contains(id) { return .detached(runtimeSessionID: id) }
        if terminals[id] != nil { return .terminal(runtimeSessionID: id) }
        // A terminal that could not be made is not asked for again on every
        // snapshot; something has to have changed first.
        if !retry, case let .unavailable(refused, _) = content, refused == id { return content }
        guard let delegate else { return .unavailable(runtimeSessionID: id, "The app is shutting down.") }
        switch delegate.makeTerminal(runtimeSessionID: id) {
        case let .success(terminal):
            Self.logger.notice("workspace: attached a terminal to \(id, privacy: .public)")
            terminals[id] = terminal
            terminal.view.frame = pane.bounds
            terminal.view.autoresizingMask = [.width, .height]
            terminal.view.isHidden = true
            pane.addSubview(terminal.view, positioned: .below, relativeTo: placeholder)
            return .terminal(runtimeSessionID: id)
        case let .failure(problem):
            return .unavailable(runtimeSessionID: id, problem.message)
        }
    }

    /// Shows the selected session's pane. Terminals exist only while the
    /// window is open, so a closed window shows nothing.
    private func updateContent(focus: Bool, retry: Bool = false) {
        guard isOpen else { return }
        let wanted = wantedContent(retry: retry)
        if wanted != content {
            content = wanted
            Self.logger.notice("workspace: showing \(String(describing: wanted), privacy: .public)")
        }
        var shown: NSView?
        if case let .terminal(id) = wanted { shown = terminals[id]?.view }
        for terminal in terminals.values where terminal.view !== shown { terminal.view.isHidden = true }
        shown?.isHidden = false
        placeholder.isHidden = shown != nil
        if shown == nil { fillPlaceholder(wanted) }
        window.title = state.selectedRow.map { "\($0.project) — \($0.label)" } ?? "Agent Mux"

        if let shown, focus {
            window.makeFirstResponder(shown)
        } else if window.firstResponder === window
                    || (window.firstResponder as? NSView)?.isHiddenOrHasHiddenAncestor == true {
            // Nothing has the keyboard, or a terminal that is no longer
            // shown had it: the sidebar takes it, so the arrow keys work.
            window.makeFirstResponder(sidebar)
        }
    }

    private func fillPlaceholder(_ content: Content) {
        let row = state.selectedRow
        switch content {
        case .empty, .terminal:
            placeholder.show(
                title: state.rows.isEmpty ? "No live sessions" : "No session selected",
                message: state.rows.isEmpty ? "Sessions appear here as they start." : "Choose a session in the sidebar."
            )
        case .external:
            placeholder.show(
                title: row.map { "\($0.project) runs in Kitty" } ?? "This session runs in Kitty",
                message: "Its terminal is a Kitty window. Agent Mux brings Kitty forward on it.",
                button: "Show in Kitty"
            ) { [weak self] in
                guard let self, let row = self.state.selectedRow else { return }
                self.activate(row)
            }
        case let .detached(id):
            placeholder.show(
                title: "Detached",
                message: "This view let go of the session: it was attached somewhere else, or detached with F12. The agent is still running.",
                button: "Reattach"
            ) { [weak self] in self?.reattach(id) }
        case let .unavailable(id, message):
            placeholder.show(title: "No terminal yet", message: message, button: "Try Again") { [weak self] in
                self?.reattach(id)
            }
        }
    }

    private func reattach(_ id: String) {
        detached.remove(id)
        guard state.selection == .embedded(id) else { return }
        updateContent(focus: true, retry: true)
    }

    /// Asks again for a terminal that could not be made, now that what it
    /// was missing (a resolved amx) may be there.
    func retryTerminal() {
        updateContent(focus: false, retry: true)
    }

    // MARK: Sidebar

    private func reloadSidebar() {
        let wanted = state.groups.flatMap { [Item.group($0)] + $0.rows.map(Item.row) }
        let dimmed: CGFloat = state.current ? 1 : 0.5
        if wanted != items || sidebar.alphaValue != dimmed {
            items = wanted
            sidebar.alphaValue = dimmed
            applying = true
            sidebar.reloadData()
            applying = false
        }
        syncSidebarSelection()
    }

    private func syncSidebarSelection() {
        let index = items.firstIndex { item in
            if case let .row(row) = item { return row.key == state.selection }
            return false
        }
        let wanted = index.map { IndexSet(integer: $0) } ?? IndexSet()
        guard sidebar.selectedRowIndexes != wanted else { return }
        applying = true
        sidebar.selectRowIndexes(wanted, byExtendingSelection: false)
        applying = false
    }

    func numberOfRows(in tableView: NSTableView) -> Int { items.count }

    func tableView(_ tableView: NSTableView, isGroupRow row: Int) -> Bool {
        if case .group = items[row] { return true }
        return false
    }

    func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool {
        if case .row = items[row] { return true }
        return false
    }

    func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
        if case .group = items[row] { return 24 }
        return 38
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        switch items[row] {
        case let .group(group):
            let cell = tableView.makeView(withIdentifier: WorkspaceGroupCellView.identifier, owner: nil) as? WorkspaceGroupCellView
                ?? WorkspaceGroupCellView()
            cell.show(group)
            return cell
        case let .row(row):
            let cell = tableView.makeView(withIdentifier: WorkspaceRowCellView.identifier, owner: nil) as? WorkspaceRowCellView
                ?? WorkspaceRowCellView()
            cell.show(row, muteGlyph: glyphs.mute)
            return cell
        }
    }

    /// The arrow keys moved the selection: show it, and stay in the sidebar.
    func tableViewSelectionDidChange(_ notification: Notification) {
        guard !applying else { return }
        let index = sidebar.selectedRow
        guard index >= 0, index < items.count, case let .row(row) = items[index] else {
            select(nil)
            return
        }
        select(row.key)
    }

    /// A click on a row focuses it.
    @objc private func rowClicked(_ sender: Any?) {
        let index = sidebar.clickedRow
        guard index >= 0, index < items.count, case let .row(row) = items[index] else { return }
        activate(row)
    }

    // MARK: Window

    static let minimumSize = NSSize(width: 640, height: 360)

    private func makeWindow() -> NSWindow {
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("session"))
        column.resizingMask = .autoresizingMask
        sidebar.addTableColumn(column)
        sidebar.headerView = nil
        sidebar.style = .sourceList
        sidebar.floatsGroupRows = false
        sidebar.allowsEmptySelection = true
        sidebar.allowsMultipleSelection = false
        sidebar.dataSource = self
        sidebar.delegate = self
        sidebar.target = self
        sidebar.action = #selector(rowClicked(_:))
        sidebar.setAccessibilityLabel("Sessions")

        let scroll = NSScrollView()
        scroll.documentView = sidebar
        scroll.hasVerticalScroller = true
        scroll.drawsBackground = false
        scroll.translatesAutoresizingMaskIntoConstraints = false

        feedLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        feedLabel.textColor = .secondaryLabelColor
        feedLabel.lineBreakMode = .byTruncatingTail
        feedLabel.isHidden = true
        feedLabel.translatesAutoresizingMaskIntoConstraints = false

        let stack = NSStackView(views: [scroll, feedLabel])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 4
        stack.edgeInsets = NSEdgeInsets(top: 0, left: 0, bottom: 6, right: 0)
        let sidebarController = NSViewController()
        sidebarController.view = stack
        NSLayoutConstraint.activate([
            scroll.widthAnchor.constraint(equalTo: stack.widthAnchor),
            feedLabel.leadingAnchor.constraint(equalTo: stack.leadingAnchor, constant: 10),
            feedLabel.trailingAnchor.constraint(lessThanOrEqualTo: stack.trailingAnchor, constant: -10),
        ])

        placeholder.frame = pane.bounds
        placeholder.autoresizingMask = [.width, .height]
        pane.addSubview(placeholder)
        let paneController = NSViewController()
        paneController.view = pane

        let split = NSSplitViewController()
        let sidebarItem = NSSplitViewItem(sidebarWithViewController: sidebarController)
        sidebarItem.minimumThickness = 180
        sidebarItem.maximumThickness = 420
        sidebarItem.canCollapse = false
        let paneItem = NSSplitViewItem(viewController: paneController)
        paneItem.minimumThickness = 320
        split.addSplitViewItem(sidebarItem)
        split.addSplitViewItem(paneItem)

        let window = NSWindow(contentViewController: split)
        window.title = "Agent Mux"
        window.styleMask = [.titled, .closable, .miniaturizable, .resizable]
        window.isReleasedWhenClosed = false
        window.minSize = Self.minimumSize
        window.setContentSize(NSSize(width: 1100, height: 700))
        window.delegate = self
        window.center()
        if !AppDelegate.isRunningTests { window.setFrameAutosaveName("AgentMuxWorkspace") }
        split.splitView.setPosition(240, ofDividerAt: 0)
        return window
    }
}

extension WorkspaceWindowController: GhosttySurfaceDelegate {
    nonisolated func surfaceChildDidExit(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalEnded(surface) }
    }

    nonisolated func surfaceDidRequestClose(_ surface: Ghostty.SurfaceView, needsConfirm: Bool) {
        MainActor.assumeIsolated { terminalEnded(surface) }
    }
}

/// What the pane shows when it has no terminal to show: a title, a line of
/// explanation, and at most one thing to do about it.
@MainActor
final class WorkspacePlaceholderView: NSView {
    let titleLabel = NSTextField(labelWithString: "")
    let messageLabel = NSTextField(wrappingLabelWithString: "")
    let button = NSButton(title: "", target: nil, action: nil)
    private var onPress: (() -> Void)?

    init() {
        super.init(frame: .zero)
        titleLabel.font = .systemFont(ofSize: 17, weight: .semibold)
        messageLabel.textColor = .secondaryLabelColor
        messageLabel.alignment = .center
        button.bezelStyle = .rounded
        button.target = self
        button.action = #selector(pressed(_:))
        let stack = NSStackView(views: [titleLabel, messageLabel, button])
        stack.orientation = .vertical
        stack.spacing = 8
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerXAnchor.constraint(equalTo: centerXAnchor),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
            stack.widthAnchor.constraint(lessThanOrEqualToConstant: 420),
            stack.leadingAnchor.constraint(greaterThanOrEqualTo: leadingAnchor, constant: 20),
        ])
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    func show(title: String, message: String, button label: String? = nil, onPress: (() -> Void)? = nil) {
        titleLabel.stringValue = title
        messageLabel.stringValue = message
        button.title = label ?? ""
        button.isHidden = label == nil
        self.onPress = onPress
    }

    @objc private func pressed(_ sender: Any?) { onPress?() }

    /// Presses the button, as a click does.
    func press() { onPress?() }
}

/// A project's header line: its name, and how many of its sessions wait.
@MainActor
final class WorkspaceGroupCellView: NSTableCellView {
    static let identifier = NSUserInterfaceItemIdentifier("workspace.group")
    let nameLabel = NSTextField(labelWithString: "")
    let countLabel = NSTextField(labelWithString: "")

    init() {
        super.init(frame: .zero)
        identifier = Self.identifier
        nameLabel.lineBreakMode = .byTruncatingTail
        nameLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        countLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize, weight: .semibold)
        countLabel.textColor = .systemOrange
        textField = nameLabel
        let stack = NSStackView(views: [nameLabel, NSView(), countLabel])
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

    func show(_ group: WorkspaceGroup) {
        nameLabel.stringValue = group.project
        let waiting = group.attention
        countLabel.stringValue = waiting > 0 ? String(waiting) : ""
        countLabel.toolTip = waiting > 0 ? "\(waiting) waiting on you" : nil
        setAccessibilityLabel(waiting > 0 ? "\(group.project), \(waiting) waiting" : group.project)
    }
}

/// A session's line: its state glyph, its profile, and a line of detail.
@MainActor
final class WorkspaceRowCellView: NSTableCellView {
    static let identifier = NSUserInterfaceItemIdentifier("workspace.row")
    let glyphLabel = NSTextField(labelWithString: "")
    let titleLabel = NSTextField(labelWithString: "")
    let detailLabel = NSTextField(labelWithString: "")

    init() {
        super.init(frame: .zero)
        identifier = Self.identifier
        glyphLabel.alignment = .center
        titleLabel.lineBreakMode = .byTruncatingTail
        detailLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        detailLabel.textColor = .secondaryLabelColor
        detailLabel.lineBreakMode = .byTruncatingTail
        textField = titleLabel
        let text = NSStackView(views: [titleLabel, detailLabel])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 1
        let stack = NSStackView(views: [glyphLabel, text])
        stack.orientation = .horizontal
        stack.spacing = 6
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            glyphLabel.widthAnchor.constraint(equalToConstant: 20),
            stack.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2),
            stack.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -4),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    func show(_ row: WorkspaceRow, muteGlyph: String) {
        glyphLabel.stringValue = row.glyph
        titleLabel.stringValue = row.muted ? "\(row.label) \(muteGlyph)" : row.label
        titleLabel.font = .systemFont(ofSize: NSFont.systemFontSize, weight: row.attention ? .semibold : .regular)
        detailLabel.stringValue = row.detail
        toolTip = row.toolTip
        var spoken = [row.project, row.label]
        if row.attention { spoken.append("needs attention") } else if row.working { spoken.append("working") }
        if !row.detail.isEmpty { spoken.append(row.detail) }
        setAccessibilityLabel(spoken.joined(separator: ", "))
    }
}

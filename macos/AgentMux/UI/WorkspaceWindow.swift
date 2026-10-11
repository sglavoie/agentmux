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
    /// A terminal running a utility view: never an attachment, never a
    /// session.
    func makeUtilityTerminal(_ view: WorkspaceUtilityView) -> Result<WorkspaceTerminal, WorkspaceTerminalProblem>
    /// Focuses a session that is a window of another app and brings that app
    /// forward.
    func focusExternally(_ row: WorkspaceRow)
    /// The window was shown or closed.
    func workspaceVisibilityChanged(_ visible: Bool)
    /// The pane moved to another session or view, or to none.
    func workspacePlaceChanged(_ place: WorkspaceRestoration.Place?)
    /// The sidebar was hidden or shown.
    func workspaceSidebarCollapsedChanged(_ collapsed: Bool)
    /// A project's sessions were dragged into another order.
    func workspaceOrderChanged(_ order: WorkspaceOrder)
    /// Asks whether a session's agent is running and whether a terminal is
    /// attached to it. It only asks: nothing is attached and nothing started.
    /// The answer arrives on the main actor, possibly before this returns.
    func sessionStatus(runtimeSessionID: String, completion: @escaping @MainActor (WorkspaceSessionStatus) -> Void)
    /// Ends a session's agent and the tmux session that holds it. The answer
    /// is nil once it is closed, or why it could not be, and arrives on the
    /// main actor, possibly before this returns.
    func closeSession(runtimeSessionID: String, completion: @escaping @MainActor (String?) -> Void)
    /// Gives a session a name, or with an empty one removes the name it has.
    /// The answer is nil once it is written, or why it could not be, and
    /// arrives on the main actor, possibly before this returns.
    func nameSession(runtimeSessionID: String, name: String, completion: @escaping @MainActor (String?) -> Void)
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
///
/// Only the × on an embedded session's row ends an agent, and it asks first
/// when that agent is working or waiting on the operator. The × on an opened
/// view closes that view and ends its program. A Kitty session and the
/// Dashboard and Overview have none.
///
/// The workspace only ever shows agents that are running. It comes back to
/// where it was after a crash or a restart (`restore(_:)`), keeps its rows and
/// terminals while the daemon is away, and when a session it was on is gone
/// it says whether the agent exited or was lost. It never starts an agent to
/// fill the gap, and restoring never takes a session from a terminal that
/// attached to it meanwhile.
///
/// Above the projects the sidebar lists the workspace's own views: the
/// Dashboard and the Overview, which are the existing TUI in a terminal of
/// their own, and any shell, editor, or pager they asked for. They are
/// utility views, not sessions: they carry no session identity, are never
/// counted as waiting, and are skipped by the session shortcuts. A utility
/// view's program belongs to its view, so unlike an attachment it keeps
/// running while the window is closed and ends only when it exits, when its
/// view is closed, or when the app quits.
///
/// A terminal opened beside a session is such a view too, listed under that
/// session's row instead of above the projects. It can be clicked and reached
/// with the arrow keys or by stepping to the next or previous session; the
/// digits count sessions only.
///
/// A session's row can be dragged to another place among its project's
/// sessions, and takes its terminals with it; a terminal can be dragged among
/// the ones beside its session. Nothing is dragged out of its project or its
/// session, and the Workspace section stays as it is.
@MainActor
final class WorkspaceWindowController: NSObject, NSWindowDelegate, NSTableViewDataSource, NSTableViewDelegate, NSMenuDelegate {
    private nonisolated static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "workspace")

    /// One line of the sidebar.
    enum Item: Equatable {
        /// The heading over the utility views.
        case section(String)
        case view(WorkspaceUtilityView)
        case group(WorkspaceGroup)
        case row(WorkspaceRow)
    }

    /// The heading the utility views are listed under.
    static let viewsSection = "Workspace"
    /// How long a just-launched session is waited for before the request to
    /// show it is forgotten.
    static let awaitLimit: TimeInterval = 60

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
        /// The session is being closed: its agent was asked to end.
        case closing(runtimeSessionID: String)
        /// The session the workspace is coming back to is not known to be
        /// running yet: the daemon has not said, or amx is being asked.
        case restoring(runtimeSessionID: String)
        /// The session the pane was on is no longer running. The status says
        /// whether its agent exited or was lost; nil while that is asked.
        case gone(runtimeSessionID: String, WorkspaceSessionStatus?)
        /// A utility view's terminal.
        case view(id: String)
        /// The Dashboard or the Overview after its program exited.
        case viewEnded(id: String)
        case viewUnavailable(id: String, String)
    }

    /// Something the pane can be on: a session or a utility view.
    private enum Place: Equatable {
        case session(AttentionCycler.Key)
        case view(String)
    }

    private weak var delegate: WorkspaceDelegate?
    private(set) var state = WorkspaceState()
    /// The order sessions were dragged into; the app keeps it across
    /// launches.
    var order = WorkspaceOrder()
    /// The line being dragged, from when the drag begins until it ends.
    private var dragged: Place?
    private(set) var items: [Item] = []
    private(set) var content: Content = .empty
    /// Whether the window is on screen; terminals exist only while it is.
    private(set) var isOpen = false

    let sidebar = WorkspaceSidebarView()
    /// Hides the sidebar and brings it back, from the title bar, so that
    /// neither the sidebar nor the pane gives up anything to it.
    let sidebarToggle = WorkspaceWindowController.makeSidebarToggle()
    /// What the delegate was last told, so it hears of each change once.
    private var reportedCollapsed = false
    private var sidebarItem: NSSplitViewItem?
    private var sidebarObservation: NSKeyValueObservation?
    let feedLabel = NSTextField(labelWithString: "")
    let placeholder = WorkspacePlaceholderView()
    private let pane = NSView()
    private(set) lazy var window: NSWindow = makeWindow()

    private var terminals: [String: WorkspaceTerminal] = [:]
    /// Sessions whose terminal ended and is not remade until asked for.
    private var detached: Set<String> = []

    /// Sessions amx was asked to close, until it has answered and a live
    /// snapshot no longer lists them: whether the pane was on each when it
    /// was closed, and whether amx has answered.
    private var closing: [String: (wasShown: Bool, answered: Bool)] = [:]
    /// Stands in for the sheet that asks before a session that is working or
    /// waiting is closed, for tests. Answers on the main actor.
    var confirmClose: ((_ row: WorkspaceRow, _ answer: @escaping @MainActor (Bool) -> Void) -> Void)?
    /// Stands in for the sheet that says why a session could not be closed.
    var reportCloseProblem: ((_ name: String, _ message: String) -> Void)?
    /// Stands in for the sheet that asks for a session's name, for tests.
    /// Answers on the main actor: the name, or nil when it was cancelled.
    var askName: ((_ row: WorkspaceRow, _ answer: @escaping @MainActor (String?) -> Void) -> Void)?
    /// Stands in for the sheet that says why a session could not be named.
    var reportNameProblem: ((_ name: String, _ message: String) -> Void)?

    /// The utility views listed above the projects.
    private(set) var views = WorkspaceViews()
    /// The utility view the pane shows, by `WorkspaceUtilityView.id`. It and
    /// `state.selection` are never both set.
    private(set) var viewSelection: String?
    private var viewTerminals: [String: WorkspaceTerminal] = [:]
    /// The Dashboard or Overview whose program exited; remade when asked for.
    private var endedViews: Set<String> = []
    /// Where the pane was when an opened view took it, to go back to when
    /// that view ends.
    private var returnPlaces: [String: Place] = [:]
    /// Where the pane is and where it was before that, which ⌘L goes between.
    /// An overlay view is neither: it is passed through.
    private var recent: (here: Place?, before: Place?)
    /// A session that was just launched and is shown once a snapshot lists
    /// it, unless the operator goes somewhere else first.
    private(set) var awaited: (runtimeSessionID: String, until: Date)?
    /// Set while the table is brought in line with `state`, so its selection
    /// callbacks are not taken for the operator's.
    private var applying = false
    /// The place the workspace is coming back to after a launch, until a live
    /// snapshot and amx have said what is there, or the operator goes
    /// somewhere else.
    private(set) var restoring: WorkspaceRestoration.Place?
    /// The session the pane was on when it stopped running, and what became
    /// of it once amx has answered.
    private(set) var gone: (runtimeSessionID: String, name: String, status: WorkspaceSessionStatus?)?
    /// Counts status questions, so an answer to one that was overtaken by
    /// the operator, or by a newer question, is dropped.
    private var statusQuestion = 0
    /// True while amx is being asked about the session being restored.
    private var restoreChecking = false
    private var reportedPlace: WorkspaceRestoration.Place?

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

    /// The utility views that have a terminal right now, by id.
    var runningViewIDs: Set<String> { Set(viewTerminals.keys) }

    func utilityTerminalView(for id: String) -> NSView? { viewTerminals[id]?.view }

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
        // The utility views keep their programs: an editor with unsaved work
        // or a running shell is not an attachment that can simply be remade.
        for terminal in viewTerminals.values { terminal.view.isHidden = true }
        awaited = nil
        leaveRecovery()
        content = .empty
        delegate?.workspaceVisibilityChanged(false)
    }

    // MARK: State

    /// Brings the sidebar and the pane in line with the store.
    func refresh() {
        guard let store = delegate?.store else { return }
        var live: [WorkspaceGroup]?
        if store.feed == .live, let snapshot = store.snapshot {
            live = WorkspaceSidebar.groups(store.sessions, snapshotSocket: snapshot.kitty.socket, now: now(), glyphs: glyphs, order: order)
        }
        let wasCurrent = state.current
        let previous = state.selection
        let previousName = state.selectedRow.map { "\($0.project) — \($0.label)" }
        let lost = state.apply(live)
        if state.current != wasCurrent {
            Self.logger.notice("workspace: rows \(self.state.current ? "current" : "held from the last snapshot", privacy: .public) (\(self.state.rows.count) session(s), selection \(Self.describe(self.state.selection), privacy: .public))")
        }
        if lost {
            Self.logger.notice("workspace: selected session \(Self.describe(previous), privacy: .public) is no longer listed")
            // A session the operator closed is not reported as gone.
            if let previous, !previous.runtimeSessionID.isEmpty, closing[previous.runtimeSessionID] == nil {
                sessionGone(previous.runtimeSessionID, name: previousName ?? "This session")
            }
        }
        if state.current {
            // Only a live snapshot says a session is gone.
            let listed = state.embeddedRuntimeSessionIDs
            for id in terminals.keys where !listed.contains(id) {
                Self.logger.notice("workspace: \(id, privacy: .public) is no longer listed; detaching its terminal")
                discardTerminal(id)
            }
            detached.formIntersection(listed)
            closing = closing.filter { !$0.value.answered || listed.contains($0.key) }
        }
        reloadSidebar()
        let notice = WorkspaceSidebar.feedNotice(store.feed)
        feedLabel.stringValue = notice ?? ""
        feedLabel.isHidden = notice == nil
        if let awaited {
            if let row = state.row(.embedded(awaited.runtimeSessionID)) {
                Self.logger.notice("workspace: awaited session \(awaited.runtimeSessionID, privacy: .public) is listed; showing it")
                self.awaited = nil
                activate(row)
                return
            }
            if now() >= awaited.until {
                Self.logger.notice("workspace: awaited session \(awaited.runtimeSessionID, privacy: .public) was never listed; giving up")
                self.awaited = nil
            }
        }
        continueRestoring()
        updateContent(focus: false)
    }

    // MARK: Recovery

    /// Comes back to where the workspace was when the app last ran. A view is
    /// shown at once. A session is shown once a live snapshot lists it and
    /// amx confirms nobody else attached to it meanwhile; until then the pane
    /// says it is waiting. A session that is not running any more is reported
    /// as exited or lost. Nothing is ever started.
    func restore(_ place: WorkspaceRestoration.Place) {
        Self.logger.notice("workspace: restoring \(place.encoded, privacy: .public)")
        switch place {
        case let .view(id):
            if !showView(id, focus: true) { showView(WorkspaceUtilityView.dashboard.id) }
        case .session, .kitty:
            state.select(nil)
            viewSelection = nil
            gone = nil
            restoring = place
            reportedPlace = place
            syncSidebarSelection()
            continueRestoring()
            updateContent(focus: false)
        }
    }

    /// Takes the next step of a restore once the feed is live.
    private func continueRestoring() {
        guard let place = restoring, !restoreChecking, state.current, let key = place.sessionKey else { return }
        guard case let .session(id) = place else {
            // A Kitty session is selected, never raised: coming back must not
            // pull another app forward.
            restoring = nil
            if state.row(key) != nil {
                Self.logger.notice("workspace: restored the selection of Kitty window \(key.windowID)")
                select(key)
            } else {
                Self.logger.notice("workspace: Kitty window \(key.windowID) is no longer listed; nothing restored")
                reportPlace()
            }
            return
        }
        let listed = state.row(key) != nil
        let name = state.row(key).map { "\($0.project) — \($0.label)" } ?? "The session this window was on"
        restoring = nil
        if !listed {
            Self.logger.notice("workspace: \(id, privacy: .public) is not listed after the restart; asking what became of it")
            sessionGone(id, name: name)
            return
        }
        // Listed, so running. Whether this window may take it is the server's
        // to say: a terminal that attached while the app was away owns it now.
        statusQuestion += 1
        let question = statusQuestion
        restoring = place
        restoreChecking = true
        delegate?.sessionStatus(runtimeSessionID: id) { [weak self] status in
            guard let self, question == self.statusQuestion, self.restoring == place else { return }
            self.restoring = nil
            self.restoreChecking = false
            guard self.state.row(key) != nil else {
                self.sessionGone(id, name: name)
                self.updateContent(focus: false)
                return
            }
            switch status {
            case let .live(clients) where clients > 0:
                Self.logger.notice("workspace: \(id, privacy: .public) is attached elsewhere (\(clients) terminal(s)); restored detached")
                self.detached.insert(id)
                self.select(key)
            case .live:
                Self.logger.notice("workspace: restored \(id, privacy: .public); reattaching its terminal")
                self.select(key, focus: true)
            case .exited, .lost, .ended:
                self.sessionGone(id, name: name, known: status)
                self.updateContent(focus: false)
            case let .unknown(why):
                // amx could not say. The session is listed, so it is shown,
                // but its terminal waits to be asked for.
                Self.logger.notice("workspace: could not check \(id, privacy: .public) (\(why, privacy: .public)); restored detached")
                self.detached.insert(id)
                self.select(key)
            }
        }
    }

    /// The session the pane was on stopped being listed: asks what became of
    /// it and says so in the pane, until the operator goes somewhere else.
    private func sessionGone(_ id: String, name: String, known: WorkspaceSessionStatus? = nil) {
        guard isOpen else { return }
        discardTerminal(id)
        detached.remove(id)
        statusQuestion += 1
        let question = statusQuestion
        gone = (id, name, known)
        if let known {
            Self.logger.notice("workspace: \(id, privacy: .public) is not running: \(String(describing: known), privacy: .public); nothing is relaunched")
            reportPlace()
            return
        }
        delegate?.sessionStatus(runtimeSessionID: id) { [weak self] status in
            guard let self, question == self.statusQuestion, self.gone?.runtimeSessionID == id else { return }
            Self.logger.notice("workspace: \(id, privacy: .public) is not running: \(String(describing: status), privacy: .public); nothing is relaunched")
            self.gone?.status = status
            self.updateContent(focus: false)
        }
        reportPlace()
    }

    /// The operator went somewhere: whatever the pane was waiting to say or
    /// to come back to is over.
    private func leaveRecovery() {
        statusQuestion += 1
        restoring = nil
        restoreChecking = false
        gone = nil
    }

    /// Tells the app where the pane is, when that changed. While a restore
    /// is pending the place it is heading for stays the reported one.
    private func reportPlace() {
        guard isOpen else { return }
        let now: WorkspaceRestoration.Place?
        if let restoring {
            now = restoring
        } else if let viewSelection {
            now = views.view(viewSelection)?.isPermanent == true ? .view(id: viewSelection) : reportedPlace
        } else if let key = state.selection {
            now = key.runtimeSessionID.isEmpty
                ? .kitty(socket: key.socket, windowID: key.windowID)
                : .session(runtimeSessionID: key.runtimeSessionID)
        } else {
            now = nil
        }
        guard now != reportedPlace else { return }
        reportedPlace = now
        delegate?.workspacePlaceChanged(now)
    }

    /// Selects a listed session and shows what there is to show for it. With
    /// `focus`, keyboard focus moves into its terminal when it has one.
    @discardableResult
    func select(_ key: AttentionCycler.Key?, focus: Bool = false) -> Bool {
        guard state.select(key) else { return false }
        viewSelection = nil
        awaited = nil
        leaveRecovery()
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

    // MARK: Utility views

    /// Shows a utility view in the pane, starting its program if it is not
    /// running. With `focus`, its terminal takes the keyboard.
    @discardableResult
    func showView(_ id: String, focus: Bool = true) -> Bool {
        guard views.view(id) != nil else { return false }
        state.select(nil)
        viewSelection = id
        awaited = nil
        leaveRecovery()
        syncSidebarSelection()
        updateContent(focus: focus)
        return true
    }

    /// Does what a view request forwarded by the daemon asks: shows a
    /// session's native view, or opens and shows a utility view. A request
    /// this workspace cannot act on is logged and dropped.
    func open(_ request: WorkspaceViewRequest) {
        if request.kind == WorkspaceViewRequest.sessionKind {
            let id = request.runtimeSessionID
            if let row = state.row(.embedded(id)) {
                Self.logger.notice("workspace: view request for session \(id, privacy: .public)")
                activate(row)
            } else if request.awaited, WorkspaceUtilityView.isRuntimeSessionID(id) {
                Self.logger.notice("workspace: awaiting just-launched session \(id, privacy: .public)")
                awaited = (id, now().addingTimeInterval(Self.awaitLimit))
            } else {
                Self.logger.notice("workspace: view request for \(id, privacy: .public), which is not listed; ignored")
            }
            return
        }
        guard let wanted = WorkspaceUtilityView.from(request) else {
            Self.logger.notice("workspace: view request of kind \(request.kind, privacy: .public) cannot be shown; ignored")
            return
        }
        open(wanted)
    }

    /// Lists and shows a utility view. A pager is read afresh every time it
    /// is asked for; any other view that is already open is shown as it is.
    func open(_ wanted: WorkspaceUtilityView) {
        let here = place
        let view = views.open(wanted)
        if view.kind == .pager { discardViewTerminal(view.id) }
        if !view.isPermanent, let here, here != .view(view.id) { returnPlaces[view.id] = here }
        Self.logger.notice("workspace: opening \(view.kind.rawValue, privacy: .public) view \(view.id, privacy: .public)")
        reloadSidebar()
        showView(view.id, focus: true)
    }

    /// Closes the utility view the pane shows, ending its program. An opened
    /// view is unlisted; the Dashboard and the Overview stay listed and start
    /// again the next time they are shown. Reports whether the pane was on
    /// a view.
    @discardableResult
    func closeSelectedView() -> Bool {
        guard let id = viewSelection else { return false }
        closeView(id)
        return true
    }

    /// ⌘W: closes what the pane is on, as the × on its row would. A view is
    /// closed; an embedded session's agent is ended, after the same sheet
    /// when it is working or waiting. Reports whether there was anything to
    /// close: a Kitty session is not the workspace's.
    @discardableResult
    func closeShown() -> Bool {
        if closeSelectedView() { return true }
        guard let row = state.selectedRow, row.embeddedRuntimeSessionID != nil else { return false }
        closeSession(row.key)
        return true
    }

    /// Closes a utility view, ending its program, whether or not the pane
    /// shows it.
    func closeView(_ id: String) {
        guard let view = views.view(id) else { return }
        Self.logger.notice("workspace: closing view \(id, privacy: .public)")
        discardViewTerminal(id)
        endedViews.remove(id)
        if !view.isPermanent {
            retire(view)
        } else if viewSelection == id {
            viewSelection = nil
            syncSidebarSelection()
            updateContent(focus: false)
        }
    }

    /// Ends an embedded session's agent, asking first when it is working or
    /// waiting on the operator. The row stays until a live snapshot drops it;
    /// if the pane was on the session it goes to the Dashboard.
    func closeSession(_ key: AttentionCycler.Key) {
        guard let row = state.row(key), let id = row.embeddedRuntimeSessionID, closing[id] == nil else { return }
        guard row.attention || row.working else {
            endSession(row, id)
            return
        }
        (confirmClose ?? askToClose)(row) { [weak self] confirmed in
            guard let self, confirmed, self.closing[id] == nil, let row = self.state.row(key) else { return }
            self.endSession(row, id)
        }
    }

    private func askToClose(_ row: WorkspaceRow, _ answer: @escaping @MainActor (Bool) -> Void) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "End \(row.project) — \(row.label)?"
        alert.informativeText = (row.attention ? "Its agent is waiting on you." : "Its agent is working.")
            + " Ending it loses the work in progress. The conversation stays on disk and can be resumed from the Dashboard."
        alert.addButton(withTitle: "End Session").hasDestructiveAction = true
        alert.addButton(withTitle: "Cancel")
        alert.beginSheetModal(for: window) { response in
            MainActor.assumeIsolated { answer(response == .alertFirstButtonReturn) }
        }
    }

    private func sayCloseFailed(_ name: String, _ message: String) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "\(name) could not be closed"
        alert.informativeText = message
        alert.beginSheetModal(for: window)
    }

    private func endSession(_ row: WorkspaceRow, _ id: String) {
        let name = "\(row.project) — \(row.label)"
        Self.logger.notice("workspace: closing session \(id, privacy: .public)")
        closing[id] = (place == .session(row.key), false)
        // Its terminal goes first: the attachment ending with the agent is
        // not a detach to report.
        discardTerminal(id)
        detached.remove(id)
        reloadSidebar(force: true)
        updateContent(focus: false)
        delegate?.closeSession(runtimeSessionID: id) { [weak self] problem in
            guard let self, let wasShown = self.closing[id]?.wasShown else { return }
            if let problem {
                Self.logger.notice("workspace: could not close \(id, privacy: .public): \(problem, privacy: .public)")
                self.closing[id] = nil
                self.reloadSidebar(force: true)
                self.updateContent(focus: false, retry: true)
                (self.reportCloseProblem ?? self.sayCloseFailed)(name, problem)
                return
            }
            Self.logger.notice("workspace: closed session \(id, privacy: .public)")
            if self.state.current, !self.state.embeddedRuntimeSessionIDs.contains(id) {
                self.closing[id] = nil
            } else {
                self.closing[id]?.answered = true
            }
            // Still where the operator left it: on the row, or on nothing
            // because a snapshot already dropped the row.
            let stayed = self.place == .session(row.key) || (self.place == nil && self.restoring == nil)
            if wasShown, self.isOpen, stayed {
                self.showView(WorkspaceUtilityView.dashboard.id, focus: true)
            }
        }
    }

    /// Opens a shell where the session the pane is on is working, listed
    /// right below that session, and gives it the keyboard. From one of a
    /// session's terminals it opens another beside the same session. A Kitty
    /// session has its terminals in Kitty, so there is nothing to do for one.
    func openTerminalBesideSession() {
        let id = views.view(viewSelection)?.besideRuntimeSessionID ?? state.selectedRow?.embeddedRuntimeSessionID
        guard let id, state.row(.embedded(id)) != nil else {
            Self.logger.notice("workspace: no embedded session is shown; no terminal opened beside one")
            return
        }
        open(views.openTerminal(beside: id))
    }

    /// The session the pane is on, or the one the terminal, pager, or picker
    /// it shows is about: what the session function keys act on.
    var shownSessionRow: WorkspaceRow? {
        if let view = views.view(viewSelection) {
            return view.aboutRuntimeSessionID.flatMap { state.row(.embedded($0)) }
        }
        return state.selectedRow
    }

    // MARK: Naming

    static let nameSessionTitle = "Name Session…"
    static let namingNeedsEmbedded = "Only a session the workspace embeds can be named here; a Kitty session is named by its tab, in Kitty."

    /// ⌘R: asks for a name for the embedded session the pane is on, and
    /// reports whether there was one.
    @discardableResult
    func nameShownSession() -> Bool {
        guard let row = shownSessionRow, row.embeddedRuntimeSessionID != nil else {
            Self.logger.notice("workspace: no embedded session is shown; nothing to name")
            return false
        }
        nameSession(row.key)
        return true
    }

    /// Asks for a session's name and has amx write it. An empty answer
    /// removes the name; a cancelled one changes nothing. The line shows the
    /// change once a snapshot carries it.
    func nameSession(_ key: AttentionCycler.Key) {
        guard let row = state.row(key), let id = row.embeddedRuntimeSessionID else { return }
        (askName ?? askForName)(row) { [weak self] answer in
            guard let self, let answer else { return }
            let name = answer.trimmingCharacters(in: .whitespacesAndNewlines)
            guard name != row.name else { return }
            Self.logger.notice("workspace: naming session \(id, privacy: .public)")
            self.delegate?.nameSession(runtimeSessionID: id, name: name) { [weak self] problem in
                guard let self, let problem else { return }
                let title = "\(row.project) — \(row.label)"
                if let report = self.reportNameProblem {
                    report(title, problem)
                } else if !AppDelegate.isRunningTests {
                    let alert = NSAlert()
                    alert.alertStyle = .warning
                    alert.messageText = "\(title) could not be named"
                    alert.informativeText = problem
                    alert.beginSheetModal(for: self.window)
                }
            }
        }
    }

    private func askForName(_ row: WorkspaceRow, _ answer: @escaping @MainActor (String?) -> Void) {
        let field = NSTextField(string: row.name)
        field.placeholderString = "No name"
        field.frame = NSRect(x: 0, y: 0, width: 260, height: 24)
        let alert = NSAlert()
        alert.messageText = "Name \(row.project) — \(row.label)"
        alert.informativeText = "The name is shown above the session in the sidebar. Leave it empty to remove it."
        alert.accessoryView = field
        alert.addButton(withTitle: "Name")
        alert.addButton(withTitle: "Cancel")
        alert.window.initialFirstResponder = field
        alert.beginSheetModal(for: window) { response in
            MainActor.assumeIsolated { answer(response == .alertFirstButtonReturn ? field.stringValue : nil) }
        }
    }

    /// The menu a right-click on the session list shows: what can be done to
    /// the line under the pointer. Only a session's line has anything.
    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        for item in contextMenuItems(forRow: sidebar.clickedRow) { menu.addItem(item) }
    }

    func contextMenuItems(forRow index: Int) -> [NSMenuItem] {
        guard index >= 0, index < items.count, case let .row(row) = items[index] else { return [] }
        let name = NSMenuItem(title: Self.nameSessionTitle, action: #selector(nameClickedSession(_:)), keyEquivalent: "")
        name.representedObject = row.key
        if row.embeddedRuntimeSessionID != nil {
            name.target = self
        } else {
            // No target and no action: the item is listed, greyed out.
            name.action = nil
            name.toolTip = Self.namingNeedsEmbedded
        }
        return [name]
    }

    @objc private func nameClickedSession(_ sender: Any?) {
        guard let key = (sender as? NSMenuItem)?.representedObject as? AttentionCycler.Key else { return }
        nameSession(key)
    }

    /// F5 and F6: opens the icon or color picker for the embedded session the
    /// pane is on, and reports whether there was one. A Kitty session has its
    /// pickers in Kitty.
    @discardableResult
    func openPicker(_ kind: WorkspaceUtilityView.Kind) -> Bool {
        guard let id = shownSessionRow?.embeddedRuntimeSessionID,
              let picker = WorkspaceUtilityView.picker(kind, runtimeSessionID: id) else {
            Self.logger.notice("workspace: no embedded session is shown; no picker opened")
            return false
        }
        open(picker)
        return true
    }

    /// Notes where the pane is, keeping where it was before.
    private func notePlace() {
        guard let here = place, here != recent.here else { return }
        if case let .view(id) = here, views.view(id)?.isOverlay == true { return }
        recent = (here, recent.here)
    }

    /// ⌘L: goes back to where the pane was before it came here, so that
    /// pressing it again returns. Reports whether that place is still there.
    @discardableResult
    func toggleRecentPlace() -> Bool {
        switch recent.before {
        case let .session(key)?:
            guard let row = state.row(key) else { return false }
            go(to: row, raising: false)
        case let .view(id)?:
            guard showView(id, focus: true) else { return false }
        case nil:
            return false
        }
        return true
    }

    /// Where the pane is now.
    private var place: Place? {
        if let viewSelection { return .view(viewSelection) }
        return state.selection.map(Place.session)
    }

    /// Unlists an opened view and goes back to where the pane was before it.
    private func retire(_ view: WorkspaceUtilityView) {
        views.close(view.id)
        let back = returnPlaces.removeValue(forKey: view.id)
        reloadSidebar()
        guard viewSelection == view.id else { return }
        viewSelection = nil
        switch back {
        case let .session(key)? where state.row(key) != nil:
            select(key, focus: true)
        case let .view(id)? where views.view(id) != nil:
            showView(id, focus: true)
        default:
            showView(WorkspaceUtilityView.dashboard.id, focus: true)
        }
    }

    private func discardViewTerminal(_ id: String) {
        guard let terminal = viewTerminals.removeValue(forKey: id) else { return }
        if isOpen, let responder = window.firstResponder as? NSView, responder.isDescendant(of: terminal.view) {
            window.makeFirstResponder(sidebar)
        }
        terminal.view.removeFromSuperview()
        terminal.close()
    }

    /// Moves `offset` places through the sessions and the terminals listed
    /// under them, in sidebar order, wrapping; from anywhere else, to the
    /// first (or last) of them. A Kitty row is selected, not raised: stepping
    /// through the sidebar never leaves the app.
    ///
    /// With `raising` the step is from session to session: terminals are
    /// passed over, one of them counts as standing just below its session,
    /// and a Kitty row comes forward in Kitty. With `sessionsOnly` the step
    /// is the same one and stays in the app, as F7 and F9 do.
    func selectNeighbor(_ offset: Int, raising: Bool = false, sessionsOnly: Bool = false) {
        let listed = state.embeddedRuntimeSessionIDs
        let skipping = raising || sessionsOnly
        let stops = items.filter { item in
            switch item {
            case .row: return true
            case let .view(view): return !skipping && view.besideRuntimeSessionID.map(listed.contains) ?? false
            case .section, .group: return false
            }
        }
        guard !stops.isEmpty else { return }
        let beside = skipping ? views.view(viewSelection)?.besideRuntimeSessionID : nil
        let here = stops.firstIndex { item in
            switch item {
            case let .row(row):
                if let beside { return row.embeddedRuntimeSessionID == beside }
                return viewSelection == nil && row.key == state.selection
            case let .view(view): return view.id == viewSelection
            case .section, .group: return false
            }
        }
        // Up from a terminal, the nearest session is its own.
        let step = beside != nil && offset < 0 ? offset + 1 : offset
        let next = here.map { (($0 + step) % stops.count + stops.count) % stops.count }
            ?? (offset < 0 ? stops.count - 1 : 0)
        switch stops[next] {
        case let .row(row): go(to: row, raising: raising)
        case let .view(view): showView(view.id, focus: true)
        case .section, .group: break
        }
    }

    /// Goes to the session at `index` in sidebar order; nothing when fewer
    /// sessions are listed. A Kitty row stays in the app unless `raising`.
    func selectSession(at index: Int, raising: Bool = false) {
        let rows = state.rows
        guard rows.indices.contains(index) else { return }
        go(to: rows[index], raising: raising)
    }

    /// An embedded session takes the keyboard either way. A Kitty session is
    /// only selected here, or with `raising` focused as a click does.
    private func go(to row: WorkspaceRow, raising: Bool) {
        if raising {
            activate(row)
        } else {
            select(row.key, focus: row.embeddedRuntimeSessionID != nil)
        }
    }

    /// A terminal's program ended, or it asked to be closed: its attachment
    /// is over. The session is not: it stays listed until the daemon drops it.
    func terminalEnded(_ view: NSView) {
        if let id = viewTerminals.first(where: { $0.value.view === view })?.key {
            utilityEnded(id)
            return
        }
        guard let id = terminals.first(where: { $0.value.view === view })?.key else { return }
        if closing[id] != nil {
            discardTerminal(id)
            return
        }
        Self.logger.notice("workspace: the terminal for \(id, privacy: .public) ended; detached")
        discardTerminal(id)
        detached.insert(id)
        updateContent(focus: false)
    }

    /// A utility view's program exited. A shell, editor, or pager is done
    /// and goes away; the Dashboard and the Overview stay listed, and say
    /// they stopped until they are started again.
    private func utilityEnded(_ id: String) {
        Self.logger.notice("workspace: the program of view \(id, privacy: .public) ended")
        discardViewTerminal(id)
        guard let view = views.view(id) else { return }
        if view.isPermanent {
            endedViews.insert(id)
            updateContent(focus: false)
        } else {
            retire(view)
        }
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

    private func wantedViewContent(_ view: WorkspaceUtilityView, retry: Bool) -> Content {
        let id = view.id
        if endedViews.contains(id) { return .viewEnded(id: id) }
        if viewTerminals[id] != nil { return .view(id: id) }
        if !retry, case let .viewUnavailable(refused, _) = content, refused == id { return content }
        guard let delegate else { return .viewUnavailable(id: id, "The app is shutting down.") }
        switch delegate.makeUtilityTerminal(view) {
        case let .success(terminal):
            Self.logger.notice("workspace: started view \(id, privacy: .public)")
            viewTerminals[id] = terminal
            terminal.view.frame = pane.bounds
            terminal.view.autoresizingMask = [.width, .height]
            terminal.view.isHidden = true
            pane.addSubview(terminal.view, positioned: .below, relativeTo: placeholder)
            return .view(id: id)
        case let .failure(problem):
            return .viewUnavailable(id: id, problem.message)
        }
    }

    private func wantedContent(retry: Bool) -> Content {
        if let view = views.view(viewSelection) { return wantedViewContent(view, retry: retry) }
        guard let row = state.selectedRow else {
            if let gone { return .gone(runtimeSessionID: gone.runtimeSessionID, gone.status) }
            if case let .session(id)? = restoring { return .restoring(runtimeSessionID: id) }
            return .empty
        }
        guard let id = row.embeddedRuntimeSessionID else { return .external(row.key) }
        if closing[id] != nil { return .closing(runtimeSessionID: id) }
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
        notePlace()
        let wanted = wantedContent(retry: retry)
        if wanted != content {
            content = wanted
            Self.logger.notice("workspace: showing \(String(describing: wanted), privacy: .public)")
        }
        var shown: NSView?
        if case let .terminal(id) = wanted { shown = terminals[id]?.view }
        if case let .view(id) = wanted { shown = viewTerminals[id]?.view }
        for terminal in terminals.values where terminal.view !== shown { terminal.view.isHidden = true }
        for terminal in viewTerminals.values where terminal.view !== shown { terminal.view.isHidden = true }
        shown?.isHidden = false
        placeholder.isHidden = shown != nil
        if shown == nil { fillPlaceholder(wanted) }
        window.title = views.view(viewSelection)?.title
            ?? state.selectedRow.map { "\($0.project) — \($0.label)" } ?? "Agent Mux"

        if let shown, focus {
            window.makeFirstResponder(shown)
        } else if window.firstResponder === window
                    || (window.firstResponder as? NSView)?.isHiddenOrHasHiddenAncestor == true {
            // Nothing has the keyboard, or a terminal that is no longer
            // shown had it: the sidebar takes it, so the arrow keys work.
            window.makeFirstResponder(sidebar)
        }
        reportPlace()
    }

    private func fillPlaceholder(_ content: Content) {
        let row = state.selectedRow
        switch content {
        case .empty, .terminal, .view:
            placeholder.show(
                title: state.rows.isEmpty ? "No live sessions" : "No session selected",
                message: state.rows.isEmpty
                    ? "Sessions appear here as they start. The Dashboard launches them."
                    : "Choose a session in the sidebar.",
                button: "Open Dashboard"
            ) { [weak self] in self?.showView(WorkspaceUtilityView.dashboard.id) }
        case let .viewEnded(id):
            let name = views.view(id)?.title ?? "This view"
            placeholder.show(
                title: "\(name) closed",
                message: "It quit. No session was affected: the agents keep running.",
                button: "Reopen"
            ) { [weak self] in self?.restartView(id) }
        case let .viewUnavailable(id, message):
            placeholder.show(title: "No terminal yet", message: message, button: "Try Again") { [weak self] in
                self?.restartView(id)
            }
        case .external:
            placeholder.show(
                title: row.map { "\($0.project) runs in Kitty" } ?? "This session runs in Kitty",
                message: "Its terminal is a Kitty window. Agent Mux brings Kitty forward on it.",
                button: "Show in Kitty"
            ) { [weak self] in
                guard let self, let row = self.state.selectedRow else { return }
                self.activate(row)
            }
        case .restoring:
            placeholder.show(
                title: "Restoring",
                message: state.current
                    ? "Checking that the session this window was on is still running."
                    : "Waiting for the daemon to say whether the session this window was on is still running. Nothing is started meanwhile.",
                button: "Open Dashboard"
            ) { [weak self] in self?.showView(WorkspaceUtilityView.dashboard.id) }
        case let .gone(_, status):
            let name = gone?.name ?? "This session"
            let notice = status?.notice(for: name)
                ?? ("No longer listed", status == nil
                    ? "\(name) is no longer running. Checking what became of it."
                    : "\(name) is no longer listed by the daemon. Nothing was restarted.")
            placeholder.show(title: notice.0, message: notice.1, button: "Open Dashboard") { [weak self] in
                self?.showView(WorkspaceUtilityView.dashboard.id)
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
        case .closing:
            placeholder.show(title: "Closing", message: "This session's agent is being ended.")
        }
    }

    private func reattach(_ id: String) {
        detached.remove(id)
        guard state.selection == .embedded(id) else { return }
        updateContent(focus: true, retry: true)
    }

    private func restartView(_ id: String) {
        endedViews.remove(id)
        guard viewSelection == id else { return }
        updateContent(focus: true, retry: true)
    }

    /// Asks again for a terminal that could not be made, now that what it
    /// was missing (a resolved amx) may be there.
    func retryTerminal() {
        updateContent(focus: false, retry: true)
    }

    // MARK: Sidebar

    private func reloadSidebar(force: Bool = false) {
        // A session's terminals follow its row. One whose session is no
        // longer listed keeps running, among the workspace's own views.
        let listed = state.embeddedRuntimeSessionIDs
        let own = views.all.filter { view in view.besideRuntimeSessionID.map { !listed.contains($0) } ?? true }
        let wanted = [Item.section(Self.viewsSection)] + own.map(Item.view)
            + state.groups.flatMap { group in
                [Item.group(group)] + group.rows.flatMap { row in
                    [Item.row(row)] + views.terminals(beside: row.embeddedRuntimeSessionID).map(Item.view)
                }
            }
        let dimmed: CGFloat = state.current ? 1 : 0.5
        if force || wanted != items || sidebar.alphaValue != dimmed {
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
            switch item {
            case let .view(view): return view.id == viewSelection
            case let .row(row): return viewSelection == nil && row.key == state.selection
            case .section, .group: return false
            }
        }
        let wanted = index.map { IndexSet(integer: $0) } ?? IndexSet()
        guard sidebar.selectedRowIndexes != wanted else { return }
        applying = true
        sidebar.selectRowIndexes(wanted, byExtendingSelection: false)
        applying = false
    }

    func numberOfRows(in tableView: NSTableView) -> Int { items.count }

    func tableView(_ tableView: NSTableView, isGroupRow row: Int) -> Bool {
        switch items[row] {
        case .section, .group: return true
        case .view, .row: return false
        }
    }

    func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool {
        !self.tableView(tableView, isGroupRow: row)
    }

    func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
        switch items[row] {
        case .section, .group: return 24
        case let .row(row) where !row.name.isEmpty: return 54
        case .view, .row: return 38
        }
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        switch items[row] {
        case let .section(title):
            let cell = tableView.makeView(withIdentifier: WorkspaceGroupCellView.identifier, owner: nil) as? WorkspaceGroupCellView
                ?? WorkspaceGroupCellView()
            cell.show(section: title)
            return cell
        case let .view(view):
            let cell = tableView.makeView(withIdentifier: WorkspaceRowCellView.identifier, owner: nil) as? WorkspaceRowCellView
                ?? WorkspaceRowCellView()
            cell.show(view)
            cell.onClose = { [weak self] in self?.closeView(view.id) }
            return cell
        case let .group(group):
            let cell = tableView.makeView(withIdentifier: WorkspaceGroupCellView.identifier, owner: nil) as? WorkspaceGroupCellView
                ?? WorkspaceGroupCellView()
            cell.show(group)
            return cell
        case let .row(row):
            let cell = tableView.makeView(withIdentifier: WorkspaceRowCellView.identifier, owner: nil) as? WorkspaceRowCellView
                ?? WorkspaceRowCellView()
            cell.show(row, muteGlyph: glyphs.mute, closing: row.embeddedRuntimeSessionID.map { closing[$0] != nil } ?? false)
            cell.onClose = { [weak self] in self?.closeSession(row.key) }
            return cell
        }
    }

    /// A session's line is drawn on the session's color; every other line,
    /// the Dashboard and the Overview among them, on the list's own.
    func tableView(_ tableView: NSTableView, rowViewForRow row: Int) -> NSTableRowView? {
        let view = WorkspaceRowView()
        if case let .row(row) = items[row], let color = row.tabColor {
            view.tabColor = NSColor(srgbRed: color.red, green: color.green, blue: color.blue, alpha: 1)
        }
        return view
    }

    func tableView(_ tableView: NSTableView, didAdd rowView: NSTableRowView, forRow row: Int) {
        (rowView as? WorkspaceRowView)?.syncCell()
    }

    /// The arrow keys moved the selection: show it, and stay in the sidebar.
    func tableViewSelectionDidChange(_ notification: Notification) {
        guard !applying else { return }
        let index = sidebar.selectedRow
        guard index >= 0, index < items.count else {
            select(nil)
            return
        }
        switch items[index] {
        case let .row(row): select(row.key)
        case let .view(view): showView(view.id, focus: false)
        case .section, .group: select(nil)
        }
    }

    // MARK: Dragging

    static let dragType = NSPasteboard.PasteboardType("io.github.sglavoie.agentmux.workspace-line")

    /// Where the line at `index` may be dropped, as the lines it would land
    /// above; the last is the line after its last sibling. A session goes
    /// among the sessions of its project and a terminal among the ones
    /// beside its session; no other line moves.
    func dropSlots(forItemAt index: Int) -> [Int] {
        guard items.indices.contains(index) else { return [] }
        switch items[index] {
        case .section, .group:
            return []
        case .row:
            guard let group = items[..<index].lastIndex(where: { if case .group = $0 { return true } else { return false } })
            else { return [] }
            let end = items[(group + 1)...].firstIndex { item in
                switch item {
                case .section, .group: return true
                case .view, .row: return false
                }
            } ?? items.count
            return (group + 1 ..< end).filter { if case .row = items[$0] { return true } else { return false } } + [end]
        case let .view(view):
            guard view.besideRuntimeSessionID != nil,
                  let session = items[..<index].lastIndex(where: { if case .view = $0 { return false } else { return true } }),
                  case let .row(row) = items[session], row.embeddedRuntimeSessionID == view.besideRuntimeSessionID
            else { return [] }
            let end = items[(session + 1)...].firstIndex { if case .view = $0 { return false } else { return true } } ?? items.count
            return Array(session + 1 ... end)
        }
    }

    /// The slot a line dragged from `index` takes when it is let go over
    /// `proposed`: the first one at or below it. Nil outside the line's
    /// project or session, and where it would land back where it is.
    func dropSlot(forItemAt index: Int, proposed: Int) -> Int? {
        let slots = dropSlots(forItemAt: index)
        guard let first = slots.first, proposed >= first, let slot = slots.first(where: { $0 >= proposed }) else { return nil }
        let after = slots.first { $0 > index }
        return slot == index || slot == after ? nil : slot
    }

    /// Moves the line at `index` to above the line at `proposed`; false when
    /// it may not go there.
    @discardableResult
    func moveItem(at index: Int, to proposed: Int) -> Bool {
        guard let slot = dropSlot(forItemAt: index, proposed: proposed) else { return false }
        switch items[index] {
        case .section, .group:
            return false
        case let .row(row):
            guard let group = state.groups.first(where: { $0.project == row.project }) else { return false }
            var keys = group.rows.map(\.key).filter { $0 != row.key }
            var at = keys.count
            if slot < items.count, case let .row(next) = items[slot] { at = keys.firstIndex(of: next.key) ?? at }
            keys.insert(row.key, at: at)
            order.set(keys, for: row.project)
            state.arrange(order)
            delegate?.workspaceOrderChanged(order)
        case let .view(view):
            var next: String?
            if slot < items.count, case let .view(other) = items[slot], other.besideRuntimeSessionID == view.besideRuntimeSessionID {
                next = other.id
            }
            views.moveTerminal(view.id, before: next)
        }
        reloadSidebar()
        return true
    }

    private func draggedIndex() -> Int? {
        guard let dragged else { return nil }
        return items.firstIndex { item in
            switch item {
            case let .row(row): return dragged == .session(row.key)
            case let .view(view): return dragged == .view(view.id)
            case .section, .group: return false
            }
        }
    }

    /// A line is dragged only when there is somewhere else for it to go.
    func tableView(_ tableView: NSTableView, pasteboardWriterForRow row: Int) -> NSPasteboardWriting? {
        guard dropSlots(forItemAt: row).count > 2 else { return nil }
        let item = NSPasteboardItem()
        item.setString("", forType: Self.dragType)
        return item
    }

    func tableView(_ tableView: NSTableView, draggingSession session: NSDraggingSession, willBeginAt screenPoint: NSPoint, forRowIndexes rowIndexes: IndexSet) {
        dragged = nil
        guard let index = rowIndexes.first, items.indices.contains(index) else { return }
        switch items[index] {
        case let .row(row): dragged = .session(row.key)
        case let .view(view): dragged = .view(view.id)
        case .section, .group: break
        }
    }

    func tableView(_ tableView: NSTableView, draggingSession session: NSDraggingSession, endedAt screenPoint: NSPoint, operation: NSDragOperation) {
        dragged = nil
    }

    func tableView(_ tableView: NSTableView, validateDrop info: NSDraggingInfo, proposedRow row: Int, proposedDropOperation dropOperation: NSTableView.DropOperation) -> NSDragOperation {
        guard info.draggingSource as? NSTableView === sidebar, let index = draggedIndex(),
              let slot = dropSlot(forItemAt: index, proposed: row) else { return [] }
        tableView.setDropRow(slot, dropOperation: .above)
        return .move
    }

    func tableView(_ tableView: NSTableView, acceptDrop info: NSDraggingInfo, row: Int, dropOperation: NSTableView.DropOperation) -> Bool {
        guard info.draggingSource as? NSTableView === sidebar, let index = draggedIndex() else { return false }
        return moveItem(at: index, to: row)
    }

    /// A click on a row focuses it.
    @objc private func rowClicked(_ sender: Any?) {
        focusItem(at: sidebar.clickedRow)
    }

    /// Gives the keyboard to the session list: the arrow keys then move the
    /// selection and Return goes into what is selected. A hidden list is
    /// shown first.
    func focusSidebar() {
        setSidebarCollapsed(false)
        window.makeFirstResponder(sidebar)
    }

    /// Whether the sidebar is hidden and the pane has the whole window.
    var isSidebarCollapsed: Bool {
        _ = window
        return sidebarItem?.isCollapsed ?? false
    }

    @objc func toggleSidebar(_ sender: Any? = nil) {
        setSidebarCollapsed(!isSidebarCollapsed, animated: true)
    }

    func setSidebarCollapsed(_ collapsed: Bool, animated: Bool = false) {
        guard collapsed != isSidebarCollapsed, let sidebarItem else { return }
        if animated, !AppDelegate.isRunningTests {
            sidebarItem.animator().isCollapsed = collapsed
        } else {
            sidebarItem.isCollapsed = collapsed
        }
    }

    /// The sidebar was hidden or shown, by its buttons, the menu, or a drag
    /// of the divider.
    private func sidebarCollapseChanged() {
        let collapsed = sidebarItem?.isCollapsed ?? false
        guard collapsed != reportedCollapsed else { return }
        reportedCollapsed = collapsed
        if collapsed, window.firstResponder === window
            || (window.firstResponder as? NSView)?.isDescendant(of: sidebar) == true {
            // A list that cannot be seen does not keep the keyboard.
            var shown: NSView?
            if case let .terminal(id) = content { shown = terminals[id]?.view }
            if case let .view(id) = content { shown = viewTerminals[id]?.view }
            if let shown { window.makeFirstResponder(shown) }
        }
        delegate?.workspaceSidebarCollapsedChanged(collapsed)
    }

    private static func makeSidebarToggle() -> NSButton {
        let button = NSButton(title: "", target: nil, action: nil)
        button.image = NSImage(systemSymbolName: "line.3.horizontal", accessibilityDescription: "Toggle Sidebar")?
            .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: 13, weight: .regular))
        button.imagePosition = .imageOnly
        button.isBordered = false
        button.contentTintColor = .secondaryLabelColor
        button.refusesFirstResponder = true
        button.toolTip = "Toggle Sidebar (⌘B)"
        button.translatesAutoresizingMaskIntoConstraints = false
        return button
    }

    /// Return in the session list: focuses the selected row, as a click does.
    func focusSelectedItem() {
        focusItem(at: sidebar.selectedRow)
    }

    private func focusItem(at index: Int) {
        guard index >= 0, index < items.count else { return }
        switch items[index] {
        case let .row(row): activate(row)
        case let .view(view): showView(view.id, focus: true)
        case .section, .group: break
        }
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
        sidebar.onReturn = { [weak self] in self?.focusSelectedItem() }
        sidebar.registerForDraggedTypes([Self.dragType])
        sidebar.setDraggingSourceOperationMask(.move, forLocal: true)
        sidebar.setDraggingSourceOperationMask([], forLocal: false)
        let context = NSMenu()
        context.delegate = self
        sidebar.menu = context
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
        sidebarItem.canCollapse = true
        sidebarItem.canCollapseFromWindowResize = false
        self.sidebarItem = sidebarItem
        sidebarObservation = sidebarItem.observe(\.isCollapsed) { [weak self] _, _ in
            MainActor.assumeIsolated { self?.sidebarCollapseChanged() }
        }
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

        sidebarToggle.target = self
        sidebarToggle.action = #selector(toggleSidebar(_:))
        let holder = NSView(frame: NSRect(x: 0, y: 0, width: 32, height: 28))
        holder.addSubview(sidebarToggle)
        NSLayoutConstraint.activate([
            sidebarToggle.centerXAnchor.constraint(equalTo: holder.centerXAnchor),
            sidebarToggle.centerYAnchor.constraint(equalTo: holder.centerYAnchor),
            sidebarToggle.widthAnchor.constraint(equalToConstant: 24),
            sidebarToggle.heightAnchor.constraint(equalToConstant: 22),
        ])
        let accessory = NSTitlebarAccessoryViewController()
        accessory.view = holder
        accessory.layoutAttribute = .leading
        window.addTitlebarAccessoryViewController(accessory)
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

/// The session list. It keeps the keys a list needs — the arrows, and Return
/// to go into the selected row — and nothing typed here reaches a program.
@MainActor
final class WorkspaceSidebarView: NSTableView {
    var onReturn: (() -> Void)?

    override func keyDown(with event: NSEvent) {
        let returnKeys: Set<UInt16> = [36, 76]
        if returnKeys.contains(event.keyCode),
           event.modifierFlags.isDisjoint(with: [.command, .control, .option]) {
            onReturn?()
            return
        }
        super.keyDown(with: event)
    }
}

/// A line of the session list, drawn on its session's color: the color
/// while the line is not the selected one, and the list's own selection
/// while it is.
@MainActor
final class WorkspaceRowView: NSTableRowView {
    /// The text on a session's color. The daemon publishes colors bright
    /// enough for it.
    static let tabText = NSColor.black

    var tabColor: NSColor? {
        didSet {
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

    /// Whether the color is what the line shows now.
    var showsTabColor: Bool { tabColor != nil && !isSelected }

    /// Tells the line's cell what it is drawn on, so its text stays legible.
    func syncCell() {
        guard numberOfColumns > 0 else { return }
        (view(atColumn: 0) as? WorkspaceRowCellView)?.setTabText(showsTabColor ? Self.tabText : nil)
    }

    override func drawBackground(in dirtyRect: NSRect) {
        guard showsTabColor, let tabColor else {
            super.drawBackground(in: dirtyRect)
            return
        }
        tabColor.setFill()
        outline().fill()
    }

    /// The selected line has a border in the accent color around the list's
    /// selection, which on its own is a gray close to the lines beside it.
    /// It is a view over the line, not something drawn here: a source list
    /// paints its selection without asking the row view to.
    let selectionBorder = WorkspaceSelectionBorderView()

    static let selectionBorderWidth: CGFloat = 2
    /// How far the list's selection is inset from the row's sides, and how
    /// round its corners are.
    static let selectionInset: CGFloat = 10
    static let selectionRadius: CGFloat = 10

    override func layout() {
        super.layout()
        if subviews.last !== selectionBorder {
            addSubview(selectionBorder, positioned: .above, relativeTo: nil)
        }
        selectionBorder.frame = bounds.insetBy(dx: Self.selectionInset, dy: 0)
        selectionBorder.isHidden = !isSelected
    }

    /// The shape a line's color follows: the selection's, a point shorter so
    /// two colored lines do not run together.
    private func outline() -> NSBezierPath {
        NSBezierPath(roundedRect: bounds.insetBy(dx: Self.selectionInset, dy: 1),
                     xRadius: Self.selectionRadius, yRadius: Self.selectionRadius)
    }
}

/// The border of the selected line. It takes no clicks.
@MainActor
final class WorkspaceSelectionBorderView: NSView {
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
        layer?.borderColor = NSColor.controlAccentColor.cgColor
    }

    override func hitTest(_ point: NSPoint) -> NSView? { nil }
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

    /// A heading that is not a project: it counts nothing.
    func show(section title: String) {
        nameLabel.stringValue = title
        countLabel.stringValue = ""
        countLabel.toolTip = nil
        setAccessibilityLabel(title)
    }

    func show(_ group: WorkspaceGroup) {
        nameLabel.stringValue = group.project
        let waiting = group.attention
        countLabel.stringValue = waiting > 0 ? String(waiting) : ""
        countLabel.toolTip = waiting > 0 ? "\(waiting) waiting on you" : nil
        setAccessibilityLabel(waiting > 0 ? "\(group.project), \(waiting) waiting" : group.project)
    }
}

/// A session's line: its state glyph, its profile, and a line of detail,
/// with a × in the top right corner when the line can be closed. A session
/// that was given a name has it on a line of its own above the profile.
@MainActor
final class WorkspaceRowCellView: NSTableCellView {
    static let identifier = NSUserInterfaceItemIdentifier("workspace.row")
    let glyphLabel = NSTextField(labelWithString: "")
    let nameLabel = NSTextField(labelWithString: "")
    let titleLabel = NSTextField(labelWithString: "")
    let detailLabel = NSTextField(labelWithString: "")
    let closeButton = NSButton(title: "", target: nil, action: nil)
    /// What the × does for the line shown now.
    var onClose: (() -> Void)?

    init() {
        super.init(frame: .zero)
        identifier = Self.identifier
        glyphLabel.alignment = .center
        nameLabel.font = .systemFont(ofSize: NSFont.systemFontSize, weight: .bold)
        nameLabel.textColor = Self.nameColor
        nameLabel.lineBreakMode = .byTruncatingTail
        nameLabel.isHidden = true
        titleLabel.lineBreakMode = .byTruncatingTail
        detailLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        detailLabel.textColor = .secondaryLabelColor
        detailLabel.lineBreakMode = .byTruncatingTail
        textField = titleLabel
        let text = NSStackView(views: [nameLabel, titleLabel, detailLabel])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 1
        let stack = NSStackView(views: [glyphLabel, text])
        stack.orientation = .horizontal
        stack.spacing = 6
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
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
        NSLayoutConstraint.activate([
            glyphLabel.widthAnchor.constraint(equalToConstant: 20),
            stack.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2),
            stack.trailingAnchor.constraint(lessThanOrEqualTo: closeButton.leadingAnchor, constant: -2),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
            closeButton.topAnchor.constraint(equalTo: topAnchor, constant: 3),
            closeButton.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -4),
            closeButton.widthAnchor.constraint(equalToConstant: 14),
            closeButton.heightAnchor.constraint(equalToConstant: 14),
        ])
    }

    @objc private func closePressed(_ sender: Any?) { onClose?() }

    /// A session's name on its color: a dark blue, which reads on every
    /// color the daemon publishes, as black does.
    static let nameOnTabColor = NSColor(srgbRed: 0, green: 0.16, blue: 0.55, alpha: 1)
    /// A session's name on the list's own background: the same dark blue in
    /// a light appearance, and a blue light enough to read in a dark one.
    static let nameColor = NSColor(name: nil) { appearance in
        appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
            ? NSColor(srgbRed: 0.45, green: 0.68, blue: 1, alpha: 1) : nameOnTabColor
    }

    /// Draws the text in `color`, for a session's color behind it, or as the
    /// list draws it when there is none.
    func setTabText(_ color: NSColor?) {
        nameLabel.textColor = color == nil ? Self.nameColor : Self.nameOnTabColor
        glyphLabel.textColor = color ?? .labelColor
        titleLabel.textColor = color ?? .labelColor
        detailLabel.textColor = color?.withAlphaComponent(0.8) ?? .secondaryLabelColor
        closeButton.contentTintColor = color?.withAlphaComponent(0.8) ?? .tertiaryLabelColor
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    /// A utility view's line: what it is and what it is about. It shows no
    /// session state, because it has none.
    func show(_ view: WorkspaceUtilityView) {
        nameLabel.stringValue = ""
        nameLabel.isHidden = true
        glyphLabel.stringValue = view.glyph
        titleLabel.stringValue = view.title
        titleLabel.font = .systemFont(ofSize: NSFont.systemFontSize, weight: .regular)
        detailLabel.stringValue = view.detail
        toolTip = view.target.isEmpty ? view.detail : view.target
        setAccessibilityLabel("\(view.title), \(view.kind.rawValue) view")
        closeButton.isHidden = view.isPermanent
        closeButton.toolTip = "Close \(view.title)"
        closeButton.setAccessibilityLabel("Close \(view.title)")
    }

    /// Only an embedded session can be closed from here; `closing` is set
    /// once it was asked to be.
    func show(_ row: WorkspaceRow, muteGlyph: String, closing: Bool = false) {
        nameLabel.stringValue = row.name
        nameLabel.isHidden = row.name.isEmpty
        glyphLabel.stringValue = row.glyph
        titleLabel.stringValue = row.muted ? "\(row.label) \(muteGlyph)" : row.label
        titleLabel.font = .systemFont(ofSize: NSFont.systemFontSize, weight: row.attention ? .semibold : .regular)
        detailLabel.stringValue = row.detail
        toolTip = row.toolTip
        var spoken = (row.name.isEmpty ? [] : [row.name]) + [row.project, row.label]
        if row.attention { spoken.append("needs attention") } else if row.working { spoken.append("working") }
        if !row.detail.isEmpty { spoken.append(row.detail) }
        setAccessibilityLabel(spoken.joined(separator: ", "))
        if closing { detailLabel.stringValue = "closing…" }
        closeButton.isHidden = row.embeddedRuntimeSessionID == nil || closing
        closeButton.toolTip = "End \(row.project) — \(row.label)"
        closeButton.setAccessibilityLabel("End \(row.project), \(row.label)")
    }
}

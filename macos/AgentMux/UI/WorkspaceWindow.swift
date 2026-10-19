import AppKit
import os

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
/// The × on a session row and ⌘W detach its terminal. End Session… is
/// a separate action that always asks before ending an agent. The × on an opened
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
/// digits count sessions only, and each of the first ten sessions shows its
/// own on its line.
///
/// A session's row can be dragged to another place among its project's
/// sessions, and takes its terminals with it; a terminal can be dragged among
/// the ones beside its session. Nothing is dragged out of its project or its
/// session, and the Workspace section stays as it is.
///
/// A session can be pinned from its line's right-click menu or the Session
/// menu. The pinned ones are listed first, under a Pinned line above every
/// project, each naming the project it left; they are dragged among
/// themselves, and unpinning one puts it back under its project.
///
/// On the other side of the pane is the inspector, hidden until it is asked
/// for (`WorkspaceInspectorView`): the sessions waiting on the operator, in
/// the order they are listed here, or what the daemon says about the session
/// the pane is on.
/// It is not a session and not a view, never counts as waiting, starts
/// nothing, and takes the keyboard only through Window › Waiting.
@MainActor
final class WorkspaceWindowController: NSObject, NSWindowDelegate, NSTableViewDataSource, NSTableViewDelegate, NSMenuDelegate {
    private nonisolated static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "workspace")

    typealias Item = WorkspaceSidebarItem

    /// The lines of `new` that show something other than they did in
    /// `old`, when the two are the same lines in the same order; nil when
    /// they are not and the whole list is to be loaded again.
    static func changedLines(from old: [Item], to new: [Item]) -> IndexSet? {
        Item.changedLines(from: old, to: new)
    }

    /// Retain physical targets during an interaction, refreshing their live
    /// values in place. New and reordered rows appear when the interaction ends.
    static func holdingSidebar(_ old: [Item], latest: [Item]) -> [Item] {
        Item.holding(old, latest: latest)
    }

    /// Hover holds attention reordering, but must not hide newly opened
    /// sessions or views for as long as the pointer rests in the sidebar.
    static func hasNewSidebarTargets(_ latest: [Item], comparedTo old: [Item]) -> Bool {
        Item.hasNewTargets(latest, comparedTo: old)
    }

    /// The heading the utility views are listed under.
    static let viewsSection = "Workspace"
    /// How long a just-launched session is waited for before the request to
    /// show it is forgotten.
    static let awaitLimit: TimeInterval = 60

    typealias Content = WorkspacePaneContent

    /// Something the pane can be on: a session or a utility view.
    private enum Place: Equatable {
        case review
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
    private var sidebarMenuOpen = false
    private(set) var items: [Item] = []
    private(set) var content: Content = .empty
    /// Whether the window is on screen; terminals exist only while it is.
    private(set) var isOpen = false

    let sidebar = WorkspaceSidebarView()
    /// Hides the sidebar and brings it back, from the title bar, so that
    /// neither the sidebar nor the pane gives up anything to it.
    let sidebarToggle = WorkspaceWindowChrome.makeSidebarToggle()
    /// What the delegate was last told, so it hears of each change once.
    private var reportedCollapsed = false
    private var reportedShowing: TerminalNotice.Origin?
    private var reportedOtherShowing: TerminalNotice.Origin?
    private var sidebarItem: NSSplitViewItem?
    private var contentItem: NSSplitViewItem?
    private var sidebarObservation: NSKeyValueObservation?
    let inspector = WorkspaceInspectorView()
    /// Hides the inspector and brings it back, from the title bar.
    let inspectorToggle = WorkspaceWindowChrome.makeInspectorToggle()
    /// The tab the inspector is turned to; the app keeps it across launches.
    private(set) var inspectorTab = WorkspaceInspectorTab.waiting
    private var reportedInspectorCollapsed = true
    private var inspectorItem: NSSplitViewItem?
    private var inspectorObservation: NSKeyValueObservation?
    /// The widths the sidebar and the inspector open at, which the app sets
    /// before the window is made; nil gives each its default.
    var sidebarWidth: CGFloat?
    var inspectorWidth: CGFloat?
    /// The widths the delegate was last told of.
    private var reportedSidebarWidth: CGFloat?
    private var reportedInspectorWidth: CGFloat?
    private var splitObservation: NSObjectProtocol?
    let feedLabel = NSTextField(labelWithString: "")
    /// Start Daemon or Restart Daemon under the feed's notice, whenever the
    /// feed says one would help: held rows keep the pane's own button away,
    /// so the sidebar offers it whatever is listed.
    let feedButton = NSButton(title: "", target: nil, action: nil)
    let placeholder = WorkspacePlaceholderView()
    /// The Go to… list, above whatever the pane shows while it is up.
    let switcher = WorkspaceSwitcherView()
    /// What had the keyboard when the Go to… list took it.
    private weak var switcherReturn: NSResponder?
    private weak var waitingReturn: NSResponder?
    private let pane = NSView()
    private let otherPane = NSView()
    private let otherPlaceholder = WorkspacePlaceholderView()
    private lazy var paneLayout: WorkspacePaneLayout = {
        let layout = WorkspacePaneLayout(active: pane, other: otherPane)
        layout.onResize = { [weak self] in self?.reportLayout() }
        return layout
    }()
    private var otherPlace: Place?
    private(set) var otherContent: Content = .empty
    private(set) var activeOnRight = false
    private var reviewSelected = false
    private var reviewView: NSView?
    private var reviewCanClose: () -> Bool = { true }
    private var reviewClosed: () -> Void = {}
    struct RestoredReview {
        var view: NSView
        var canClose: () -> Bool
        var close: () -> Void
        var directory: () -> String?
    }
    var makeRestoredReview: ((String) -> RestoredReview?)?
    var onLayoutChanged: (WorkspaceRestoration.Layout?) -> Void = { _ in }
    var reviewDirectory: () -> String? = { nil }
    private var pendingLayout: WorkspaceRestoration.Layout?
    private var layoutChecking = false
    private var applyingLayout = false
    private var layoutGeneration = 0
    private var layoutConversation: ConversationIdentity?
    private var restoringFraction: Double?
    private var paneClickMonitor: Any?
    var isSplit: Bool { otherPlace != nil }
    var hasReview: Bool { reviewView != nil }
    var splitUnavailableReason: String? {
        if !isOpen || place == nil { return "Select a session or view first" }
        if reviewSelected { return "Focus a session or shell first" }
        if !reviewCanClose() { return "Wait for the checkout operation to finish" }
        return nil
    }
    var canHideOtherPane: Bool { isSplit && (otherPlace != .review || reviewCanClose()) }

    private(set) lazy var window: NSWindow = makeWindow()

    private var terminals: [String: WorkspaceTerminal] = [:]
    /// Sessions whose terminal ended and is not remade until asked for.
    private var detached: Set<String> = []
    /// Sessions whose terminal's renderer said it failed.
    private(set) var rendererFailed: Set<String> = []
    /// Utility views, by id, whose terminal's renderer said it failed.
    private(set) var rendererFailedViews: Set<String> = []

    /// Sessions and views, by runtime session or view id, that rang the
    /// bell while another was shown; marked on their line until shown or
    /// until `bellHold` has passed.
    private(set) var ringingSessions: Set<String> = []
    private(set) var ringingViews: Set<String> = []
    /// Counts bells, so an expiry overtaken by a later bell is dropped.
    private var bellCount = 0
    static let bellHold: TimeInterval = 60
    /// What the bell does besides the mark, replaced in tests: the app in
    /// front, the beep, and the Dock's attention request.
    var isAppActive: () -> Bool = { NSApp.isActive }
    var beep: () -> Void = { NSSound.beep() }
    var requestAttention: () -> Void = { _ = NSApp.requestUserAttention(.informationalRequest) }

    /// The title each utility view's program last set (OSC 0/2), by view id.
    private(set) var programTitles: [String: String] = [:]
    /// The link under the pointer in the terminal shown, at the pane's
    /// bottom corner. Clicks go through it to the terminal beneath.
    let linkLabel = WorkspaceLinkLabel(labelWithString: "")
    /// The search open in each utility view, by view id, shown in the find
    /// bar while the pane is on that view.
    private(set) var searches: [String: Ghostty.Search] = [:]
    /// Which time a search was opened in each utility view, so the find
    /// bar can tell a search closed and opened again from the one it typed into.
    private var searchGenerations: [String: Int] = [:]
    private var searchGeneration = 0
    /// Where each utility view's viewport sits in its buffer, by view id.
    private(set) var scrollbars: [String: Ghostty.Scrollbar] = [:]
    /// The progress each utility view's program last reported (OSC 9;4), by
    /// view id, shown on its line. It is never attention.
    private(set) var progresses: [String: Ghostty.Progress] = [:]
    let findBar = WorkspaceFindBar()
    let scrollbarView = WorkspaceScrollbarView()
    /// Runs a Ghostty binding action in a terminal, replaced in tests.
    var performTerminalAction: (NSView, String) -> Bool = { view, action in
        (view as? Ghostty.SurfaceView)?.performBindingAction(action) ?? false
    }

    /// Sessions amx was asked to close, until it has answered and a live
    /// snapshot no longer lists them: whether the pane was on each when it
    /// was closed, and whether amx has answered.
    private var closing: [String: (wasShown: Bool, answered: Bool)] = [:]
    /// Settings controls close actions, including Ghostty's close callback.
    /// App quit confirmation remains independent of this preference.
    var confirmBeforeClosing = true
    /// Stands in for the sheet that asks before a session is closed, for
    /// tests. Answers on the main actor.
    var confirmClose: ((_ row: WorkspaceRow, _ answer: @escaping @MainActor (Bool) -> Void) -> Void)?
    /// Stands in for the sheet that asks before a utility view whose program
    /// is still running is closed, for tests. Answers on the main actor.
    var confirmViewClose: ((_ title: String, _ answer: @escaping @MainActor (Bool) -> Void) -> Void)?
    /// Stands in for the sheet that asks once, naming every view whose
    /// program is still running, before the app quits, for tests. Answers on
    /// the main actor.
    var confirmQuitWithViews: ((_ titles: [String], _ answer: @escaping @MainActor (Bool) -> Void) -> Void)?
    /// Stands in for the sheet that says why a session could not be closed.
    var reportCloseProblem: ((_ name: String, _ message: String) -> Void)?
    /// Stands in for the sheet that asks for a session's name, for tests.
    /// Answers on the main actor: the name, or nil when it was cancelled.
    var askName: ((_ row: WorkspaceRow, _ answer: @escaping @MainActor (String?) -> Void) -> Void)?
    var askTerminalName: ((_ view: WorkspaceUtilityView, _ answer: @escaping @MainActor (String?) -> Void) -> Void)?
    /// Stands in for the sheet that says why a session could not be named.
    var reportNameProblem: ((_ name: String, _ message: String) -> Void)?

    /// The utility views listed above the projects.
    private(set) var views = WorkspaceViews()
    /// The last name each embedded session was listed under, by runtime
    /// session, kept while a view is about it: a terminal whose session is
    /// no longer listed still says whose it was.
    private var sessionNames: [String: String] = [:]
    /// Each sidebar line's `decoration` as last drawn.
    private struct LineDecoration: Equatable {
        let label: String?
        let selectionColor: NSColor?
    }
    private var drawnDecorations: [LineDecoration] = []
    /// The font size each session's last terminal had when it was let go,
    /// by runtime session, so a zoom survives the window closing. Kept for
    /// the app's lifetime, until a new default zoom replaces every one.
    private(set) var fontSizes: [String: Float] = [:]
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
    private(set) var gone: (runtimeSessionID: String, name: String, status: WorkspaceSessionStatus?, conversation: ConversationIdentity?)?
    /// The conversation last known for the session the pane is on or is
    /// coming back to. It outlives the row, so a session that stops being
    /// listed can still be resumed from Past Sessions.
    private var shownConversation: (runtimeSessionID: String, conversation: ConversationIdentity)?
    private var reportedConversation: ConversationIdentity?
    /// Counts status questions, so an answer to one that was overtaken by
    /// the operator, or by a newer question, is dropped.
    private var statusQuestion = 0
    /// True while amx is being asked about the session being restored.
    private var restoreChecking = false
    private var reportedPlace: WorkspaceRestoration.Place?

    private let glyphs: Glyphs
    private let now: () -> Date

    /// How often the ages the sidebar and the inspector show ("4m", "Since")
    /// are said again while the window is open. The daemon sends nothing
    /// while nothing changes, so without it they would stand still.
    static let ageRefreshInterval: TimeInterval = 30
    /// Runs while the window is open, and only then.
    private(set) var ageTimer: Timer?

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
            comeBackToSelection()
        }
        startAgeTimer()
        NSApp.activate()
        window.makeKeyAndOrderFront(nil)
        refresh()
    }

    /// Opens the window without activating the app or ordering it in, for
    /// tests: everything else behaves as it does on screen.
    func openForTesting() {
        _ = window
        if !isOpen {
            isOpen = true
            comeBackToSelection()
        }
        startAgeTimer()
        refresh()
    }

    /// Reopening comes back to the session the window was on the way a
    /// launch does: a terminal that attached to it while the window was
    /// closed keeps it, so it is attached again only once amx says nobody
    /// else has it.
    private func comeBackToSelection() {
        guard let key = state.selection, viewSelection == nil, !key.runtimeSessionID.isEmpty else { return }
        let id = key.runtimeSessionID
        let conversation = state.row(key)?.conversation
            ?? (shownConversation?.runtimeSessionID == id ? shownConversation?.conversation : nil)
        restore(.session(runtimeSessionID: id), conversation: conversation)
    }

    private func startAgeTimer() {
        guard ageTimer == nil else { return }
        let timer = Timer(timeInterval: Self.ageRefreshInterval, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.refreshAges() }
        }
        timer.tolerance = Self.ageRefreshInterval / 6
        RunLoop.main.add(timer, forMode: .common)
        ageTimer = timer
    }

    /// Says the ages again, as a snapshot would: only the lines whose text
    /// changed are drawn again, so the selection, the pointer's ×, and the
    /// keyboard stay where they are.
    func refreshAges() {
        guard isOpen else { return }
        refresh()
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool { reviewCanClose() }

    func windowWillClose(_ notification: Notification) {
        guard isOpen else { return }
        reportLayout()
        pendingLayout = nil
        layoutGeneration += 1
        isOpen = false
        if let paneClickMonitor { NSEvent.removeMonitor(paneClickMonitor) }
        paneClickMonitor = nil
        if reviewSelected {
            switch otherPlace {
            case let .session(key)?: state.select(key); viewSelection = nil
            case let .view(id)?: state.select(nil); viewSelection = id
            default: break
            }
        }
        otherPlace = nil
        otherContent = .empty
        activeOnRight = false
        reviewView?.removeFromSuperview()
        reviewView = nil
        reviewSelected = false
        reviewClosed()
        reviewClosed = {}
        paneLayout.arrange(split: false, activeOnRight: false)
        ageTimer?.invalidate()
        ageTimer = nil
        Self.logger.notice("workspace: closed, detaching \(self.terminals.count) terminal(s)")
        for id in Array(terminals.keys) { discardTerminal(id) }
        detached.removeAll()
        // The utility views keep their programs: an editor with unsaved work
        // or a running shell is not an attachment that can simply be remade.
        for terminal in viewTerminals.values { terminal.view.isHidden = true }
        closeSwitcher(restoring: false)
        inspector.discardContext()
        awaited = nil
        leaveRecovery()
        content = .empty
        reportedShowing = nil
        reportedOtherShowing = nil
        delegate?.workspaceVisibilityChanged(false)
    }

    /// Kept for the next launch. A window closed while in full screen, or
    /// one the app is quitting with, keeps what it had: neither is the
    /// operator leaving full screen.
    func windowDidEnterFullScreen(_ notification: Notification) {
        guard isOpen else { return }
        delegate?.workspaceFullScreenChanged(true)
    }

    func windowDidExitFullScreen(_ notification: Notification) {
        guard isOpen else { return }
        delegate?.workspaceFullScreenChanged(false)
    }

    /// At launch, for a window that was in full screen when the app last ran.
    func enterFullScreen() {
        guard isOpen, !window.styleMask.contains(.fullScreen) else { return }
        window.toggleFullScreen(nil)
    }

    func windowDidBecomeKey(_ notification: Notification) {
        guard isOpen else { return }
        reportedShowing = showing
        if let showing { delegate?.workspaceShowing(showing) }
        if let otherShowing { delegate?.workspaceShowing(otherShowing) }
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
        let previousName = state.selectedRow?.displayName
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
            // Only a live snapshot says a session is gone, and what was kept
            // of it in the orders dragged into with it.
            let kept = order
            order.forget(notAmong: state.rows)
            if order != kept { delegate?.workspaceOrderChanged(order) }
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
        feedLabel.toolTip = notice
        let action = WorkspaceSidebar.daemonAction(store.feed)
        feedButton.title = action?.title ?? ""
        feedButton.isHidden = action == nil
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
        continueRestoringLayout()
        updateContent(focus: false)
    }

    // MARK: Recovery

    func restoreLayout(_ layout: WorkspaceRestoration.Layout, conversation: ConversationIdentity? = nil) {
        var layout = layout
        if layout.other == layout.active { layout.other = nil }
        pendingLayout = layout
        layoutConversation = conversation
        layoutChecking = false
        layoutGeneration += 1
        continueRestoringLayout()
    }

    private func continueRestoringLayout() {
        guard let layout = pendingLayout, !layoutChecking, state.current, isOpen else { return }
        // The active pane goes through restore(_:), including its ownership
        // check. Check the inactive session before allowing it to make a terminal.
        if case let .session(encoded)? = layout.other,
           case let .session(id)? = WorkspaceRestoration.Place(encoded: encoded),
           state.row(.embedded(id)) != nil {
            layoutChecking = true
            let generation = layoutGeneration
            delegate?.sessionStatus(runtimeSessionID: id) { [weak self] status in
                guard let self, generation == self.layoutGeneration, self.pendingLayout != nil else { return }
                self.layoutChecking = false
                if status != .live(clients: 0) { self.detached.insert(id) }
                self.applyLayout(layout)
            }
        } else { applyLayout(layout) }
    }

    private func applyLayout(_ layout: WorkspaceRestoration.Layout) {
        applyingLayout = true
        pendingLayout = nil
        otherPlace = layout.other.flatMap(restorePane)
        activeOnRight = otherPlace != nil && layout.activeOnRight
        restoringFraction = layout.fraction
        if case let .session(encoded) = layout.active, let place = WorkspaceRestoration.Place(encoded: encoded) {
            restore(place, conversation: layoutConversation)
        } else if let active = restorePane(layout.active) {
            state.select(nil)
            viewSelection = nil
            reviewSelected = false
            switch active {
            case .review: reviewSelected = true
            case let .view(id): viewSelection = id
            case let .session(key): state.select(key)
            }
        }
        applyingLayout = false
        reloadSidebar()
        updateContent(focus: true)
    }

    private func restorePane(_ pane: WorkspaceRestoration.Pane) -> Place? {
        switch pane {
        case let .session(encoded):
            return WorkspaceRestoration.Place(encoded: encoded)?.sessionKey.map(Place.session)
        case let .utility(view):
            guard !view.isOverlay else { return nil }
            views.open(view)
            if !view.isPermanent { endedViews.insert(view.id) }
            return .view(view.id)
        case let .review(directory):
            guard let review = makeRestoredReview?(directory) else { return nil }
            reviewView = review.view
            reviewCanClose = review.canClose
            reviewClosed = review.close
            reviewDirectory = review.directory
            return .review
        }
    }

    private func savedPane(_ place: Place?) -> WorkspaceRestoration.Pane? {
        switch place {
        case let .session(key)?:
            return .session((key.runtimeSessionID.isEmpty
                ? WorkspaceRestoration.Place.kitty(socket: key.socket, windowID: key.windowID)
                : .session(runtimeSessionID: key.runtimeSessionID)).encoded)
        case let .view(id)?: return views.view(id).flatMap { $0.isOverlay ? nil : .utility($0) }
        case .review?: return reviewDirectory().map(WorkspaceRestoration.Pane.review)
        case nil: return nil
        }
    }

    private func reportLayout() {
        guard isOpen, pendingLayout == nil, !applyingLayout else { return }
        let active = restoring.map { WorkspaceRestoration.Pane.session($0.encoded) }
            ?? savedPane(place)
            ?? gone.map { .session(WorkspaceRestoration.Place.session(runtimeSessionID: $0.runtimeSessionID).encoded) }
        if let active { onLayoutChanged(.init(active: active, other: savedPane(otherPlace), activeOnRight: activeOnRight, fraction: paneLayout.fraction)) }
    }

    /// Comes back to where the workspace was when the app last ran. A view is
    /// shown at once. A session is shown once a live snapshot lists it and
    /// amx confirms nobody else attached to it meanwhile; until then the pane
    /// says it is waiting. A session that is not running any more is reported
    /// as exited or lost, with the conversation saved for it when there is
    /// one. Nothing is ever started.
    func restore(_ place: WorkspaceRestoration.Place, conversation: ConversationIdentity? = nil) {
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
            shownConversation = nil
            if case let .session(id) = place, let conversation {
                shownConversation = (id, conversation)
            }
            reportedConversation = shownConversation?.conversation
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
        let name = state.row(key)?.displayName ?? "The session this window was on"
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
            case .exited, .lost, .ended, .unknownSession:
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
        gone = (id, name, known, shownConversation?.runtimeSessionID == id ? shownConversation?.conversation : nil)
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

    /// Whether the pane is somewhere: on a session or a view, coming back to
    /// one, or saying what became of one. Opening the window on nothing in
    /// particular must leave such a place alone.
    var hasPlace: Bool {
        reviewSelected || state.selection != nil || viewSelection != nil || restoring != nil || gone != nil
    }

    /// Lands the pane on the Dashboard when it has no place, as opening the
    /// window on nothing in particular does.
    func showDashboardUnlessPlaced() {
        guard !hasPlace else { return }
        showView(WorkspaceUtilityView.dashboard.id)
    }

    /// The operator went somewhere: whatever the pane was waiting to say or
    /// to come back to is over.
    private func leaveRecovery() {
        if !applyingLayout {
            pendingLayout = nil
            layoutChecking = false
            layoutGeneration += 1
        }
        statusQuestion += 1
        restoring = nil
        restoreChecking = false
        gone = nil
    }

    /// Tells the app where the pane is, when that changed. While a restore
    /// is pending the place it is heading for stays the reported one.
    private func reportPlace() {
        guard isOpen else { return }
        reportLayout()
        let now: WorkspaceRestoration.Place?
        if let restoring {
            now = restoring
        } else if reviewSelected {
            now = reportedPlace
        } else if let viewSelection {
            now = views.view(viewSelection)?.isPermanent == true ? .view(id: viewSelection) : reportedPlace
        } else if let key = state.selection {
            now = key.runtimeSessionID.isEmpty
                ? .kitty(socket: key.socket, windowID: key.windowID)
                : .session(runtimeSessionID: key.runtimeSessionID)
        } else {
            now = nil
        }
        reportConversation(of: now)
        guard now != reportedPlace else { return }
        reportedPlace = now
        delegate?.workspacePlaceChanged(now)
    }

    /// Tells the app the conversation of the session the pane is on, when
    /// that changed. A session whose row stops naming one keeps the last it
    /// had: an agent between two conversations is still that agent.
    private func reportConversation(of place: WorkspaceRestoration.Place?) {
        if case let .session(id)? = place {
            if let conversation = state.row(.embedded(id))?.conversation {
                shownConversation = (id, conversation)
            } else if shownConversation?.runtimeSessionID != id {
                shownConversation = nil
            }
        } else if gone == nil {
            shownConversation = nil
        }
        let now = place == nil ? nil : shownConversation?.conversation
        guard now != reportedConversation else { return }
        reportedConversation = now
        delegate?.workspaceConversationChanged(now)
    }

    /// Selects a listed session and shows what there is to show for it. With
    /// `focus`, keyboard focus moves into its terminal when it has one.
    @discardableResult
    func select(_ key: AttentionCycler.Key?, focus: Bool = false) -> Bool {
        if let key, otherPlace == .session(key) { return focusOtherPane() }
        guard state.select(key) else { return false }
        reviewSelected = false
        viewSelection = nil
        awaited = nil
        leaveRecovery()
        syncSidebarSelection()
        updateContent(focus: focus)
        return true
    }

    /// Focuses a row the way a click does: an embedded session takes the
    /// keyboard here, a Kitty session comes forward in Kitty. With
    /// `reattaching`, which only the operator's own click or Return on the
    /// row asks for, a session whose terminal was detached is attached
    /// again, taking it from whichever terminal has it.
    func activate(_ row: WorkspaceRow, reattaching: Bool = false) {
        switch row.target {
        case let .embedded(id):
            Self.logger.notice("workspace: focus embedded \(id, privacy: .public)")
            if reattaching, detached.remove(id) != nil {
                Self.logger.notice("workspace: reattaching \(id, privacy: .public)")
            }
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

    // MARK: Go to

    /// Whether the Go to… list is up.
    var isSwitching: Bool { !switcher.isHidden }

    /// ⌘P: lists every session and view to go to by name, or puts the list
    /// away when it is up. It goes only where a click on the sidebar would.
    func toggleSwitcher(actions: Bool = false) {
        guard isOpen else { return }
        if isSwitching {
            if actions { switcher.enter("> "); return }
            return closeSwitcher(restoring: true)
        }
        switcherReturn = window.firstResponder
        switcher.show(switcherEntries())
        if actions { switcher.enter("> ") }
        switcher.isHidden = false
        window.makeFirstResponder(switcher.field)
    }

    private func switcherEntries() -> [WorkspaceSwitcher.Entry] {
        let previous: WorkspaceSwitcher.Entry.Place?
        switch recent.before {
        case let .session(key)?: previous = .session(key)
        case let .view(id)?: previous = .view(id)
        case .review?, nil: previous = nil
        }
        return WorkspaceSwitcher.entries(rows: state.rows, views: views.all, names: sessionNames, previous: previous)
            + (delegate?.workspaceCommands ?? []).map { entry in
                var entry = entry
                if case let .command(command) = entry.place {
                    switch command {
                    case .newTerminal, .splitShell, .splitChanges, .history, .changes:
                        if shownSessionRow != nil { entry.relevance = 10 }
                    case .focusOtherPane, .swapPanes, .collapsePane:
                        if isSplit { entry.relevance = 10 }
                    default: break
                    }
                }
                return entry
            }
    }

    func refreshCommands() {
        if isSwitching { switcher.update(switcherEntries()) }
        if case .empty = content { fillPlaceholder(content) }
    }

    func focusWaiting() {
        if window.firstResponder !== inspector { waitingReturn = window.firstResponder }
        setInspectorCollapsed(false)
        setInspectorTab(.waiting)
        inspector.focusReader()
    }

    /// Puts the list away. With `restoring`, the keyboard goes back to what
    /// had it; without, it stays where a click has just put it.
    private func closeSwitcher(restoring: Bool) {
        guard isSwitching else { return }
        switcher.isHidden = true
        let back = switcherReturn
        switcherReturn = nil
        if restoring, let back, (back as? NSView)?.isHiddenOrHasHiddenAncestor != true {
            window.makeFirstResponder(back)
        }
    }

    /// Goes to a place of the list as a click on its sidebar line does.
    private func go(to entry: WorkspaceSwitcher.Entry) {
        closeSwitcher(restoring: true)
        switch entry.place {
        case let .session(key):
            if let row = state.row(key) { activate(row, reattaching: true) }
        case let .view(id):
            showView(id, focus: true)
        case let .command(command):
            delegate?.workspacePerform(command)
        }
    }

    // MARK: Utility views

    /// Shows a utility view in the pane, starting its program if it is not
    /// running. With `focus`, its terminal takes the keyboard.
    @discardableResult
    func showView(_ id: String, focus: Bool = true) -> Bool {
        guard views.view(id) != nil else { return false }
        if otherPlace == .view(id) { return focusOtherPane() }
        reviewSelected = false
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
    /// New Shell (⌘T): always one more shell, never the one already open.
    func openNewShell(in directory: String) {
        open(views.nextShell(in: directory))
    }

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

    /// ⌘W closes a utility view or detaches an embedded session terminal.
    /// Reports whether there was anything to close: a Kitty session is not
    /// the workspace's.
    @discardableResult
    func closeShown() -> Bool {
        if reviewSelected {
            guard reviewCanClose() else { return true }
            if isSplit { _ = focusOtherPane(); collapseOtherPane() }
            else { reviewSelected = false; releaseReview(); showView(WorkspaceUtilityView.dashboard.id) }
            return true
        }
        if closeSelectedView() { return true }
        guard let row = state.selectedRow, row.embeddedRuntimeSessionID != nil else { return false }
        detachSession(row.key)
        return true
    }

    /// Releases only this viewer. The live session stays listed and can be reopened.
    func detachSession(_ key: AttentionCycler.Key) {
        guard let row = state.row(key), let id = row.embeddedRuntimeSessionID, closing[id] == nil else { return }
        detached.insert(id)
        discardTerminal(id)
        if place == .session(key), isSplit { _ = focusOtherPane(); collapseOtherPane() }
        reloadSidebar(force: true)
        updateContent(focus: false)
    }

    /// Closes a utility view, ending its program, whether or not the pane
    /// shows it. A shell, editor, or pager whose program still needs it is
    /// confirmed first when the close confirmation setting is enabled.
    func closeView(_ id: String) {
        guard let view = views.view(id) else { return }
        guard confirmBeforeClosing, needsConfirmBeforeClosing(id) else {
            closeViewNow(id)
            return
        }
        let terminal = viewTerminals[id]?.view
        Self.logger.notice("workspace: closing view \(id, privacy: .public) with its program running; asking")
        (confirmViewClose ?? askToCloseView)(view.displayName) { [weak self] confirmed in
            // Still the same terminal: it may have ended meanwhile.
            guard let self, confirmed, self.viewTerminals[id]?.view === terminal else { return }
            self.closeViewNow(id)
        }
    }

    /// Whether closing the view would end a program that should be asked
    /// about first.
    private func needsConfirmBeforeClosing(_ id: String) -> Bool {
        guard let view = views.view(id), view.asksBeforeClosing, let terminal = viewTerminals[id] else { return false }
        return terminal.needsConfirm()
    }

    /// The titles of the views whose programs should be asked about before
    /// the app quits, in sidebar order.
    var viewsNeedingConfirmation: [String] {
        views.all.filter { needsConfirmBeforeClosing($0.id) }.map(\.displayName)
    }

    /// Asks once, naming every view that needs it, before the app quits.
    /// Reports whether it asked; when it did, `answer` gets the operator's
    /// reply, on the main actor. A closed window keeps its views' programs,
    /// so it asks too.
    func confirmQuit(_ answer: @escaping @MainActor (Bool) -> Void) -> Bool {
        let titles = viewsNeedingConfirmation
        guard !titles.isEmpty else { return false }
        Self.logger.notice("workspace: quitting with \(titles.count) view programs running; asking")
        (confirmQuitWithViews ?? askToQuit)(titles, answer)
        return true
    }

    private func closeViewNow(_ id: String) {
        guard let view = views.view(id) else { return }
        Self.logger.notice("workspace: closing view \(id, privacy: .public)")
        discardViewTerminal(id)
        endedViews.remove(id)
        if !view.isPermanent {
            retire(view)
        } else if viewSelection == id || otherPlace == .view(id) {
            // The pane stays on it and says it closed, with Reopen, as when
            // its program quits on its own.
            endedViews.insert(id)
            updateContent(focus: false)
        }
    }

    /// What ⌘W does where the pane is now, as its menu item says it.
    var closeShownTitle: String {
        if reviewSelected || viewSelection != nil { return Self.closeViewTitle }
        if state.selectedRow?.embeddedRuntimeSessionID != nil { return Self.detachSessionTitle }
        return Self.closeTitle
    }

    static let closeTitle = "Close"
    static let closeViewTitle = "Close View"
    static let endSessionTitle = "End Session…"
    static let detachSessionTitle = "Detach Terminal"

    /// Ends an embedded session's agent, asking first by default: an idle agent's
    /// conversation is kept, but the agent is not. The row stays until a live
    /// snapshot drops it; if the pane was on the session it goes to the
    /// Dashboard.
    func closeSession(_ key: AttentionCycler.Key) {
        guard let row = state.row(key), let id = row.embeddedRuntimeSessionID, closing[id] == nil else { return }
        guard confirmBeforeClosing else {
            endSession(row, id)
            return
        }
        (confirmClose ?? askToClose)(row) { [weak self] confirmed in
            guard let self, confirmed, self.closing[id] == nil, let row = self.state.row(key) else { return }
            self.endSession(row, id)
        }
    }

    /// What ending a session keeps. It names Past Sessions, where the pane
    /// sends the operator once an agent has stopped (`resumeTitle`).
    static let conversationKept = "The conversation stays on disk and can be resumed from Past Sessions."

    private func askToClose(_ row: WorkspaceRow, _ answer: @escaping @MainActor (Bool) -> Void) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "End \(row.displayName)?"
        if row.attention || row.working {
            alert.informativeText = (row.attention ? "Its agent is waiting on you." : "Its agent is working.")
                + " Ending it loses the work in progress. \(Self.conversationKept)"
        } else {
            alert.informativeText = "Its agent is idle and stops when the session ends. \(Self.conversationKept)"
        }
        alert.addButton(withTitle: "End Session").hasDestructiveAction = true
        alert.addButton(withTitle: "Cancel")
        alert.beginSheetModal(for: window) { response in
            MainActor.assumeIsolated { answer(response == .alertFirstButtonReturn) }
        }
    }

    private func sayCloseFailed(_ name: String, _ message: String) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "\(name) could not be ended"
        alert.informativeText = message
        sayLater(alert)
    }

    /// Shows a failure that answered after the operator asked: on the window
    /// while it is open, or on its own when the window was closed meanwhile,
    /// since a sheet on a closed window is never seen.
    private func sayLater(_ alert: NSAlert) {
        if isOpen {
            alert.beginSheetModal(for: window)
        } else {
            NSApp.activate()
            alert.runModal()
        }
    }

    private func endSession(_ row: WorkspaceRow, _ id: String) {
        let name = row.displayName
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
    /// Reports whether it opened one.
    @discardableResult
    func openTerminalBesideSession() -> Bool {
        guard let id = sessionForTerminalBeside else {
            Self.logger.notice("workspace: no embedded session is shown; no terminal opened beside one")
            return false
        }
        open(views.openTerminal(beside: id))
        return true
    }

    /// Whether ⌘↩ has an embedded session to open a terminal beside.
    var canOpenTerminalBesideSession: Bool { sessionForTerminalBeside != nil }

    private var sessionForTerminalBeside: String? {
        let id = views.view(viewSelection)?.besideRuntimeSessionID ?? state.selectedRow?.embeddedRuntimeSessionID
        guard let id, state.row(.embedded(id)) != nil else { return nil }
        return id
    }

    static let terminalNeedsEmbedded = "A terminal opens below a session the workspace embeds; a Kitty session has its terminals in Kitty."

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
    static let nameTerminalTitle = "Name Terminal…"
    static let namingNeedsEmbedded = "Only a session the workspace embeds can be named here; a Kitty session is named by its tab, in Kitty."

    var nameShownTitle: String {
        views.view(viewSelection)?.canName == true ? Self.nameTerminalTitle : Self.nameSessionTitle
    }

    var canNameShown: Bool {
        views.view(viewSelection)?.canName == true || shownSessionRow?.embeddedRuntimeSessionID != nil
    }

    /// ⌘R: names the session, terminal, or shell the pane is on.
    @discardableResult
    func nameShownSession() -> Bool {
        if let view = views.view(viewSelection), view.canName {
            nameTerminal(view.id)
            return true
        }
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
                let title = row.displayName
                if let report = self.reportNameProblem {
                    report(title, problem)
                } else if !AppDelegate.isRunningTests {
                    let alert = NSAlert()
                    alert.alertStyle = .warning
                    alert.messageText = "\(title) could not be named"
                    alert.informativeText = problem
                    self.sayLater(alert)
                }
            }
        }
    }

    private func askForName(_ row: WorkspaceRow, _ answer: @escaping @MainActor (String?) -> Void) {
        askForName(title: row.displayName, name: row.name,
                   explanation: "The name is shown above the session in the sidebar. Leave it empty to remove it.", answer)
    }

    func nameTerminal(_ id: String) {
        guard let view = views.view(id), view.canName else { return }
        let terminal = viewTerminals[id]?.view
        (askTerminalName ?? askForTerminalName)(view) { [weak self] answer in
            guard let self, let answer, self.views.view(id) == view,
                  self.viewTerminals[id]?.view === terminal else { return }
            self.views.name(id, answer)
            guard let named = self.views.view(id), named != view else { return }
            (terminal as? Ghostty.SurfaceView)?.hostName = named.displayName
            self.reloadSidebar()
            self.window.title = self.windowTitle
            self.paneLayout.titles(active: self.title(for: self.place), other: self.title(for: self.otherPlace))
            self.reportLayout()
        }
    }

    private func askForTerminalName(_ view: WorkspaceUtilityView, _ answer: @escaping @MainActor (String?) -> Void) {
        askForName(title: view.displayName, name: view.name ?? "",
                   explanation: "The name is shown in the sidebar. Leave it empty to restore the default name.", answer)
    }

    private func askForName(title: String, name: String, explanation: String, _ answer: @escaping @MainActor (String?) -> Void) {
        let field = NSTextField(string: name)
        field.placeholderString = "No name"
        field.frame = NSRect(x: 0, y: 0, width: 260, height: 24)
        let alert = NSAlert()
        alert.messageText = "Name \(title)"
        alert.informativeText = explanation
        alert.accessoryView = field
        alert.addButton(withTitle: "Name")
        alert.addButton(withTitle: "Cancel")
        alert.window.initialFirstResponder = field
        alert.beginSheetModal(for: window) { response in
            MainActor.assumeIsolated { answer(response == .alertFirstButtonReturn ? field.stringValue : nil) }
        }
    }

    /// The menu a right-click on the session list shows: what can be done to
    /// the line under the pointer. A line with nothing to offer shows no menu
    /// (`WorkspaceSidebarView.hasMenu`).
    func menuWillOpen(_ menu: NSMenu) { sidebarMenuOpen = true }
    func menuDidClose(_ menu: NSMenu) {
        sidebarMenuOpen = false
        reloadSidebar()
    }

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        for item in contextMenuItems(forRow: sidebar.clickedRow) { menu.addItem(item) }
    }

    static let newTerminalTitle = "New Terminal Below"
    static let copyRuntimeSessionIDTitle = "Copy Runtime Session ID"

    /// Takes a copied runtime session ID; the general pasteboard unless a
    /// test says otherwise.
    var copyToPasteboard: (String) -> Void = { value in
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(value, forType: .string)
    }

    func contextMenuItems(forRow index: Int) -> [NSMenuItem] {
        if index < 0 { return listMenuItems() }
        guard index < items.count else { return [] }
        switch items[index] {
        case let .row(row): return state.row(row.key).map(sessionMenuItems) ?? []
        case let .view(view):
            // The Dashboard and the Overview have nothing to close until their
            // program runs.
            guard !view.isPermanent || viewTerminals[view.id] != nil else { return [] }
            let close = NSMenuItem(title: Self.closeTitle, action: #selector(closeClickedView(_:)), keyEquivalent: "")
            close.representedObject = view.id
            close.target = self
            if view.canName {
                let name = NSMenuItem(title: Self.nameTerminalTitle, action: #selector(nameClickedTerminal(_:)), keyEquivalent: "")
                name.representedObject = view.id
                name.target = self
                return [name, close]
            }
            return [close]
        case let .group(group): return groupMenuItems(group)
        case .section: return []
        }
    }

    @objc private func nameClickedTerminal(_ sender: Any?) {
        guard let id = (sender as? NSMenuItem)?.representedObject as? String else { return }
        nameTerminal(id)
    }

    /// The list's empty space: where to go, and what to open, as the main
    /// menu offers them. Each goes to the app, as the main menu's do.
    private func listMenuItems() -> [NSMenuItem] {
        let entries: [(String, Selector)] = [
            ("Go to…", #selector(AppDelegate.showSwitcher(_:))),
            ("New Shell", #selector(AppDelegate.newShellFromMenu(_:))),
            ("Dashboard", #selector(AppDelegate.openDashboardFromMenu(_:))),
            ("Overview", #selector(AppDelegate.openOverviewFromMenu(_:))),
        ]
        return entries.map { title, action in
            let item = NSMenuItem(title: title, action: action, keyEquivalent: "")
            item.target = delegate
            return item
        }
    }

    static let collapseProjectTitle = "Collapse Project"
    static let expandProjectTitle = "Expand Project"
    static let collapsePinnedTitle = "Collapse Pinned"
    static let expandPinnedTitle = "Expand Pinned"
    static let showInKittyTitle = "Show in Kitty"
    static let reattachTitle = "Reattach"

    private func groupMenuItems(_ group: WorkspaceGroup) -> [NSMenuItem] {
        guard !group.needsAttention else { return [] }
        let title = group.pinned
            ? (group.collapsed ? Self.expandPinnedTitle : Self.collapsePinnedTitle)
            : (group.collapsed ? Self.expandProjectTitle : Self.collapseProjectTitle)
        let toggle = NSMenuItem(title: title, action: #selector(toggleClickedGroup(_:)), keyEquivalent: "")
        toggle.representedObject = [group.project, group.pinned] as [Any]
        toggle.target = self
        return [toggle]
    }

    @objc private func toggleClickedGroup(_ sender: Any?) {
        guard let named = (sender as? NSMenuItem)?.representedObject as? [Any],
              let project = named.first as? String, let pinned = named.last as? Bool,
              let group = state.groups.first(where: { !$0.needsAttention && $0.project == project && $0.pinned == pinned })
        else { return }
        setCollapsed(project: project, pinned: pinned, !group.collapsed)
    }

    /// Collapses a project's group, or the pinned one, to its header, or
    /// expands it. It is kept with the order sessions were dragged into, so
    /// it outlasts the window and the app. The pane stays where it is, even
    /// on a session the collapse took off the list.
    func setCollapsed(project: String, pinned: Bool, _ collapsed: Bool) {
        guard order.isCollapsed(project: project, pinned: pinned) != collapsed else { return }
        order.setCollapsed(project: project, pinned: pinned, collapsed)
        state.arrange(order)
        delegate?.workspaceOrderChanged(order)
        reloadSidebar(allowReorder: true)
    }

    private func sessionMenuItems(_ row: WorkspaceRow) -> [NSMenuItem] {
        let name = NSMenuItem(title: Self.nameSessionTitle, action: #selector(nameClickedSession(_:)), keyEquivalent: "")
        name.representedObject = row.key
        if row.embeddedRuntimeSessionID != nil {
            name.target = self
        } else {
            // No target and no action: the item is listed, greyed out.
            name.action = nil
            name.isEnabled = false
            name.toolTip = Self.namingNeedsEmbedded
        }
        // The inspector's Color…, Icon…, and Mute, for the line under the
        // pointer rather than the session the pane is on.
        let looks = [WorkspaceInspectorAction.color, .icon, .mute].map { action in
            let item = NSMenuItem(title: action.title(muted: row.muted), action: #selector(clickedSessionAction(_:)), keyEquivalent: "")
            item.representedObject = [row.key, action] as [Any]
            if row.embeddedRuntimeSessionID != nil || !action.needsEmbedded {
                item.target = self
            } else {
                item.action = nil
                item.isEnabled = false
                item.toolTip = WorkspaceInspectorView.kittyOnly
            }
            return item
        }
        let pin = NSMenuItem(title: row.pinned ? Self.unpinSessionTitle : Self.pinSessionTitle,
                             action: #selector(pinClickedSession(_:)), keyEquivalent: "")
        pin.representedObject = row.key
        pin.target = self
        guard let id = row.embeddedRuntimeSessionID else {
            // What a click on the line does: the window comes forward in Kitty.
            let show = NSMenuItem(title: Self.showInKittyTitle, action: #selector(activateClickedSession(_:)), keyEquivalent: "")
            show.representedObject = row.key
            show.target = self
            return [name] + looks + [pin, .separator(), show]
        }
        // What the × and the Session menu do, for the line under the pointer.
        let terminal = NSMenuItem(title: Self.newTerminalTitle, action: #selector(openTerminalBesideClickedSession(_:)), keyEquivalent: "")
        terminal.representedObject = id
        terminal.target = self
        let copy = NSMenuItem(title: Self.copyRuntimeSessionIDTitle, action: #selector(copyClickedRuntimeSessionID(_:)), keyEquivalent: "")
        copy.representedObject = id
        copy.target = self
        let end = NSMenuItem(title: Self.endSessionTitle, action: #selector(endClickedSession(_:)), keyEquivalent: "")
        end.representedObject = row.key
        end.target = self
        end.isEnabled = closing[id] == nil
        guard detached.contains(id) else { return [name] + looks + [pin, .separator(), terminal, copy, .separator(), end] }
        // What a click or Return on the line does: attach again, taking the
        // session from whichever terminal has it.
        let reattach = NSMenuItem(title: Self.reattachTitle, action: #selector(reattachClickedSession(_:)), keyEquivalent: "")
        reattach.representedObject = row.key
        reattach.target = self
        return [name] + looks + [pin, .separator(), reattach, terminal, copy, .separator(), end]
    }

    @objc private func clickedSessionAction(_ sender: Any?) {
        guard let named = (sender as? NSMenuItem)?.representedObject as? [Any],
              let key = named.first as? AttentionCycler.Key, let action = named.last as? WorkspaceInspectorAction,
              let row = state.row(key)
        else { return }
        switch action {
        case .color: openPicker(.colorPicker, for: row)
        case .icon: openPicker(.iconPicker, for: row)
        case .mute: delegate?.workspaceToggleMute(row)
        case .name, .clearState, .markActive, .reviewChanges: return
        }
    }

    @objc private func activateClickedSession(_ sender: Any?) {
        guard let key = (sender as? NSMenuItem)?.representedObject as? AttentionCycler.Key, let row = state.row(key) else { return }
        activate(row)
    }

    @objc private func reattachClickedSession(_ sender: Any?) {
        guard let key = (sender as? NSMenuItem)?.representedObject as? AttentionCycler.Key, let row = state.row(key) else { return }
        activate(row, reattaching: true)
    }

    @objc private func closeClickedView(_ sender: Any?) {
        guard let id = (sender as? NSMenuItem)?.representedObject as? String else { return }
        closeView(id)
    }

    @objc private func openTerminalBesideClickedSession(_ sender: Any?) {
        guard let id = (sender as? NSMenuItem)?.representedObject as? String, state.row(.embedded(id)) != nil else { return }
        open(views.openTerminal(beside: id))
    }

    @objc private func copyClickedRuntimeSessionID(_ sender: Any?) {
        guard let id = (sender as? NSMenuItem)?.representedObject as? String else { return }
        copyToPasteboard(id)
    }

    @objc private func endClickedSession(_ sender: Any?) {
        guard let key = (sender as? NSMenuItem)?.representedObject as? AttentionCycler.Key else { return }
        closeSession(key)
    }

    @objc private func pinClickedSession(_ sender: Any?) {
        guard let key = (sender as? NSMenuItem)?.representedObject as? AttentionCycler.Key, let row = state.row(key) else { return }
        setPinned(key, !row.pinned)
    }

    // MARK: Pinning

    static let pinSessionTitle = "Pin to Top"
    static let unpinSessionTitle = "Unpin"
    /// The Session menu's item, which says which of the two it does.
    static let pinShownSessionTitle = "Pin Session"
    static let unpinShownSessionTitle = "Unpin Session"

    /// What the Session menu's pin item does to the session the pane is on.
    var pinShownTitle: String {
        shownSessionRow?.pinned == true ? Self.unpinShownSessionTitle : Self.pinShownSessionTitle
    }

    /// Pins a listed session, which then leaves its project for the Pinned
    /// group above them all, or unpins it, which puts it back. Any session
    /// can be pinned, a Kitty one too: a pin is a place in this list.
    func setPinned(_ key: AttentionCycler.Key, _ pinned: Bool) {
        guard let row = state.row(key), row.pinned != pinned else { return }
        // Only a live snapshot says a session is gone, and its pin with it.
        if state.current { order.forgetPins(notAmong: state.rows.map(\.key)) }
        order.setPinned(key, pinned)
        state.arrange(order)
        delegate?.workspaceOrderChanged(order)
        reloadSidebar()
    }

    /// Pins the session the pane is on, or unpins it when it is pinned, and
    /// reports whether there was one.
    @discardableResult
    func togglePinOfShownSession() -> Bool {
        guard let row = shownSessionRow else { return false }
        setPinned(row.key, !row.pinned)
        return true
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
        guard let row = shownSessionRow, openPicker(kind, for: row) else {
            Self.logger.notice("workspace: no embedded session is shown; no picker opened")
            return false
        }
        return true
    }

    /// Opens the icon or color picker for a listed embedded session, and
    /// reports whether it is one.
    @discardableResult
    func openPicker(_ kind: WorkspaceUtilityView.Kind, for row: WorkspaceRow) -> Bool {
        guard let id = row.embeddedRuntimeSessionID,
              let picker = WorkspaceUtilityView.picker(kind, runtimeSessionID: id) else { return false }
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
        case .review?:
            guard reviewView != nil else { return false }
            if otherPlace == .review { return focusOtherPane() }
            reviewSelected = true
            state.select(nil)
            viewSelection = nil
            updateContent(focus: true)
        case nil:
            return false
        }
        return true
    }

    /// Where the pane is now.
    private var place: Place? {
        if reviewSelected { return .review }
        if let viewSelection { return .view(viewSelection) }
        return state.selection.map(Place.session)
    }

    /// Unlists an opened view and goes back to where the pane was before it.
    private func retire(_ view: WorkspaceUtilityView) {
        views.close(view.id)
        let back = returnPlaces.removeValue(forKey: view.id)
        reloadSidebar()
        if otherPlace == .view(view.id) {
            otherPlace = nil
            otherContent = .empty
            updateContent(focus: false)
        }
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
        ringingViews.remove(id)
        rendererFailedViews.remove(id)
        programTitles[id] = nil
        searches[id] = nil
        searchGenerations[id] = nil
        scrollbars[id] = nil
        progresses[id] = nil
        if isOpen, let responder = window.firstResponder as? NSView, responder.isDescendant(of: terminal.view) {
            focusListOrPane()
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
        switch neighborStop(offset, skipping: raising || sessionsOnly) {
        case let .row(row)?: go(to: row, raising: raising)
        case let .view(view)?: showView(view.id, focus: true)
        case .section?, .group?, nil: break
        }
    }

    /// The project of the active session or one of its terminals. Global
    /// utility views have no project, even if a session was shown before.
    private var tabProject: String? {
        guard !reviewSelected else { return nil }
        if let viewSelection {
            return views.view(viewSelection)?.besideRuntimeSessionID
                .flatMap { state.row(.embedded($0))?.project }
        }
        return state.selectedRow?.project
    }

    var canSelectProjectTab: Bool { tabProject != nil }

    /// ⌘← / ⌘→ wrap through this project's sessions and their terminals in
    /// sidebar order, including pinned, promoted, and collapsed sessions.
    func selectProjectTab(_ offset: Int) {
        guard let project = tabProject else { return }
        let stops = state.rows.filter { $0.project == project }.flatMap { row in
            [Item.row(row)] + views.terminals(beside: row.embeddedRuntimeSessionID).map(Item.view)
        }
        guard let here = stops.firstIndex(where: { item in
            switch item {
            case let .row(row): return viewSelection == nil && row.key == state.selection
            case let .view(view): return view.id == viewSelection
            case .section, .group: return false
            }
        }) else { return }
        let next = stops[((here + offset) % stops.count + stops.count) % stops.count]
        let key: AttentionCycler.Key?
        switch next {
        case let .row(row): key = row.key
        case let .view(view): key = view.besideRuntimeSessionID.map { .embedded($0) }
        case .section, .group: return
        }
        if let group = group(of: key), group.collapsed {
            setCollapsed(project: group.project, pinned: group.pinned, false)
        }
        switch next {
        case let .row(row): go(to: row, raising: false)
        case let .view(view): showView(view.id, focus: true)
        case .section, .group: break
        }
    }

    /// ↑ and ↓ in the session list while none of its lines is selected,
    /// because the pane's session, or the terminal beside it, was collapsed
    /// away: the step is from there, and stays in the list as an arrow key
    /// does. False when the pane is on nothing, which leaves the key to the
    /// list.
    func stepFromUnlistedPlace(_ offset: Int) -> Bool {
        guard place != nil else { return false }
        switch neighborStop(offset, skipping: false) {
        case let .row(row)?: select(row.key)
        case let .view(view)?: showView(view.id, focus: false)
        case .section?, .group?, nil: return false
        }
        return true
    }

    /// The line `offset` steps from where the pane is; see `selectNeighbor`.
    private func neighborStop(_ offset: Int, skipping: Bool) -> Item? {
        let listed = state.embeddedRuntimeSessionIDs
        // A terminal beside a session counts as standing just below it when
        // terminals are passed over, or when its line was collapsed away.
        let shownView = views.view(viewSelection)
        let viewListed = shownView.map { items.contains(.view($0)) } ?? false
        let beside = skipping || !viewListed ? shownView?.besideRuntimeSessionID : nil
        // A collapsed project lists none of its sessions, but the one the
        // pane is on still stands where its row would be, so the step is from
        // there rather than from the top.
        let stops = items.flatMap { item -> [Item] in
            switch item {
            case .row: return [item]
            case let .view(view): return !skipping && view.besideRuntimeSessionID.map(listed.contains) ?? false ? [item] : []
            case let .group(group) where group.collapsed:
                return group.rows.filter { row in
                    if let beside { return row.embeddedRuntimeSessionID == beside }
                    return viewSelection == nil && row.key == state.selection
                }.map(Item.row)
            case .section, .group: return []
            }
        }
        guard !stops.isEmpty else { return nil }
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
        return stops[next]
    }

    /// Goes to the session at `index` in sidebar order; nothing when fewer
    /// sessions are listed. A Kitty row stays in the app unless `raising`.
    /// The sessions of a collapsed project keep their places, so going to
    /// one expands its project.
    func selectSession(at index: Int, raising: Bool = false) {
        let rows = state.rows
        guard rows.indices.contains(index) else { return }
        let row = rows[index]
        if let group = group(of: row.key), group.collapsed {
            setCollapsed(project: group.project, pinned: group.pinned, false)
        }
        go(to: row, raising: raising)
    }

    /// The group a session is listed in.
    private func group(of key: AttentionCycler.Key?) -> WorkspaceGroup? {
        guard let key else { return nil }
        return state.groups.first { $0.rows.contains { $0.key == key } }
    }

    /// Whether the session at `index` in sidebar order lives in Kitty, which
    /// ⌘⇧ and that digit bring forward there.
    func sessionIsInKitty(at index: Int) -> Bool {
        let rows = state.rows
        guard rows.indices.contains(index), case .kitty = rows[index].target else { return false }
        return true
    }

    /// Whether any of the sessions the digits go to lives in Kitty: without
    /// one, the Session menu has nothing to show in Kitty.
    var digitsReachKitty: Bool {
        WorkspaceMenu.sessionKeys.indices.contains { sessionIsInKitty(at: $0) }
    }

    /// The name of the session at `index` in sidebar order, which ⌘ and
    /// that digit go to; nil when fewer sessions are listed.
    func sessionName(at index: Int) -> String? {
        let rows = state.rows
        return rows.indices.contains(index) ? rows[index].displayName : nil
    }

    /// What the Session menu's place items say: the session they go to, or
    /// only their number when there is none there.
    static func sessionItemTitle(at index: Int, name: String?, inKitty: Bool = false) -> String {
        let place = "Session \(index + 1)" + (inKitty ? " in Kitty" : "")
        guard let name else { return place }
        return "\(place): \(name)"
    }

    /// The digit that goes to a session with ⌘, which its line shows; nil
    /// past the tenth, which has none.
    func sessionKey(of key: AttentionCycler.Key) -> String? {
        guard let index = state.rows.firstIndex(where: { $0.key == key }),
              WorkspaceMenu.sessionKeys.indices.contains(index) else { return nil }
        return WorkspaceMenu.sessionKeys[index]
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

    /// A terminal asked to be closed. A utility view whose program Ghostty
    /// says is still running (`needsConfirm`) asks first when confirmation
    /// is enabled; an attachment's close only detaches, so it is never
    /// asked about.
    func terminalAskedToClose(_ view: NSView, needsConfirm: Bool) {
        guard confirmBeforeClosing, needsConfirm, let id = viewTerminals.first(where: { $0.value.view === view })?.key,
              let utility = views.view(id), utility.asksBeforeClosing else {
            terminalEnded(view)
            return
        }
        Self.logger.notice("workspace: view \(id, privacy: .public) asked to close with its program running; asking")
        (confirmViewClose ?? askToCloseView)(utility.displayName) { [weak self] confirmed in
            // Still the same terminal: it may have ended meanwhile.
            guard let self, confirmed, self.viewTerminals[id]?.view === view else { return }
            self.utilityEnded(id)
        }
    }

    private func askToCloseView(_ title: String, _ answer: @escaping @MainActor (Bool) -> Void) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "Close the view \(title)?"
        alert.informativeText = "Closing the view ends that process. No session is affected."
        alert.addButton(withTitle: "Close View").hasDestructiveAction = true
        alert.addButton(withTitle: "Cancel")
        alert.beginSheetModal(for: window) { response in
            MainActor.assumeIsolated { answer(response == .alertFirstButtonReturn) }
        }
    }

    private func askToQuit(_ titles: [String], _ answer: @escaping @MainActor (Bool) -> Void) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = WorkspaceViews.quitQuestion(titles)
        alert.informativeText = "Quitting ends those processes. Sessions live on and are shown again at the next launch."
        alert.addButton(withTitle: "Quit").hasDestructiveAction = true
        alert.addButton(withTitle: "Cancel")
        guard isOpen else {
            // No window to hang a sheet on, and opening one to ask would be
            // saved as open for the next launch: an alert of its own instead,
            // once the app has been told the answer is coming.
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    NSApp.activate()
                    answer(alert.runModal() == .alertFirstButtonReturn)
                }
            }
            return
        }
        alert.beginSheetModal(for: window) { response in
            MainActor.assumeIsolated { answer(response == .alertFirstButtonReturn) }
        }
    }

    // MARK: Terminal events

    /// The terminal the pane shows, if it shows one.
    var shownTerminalView: NSView? {
        switch content {
        case let .terminal(id): return terminals[id]?.view
        case let .view(id): return viewTerminals[id]?.view
        default: return nil
        }
    }

    private func sessionID(of view: NSView) -> String? {
        terminals.first(where: { $0.value.view === view })?.key
    }

    private func viewID(of view: NSView) -> String? {
        viewTerminals.first(where: { $0.value.view === view })?.key
    }

    /// What a bell does, given where it rang.
    struct BellResponse: Equatable {
        /// Mark the line of the session or view it rang in.
        var mark: Bool
        var beep: Bool
        /// Ask the Dock for attention, once.
        var requestAttention: Bool

        /// A bell in the terminal shown is heard, and only when the app is
        /// in front and Ghostty's `bell-features` include `system`; one in a
        /// hidden terminal marks its line instead. Either asks for attention
        /// while another app is in front.
        static func of(shown: Bool, appActive: Bool, systemBell: Bool) -> BellResponse {
            BellResponse(mark: !shown, beep: shown && appActive && systemBell, requestAttention: !appActive)
        }
    }

    /// A program rang the bell in one of the workspace's terminals.
    func terminalRangBell(_ view: NSView, systemBell: Bool) {
        let session = sessionID(of: view)
        let utility = session == nil ? viewID(of: view) : nil
        guard session != nil || utility != nil else { return }
        let shown = isOpen && (view === shownTerminalView || view === visibleView(otherContent))
        let response = BellResponse.of(shown: shown, appActive: isAppActive(), systemBell: systemBell)
        if response.beep { beep() }
        if response.requestAttention { requestAttention() }
        guard response.mark else { return }
        if let session { ringingSessions.insert(session) }
        if let utility { ringingViews.insert(utility) }
        reloadLines()
        bellCount += 1
        let count = bellCount
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.bellHold) { [weak self] in
            guard let self, self.bellCount == count else { return }
            self.clearBells(all: true)
        }
    }

    /// Takes the bell mark off what the pane shows, or off every line.
    private func clearBells(all: Bool = false) {
        var changed = false
        if all {
            changed = !ringingSessions.isEmpty || !ringingViews.isEmpty
            ringingSessions.removeAll()
            ringingViews.removeAll()
        } else {
            for visible in [content, otherContent] {
                switch visible {
                case let .terminal(id): changed = (ringingSessions.remove(id) != nil) || changed
                case let .view(id): changed = (ringingViews.remove(id) != nil) || changed
                default: break
                }
            }
        }
        if changed { reloadLines() }
    }

    /// Redraws the sidebar's lines in place, keeping its selection.
    private func reloadLines() {
        guard sidebar.numberOfRows > 0 else { return }
        reload(IndexSet(integersIn: 0..<sidebar.numberOfRows))
    }

    /// Redraws the given lines in place, keeping the selection, and gives
    /// each its session's color again: the row views stay.
    private func reload(_ indexes: IndexSet) {
        applying = true
        sidebar.reloadData(forRowIndexes: indexes, columnIndexes: IndexSet(integer: 0))
        applying = false
        for index in indexes {
            guard let rowView = sidebar.rowView(atRow: index, makeIfNecessary: false) as? WorkspaceRowView else { continue }
            rowView.tabColor = Self.tabColor(of: items[index])
            rowView.selectionBorder.color = selectionColor(of: items[index]) ?? .controlAccentColor
        }
        syncHover(indexes)
    }

    /// A line drawn again is a fresh cell that knows nothing of the
    /// pointer: its × follows where the pointer is now. The other lines'
    /// own tracking areas already know.
    private func syncHover(_ indexes: IndexSet) {
        guard NSApp.isActive, let window = sidebar.window, window.isVisible else { return }
        syncHover(at: window.mouseLocationOutsideOfEventStream, lines: indexes)
    }

    /// Hovers whichever of `lines` is under `point`, in window
    /// coordinates, and none of the others.
    func syncHover(at point: NSPoint, lines: IndexSet) {
        let inList = sidebar.visibleRect.contains(sidebar.convert(point, from: nil))
        for index in lines where index < sidebar.numberOfRows {
            guard let cell = sidebar.view(atColumn: 0, row: index, makeIfNecessary: false) as? WorkspaceRowCellView else { continue }
            cell.isHovered = inList && cell.bounds.contains(cell.convert(point, from: nil))
        }
    }

    /// A utility view's program set its title. A session's terminal shows
    /// what tmux titles it, which says nothing the sidebar does not.
    func terminalTitleChanged(_ view: NSView, title: String) {
        guard let id = viewID(of: view) else { return }
        guard programTitles[id] != title else { return }
        programTitles[id] = title.isEmpty ? nil : title
        reloadLines()
        if viewSelection == id { window.title = windowTitle }
    }

    /// The window's title: the view or session the pane is on, and for a
    /// view the title its program set.
    private var windowTitle: String {
        if reviewSelected { return "Checkout Changes — Agent Mux" }
        if let view = views.view(viewSelection) {
            guard let program = programTitles[view.id], program != view.displayName else { return view.displayName }
            return "\(view.displayName) — \(program)"
        }
        return state.selectedRow?.displayName ?? "Agent Mux"
    }

    /// Under the title, a session's line from the sidebar (harness, branch,
    /// age, why it waits), so the window says it with the sidebar hidden.
    /// A view's title already says what its program does.
    private var windowSubtitle: String {
        guard viewSelection == nil else { return "" }
        return state.selectedRow?.detail ?? ""
    }

    /// The pointer moved onto a link, or off one, in a terminal.
    func terminalHoveredLink(_ view: NSView, url: String?) {
        guard view === shownTerminalView else { return }
        showLink(url)
    }

    static let copiedNotice = "Copied to the clipboard"
    private var copiedNoticeEnd: DispatchWorkItem?

    /// The shown terminal put text on the clipboard. Where a selection made
    /// with the mouse is tmux's and is copied when the button is let go,
    /// nothing on screen says so; the link line does, for a moment.
    func terminalCopied(_ view: NSView) {
        guard view === shownTerminalView else { return }
        showLink(Self.copiedNotice)
        NSAccessibility.post(element: window, notification: .announcementRequested,
                             userInfo: [.announcement: Self.copiedNotice, .priority: NSAccessibilityPriorityLevel.medium.rawValue])
        copiedNoticeEnd?.cancel()
        let end = DispatchWorkItem { [weak self] in
            MainActor.assumeIsolated { self?.endCopiedNotice() }
        }
        copiedNoticeEnd = end
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5, execute: end)
    }

    func endCopiedNotice() {
        copiedNoticeEnd?.cancel()
        copiedNoticeEnd = nil
        guard linkLabel.stringValue == Self.copiedNotice else { return }
        showLink((shownTerminalView as? Ghostty.SurfaceView)?.hoveredLink)
    }

    private func showLink(_ url: String?) {
        linkLabel.stringValue = url ?? ""
        linkLabel.isHidden = url == nil
    }

    /// The utility view the pane shows, and its terminal.
    private var shownUtility: (id: String, view: NSView)? {
        guard case let .view(id) = content, let view = viewTerminals[id]?.view else { return nil }
        return (id, view)
    }

    /// Shows the find bar and the scrollbar of the utility view the pane is
    /// on, and neither for anything else.
    private func syncOverlays() {
        let shown = shownUtility
        let search = shown.flatMap { searches[$0.id] }
        findBar.context = shown.flatMap { shown in
            search == nil ? nil : AnyHashable("\(shown.id)#\(searchGenerations[shown.id] ?? 0)")
        }
        if let search { findBar.show(search) }
        findBar.isHidden = search == nil
        scrollbarView.terminal = shown?.view
        scrollbarView.scrollbar = shown.flatMap { scrollbars[$0.id] }
    }

    /// A search began, ended, or was counted in a utility view's buffer.
    /// A search asked for again takes the find bar's field. One begun from
    /// a selection only names its needle (`search_selection`), so it is
    /// searched for here, in place of whatever that view searched before.
    func terminalSearch(_ view: NSView, _ update: Ghostty.SearchUpdate) {
        guard let id = viewID(of: view) else { return }
        let hadKeyboard = findBar.hasKeyboard
        let wasOpen = searches[id] != nil
        searches[id] = Ghostty.Search.applying(update, to: searches[id])
        if wasOpen != (searches[id] != nil) {
            searchGeneration += 1
            searchGenerations[id] = searchGeneration
        }
        var seed: String?
        if case let .started(needle) = update, !needle.isEmpty {
            _ = performTerminalAction(view, "search:\(needle)")
            seed = needle
        }
        guard shownUtility?.view === view else { return }
        if let seed { findBar.seed(seed) }
        syncOverlays()
        switch update {
        case .started: findBar.focus()
        case .ended where hadKeyboard: window.makeFirstResponder(view)
        default: break
        }
    }

    /// The find bar's needle changed, or a match was asked for.
    func findBarNeedleChanged(_ needle: String) {
        guard let shown = shownUtility, searches[shown.id] != nil else { return }
        searches[shown.id]?.needle = needle
        _ = performTerminalAction(shown.view, "search:\(needle)")
    }

    func findBarNavigate(next: Bool) {
        guard let shown = shownUtility, searches[shown.id] != nil else { return }
        _ = performTerminalAction(shown.view, next ? "navigate_search:next" : "navigate_search:previous")
    }

    /// Closes the find bar and ends the search; the terminal takes the
    /// keyboard back if the bar had it.
    func closeFindBar() {
        guard let shown = shownUtility else { return }
        _ = performTerminalAction(shown.view, "end_search")
        terminalSearch(shown.view, .ended)
        if window.firstResponder === window || (window.firstResponder as? NSView)?.isHiddenOrHasHiddenAncestor == true {
            window.makeFirstResponder(shown.view)
        }
    }

    /// Where a utility view's viewport sits in its buffer changed.
    func terminalScrollbar(_ view: NSView, _ scrollbar: Ghostty.Scrollbar?) {
        guard let id = viewID(of: view) else { return }
        scrollbars[id] = scrollbar
        if shownUtility?.view === view { scrollbarView.scrollbar = scrollbar }
    }

    /// The scrollbar's knob was moved.
    func scrollbarMoved(toRow row: UInt64) {
        guard let shown = shownUtility else { return }
        _ = performTerminalAction(shown.view, "scroll_to_row:\(row)")
    }

    /// A utility view's program reported progress, or its report went. Shown
    /// on the view's line, never as attention: a session's terminal reports
    /// none here.
    func terminalProgress(_ view: NSView, _ progress: Ghostty.Progress?) {
        guard let id = viewID(of: view), progresses[id] != progress else { return }
        progresses[id] = progress
        reloadLines()
    }

    /// A terminal's renderer failed or recovered. A terminal that draws
    /// nothing is set aside for a placeholder: a session's offers to
    /// reattach, a utility view's to restart its program.
    func terminalRendererHealthChanged(_ view: NSView, healthy: Bool) {
        let changed: Bool
        if let id = sessionID(of: view) {
            Self.logger.notice("workspace: the renderer of \(id, privacy: .public) is \(healthy ? "healthy again" : "unhealthy", privacy: .public)")
            changed = healthy ? rendererFailed.remove(id) != nil : rendererFailed.insert(id).inserted
        } else if let id = viewID(of: view) {
            Self.logger.notice("workspace: the renderer of view \(id, privacy: .public) is \(healthy ? "healthy again" : "unhealthy", privacy: .public)")
            changed = healthy ? rendererFailedViews.remove(id) != nil : rendererFailedViews.insert(id).inserted
        } else {
            return
        }
        if changed { updateContent(focus: false) }
    }

    /// A program asked for a desktop notification.
    func terminalNotification(_ view: NSView, title: String, body: String) {
        let notice: TerminalNotice
        if let id = sessionID(of: view) {
            notice = TerminalNotice(title: title, body: body, source: sessionNames[id] ?? id, origin: .session(runtimeSessionID: id))
        } else if let id = viewID(of: view), let utility = views.view(id) {
            notice = TerminalNotice(title: title, body: body, source: utility.displayName, origin: .view(id: id))
        } else {
            return
        }
        delegate?.workspaceTerminalNotification(notice, onScreen: window.isKeyWindow && (notice.origin == showing || notice.origin == otherShowing))
    }

    /// The terminal the pane shows, as a terminal notice names it.
    var showing: TerminalNotice.Origin? {
        guard isOpen else { return nil }
        switch content {
        case let .terminal(id): return .session(runtimeSessionID: id)
        case let .view(id): return .view(id: id)
        default: return nil
        }
    }

    /// Whether a terminal notice's origin is being looked at: shown in the
    /// pane of the key window, where a banner would only repeat it.
    static func onScreen(_ origin: TerminalNotice.Origin, showing: TerminalNotice.Origin?, key: Bool) -> Bool {
        key && origin == showing
    }

    /// Tells the app which terminal the pane shows, when that changed.
    private func reportShowing() {
        let now = showing
        let other = otherShowing
        if now != reportedShowing, let now { delegate?.workspaceShowing(now) }
        if other != reportedOtherShowing, let other { delegate?.workspaceShowing(other) }
        reportedShowing = now
        reportedOtherShowing = other
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

    /// A new default zoom resets every open terminal; the sessions whose
    /// terminal was let go come back at it too, not at the zoom they had.
    func forgetFontSizes() {
        fontSizes.removeAll()
    }

    private func discardTerminal(_ id: String) {
        guard let terminal = terminals.removeValue(forKey: id) else { return }
        // The session's next terminal comes back at the zoom this one had.
        if let size = terminal.fontSize() { fontSizes[id] = size }
        ringingSessions.remove(id)
        rendererFailed.remove(id)
        if isOpen, let responder = window.firstResponder as? NSView, responder.isDescendant(of: terminal.view) {
            focusListOrPane()
        }
        terminal.view.removeFromSuperview()
        terminal.close()
    }

    private static func describe(_ key: AttentionCycler.Key?) -> String {
        guard let key else { return "none" }
        return key.runtimeSessionID.isEmpty ? "kitty window \(key.windowID)" : key.runtimeSessionID
    }

    // MARK: Pane

    /// Preserve the current place on the left and select a different live
    /// session on the right. The original terminal remains attached exactly once.
    @discardableResult
    func split(with key: AttentionCycler.Key) -> Bool {
        guard isOpen, state.row(key)?.embeddedRuntimeSessionID != nil,
              place != .session(key), prepareSplit() else { return false }
        return select(key, focus: true)
    }

    @discardableResult
    func splitWithShell() -> Bool {
        guard canOpenTerminalBesideSession, prepareSplit() else { return false }
        return openTerminalBesideSession()
    }

    @discardableResult
    func splitWithReview(_ view: NSView, canClose: @escaping () -> Bool, onClose: @escaping () -> Void) -> Bool {
        guard prepareSplit() else { return false }
        releaseReview()
        reviewView = view
        reviewCanClose = canClose
        reviewClosed = onClose
        reviewSelected = true
        state.select(nil)
        viewSelection = nil
        leaveRecovery()
        syncSidebarSelection()
        updateContent(focus: true)
        return true
    }

    @discardableResult
    func focusReview() -> Bool {
        guard isOpen, reviewView != nil else { return false }
        if otherPlace == .review { return focusOtherPane() }
        reviewSelected = true
        state.select(nil)
        viewSelection = nil
        leaveRecovery()
        syncSidebarSelection()
        updateContent(focus: true)
        return true
    }

    private func prepareSplit() -> Bool {
        guard isOpen, let place, place != .review, reviewCanClose() else { return false }
        if otherPlace == .review { releaseReview() }
        otherPlace = place
        otherContent = content
        activeOnRight = true
        return true
    }

    /// Clicking an inactive terminal promotes its container and selection
    /// together. The physical terminal stays under the pointer.
    @discardableResult
    func focusOtherPane() -> Bool {
        guard isOpen, let next = otherPlace else { return false }
        let previous = place
        let previousContent = content
        content = otherContent
        otherPlace = previous
        otherContent = previousContent
        activeOnRight.toggle()
        reviewSelected = false
        viewSelection = nil
        state.select(nil)
        leaveRecovery()
        switch next {
        case let .session(key):
            if !state.select(key), !key.runtimeSessionID.isEmpty {
                sessionGone(key.runtimeSessionID, name: key.runtimeSessionID)
            }
        case let .view(id): viewSelection = id
        case .review: reviewSelected = true
        }
        syncSidebarSelection()
        updateContent(focus: true)
        return true
    }

    func swapPanes() {
        guard isSplit else { return }
        activeOnRight.toggle()
        paneLayout.arrange(split: true, activeOnRight: activeOnRight)
        reportLayout()
    }

    /// Hiding a pane never ends its agent or shell. Utility views stay in the
    /// sidebar, and a hidden session keeps its existing workspace attachment.
    func collapseOtherPane() {
        guard isSplit else { return }
        if otherPlace == .review {
            guard reviewCanClose() else { return }
            releaseReview()
        }
        otherPlace = nil
        otherContent = .empty
        activeOnRight = false
        updateContent(focus: true)
    }

    private func releaseReview() {
        reviewView?.removeFromSuperview()
        reviewView = nil
        reviewClosed()
        reviewClosed = {}
        reviewCanClose = { true }
    }

    private func fitPanes() {
        let middle: CGFloat = isSplit ? 2 * WorkspacePaneLayout.minimumPaneWidth + 1 : 320
        if contentItem?.minimumThickness != middle { contentItem?.minimumThickness = middle }
        let width: CGFloat
        if isSplit {
            let left = sidebarItem.flatMap { $0.isCollapsed ? nil : max(180, $0.viewController.view.frame.width) } ?? 0
            let right = inspectorItem.flatMap { $0.isCollapsed ? nil : max(220, $0.viewController.view.frame.width) } ?? 0
            width = middle + left + right + 2
        } else { width = Self.minimumSize.width }
        let minimum = NSSize(width: width, height: Self.minimumSize.height)
        if window.minSize != minimum { window.minSize = minimum }
        if isSplit, window.frame.width < width {
            var frame = window.frame
            frame.size.width = width
            window.setFrame(frame, display: true)
        }
    }

    private func wantedOtherContent() -> Content {
        switch otherPlace {
        case let .session(key)?:
            guard let row = state.row(key) else { return .gone(runtimeSessionID: key.runtimeSessionID, nil) }
            return wantedSessionContent(row, previous: otherContent, retry: false)
        case let .view(id)?:
            guard let view = views.view(id) else { return .empty }
            return wantedViewContent(view, previous: otherContent, retry: false)
        case .review?: return .review
        case nil: return .empty
        }
    }

    private func visibleView(_ content: Content) -> NSView? {
        switch content {
        case let .terminal(id): return terminals[id]?.view
        case let .view(id): return viewTerminals[id]?.view
        case .review: return reviewView
        default: return nil
        }
    }

    private func placeView(_ view: NSView?, in container: NSView, below placeholder: NSView) {
        guard let view else { return }
        if view.superview !== container {
            view.removeFromSuperview()
            container.addSubview(view, positioned: .below, relativeTo: placeholder)
        }
        view.frame = container.bounds
        view.autoresizingMask = [.width, .height]
        view.isHidden = false
    }

    private func title(for place: Place?) -> String {
        switch place {
        case let .session(key)?: return state.row(key)?.displayName ?? "Session ended"
        case let .view(id)?: return views.view(id)?.displayName ?? "View closed"
        case .review?: return "Checkout Changes"
        case nil: return "Workspace"
        }
    }

    private var otherExplanation: String {
        otherContent.otherPaneExplanation
    }

    private var otherShowing: TerminalNotice.Origin? {
        guard isOpen, isSplit else { return nil }
        return otherContent.terminalNoticeOrigin
    }

    private func installPaneClickMonitor() {
        guard paneClickMonitor == nil else { return }
        paneClickMonitor = NSEvent.addLocalMonitorForEvents(matching: [.leftMouseDown, .rightMouseDown]) { [weak self] event in
            MainActor.assumeIsolated {
                guard let self, self.isOpen, self.isSplit, event.window === self.window else { return }
                let point = self.otherPane.convert(event.locationInWindow, from: nil)
                if self.otherPane.bounds.contains(point) { _ = self.focusOtherPane() }
            }
            return event
        }
    }

    private func wantedViewContent(_ view: WorkspaceUtilityView, previous: Content, retry: Bool) -> Content {
        let id = view.id
        if endedViews.contains(id) { return .viewEnded(id: id) }
        if viewTerminals[id] != nil {
            return rendererFailedViews.contains(id) ? .viewRendererFailed(id: id) : .view(id: id)
        }
        if !retry, case let .viewUnavailable(refused, _) = previous, refused == id { return previous }
        guard let delegate else { return .viewUnavailable(id: id, "The app is shutting down.") }
        switch delegate.makeUtilityTerminal(view) {
        case let .success(terminal):
            Self.logger.notice("workspace: started view \(id, privacy: .public)")
            viewTerminals[id] = terminal
            (terminal.view as? Ghostty.SurfaceView)?.hostName = view.displayName
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
        if reviewSelected { return .review }
        if let view = views.view(viewSelection) { return wantedViewContent(view, previous: content, retry: retry) }
        guard let row = state.selectedRow else {
            if let gone { return .gone(runtimeSessionID: gone.runtimeSessionID, gone.status) }
            if case let .session(id)? = restoring { return .restoring(runtimeSessionID: id) }
            return .empty
        }
        return wantedSessionContent(row, previous: content, retry: retry)
    }

    private func wantedSessionContent(_ row: WorkspaceRow, previous: Content, retry: Bool) -> Content {
        guard let id = row.embeddedRuntimeSessionID else { return .external(row.key) }
        if closing[id] != nil { return .closing(runtimeSessionID: id) }
        if detached.contains(id) { return .detached(runtimeSessionID: id) }
        if terminals[id] != nil {
            return rendererFailed.contains(id) ? .rendererFailed(runtimeSessionID: id) : .terminal(runtimeSessionID: id)
        }
        // A terminal that could not be made is not asked for again on every
        // snapshot; something has to have changed first.
        if !retry, case let .unavailable(refused, _) = previous, refused == id { return previous }
        guard let delegate else { return .unavailable(runtimeSessionID: id, "The app is shutting down.") }
        switch delegate.makeTerminal(runtimeSessionID: id, fontSize: fontSizes[id]) {
        case let .success(terminal):
            Self.logger.notice("workspace: attached a terminal to \(id, privacy: .public)")
            terminals[id] = terminal
            (terminal.view as? Ghostty.SurfaceView)?.hostName = row.displayName
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
        otherContent = wantedOtherContent()
        let shown = visibleView(wanted)
        let otherShown = visibleView(otherContent)
        for terminal in terminals.values { terminal.view.isHidden = terminal.view !== shown && terminal.view !== otherShown }
        for terminal in viewTerminals.values { terminal.view.isHidden = terminal.view !== shown && terminal.view !== otherShown }
        reviewView?.isHidden = reviewView !== shown && reviewView !== otherShown
        placeView(shown, in: pane, below: placeholder)
        placeView(otherShown, in: otherPane, below: otherPlaceholder)
        placeholder.isHidden = shown != nil
        otherPlaceholder.isHidden = otherShown != nil
        if shown == nil { fillPlaceholder(wanted) }
        if otherShown == nil {
            otherPlaceholder.show(title: title(for: otherPlace), message: otherExplanation, button: "Focus pane") { [weak self] in
                _ = self?.focusOtherPane()
            }
        }
        fitPanes()
        paneLayout.arrange(split: isSplit, activeOnRight: activeOnRight)
        if let fraction = restoringFraction {
            restoringFraction = nil
            window.contentView?.layoutSubtreeIfNeeded()
            paneLayout.restoreFraction(fraction)
        }
        paneLayout.titles(active: title(for: place), other: title(for: otherPlace))
        installPaneClickMonitor()
        window.title = windowTitle
        window.subtitle = windowSubtitle
        clearBells()
        showLink((shown as? Ghostty.SurfaceView)?.hoveredLink)
        syncOverlays()

        if let shown, focus {
            if !window.makeFirstResponder(shown) { window.makeFirstResponder(nil) }
        } else if window.firstResponder === window
                    || (window.firstResponder as? NSView)?.isHiddenOrHasHiddenAncestor == true {
            // Nothing has the keyboard, or a terminal that is no longer
            // shown had it: the sidebar takes it, so the arrow keys work.
            focusListOrPane()
        }
        reloadInspector()
        reportPlace()
        reportShowing()
    }

    static let resumeTitle = "Resume from Past Sessions"
    static let startDaemonTitle = WorkspaceSidebar.DaemonAction.start.title
    static let restartDaemonTitle = WorkspaceSidebar.DaemonAction.restart.title

    /// Asks the app to start or restart the daemon, from the pane's button or
    /// the one under the sidebar.
    private func performDaemonAction(_ action: WorkspaceSidebar.DaemonAction) {
        Self.logger.notice("workspace: the operator asked to \(action == .start ? "start" : "restart", privacy: .public) the daemon")
        switch action {
        case .start: delegate?.workspaceStartDaemon()
        case .restart: delegate?.workspaceRestartDaemon()
        }
    }

    @objc private func daemonButtonPressed(_ sender: Any?) {
        guard let feed = delegate?.store.feed, let action = WorkspaceSidebar.daemonAction(feed) else { return }
        performDaemonAction(action)
    }

    private func fillPlaceholder(_ content: Content) {
        let row = state.selectedRow
        switch content {
        case .empty, .terminal, .view, .review:
            // Rows held from the last snapshot can still be chosen; with none
            // listed, what is wrong is the feed, not the sessions.
            if state.rows.isEmpty, let feed = delegate?.store.feed, let down = WorkspaceSidebar.feedExplanation(feed) {
                placeholder.show(title: down.title, message: down.message, button: down.action?.title) { [weak self] in
                    if let action = down.action { self?.performDaemonAction(action) }
                }
                break
            }
            if state.rows.isEmpty {
                placeholder.show(title: "No live sessions",
                    message: [delegate?.workspaceProjectGuidance ?? "", delegate?.workspaceLaunchDescription ?? ""].joined(separator: "\n\n"),
                    button: "Launch Project…", secondary: [
                        ("Conversation History", { [weak self] in self?.delegate?.workspacePerform(.history) }),
                        ("Open Dashboard", { [weak self] in self?.showView(WorkspaceUtilityView.dashboard.id) }),
                    ]) { [weak self] in
                        guard let self else { return }
                        self.delegate?.workspaceLaunchProjects(from: self.placeholder.button)
                    }
                break
            }
            placeholder.show(
                title: "No session selected",
                message: "Choose a session in the sidebar.",
                button: "Open Dashboard"
            ) { [weak self] in self?.showView(WorkspaceUtilityView.dashboard.id) }
        case let .viewEnded(id):
            let name = views.view(id)?.displayName ?? "This view"
            placeholder.show(
                title: "\(name) closed",
                message: "It is no longer running. No session was affected: the agents keep running.",
                button: "Reopen"
            ) { [weak self] in self?.restartView(id) }
        case let .viewUnavailable(id, message):
            placeholder.show(title: "No terminal yet", message: message, button: "Try Again") { [weak self] in
                self?.restartView(id)
            }
        case let .viewRendererFailed(id):
            let name = views.view(id)?.displayName ?? "This view"
            placeholder.show(
                title: "The terminal stopped drawing",
                message: "\(name)'s renderer failed, so it can no longer be seen here. No session was affected; restarting ends its program and starts it in a new terminal.",
                button: "Restart",
                isDefault: true
            ) { [weak self] in self?.restartViewAfterRendererFailure(id) }
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
            // A daemon that is down or too old never says: what would bring
            // it back is offered here, not only under the sidebar.
            if !state.current, let feed = delegate?.store.feed, let down = WorkspaceSidebar.feedExplanation(feed),
               let action = down.action {
                placeholder.show(
                    title: down.title,
                    message: "The window comes back to the session it was on once the daemon lists it. \(down.message)",
                    button: action.title,
                    isDefault: true
                ) { [weak self] in self?.performDaemonAction(action) }
                break
            }
            placeholder.show(
                title: "Restoring",
                message: state.current
                    ? "Checking that the session this window was on is still running."
                    : "Waiting for the daemon to say whether the session this window was on is still running. Nothing is started meanwhile.",
                button: "Open Dashboard"
            ) { [weak self] in self?.showView(WorkspaceUtilityView.dashboard.id) }
        case let .gone(_, status):
            let name = gone?.name ?? "This session"
            let conversation = gone?.conversation
            let notice = status?.notice(for: name, resumable: conversation != nil)
                ?? ("No longer listed", status == nil
                    ? "\(name) is no longer running. Checking what became of it."
                    : "\(name) is no longer listed by the daemon. Nothing was restarted.")
            guard let conversation else {
                placeholder.show(title: notice.0, message: notice.1, button: "Open Dashboard") { [weak self] in
                    self?.showView(WorkspaceUtilityView.dashboard.id)
                }
                break
            }
            // Past Sessions on that conversation, where resuming is a key the
            // operator presses: the workspace still starts no agent.
            placeholder.show(title: notice.0, message: notice.1, button: Self.resumeTitle) { [weak self] in
                self?.open(.history(conversation))
            }
        case let .detached(id):
            placeholder.show(
                title: "Detached",
                message: "This window let go of the session. The agent is still running; reattach when you want to use it here.",
                button: "Reattach",
                isDefault: true
            ) { [weak self] in self?.reattach(id) }
        case let .unavailable(id, message):
            placeholder.show(title: "No terminal yet", message: message, button: "Try Again") { [weak self] in
                self?.reattach(id)
            }
        case .closing:
            placeholder.show(title: "Ending", message: "This session's agent is being ended.")
        case let .rendererFailed(id):
            placeholder.show(
                title: "The terminal stopped drawing",
                message: "Its renderer failed, so what the agent shows can no longer be seen here. The agent is still running; reattaching makes a new terminal for it.",
                button: "Reattach",
                isDefault: true
            ) { [weak self] in self?.reattachAfterRendererFailure(id) }
        }
    }

    /// Lets go of a terminal whose renderer failed and attaches a new one.
    private func reattachAfterRendererFailure(_ id: String) {
        Self.logger.notice("workspace: reattaching \(id, privacy: .public) after a renderer failure")
        discardTerminal(id)
        reattach(id)
    }

    /// Ends a utility view whose renderer failed and starts it again in a
    /// new terminal.
    private func restartViewAfterRendererFailure(_ id: String) {
        Self.logger.notice("workspace: restarting view \(id, privacy: .public) after a renderer failure")
        discardViewTerminal(id)
        restartView(id)
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

    private func reloadSidebar(force: Bool = false, allowReorder: Bool = false) {
        // A session's terminals follow its row. One whose session is no
        // longer listed keeps running, among the workspace's own views.
        let listed = state.embeddedRuntimeSessionIDs
        for row in state.rows {
            if let id = row.embeddedRuntimeSessionID {
                sessionNames[id] = row.displayName
                (terminals[id]?.view as? Ghostty.SurfaceView)?.hostName = row.displayName
            }
        }
        let about = Set(views.all.compactMap(\.aboutRuntimeSessionID))
        sessionNames = sessionNames.filter { listed.contains($0.key) || about.contains($0.key) }
        let own = views.all.filter { view in view.besideRuntimeSessionID.map { !listed.contains($0) } ?? true }
        var wanted = [Item.section(Self.viewsSection)] + own.map(Item.view)
            + state.groups.flatMap { group in
                // A collapsed project lists its header only, which still
                // counts what waits under it.
                [Item.group(group)] + (group.collapsed ? [] : group.rows).flatMap { row in
                    [Item.row(row)] + views.terminals(beside: row.embeddedRuntimeSessionID).map(Item.view)
                }
            }
        let holdingHover = sidebar.pointerInside && !Self.hasNewSidebarTargets(wanted, comparedTo: items)
        if !allowReorder, !items.isEmpty, holdingHover || sidebarMenuOpen || dragged != nil {
            wanted = Self.holdingSidebar(items, latest: wanted)
        }
        let dimmed: CGFloat = state.current ? 1 : 0.5
        let decorations = wanted.map(decoration)
        let ended = IndexSet(wanted.indices.filter { index in
            if case let .row(row) = wanted[index] { return state.row(row.key) == nil }
            return false
        })
        if force || wanted != items || sidebar.alphaValue != dimmed || decorations != drawnDecorations || !ended.isEmpty {
            // A snapshot mostly moves ages and glyphs: only those lines are
            // drawn again, and the pointer's × stays where it is. A line
            // whose ⌘ digit or session name moved is one of them, though
            // its own item is the same: a session started above it, in a
            // collapsed project, or the session beside it was renamed.
            var changed = force ? nil : Self.changedLines(from: items, to: wanted)
            changed?.formUnion(ended)
            if changed != nil, decorations.count == drawnDecorations.count {
                changed?.formUnion(IndexSet(decorations.indices.filter { decorations[$0] != drawnDecorations[$0] }))
            }
            items = wanted
            drawnDecorations = decorations
            sidebar.alphaValue = dimmed
            if let changed {
                if !changed.isEmpty { reload(changed) }
            } else {
                applying = true
                sidebar.reloadData()
                applying = false
                syncHover(IndexSet(integersIn: 0..<sidebar.numberOfRows))
            }
        }
        syncSidebarSelection()
        reloadInspector()
        if isSwitching { switcher.update(switcherEntries()) }
    }

    /// What a line draws that its item does not carry: a session's ⌘ digit,
    /// or the name and selection color inherited by a view.
    private func decoration(_ item: Item) -> LineDecoration {
        let label: String?
        switch item {
        case let .row(row): label = sessionKey(of: row.key)
        case let .view(view): label = view.aboutRuntimeSessionID.flatMap { sessionNames[$0] }
        case .section, .group: label = nil
        }
        return LineDecoration(label: label, selectionColor: selectionColor(of: item))
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

    /// What the operator's click or key may select: never a heading, and
    /// never nothing. A click on the list's empty space, or ⌘-click on the
    /// selected line, keeps the line selected, so the pane stays where it
    /// is and so does the place restored at the next launch. The workspace
    /// still clears the selection itself (`syncSidebarSelection`).
    func tableView(_ tableView: NSTableView, selectionIndexesForProposedSelection proposed: IndexSet) -> IndexSet {
        let selectable = proposed.filteredIndexSet { $0 < items.count && self.tableView(tableView, shouldSelectRow: $0) }
        if selectable.isEmpty, !applying { return tableView.selectedRowIndexes }
        return selectable
    }

    /// What typing in the list matches: a line's name. The label a cell
    /// exposes starts with glyphs and, for a session, is not its name.
    func tableView(_ tableView: NSTableView, typeSelectStringFor tableColumn: NSTableColumn?, row: Int) -> String? {
        guard row < items.count else { return nil }
        return Self.typeSelectString(items[row])
    }

    static func typeSelectString(_ item: Item) -> String? {
        switch item {
        case let .row(row): return row.displayName
        case let .view(view): return view.displayName
        case .section, .group: return nil
        }
    }

    func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
        switch items[row] {
        case .section, .group: return 34
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
            cell.show(view, session: view.aboutRuntimeSessionID.flatMap { sessionNames[$0] },
                      program: programTitles[view.id], bell: ringingViews.contains(view.id),
                      progress: progresses[view.id])
            cell.onClose = { [weak self] in self?.closeView(view.id) }
            return cell
        case let .group(group):
            let cell = tableView.makeView(withIdentifier: WorkspaceGroupCellView.identifier, owner: nil) as? WorkspaceGroupCellView
                ?? WorkspaceGroupCellView()
            cell.show(group)
            cell.onToggle = group.needsAttention ? nil : { [weak self] in
                self?.setCollapsed(project: group.project, pinned: group.pinned, !group.collapsed)
            }
            return cell
        case let .row(row):
            let cell = tableView.makeView(withIdentifier: WorkspaceRowCellView.identifier, owner: nil) as? WorkspaceRowCellView
                ?? WorkspaceRowCellView()
            cell.show(row, muteGlyph: glyphs.mute, closing: row.embeddedRuntimeSessionID.map { closing[$0] != nil } ?? false,
                      sessionKey: sessionKey(of: row.key), bell: row.embeddedRuntimeSessionID.map(ringingSessions.contains) ?? false)
            let current = state.row(row.key) != nil
            cell.alphaValue = current ? 1 : 0.5
            cell.closeButton.isEnabled = current
            if !current { cell.detailLabel.stringValue = "Session ended" }
            cell.onClose = { [weak self] in
                guard let self, self.state.row(row.key) != nil else { return }
                self.detachSession(row.key)
            }
            return cell
        }
    }

    /// A session's line is drawn on the session's color; every other line,
    /// the Dashboard and the Overview among them, on the list's own.
    func tableView(_ tableView: NSTableView, rowViewForRow row: Int) -> NSTableRowView? {
        let view: WorkspaceRowView
        if case let .view(utility) = items[row], utility.kind == .terminal {
            view = WorkspaceTerminalRowView()
        } else if case let .row(session) = items[row] {
            let sessionView = WorkspaceSessionRowView()
            sessionView.hasStateGlyph = !session.glyph.isEmpty
            view = sessionView
        } else {
            view = WorkspaceRowView()
        }
        view.tabColor = Self.tabColor(of: items[row])
        view.selectionBorder.color = selectionColor(of: items[row]) ?? .controlAccentColor
        return view
    }

    /// Terminals listed beneath a session share its focus border color.
    private func selectionColor(of item: Item) -> NSColor? {
        if case let .view(view) = item,
           let id = view.besideRuntimeSessionID,
           let row = state.row(.embedded(id)) {
            return Self.tabColor(of: .row(row))
        }
        return Self.tabColor(of: item)
    }

    private static func tabColor(of item: Item) -> NSColor? {
        guard case let .row(row) = item, let color = row.tabColor else { return nil }
        return NSColor(srgbRed: color.red, green: color.green, blue: color.blue, alpha: 1)
    }

    func tableView(_ tableView: NSTableView, didAdd rowView: NSTableRowView, forRow row: Int) {
        (rowView as? WorkspaceRowView)?.syncCell()
    }

    /// The arrow keys moved the selection: show it, and stay in the sidebar.
    func tableViewSelectionDidChange(_ notification: Notification) {
        guard !applying else { return }
        let index = sidebar.selectedRow
        // The operator cannot empty the list's selection
        // (`selectionIndexesForProposedSelection`); should it empty anyway,
        // the pane stays where it is rather than going blank.
        guard index >= 0, index < items.count else { return }
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
        Item.dropSlots(forItemAt: index, in: items)
    }

    /// The slot a line dragged from `index` takes when it is let go over
    /// `proposed`: the first one at or below it. Nil outside the line's
    /// project or session, and where it would land back where it is.
    func dropSlot(forItemAt index: Int, proposed: Int) -> Int? {
        Item.dropSlot(forItemAt: index, proposed: proposed, in: items)
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
            guard let group = state.groups.first(where: { !$0.needsAttention && $0.pinned == row.pinned && (row.pinned || $0.project == row.project) })
            else { return false }
            var keys = group.rows.map(\.key).filter { $0 != row.key }
            var at = keys.count
            if slot < items.count, case let .row(next) = items[slot] { at = keys.firstIndex(of: next.key) ?? at }
            keys.insert(row.key, at: at)
            // Keep the saved places of siblings temporarily in Needs attention.
            let base = WorkspaceSidebar.groups(state.rows, order: order, prioritizeAttention: false)
                .first { $0.pinned == row.pinned && (row.pinned || $0.project == row.project) }
            let moved = Set(keys)
            var next = keys.makeIterator()
            let saved = (base?.rows.map(\.key) ?? keys).map { moved.contains($0) ? next.next()! : $0 }
            if row.pinned {
                order.setPinned(saved)
            } else {
                order.set(saved, for: row.project)
            }
            state.arrange(order)
            delegate?.workspaceOrderChanged(order)
        case let .view(view):
            var next: String?
            if slot < items.count, case let .view(other) = items[slot], other.besideRuntimeSessionID == view.besideRuntimeSessionID {
                next = other.id
            }
            views.moveTerminal(view.id, before: next)
        }
        reloadSidebar(allowReorder: true)
        // The waiting sessions are listed in the order they have here.
        reloadInspector()
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
        reloadSidebar()
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

    /// A click on a row focuses it; one on a project's header collapses or
    /// expands the project.
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
            focusListOrPane()
        }
        delegate?.workspaceSidebarCollapsedChanged(collapsed)
    }

    /// Gives the keyboard to the session list, so the arrow keys work; while
    /// the list is hidden, to what the pane shows instead: its terminal, the
    /// placeholder's button, or else the window. A list that cannot be seen
    /// must not switch sessions under keys typed into it.
    private func focusListOrPane() {
        guard isSidebarCollapsed else {
            window.makeFirstResponder(sidebar)
            return
        }
        var shown: NSView?
        if case let .terminal(id) = content { shown = terminals[id]?.view }
        if case let .view(id) = content { shown = viewTerminals[id]?.view }
        if case .review = content { shown = reviewView }
        if let shown, !shown.isHidden, window.makeFirstResponder(shown) { return }
        if !placeholder.isHidden, !placeholder.button.isHidden, window.makeFirstResponder(placeholder.button) { return }
        window.makeFirstResponder(nil)
    }

    // MARK: Widths

    static let sidebarWidths: ClosedRange<CGFloat> = 180...420
    static let inspectorWidths: ClosedRange<CGFloat> = 220...420
    static let defaultSidebarWidth: CGFloat = 240
    static let defaultInspectorWidth: CGFloat = 260

    /// The width to open at: the saved one, kept within what the item
    /// allows, else the default.
    static func width(_ saved: CGFloat?, default fallback: CGFloat, in range: ClosedRange<CGFloat>) -> CGFloat {
        guard let saved, saved.isFinite, saved > 0 else { return fallback }
        return min(max(saved, range.lowerBound), range.upperBound)
    }

    /// The split view was laid out again: by a drag of a divider, a hidden
    /// item shown, or the window resized. The width of what is shown is
    /// reported when it changed; a hidden one reports nothing.
    private func splitResized() {
        if isOpen && isSplit { fitPanes() }
        let sidebarShown = sidebarItem.flatMap { $0.isCollapsed ? nil : $0.viewController.view.frame.width }
        let inspectorShown = inspectorItem.flatMap { $0.isCollapsed ? nil : $0.viewController.view.frame.width }
        // Mid-animation an item is narrower than it may be, so a width is
        // kept only within its limits.
        let sidebar = sidebarShown.flatMap { Self.sidebarWidths.contains($0) ? $0 : nil }
        let inspector = inspectorShown.flatMap { Self.inspectorWidths.contains($0) ? $0 : nil }
        let sidebarChanged = sidebar != nil && sidebar != reportedSidebarWidth
        let inspectorChanged = inspector != nil && inspector != reportedInspectorWidth
        guard sidebarChanged || inspectorChanged else { return }
        if sidebarChanged { reportedSidebarWidth = sidebar }
        if inspectorChanged { reportedInspectorWidth = inspector }
        delegate?.workspaceLayoutChanged(sidebarWidth: sidebarChanged ? sidebar : nil,
                                         inspectorWidth: inspectorChanged ? inspector : nil)
    }

    // MARK: Inspector

    /// Whether the inspector is hidden, as it is until it is asked for.
    var isInspectorCollapsed: Bool {
        _ = window
        return inspectorItem?.isCollapsed ?? true
    }

    @objc func toggleInspector(_ sender: Any? = nil) {
        setInspectorCollapsed(!isInspectorCollapsed, animated: true)
    }

    func setInspectorCollapsed(_ collapsed: Bool, animated: Bool = false) {
        guard collapsed != isInspectorCollapsed, let inspectorItem else { return }
        if animated, !AppDelegate.isRunningTests {
            inspectorItem.animator().isCollapsed = collapsed
        } else {
            inspectorItem.isCollapsed = collapsed
        }
    }

    /// Turns the inspector to a tab. It stays hidden if it is.
    func setInspectorTab(_ tab: WorkspaceInspectorTab) {
        guard tab != inspectorTab else { return }
        inspectorTab = tab
        reloadInspector()
        delegate?.workspaceInspectorChanged(collapsed: reportedInspectorCollapsed, tab: tab)
    }

    private func inspectorCollapseChanged() {
        let collapsed = inspectorItem?.isCollapsed ?? true
        guard collapsed != reportedInspectorCollapsed else { return }
        reportedInspectorCollapsed = collapsed
        if collapsed { inspector.discardContext() }
        delegate?.workspaceInspectorChanged(collapsed: collapsed, tab: inspectorTab)
    }

    /// Brings the inspector in line with the store and with where the pane
    /// is. The session it describes is the one the session keys act on.
    private func reloadInspector() {
        guard let store = delegate?.store else { return }
        let socket = store.snapshot?.kitty.socket ?? ""
        // Only what is listed can be gone to, so only that is shown.
        let sessions = store.sessions.filter { state.row(AttentionCycler.key($0, snapshotSocket: socket)) != nil }
        let shown = shownSessionRow.flatMap { row in
            sessions.first { AttentionCycler.key($0, snapshotSocket: socket) == row.key }
        }
        inspector.show(WorkspaceInspectorView.Model(
            tab: inspectorTab,
            waiting: WorkspaceInspector.waiting(
                sessions, asl: store.snapshot?.asl.waiting ?? [], snapshotSocket: socket, now: now(), glyphs: glyphs, order: order
            ),
            inspection: shown.map { WorkspaceInspector.inspection($0, snapshotSocket: socket, now: now(), glyphs: glyphs) },
            current: state.current,
            feedNotice: WorkspaceSidebar.feedNotice(store.feed)
        ))
    }

    /// Return in the session list: focuses the selected row, as a click does.
    func focusSelectedItem() {
        focusItem(at: sidebar.selectedRow)
    }

    /// Does what a click on the line at `index` does.
    func focusItem(at index: Int) {
        guard index >= 0, index < items.count else { return }
        switch items[index] {
        case let .row(row):
            if let current = state.row(row.key) { activate(current, reattaching: true) }
        case let .view(view): showView(view.id, focus: true)
        case let .group(group):
            if !group.needsAttention { setCollapsed(project: group.project, pinned: group.pinned, !group.collapsed) }
        case .section: break
        }
    }

    /// ← and → in the session list: collapse or expand the project of the
    /// session the list is on, which is the one the pane is on once its row
    /// was collapsed away.
    func setSelectedProjectCollapsed(_ collapsed: Bool) {
        let index = sidebar.selectedRow
        var key = state.selection
        if index >= 0, index < items.count {
            switch items[index] {
            case let .row(row): key = row.key
            case let .view(view): key = view.besideRuntimeSessionID.map(AttentionCycler.Key.embedded)
            case .section, .group: key = nil
            }
        } else if let beside = views.view(viewSelection)?.besideRuntimeSessionID {
            key = .embedded(beside)
        }
        guard let group = group(of: key), !group.needsAttention else { return }
        setCollapsed(project: group.project, pinned: group.pinned, collapsed)
    }

    // MARK: Window

    static let minimumSize = NSSize(width: 640, height: 360)

    private func makeWindow() -> NSWindow {
        let sidebarController = WorkspaceWindowChrome.sidebar(table: sidebar, feedLabel: feedLabel, feedButton: feedButton)
        sidebar.dataSource = self
        sidebar.delegate = self
        sidebar.target = self
        sidebar.action = #selector(rowClicked(_:))
        sidebar.onPointerChanged = { [weak self] in self?.reloadSidebar() }
        sidebar.onReturn = { [weak self] in self?.focusSelectedItem() }
        sidebar.onCollapse = { [weak self] collapsed in self?.setSelectedProjectCollapsed(collapsed) }
        sidebar.onStep = { [weak self] offset in self?.stepFromUnlistedPlace(offset) ?? false }
        sidebar.hasMenu = { [weak self] row in !(self?.contextMenuItems(forRow: row).isEmpty ?? true) }
        sidebar.registerForDraggedTypes([Self.dragType])
        sidebar.setDraggingSourceOperationMask(.move, forLocal: true)
        sidebar.setDraggingSourceOperationMask([], forLocal: false)
        let context = NSMenu()
        // Each item says itself whether it can be chosen: End Session… is
        // greyed out on a session already closing, which validation would
        // otherwise turn back on.
        context.autoenablesItems = false
        context.delegate = self
        sidebar.menu = context
        feedButton.target = self
        feedButton.action = #selector(daemonButtonPressed(_:))

        scrollbarView.onScroll = { [weak self] row in self?.scrollbarMoved(toRow: row) }
        findBar.onNeedle = { [weak self] needle in self?.findBarNeedleChanged(needle) }
        findBar.onNext = { [weak self] in self?.findBarNavigate(next: true) }
        findBar.onPrevious = { [weak self] in self?.findBarNavigate(next: false) }
        findBar.onClose = { [weak self] in self?.closeFindBar() }
        switcher.onChoose = { [weak self] entry in self?.go(to: entry) }
        switcher.onCancel = { [weak self] in self?.closeSwitcher(restoring: true) }
        switcher.onLeave = { [weak self] in self?.closeSwitcher(restoring: false) }
        WorkspaceWindowChrome.installPaneOverlays(
            in: pane, placeholder: placeholder, linkLabel: linkLabel,
            scrollbar: scrollbarView, findBar: findBar, switcher: switcher
        )
        let paneController = NSViewController()
        otherPlaceholder.frame = otherPane.bounds
        otherPlaceholder.autoresizingMask = [.width, .height]
        otherPane.addSubview(otherPlaceholder)
        paneLayout.other.onFocus = { [weak self] in _ = self?.focusOtherPane() }
        paneLayout.active.onFocus = { [weak self] in self?.updateContent(focus: true) }
        paneController.view = paneLayout

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
        contentItem = paneItem

        inspector.onTab = { [weak self] tab in self?.setInspectorTab(tab) }
        inspector.onWaiting = { [weak self] key in
            guard let self, let row = self.state.row(key) else { return }
            self.activate(row)
        }
        inspector.onAction = { [weak self] action in self?.delegate?.workspaceInspectorAction(action) }
        inspector.onAnswer = { [weak self] answer in self?.delegate?.workspaceAnswer(answer) }
        inspector.onLeaveReader = { [weak self] in
            guard let self else { return }
            let back = self.waitingReturn
            self.waitingReturn = nil
            if let back, (back as? NSView)?.isHiddenOrHasHiddenAncestor != true, self.window.makeFirstResponder(back) { return }
            self.focusListOrPane()
        }
        inspector.onContext = { [weak self] row, completion in
            self?.delegate?.workspaceContext(row, completion: completion)
        }
        let inspectorController = NSViewController()
        inspectorController.view = inspector
        let inspectorItem = NSSplitViewItem(viewController: inspectorController)
        inspectorItem.minimumThickness = 220
        inspectorItem.maximumThickness = 420
        inspectorItem.canCollapse = true
        // Like the sidebar it keeps its width while the window is resized:
        // the pane takes the difference.
        inspectorItem.holdingPriority = sidebarItem.holdingPriority
        self.inspectorItem = inspectorItem
        split.addSplitViewItem(sidebarItem)
        split.addSplitViewItem(paneItem)
        split.addSplitViewItem(inspectorItem)

        let window = NSWindow(contentViewController: split)
        window.title = "Agent Mux"
        window.styleMask = [.titled, .closable, .miniaturizable, .resizable]
        window.isReleasedWhenClosed = false
        window.minSize = Self.minimumSize
        window.setContentSize(NSSize(width: 1100, height: 700))
        window.delegate = self
        window.center()
        if !AppDelegate.isRunningTests { window.setFrameAutosaveName("AgentMuxWorkspace") }
        let splitView = split.splitView
        splitView.layoutSubtreeIfNeeded()
        // The inspector gets its width while shown, so that showing it
        // again brings that width back. It goes first: moving its divider
        // takes from the sidebar too.
        let inspectorShown = Self.width(inspectorWidth, default: Self.defaultInspectorWidth, in: Self.inspectorWidths)
        splitView.setPosition(splitView.bounds.width - inspectorShown - splitView.dividerThickness, ofDividerAt: 1)
        splitView.layoutSubtreeIfNeeded()
        splitView.setPosition(Self.width(sidebarWidth, default: Self.defaultSidebarWidth, in: Self.sidebarWidths), ofDividerAt: 0)
        // Hidden until it is asked for. An item is collapsed for good only
        // once its split view is laid out in a window.
        splitView.layoutSubtreeIfNeeded()
        inspectorItem.isCollapsed = true
        reportedSidebarWidth = sidebarWidth
        reportedInspectorWidth = inspectorWidth
        splitObservation = NotificationCenter.default.addObserver(
            forName: NSSplitView.didResizeSubviewsNotification, object: splitView, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.splitResized() }
        }
        inspectorObservation = inspectorItem.observe(\.isCollapsed) { [weak self] _, _ in
            MainActor.assumeIsolated { self?.inspectorCollapseChanged() }
        }

        sidebarToggle.target = self
        sidebarToggle.action = #selector(toggleSidebar(_:))
        window.addTitlebarAccessoryViewController(WorkspaceWindowChrome.titlebarAccessory(sidebarToggle, at: .leading))
        inspectorToggle.target = self
        inspectorToggle.action = #selector(toggleInspector(_:))
        window.addTitlebarAccessoryViewController(WorkspaceWindowChrome.titlebarAccessory(inspectorToggle, at: .trailing))
        return window
    }
}

extension WorkspaceWindowController: GhosttySurfaceDelegate {
    nonisolated func surfaceChildDidExit(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalEnded(surface) }
    }

    nonisolated func surfaceDidRequestClose(_ surface: Ghostty.SurfaceView, needsConfirm: Bool) {
        MainActor.assumeIsolated { terminalAskedToClose(surface, needsConfirm: needsConfirm) }
    }

    nonisolated func surfaceDidRingBell(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalRangBell(surface, systemBell: surface.bellFeatures.contains(.system)) }
    }

    nonisolated func surfaceDidChangeTitle(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalTitleChanged(surface, title: surface.title) }
    }

    nonisolated func surfaceDidChangeHoveredLink(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalHoveredLink(surface, url: surface.hoveredLink) }
    }

    nonisolated func surfaceDidWriteClipboard(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalCopied(surface) }
    }

    nonisolated func surfaceDidChangeRendererHealth(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalRendererHealthChanged(surface, healthy: surface.rendererHealthy) }
    }

    nonisolated func surface(_ surface: Ghostty.SurfaceView, didRequestNotificationTitled title: String, body: String) {
        MainActor.assumeIsolated { terminalNotification(surface, title: title, body: body) }
    }

    nonisolated func surface(_ surface: Ghostty.SurfaceView, didUpdateSearch update: Ghostty.SearchUpdate) {
        MainActor.assumeIsolated { terminalSearch(surface, update) }
    }

    nonisolated func surfaceDidChangeScrollbar(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalScrollbar(surface, surface.scrollbar) }
    }

    nonisolated func surfaceDidChangeProgress(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalProgress(surface, surface.progress) }
    }
}

/// The hovered link's overlay. It only shows the link: clicks under it go
/// to the terminal beneath.
@MainActor
final class WorkspaceLinkLabel: NSTextField {
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}

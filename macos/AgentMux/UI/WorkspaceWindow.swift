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
    nonisolated static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "workspace")

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
    enum Place: Equatable {
        case review
        case session(AttentionCycler.Key)
        case view(String)
    }

    weak var delegate: WorkspaceDelegate?
    var state = WorkspaceState()
    /// The order sessions were dragged into; the app keeps it across
    /// launches.
    var order = WorkspaceOrder()
    /// The line being dragged, from when the drag begins until it ends.
    var dragged: Place?
    var sidebarMenuOpen = false
    var items: [Item] = []
    var content: Content = .empty
    /// Whether the window is on screen; terminals exist only while it is.
    private(set) var isOpen = false

    let sidebar = WorkspaceSidebarView()
    private(set) var sidebarZoom: CGFloat = 1
    private var sidebarZoomView: WorkspaceSidebarZoomView?
    private var inspectorZoomView: WorkspaceSidebarZoomView?

    func zoomSidebars(_ change: WorkspaceZoomWindow.Zoom) {
        switch change {
        case .bigger: sidebarZoom = min(2, sidebarZoom + 0.1)
        case .smaller: sidebarZoom = max(0.7, sidebarZoom - 0.1)
        case .reset: sidebarZoom = 1
        }
        sidebarZoomView?.zoom = sidebarZoom
        inspectorZoomView?.zoom = sidebarZoom
        window.contentView?.layoutSubtreeIfNeeded()
    }
    /// Hides the sidebar and brings it back, from the title bar, so that
    /// neither the sidebar nor the pane gives up anything to it.
    let sidebarToggle = WorkspaceWindowChrome.makeSidebarToggle()
    /// What the delegate was last told, so it hears of each change once.
    var reportedCollapsed = false
    var reportedShowing: TerminalNotice.Origin?
    var reportedOtherShowing: TerminalNotice.Origin?
    var sidebarItem: NSSplitViewItem?
    var contentItem: NSSplitViewItem?
    private var sidebarObservation: NSKeyValueObservation?
    let inspector = WorkspaceInspectorView()
    /// Hides the inspector and brings it back, from the title bar.
    let inspectorToggle = WorkspaceWindowChrome.makeInspectorToggle()
    /// The tab the inspector is turned to; the app keeps it across launches.
    var inspectorTab = WorkspaceInspectorTab.waiting
    var reportedInspectorCollapsed = true
    var inspectorItem: NSSplitViewItem?
    private var inspectorObservation: NSKeyValueObservation?
    /// The widths the sidebar and the inspector open at, which the app sets
    /// before the window is made; nil gives each its default.
    var sidebarWidth: CGFloat?
    var inspectorWidth: CGFloat?
    /// The widths the delegate was last told of.
    var reportedSidebarWidth: CGFloat?
    var reportedInspectorWidth: CGFloat?
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
    weak var switcherReturn: NSResponder?
    weak var waitingReturn: NSResponder?
    let pane = NSView()
    let otherPane = NSView()
    let otherPlaceholder = WorkspacePlaceholderView()
    lazy var paneLayout: WorkspacePaneLayout = {
        let layout = WorkspacePaneLayout(active: pane, other: otherPane)
        layout.onResize = { [weak self] in self?.reportLayout() }
        return layout
    }()
    var otherPlace: Place?
    var otherContent: Content = .empty
    var activeOnRight = false
    var reviewSelected = false
    var reviewView: NSView?
    var reviewCanClose: () -> Bool = { true }
    var reviewClosed: () -> Void = {}
    struct RestoredReview {
        var view: NSView
        var canClose: () -> Bool
        var close: () -> Void
        var directory: () -> String?
    }
    var makeRestoredReview: ((String) -> RestoredReview?)?
    var onLayoutChanged: (WorkspaceRestoration.Layout?) -> Void = { _ in }
    var reviewDirectory: () -> String? = { nil }
    var pendingLayout: WorkspaceRestoration.Layout?
    var layoutChecking = false
    var applyingLayout = false
    var layoutGeneration = 0
    var layoutConversation: ConversationIdentity?
    var restoringFraction: Double?
    var paneClickMonitor: Any?
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

    var terminals: [String: WorkspaceTerminal] = [:]
    /// Sessions whose terminal ended and is not remade until asked for.
    var detached: Set<String> = []
    /// Sessions whose terminal's renderer said it failed.
    var rendererFailed: Set<String> = []
    /// Utility views, by id, whose terminal's renderer said it failed.
    var rendererFailedViews: Set<String> = []

    /// Sessions and views, by runtime session or view id, that rang the
    /// bell while another was shown; marked on their line until shown or
    /// until `bellHold` has passed.
    var ringingSessions: Set<String> = []
    var ringingViews: Set<String> = []
    /// Counts bells, so an expiry overtaken by a later bell is dropped.
    var bellCount = 0
    static let bellHold: TimeInterval = 60
    /// What the bell does besides the mark, replaced in tests: the app in
    /// front, the beep, and the Dock's attention request.
    var isAppActive: () -> Bool = { NSApp.isActive }
    var beep: () -> Void = { NSSound.beep() }
    var requestAttention: () -> Void = { _ = NSApp.requestUserAttention(.informationalRequest) }

    /// The title each utility view's program last set (OSC 0/2), by view id.
    var programTitles: [String: String] = [:]
    /// The link under the pointer in the terminal shown, at the pane's
    /// bottom corner. Clicks go through it to the terminal beneath.
    let linkLabel = WorkspaceLinkLabel(labelWithString: "")
    /// Why something the operator asked for here did not happen, at the top
    /// of the pane for a moment (`showNotice`): the menu bar's line is not
    /// where the operator is looking while the workspace has the keyboard.
    let noticeBanner = WorkspaceNoticeBanner()
    var noticeEnd: DispatchWorkItem?
    /// The search open in each utility view, by view id, shown in the find
    /// bar while the pane is on that view.
    var searches: [String: Ghostty.Search] = [:]
    /// Which time a search was opened in each utility view, so the find
    /// bar can tell a search closed and opened again from the one it typed into.
    var searchGenerations: [String: Int] = [:]
    var searchGeneration = 0
    /// Where each utility view's viewport sits in its buffer, by view id.
    var scrollbars: [String: Ghostty.Scrollbar] = [:]
    /// The progress each utility view's program last reported (OSC 9;4), by
    /// view id, shown on its line. It is never attention.
    var progresses: [String: Ghostty.Progress] = [:]
    let findBar = WorkspaceFindBar()
    let conversationSearchButton = NSButton(title: "Search Full Conversation…", target: nil, action: nil)
    var searchHintSessionID: String?
    let scrollbarView = WorkspaceScrollbarView()
    /// Runs a Ghostty binding action in a terminal, replaced in tests.
    var performTerminalAction: (NSView, String) -> Bool = { view, action in
        (view as? Ghostty.SurfaceView)?.performBindingAction(action) ?? false
    }

    /// Sessions amx was asked to close, until it has answered and a live
    /// snapshot no longer lists them: whether the pane was on each when it
    /// was closed, and whether amx has answered.
    var closing: [String: (wasShown: Bool, answered: Bool)] = [:]
    /// Settings controls close actions, including Ghostty's close callback.
    /// App quit confirmation remains independent of this preference.
    var confirmBeforeClosing = true
    var readConfirmation: ((@escaping (Bool) -> Void) -> Void)?
    var checkingConfirmation: Set<String> = []

    func withConfirmationPolicy(_ target: String, action: @escaping (Bool) -> Void) {
        guard checkingConfirmation.insert(target).inserted else { return }
        let done: (Bool) -> Void = { [weak self] required in
            self?.checkingConfirmation.remove(target)
            guard self != nil else { return }
            action(required)
        }
        if let readConfirmation { readConfirmation(done) } else { done(confirmBeforeClosing) }
    }
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
    /// Takes an error `showError` would show in an alert; nil shows it.
    /// Tests set it.
    var reportError: ((WorkspaceError) -> Void)?

    /// The utility views listed above the projects.
    var views = WorkspaceViews()
    /// The last name each embedded session was listed under, by runtime
    /// session, kept while a view is about it: a terminal whose session is
    /// no longer listed still says whose it was.
    var sessionNames: [String: String] = [:]
    /// Each sidebar line's `decoration` as last drawn.
    struct LineDecoration: Equatable {
        let label: String?
        let profileColor: NSColor?
    }
    var drawnDecorations: [LineDecoration] = []
    /// The font size each session's last terminal had when it was let go,
    /// by runtime session, so a zoom survives the window closing. Kept for
    /// the app's lifetime, until a new default zoom replaces every one.
    var fontSizes: [String: Float] = [:]
    /// The utility view the pane shows, by `WorkspaceUtilityView.id`. It and
    /// `state.selection` are never both set.
    var viewSelection: String?
    var viewTerminals: [String: WorkspaceTerminal] = [:]
    /// The Dashboard or Overview whose program exited; remade when asked for.
    var endedViews: Set<String> = []
    /// Where the pane was when an opened view took it, to go back to when
    /// that view ends.
    var returnPlaces: [String: Place] = [:]
    /// Where the pane is and where it was before that, which ⌘L goes between.
    /// An overlay view is neither: it is passed through.
    var recent: (here: Place?, before: Place?)
    /// A session that was just launched and is shown once a snapshot lists
    /// it, unless the operator goes somewhere else first.
    var awaited: (runtimeSessionID: String, until: Date)?
    /// Set while the table is brought in line with `state`, so its selection
    /// callbacks are not taken for the operator's.
    var applying = false
    /// The place the workspace is coming back to after a launch, until a live
    /// snapshot and amx have said what is there, or the operator goes
    /// somewhere else.
    var restoring: WorkspaceRestoration.Place?
    /// The session the pane was on when it stopped running, and what became
    /// of it once amx has answered.
    var gone: (runtimeSessionID: String, name: String, status: WorkspaceSessionStatus?, conversation: ConversationIdentity?)?
    /// The conversation last known for the session the pane is on or is
    /// coming back to. It outlives the row, so a session that stops being
    /// listed can still be resumed from Past Sessions.
    var shownConversation: (runtimeSessionID: String, conversation: ConversationIdentity)?
    var reportedConversation: ConversationIdentity?
    /// Counts status questions, so an answer to one that was overtaken by
    /// the operator, or by a newer question, is dropped.
    var statusQuestion = 0
    /// True while amx is being asked about the session being restored.
    var restoreChecking = false
    var reportedPlace: WorkspaceRestoration.Place?

    let glyphs: Glyphs
    let now: () -> Date

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
            closeTerminals(beside: Set(views.opened.compactMap(\.besideRuntimeSessionID)).subtracting(listed))
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

    /// Takes a copied runtime session ID; the general pasteboard unless a
    /// test says otherwise.
    var copyToPasteboard: (String) -> Void = { value in
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(value, forType: .string)
    }
    var copiedNoticeEnd: DispatchWorkItem?

    // MARK: Window

    static let minimumSize = NSSize(width: 640, height: 360)

    private func makeWindow() -> NSWindow {
        let sidebarController = WorkspaceWindowChrome.sidebar(table: sidebar, feedLabel: feedLabel, feedButton: feedButton)
        let sidebarZoomView = WorkspaceSidebarZoomView(content: sidebarController.view)
        sidebarZoomView.zoom = sidebarZoom
        sidebarZoomView.opaqueInLight = true
        sidebarController.view = sidebarZoomView
        self.sidebarZoomView = sidebarZoomView
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
        conversationSearchButton.target = self
        conversationSearchButton.action = #selector(searchFullConversation(_:))
        conversationSearchButton.bezelStyle = .rounded
        conversationSearchButton.controlSize = .small
        conversationSearchButton.refusesFirstResponder = true
        conversationSearchButton.toolTip = "Search saved conversation prose, including messages outside terminal Find’s scope. Carries the active terminal query into History."
        conversationSearchButton.isHidden = true
        conversationSearchButton.translatesAutoresizingMaskIntoConstraints = false
        pane.addSubview(conversationSearchButton)
        NSLayoutConstraint.activate([
            conversationSearchButton.topAnchor.constraint(equalTo: pane.topAnchor, constant: 8),
            conversationSearchButton.trailingAnchor.constraint(equalTo: pane.trailingAnchor, constant: -12),
            conversationSearchButton.leadingAnchor.constraint(greaterThanOrEqualTo: pane.leadingAnchor, constant: 8),
        ])
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
        let inspectorZoomView = WorkspaceSidebarZoomView(content: inspector)
        inspectorZoomView.zoom = sidebarZoom
        inspectorController.view = inspectorZoomView
        self.inspectorZoomView = inspectorZoomView
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

        let window = WorkspaceZoomWindow(contentViewController: split)
        window.onSidebarZoom = { [weak self] change in self?.zoomSidebars(change) }
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

/// The hovered link's overlay. It only shows the link: clicks under it go
/// to the terminal beneath.
@MainActor
final class WorkspaceLinkLabel: NSTextField {
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}

import AppKit
import SwiftUI
import os

/// The workspace window: a native sidebar of the live sessions, grouped by
/// project, beside the terminal of the selected one.
///
/// Selection and focus are separate. Selecting a row shows what there is to
/// show for it in its embedded terminal. Focusing a row, by a click or from
/// the menu bar, a shortcut, or a banner, moves keyboard focus into the terminal.
///
/// Each embedded session has at most one terminal, made the first time it is
/// selected and kept, hidden, while another is shown. A terminal is let go
/// when its `amx attach` ends, when a live snapshot no longer lists its
/// session, or when the window closes. None of those ends the agent.
///
/// The × on a session row and ⌘⇧W end its agent through the same confirmation
/// policy. ⌘W detaches its terminal. The × on an opened view closes that view
/// and ends its program. The Dashboard and Overview have none.
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

    /// Hover briefly holds attention reordering, but must not hide newly
    /// opened sessions or views while the pointer rests in the sidebar.
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
    var sessionReviewUndo: String?
    var sessionReviewBusy = false
    var sessionReviewPopover: NSPopover?
    var sessionReviewRunner = QueueWorkRunner()
    var sessionReviewService: (SessionReviewRequest) -> Result<SessionReviewResult, Error> = {
        guard !AppDelegate.isRunningTests else {
            return .failure(NSError(domain: "SessionReview", code: 1, userInfo: [NSLocalizedDescriptionKey: "Tests must supply an isolated review service."]))
        }
        return DaemonClient().sessionReview($0)
    }
    /// The order sessions were dragged into; the app keeps it across
    /// launches.
    var order = WorkspaceOrder()
    /// The line being dragged, from when the drag begins until it ends.
    var dragged: Place?
    var sidebarMenuOpen = false
    var hoverReorderTask: Task<Void, Never>?
    var items: [Item] = []
    /// Visible sessions in physical sidebar order, followed by sessions in
    /// collapsed groups. Shared by digit labels, menu titles, and actions.
    var sessionShortcutRows: [WorkspaceRow] = []
    var content: Content = .empty
    /// Whether the window is on screen; terminals exist only while it is.
    var isOpen = false

    let sidebar = WorkspaceSidebarView()
    let groupFilter = WorkspaceGroupFilter()
    lazy var welcome = WorkspaceWelcomeModel(command: { [weak self] in self?.delegate?.workspaceRecapCommand() })
    lazy var welcomeView: WorkspaceSidebarHostingView<WorkspaceWelcomeView> = {
        let host = WorkspaceSidebarHostingView(rootView: WorkspaceWelcomeView(model: welcome))
        // The stack needs logical intrinsic height; SwiftUI's automatic
        // minimum-size constraints would reserve the scaled pixel height.
        host.sizingOptions = [.intrinsicContentSize]
        return host
    }()
    /// nil is All; a literal group named All remains a separate choice.
    var selectedGroup: String? {
        didSet {
            if selectedGroup != oldValue { delegate?.workspaceGroupChanged(selectedGroup) }
        }
    }
    var showsAllWaitingGroups = false
    var groupContexts: [String: WorkspaceRestoration.GroupContext] = [:]
    var onGroupContextsChanged: ([String: WorkspaceRestoration.GroupContext]) -> Void = { _ in }
    var choosingGroupPlace = false
    var showingEmptyGroup = false
    var sidebarGrouping: WorkspaceSidebarGrouping = .stateThenProject
    var sidebarSort: WorkspaceSidebarSort = .manual
    var sidebarWaitingOnly = false

    var showOverviewInSidebar = false

    var compactSidebar = false

    /// The sidebars' zoom in percent, kept whole so steps never drift.
    var sidebarZoomPercent = 100
    var sidebarZoom: CGFloat { CGFloat(sidebarZoomPercent) / 100 }
    var sidebarZoomView: WorkspaceSidebarZoomView?
    var inspectorZoomView: WorkspaceSidebarZoomView?
    /// Told of each zoom the operator asks for with ⌥+, ⌥−, or ⌥0, so
    /// Settings can keep it; not called by `setSidebarZoom(percent:)`.
    var onSidebarZoomChanged: ((Int) -> Void)?

    /// Hides the sidebar and brings it back, from the title bar, so that
    /// neither the sidebar nor the pane gives up anything to it.
    let sidebarToggle = WorkspaceWindowChrome.makeSidebarToggle()
    /// What the delegate was last told, so it hears of each change once.
    var reportedCollapsed = false
    var reportedShowing: TerminalNotice.Origin?
    var reportedOtherShowing: TerminalNotice.Origin?
    var sidebarItem: NSSplitViewItem?
    var contentItem: NSSplitViewItem?
    var sidebarObservation: NSKeyValueObservation?
    lazy var inspector = WorkspaceInspectorView(welcome: welcomeView)
    /// Hides the inspector and brings it back, from the title bar.
    let inspectorToggle = WorkspaceWindowChrome.makeInspectorToggle()
    /// The tab the inspector is turned to; the app keeps it across launches.
    var inspectorTab = WorkspaceInspectorTab.waiting
    var reportedInspectorCollapsed = true
    var inspectorItem: NSSplitViewItem?
    var inspectorObservation: NSKeyValueObservation?
    /// The widths the sidebar and the inspector open at, which the app sets
    /// before the window is made; nil gives each its default.
    var sidebarWidth: CGFloat?
    var inspectorWidth: CGFloat?
    /// The widths the delegate was last told of.
    var reportedSidebarWidth: CGFloat?
    var reportedInspectorWidth: CGFloat?
    var splitObservation: NSObjectProtocol?
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
    /// Where the window's frame is saved; tests name their own or none.
    var frameAutosaveName: NSWindow.FrameAutosaveName? = AppDelegate.isRunningTests ? nil : "AgentMuxWorkspace"

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
    var newSessionBelow: (runtimeSessionID: String, parent: AttentionCycler.Key)?
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
    var ageTimer: Timer?

    init(delegate: WorkspaceDelegate, glyphs: Glyphs = .bundled, now: @escaping () -> Date = Date.init) {
        self.delegate = delegate
        self.glyphs = glyphs
        self.now = now
        super.init()
        welcome.onReceipt = { [weak self] receipt in
            guard let self, self.isOpen, receipt.title == "All caught up" else { return }
            WorkspaceCopyNotice.announce(receipt.title + ". " + receipt.detail, from: self.window)
        }
        welcome.onAction = { [weak self] command in self?.delegate?.workspacePerform(command) }
        inspector.onSource = { [weak self] identity in self?.delegate?.workspaceOpenHandoffSource(identity) }
        inspector.threads.command = { [weak self] in self?.delegate?.workspaceRecapCommand() }
        inspector.recap.command = { [weak self] in self?.delegate?.workspaceRecapCommand() }
        inspector.recap.onEvidence = { [weak self] identity, message in
            self?.delegate?.workspaceRecapEvidence(identity, message: message)
        }
    }

    /// Takes a copied runtime session ID; the general pasteboard unless a
    /// test says otherwise.
    var copyToPasteboard: (String) -> Void = { value in
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(value, forType: .string)
    }
    var copiedNoticeEnd: DispatchWorkItem?

}

import AppKit
import XCTest
@testable import AgentMux

/// The workspace's utility views as data: what a forwarded request becomes,
/// what each view runs, and that none of it is a session.
final class WorkspaceViewsModelTests: XCTestCase {
    private typealias F = WorkspaceFixture

    func testDecodesAForwardedViewFrame() throws {
        let line = Data(#"{"v":1,"seq":9,"type":"workspace_view","data":{"kind":"session","runtime_session_id":"\#(F.runtimeID(3))","await":true}}"#.utf8)
        XCTAssertEqual(
            SubscriptionClient.decodeFrame(line),
            .workspaceView(seq: 9, WorkspaceViewRequest(kind: "session", runtimeSessionID: F.runtimeID(3), awaited: true))
        )
        let shell = Data(#"{"v":1,"seq":10,"type":"workspace_view","data":{"kind":"shell","directory":"/work/api","title":"api","later":1}}"#.utf8)
        XCTAssertEqual(
            SubscriptionClient.decodeFrame(shell),
            .workspaceView(seq: 10, WorkspaceViewRequest(kind: "shell", directory: "/work/api", title: "api")),
            "a field this app does not know is ignored"
        )
        // A daemon that predates the feature never sends the frame; one that
        // sends a shape this app cannot read must not end the stream.
        let empty = Data(#"{"v":1,"seq":11,"type":"workspace_view","data":{}}"#.utf8)
        XCTAssertEqual(SubscriptionClient.decodeFrame(empty), .workspaceView(seq: 11, WorkspaceViewRequest()))
    }

    func testTheAppAsksForWorkspaceViews() throws {
        let data = try JSONEncoder().encode(SubscribeParams())
        let params = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(params["features"] as? [String], ["runtime_sessions", "workspace_views"])
    }

    func testEveryViewRunsAmxWithAKindAndATargetOnly() {
        XCTAssertEqual(WorkspaceUtilityView.dashboard.arguments, ["workspace-view", "dashboard"])
        XCTAssertEqual(WorkspaceUtilityView.overview.arguments, ["workspace-view", "overview"])
        let shell = WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "shell", directory: "/work/api; rm -rf /"))
        XCTAssertEqual(shell?.arguments, ["workspace-view", "shell", "/work/api; rm -rf /"],
                       "the target is one argument, whatever it holds")
        let editor = WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "editor", path: "/work/api/main.go", title: "main.go"))
        XCTAssertEqual(editor?.arguments, ["workspace-view", "edit", "/work/api/main.go"])
        XCTAssertEqual(editor?.title, "main.go")
        let pager = WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "pager", runtimeSessionID: F.runtimeID(2), title: "api"))
        XCTAssertEqual(pager?.arguments, ["workspace-view", "pager", F.runtimeID(2)])
        var views = WorkspaceViews()
        let terminal = views.openTerminal(beside: F.runtimeID(2))
        XCTAssertEqual(terminal.arguments, ["workspace-view", "session-shell", F.runtimeID(2)])
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "terminal", runtimeSessionID: F.runtimeID(2))),
                     "a terminal beside a session is the operator's to open, not a request's")
    }

    func testTheKeybindingReferenceAndThePickersAreTheOperatorsToOpen() {
        XCTAssertEqual(WorkspaceUtilityView.keybindings.arguments, ["workspace-view", "keybindings"])
        let icon = WorkspaceUtilityView.picker(.iconPicker, runtimeSessionID: F.runtimeID(2))
        XCTAssertEqual(icon?.arguments, ["workspace-view", "icon-picker", F.runtimeID(2)])
        XCTAssertEqual(icon?.aboutRuntimeSessionID, F.runtimeID(2))
        let color = WorkspaceUtilityView.picker(.colorPicker, runtimeSessionID: F.runtimeID(2))
        XCTAssertEqual(color?.arguments, ["workspace-view", "color-picker", F.runtimeID(2)])
        XCTAssertNil(color?.besideRuntimeSessionID, "a picker is not listed under its session")
        XCTAssertNil(WorkspaceUtilityView.picker(.shell, runtimeSessionID: F.runtimeID(2)))
        for kind in ["keybindings", "icon-picker", "color-picker"] {
            XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: kind, runtimeSessionID: F.runtimeID(2))),
                         "\(kind) is a key the operator presses, never a request")
        }
    }

    func testARequestThatNamesNoUsableTargetIsNotAView() {
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "session", runtimeSessionID: F.runtimeID(1))),
                     "a session is shown by its own native view, never as a utility")
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "exec", path: "/bin/sh")))
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "shell", directory: "work/api")))
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "shell")))
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "editor", path: "")))
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "pager", runtimeSessionID: "%4")))
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "pager", runtimeSessionID: "rs-XYZ")))
    }

    func testTheDashboardAndOverviewAreAlwaysListedAndOpenedViewsAreUnique() {
        var views = WorkspaceViews()
        XCTAssertEqual(views.all, [.dashboard, .overview])
        let shell = WorkspaceUtilityView(kind: .shell, target: "/work/api", title: "api")
        views.open(shell)
        views.open(WorkspaceUtilityView(kind: .shell, target: "/work/api", title: "another name"))
        views.open(WorkspaceUtilityView(kind: .editor, target: "/work/api", title: "api"))
        views.open(.dashboard)
        XCTAssertEqual(views.all.map(\.id), ["dashboard", "overview", "shell:/work/api", "editor:/work/api"])
        XCTAssertEqual(views.view("shell:/work/api")?.title, "api", "the view already open is the one kept")

        views.close("shell:/work/api")
        views.close("dashboard")
        XCTAssertEqual(views.all.map(\.id), ["dashboard", "overview", "editor:/work/api"], "the Dashboard cannot be unlisted")
    }

    func testTheShortcutSentToKittyGoesToTheWorkspaceOnlyWhenThereIsNoKittyToAsk() {
        let opened = OpenDashboardResult(windowID: 4, tabID: 2, opened: true)
        XCTAssertEqual(DashboardOpener.destination(for: .opened(opened, socket: "unix:/tmp/k")), .kitty(opened, socket: "unix:/tmp/k"))
        XCTAssertEqual(DashboardOpener.destination(for: .noSocket), .workspace)
        XCTAssertEqual(DashboardOpener.destination(for: .failed("bad config")), .failed("bad config"),
                       "a Kitty that answered with a failure is reported, not papered over")
        let unreachable = DashboardOpener.open(candidates: ["a"]) { _ in
            .failure(.command(AMXJSONError(code: "kitty_unavailable", message: "no kitty at that socket"), status: 1))
        }
        XCTAssertEqual(DashboardOpener.destination(for: unreachable), .failed("no kitty at that socket"))
        XCTAssertEqual(DashboardOpener.destination(for: DashboardOpener.open(candidates: []) { _ in .success(opened) }), .workspace)
    }

    func testALaunchWithNoKittyGoesToTmuxAndReportsItsRuntimeSession() {
        let project = ProjectEntry(profile: "work", title: "API")
        XCTAssertEqual(ProjectCatalog.workspaceLaunchArguments(for: project),
                       ["project", "launch", "--title", "API", "--profile", "work", "--backend", "tmux"])
        let stdout = Data(#"{"schema_version":1,"data":{"changed":true,"message":"launched session","backend":"tmux","runtime_session_id":"\#(F.runtimeID(7))"}}"#.utf8)
        guard case let .success(result) = AMXEnvelope.decode(ProjectLaunchResult.self, stdout: stdout, status: 0) else {
            return XCTFail("the tmux launch payload did not decode")
        }
        XCTAssertEqual(result.runtimeSessionID, F.runtimeID(7))
        XCTAssertEqual(result.windowID, 0, "a tmux launch names no Kitty window")
    }
}

/// The workspace window's utility views: the Dashboard, the Overview, and
/// the shells, editors, and pagers they ask for, beside the sessions.
@MainActor
final class WorkspaceViewsWindowTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!
    private var clock = Date(timeIntervalSince1970: 1_800_000_000)

    private let sessions = [
        F.tmux(1, project: "api", profile: "work"),
        F.tmux(2, project: "api", profile: "personal", icon: "stop", attention: true, changedAtMS: 2000),
        F.kitty(4, project: "web", icon: "key", attention: true, changedAtMS: 1000),
    ]

    override func setUp() async throws {
        app = FakeWorkspaceApp()
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback, now: { [unowned self] in self.clock })
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
        workspace.openForTesting()
    }

    override func tearDown() async throws {
        workspace.window.close()
        workspace = nil
        app = nil
    }

    private func index(ofView id: String) throws -> Int {
        try XCTUnwrap(workspace.items.firstIndex { item in
            if case let .view(view) = item { return view.id == id }
            return false
        })
    }

    func testTheDashboardIsATerminalOfItsOwnStartedWhenShown() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.madeViews, [], "listing the Dashboard starts nothing")

        XCTAssertTrue(workspace.showView("dashboard"))
        XCTAssertEqual(app.madeViews, ["dashboard"])
        XCTAssertEqual(workspace.content, .view(id: "dashboard"))
        let terminal = try XCTUnwrap(workspace.utilityTerminalView(for: "dashboard"))
        XCTAssertFalse(terminal.isHidden)
        XCTAssertTrue(workspace.placeholder.isHidden)
        XCTAssertTrue(workspace.window.firstResponder === terminal)
        XCTAssertEqual(workspace.sidebar.selectedRow, try index(ofView: "dashboard"))
        XCTAssertEqual(workspace.window.title, "Dashboard")
        XCTAssertNil(workspace.state.selection, "a utility view is not a session selection")
        XCTAssertEqual(app.made, [], "showing the Dashboard attaches to no session")

        let cell = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: try index(ofView: "dashboard")) as? WorkspaceRowCellView)
        XCTAssertEqual(cell.titleLabel.stringValue, "Dashboard")
        let heading = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: 0) as? WorkspaceGroupCellView)
        XCTAssertEqual(heading.nameLabel.stringValue, "Workspace")
        XCTAssertEqual(heading.countLabel.stringValue, "", "the views heading counts nothing")

        // The Overview is a second terminal; the Dashboard keeps running behind it.
        workspace.showView("overview")
        XCTAssertEqual(app.madeViews, ["dashboard", "overview"])
        XCTAssertTrue(terminal.isHidden)
        XCTAssertEqual(app.closedViews, [])
        workspace.showView("dashboard")
        XCTAssertEqual(app.madeViews, ["dashboard", "overview"], "coming back starts no second Dashboard")
        XCTAssertTrue(workspace.utilityTerminalView(for: "dashboard") === terminal)
    }

    func testAFocusRequestFromTheDashboardShowsTheNativeSessionView() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.showView("dashboard")
        let dashboard = try XCTUnwrap(workspace.utilityTerminalView(for: "dashboard"))

        workspace.open(WorkspaceViewRequest(kind: "session", runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        XCTAssertNil(workspace.viewSelection)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(app.made, [F.runtimeID(2)], "the session's own attachment, made by the workspace")
        let session = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(2)))
        XCTAssertTrue(workspace.window.firstResponder === session, "the native view takes the keyboard")
        XCTAssertTrue(dashboard.isHidden)
        XCTAssertEqual(app.closedViews, [], "the Dashboard keeps running behind the session")
        XCTAssertEqual(workspace.window.title, "api — personal")

        // A request for a session nobody lists does nothing at all.
        workspace.open(WorkspaceViewRequest(kind: "session", runtimeSessionID: F.runtimeID(9)))
        workspace.open(WorkspaceViewRequest(kind: "session", runtimeSessionID: "not-an-id", awaited: true))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        XCTAssertNil(workspace.awaited)
        XCTAssertEqual(app.made, [F.runtimeID(2)])
    }

    func testALaunchedSessionIsShownOnceTheDaemonListsIt() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.showView("dashboard")

        workspace.open(WorkspaceViewRequest(kind: "session", runtimeSessionID: F.runtimeID(5), awaited: true))
        XCTAssertEqual(workspace.awaited?.runtimeSessionID, F.runtimeID(5))
        XCTAssertEqual(workspace.content, .view(id: "dashboard"), "nothing moves until the session exists for the workspace")

        // A snapshot that still lacks it changes nothing.
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(workspace.content, .view(id: "dashboard"))

        app.store.apply(snapshot: F.snapshot(sessions + [F.tmux(5, project: "docs", profile: "work")]))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(5)))
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(5)))
        XCTAssertNil(workspace.awaited)
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(5)))
    }

    func testAnAwaitedSessionDoesNotPullTheOperatorBack() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.showView("dashboard")
        workspace.open(WorkspaceViewRequest(kind: "session", runtimeSessionID: F.runtimeID(5), awaited: true))

        // The operator went somewhere else before it was listed.
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        app.store.apply(snapshot: F.snapshot(sessions + [F.tmux(5, project: "docs")]))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))

        // One that never appears is forgotten.
        workspace.open(WorkspaceViewRequest(kind: "session", runtimeSessionID: F.runtimeID(6), awaited: true))
        clock = clock.addingTimeInterval(WorkspaceWindowController.awaitLimit + 1)
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertNil(workspace.awaited)
        clock = clock.addingTimeInterval(1)
        app.store.apply(snapshot: F.snapshot(sessions + [F.tmux(6, project: "late")]))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)), "a session listed after the wait ended is not jumped to")
    }

    func testShellEditorAndPagerOpenAsViewsAndGoBackWhenTheyEnd() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.showView("dashboard")

        workspace.open(WorkspaceViewRequest(kind: "shell", directory: "/work/api", title: "api"))
        XCTAssertEqual(workspace.viewSelection, "shell:/work/api")
        XCTAssertEqual(workspace.content, .view(id: "shell:/work/api"))
        XCTAssertEqual(app.madeViews, ["dashboard", "shell:/work/api"])
        let shell = try XCTUnwrap(workspace.utilityTerminalView(for: "shell:/work/api"))
        XCTAssertTrue(workspace.window.firstResponder === shell)
        XCTAssertEqual(workspace.sidebar.selectedRow, try index(ofView: "shell:/work/api"))
        let cell = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: try index(ofView: "shell:/work/api")) as? WorkspaceRowCellView)
        XCTAssertEqual(cell.titleLabel.stringValue, "api")
        XCTAssertEqual(cell.detailLabel.stringValue, "Shell · /work/api")

        // The same shell asked for again is the same view.
        workspace.showView("dashboard")
        workspace.open(WorkspaceViewRequest(kind: "shell", directory: "/work/api", title: "api"))
        XCTAssertEqual(app.madeViews, ["dashboard", "shell:/work/api"])
        XCTAssertTrue(workspace.utilityTerminalView(for: "shell:/work/api") === shell)

        // The shell exits: its view goes away and the Dashboard is back.
        workspace.terminalEnded(shell)
        XCTAssertEqual(app.closedViews, ["shell:/work/api"])
        XCTAssertNil(workspace.views.view("shell:/work/api"))
        XCTAssertEqual(workspace.viewSelection, "dashboard")
        XCTAssertEqual(workspace.content, .view(id: "dashboard"))
        XCTAssertTrue(workspace.window.firstResponder === workspace.utilityTerminalView(for: "dashboard"))

        // An editor opened from a session's view goes back to that session.
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.open(WorkspaceViewRequest(kind: "editor", path: "/work/api/main.go"))
        XCTAssertEqual(workspace.viewSelection, "editor:/work/api/main.go")
        workspace.closeSelectedView()
        XCTAssertEqual(app.closedViews, ["shell:/work/api", "editor:/work/api/main.go"])
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertNil(workspace.viewSelection)

        // A pager is read afresh each time it is asked for.
        workspace.open(WorkspaceViewRequest(kind: "pager", runtimeSessionID: F.runtimeID(1), title: "api"))
        workspace.open(WorkspaceViewRequest(kind: "pager", runtimeSessionID: F.runtimeID(1), title: "api"))
        let pager = "pager:\(F.runtimeID(1))"
        XCTAssertEqual(app.madeViews.filter { $0 == pager }.count, 2)
        XCTAssertEqual(app.closedViews.filter { $0 == pager }.count, 1)
        XCTAssertEqual(app.made, [F.runtimeID(1)], "a pager reads the session's scrollback and attaches nothing")
    }

    func testAPickerOpensForTheShownSessionAndGoesBackWhenItEnds() throws {
        app.store.apply(snapshot: F.snapshot(sessions))

        // Nothing to pick for: a view that is about no session, then a Kitty
        // session, whose pickers are Kitty's.
        workspace.showView("dashboard")
        XCTAssertNil(workspace.shownSessionRow)
        XCTAssertFalse(workspace.openPicker(.iconPicker))
        workspace.select(AttentionCycler.Key(socket: "unix:/tmp/kitty-1", windowID: 4))
        XCTAssertNotNil(workspace.shownSessionRow)
        XCTAssertFalse(workspace.openPicker(.colorPicker))
        XCTAssertEqual(app.madeViews, ["dashboard"])

        workspace.select(.embedded(F.runtimeID(2)), focus: true)
        XCTAssertTrue(workspace.openPicker(.colorPicker))
        let picker = "color-picker:\(F.runtimeID(2))"
        XCTAssertEqual(workspace.viewSelection, picker)
        XCTAssertEqual(app.madeViews, ["dashboard", picker])
        XCTAssertEqual(workspace.shownSessionRow?.embeddedRuntimeSessionID, F.runtimeID(2),
                       "the session keys still act on the session a picker is about")
        XCTAssertTrue(workspace.window.firstResponder === workspace.utilityTerminalView(for: picker))

        // The pick is made and the picker exits: back on the session.
        workspace.terminalEnded(try XCTUnwrap(workspace.utilityTerminalView(for: picker)))
        XCTAssertNil(workspace.views.view(picker))
        XCTAssertNil(workspace.viewSelection)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))

        // From a terminal beside a session, the picker is that session's.
        workspace.openTerminalBesideSession()
        XCTAssertTrue(workspace.openPicker(.iconPicker))
        XCTAssertEqual(workspace.viewSelection, "icon-picker:\(F.runtimeID(2))")
        XCTAssertEqual(app.made, [F.runtimeID(2)], "a picker attaches to nothing")
    }

    func testCommandLGoesBetweenTheTwoPlacesLastShown() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertFalse(workspace.toggleRecentPlace(), "nowhere has been shown yet")

        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        XCTAssertFalse(workspace.toggleRecentPlace(), "one place is not two")
        workspace.showView("dashboard")
        XCTAssertTrue(workspace.toggleRecentPlace())
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertNil(workspace.viewSelection)
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(1)))
        XCTAssertTrue(workspace.toggleRecentPlace())
        XCTAssertEqual(workspace.viewSelection, "dashboard")

        // The reference and the pickers are passed through: they are never
        // one of the two places.
        workspace.select(.embedded(F.runtimeID(2)), focus: true)
        workspace.open(.keybindings)
        workspace.closeSelectedView()
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        XCTAssertTrue(workspace.toggleRecentPlace())
        XCTAssertEqual(workspace.viewSelection, "dashboard")

        // A place that is gone is not somewhere to go back to.
        workspace.open(WorkspaceViewRequest(kind: "shell", directory: "/work/api"))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.closeView("shell:/work/api")
        XCTAssertFalse(workspace.toggleRecentPlace())
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
    }

    func testF7AndF9StepFromSessionToSessionWithoutLeavingTheApp() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let rows = workspace.state.rows
        workspace.select(rows[0].key, focus: true)
        for expected in [1, 2, 0] {
            workspace.selectNeighbor(1, sessionsOnly: true)
            XCTAssertEqual(workspace.state.selection, rows[expected].key)
            XCTAssertNil(workspace.viewSelection)
        }
        workspace.selectNeighbor(-1, sessionsOnly: true)
        XCTAssertEqual(workspace.state.selection, rows[2].key, "before the first one is the last")
        XCTAssertTrue(app.external.isEmpty, "a Kitty session is selected here, not raised in Kitty")

        // The terminals under a session are passed over.
        let embedded = try XCTUnwrap(rows.first { $0.embeddedRuntimeSessionID == F.runtimeID(1) })
        workspace.select(embedded.key, focus: true)
        workspace.openTerminalBesideSession()
        workspace.selectNeighbor(-1, sessionsOnly: true)
        XCTAssertEqual(workspace.state.selection, embedded.key, "up from a terminal is its own session")
    }

    func testTheCloseButtonClosesAnOpenedViewWhereverThePaneIs() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.showView("dashboard")
        workspace.open(WorkspaceViewRequest(kind: "shell", directory: "/work/api", title: "api"))
        workspace.open(WorkspaceViewRequest(kind: "editor", path: "/work/api/main.go"))
        func cell(_ id: String) throws -> WorkspaceRowCellView {
            try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: try index(ofView: id), makeIfNecessary: true) as? WorkspaceRowCellView)
        }
        XCTAssertTrue(try cell("dashboard").closeButton.isHidden, "the Dashboard is always listed")
        XCTAssertTrue(try cell("overview").closeButton.isHidden)

        // The pane is on the editor; the shell is closed from its row.
        try cell("shell:/work/api").closeButton.performClick(nil)
        XCTAssertEqual(app.closedViews, ["shell:/work/api"])
        XCTAssertNil(workspace.views.view("shell:/work/api"))
        XCTAssertEqual(workspace.viewSelection, "editor:/work/api/main.go", "the pane stays where it was")
        XCTAssertEqual(workspace.content, .view(id: "editor:/work/api/main.go"))

        // Closing the view the pane is on goes back to where it came from.
        try cell("editor:/work/api/main.go").closeButton.performClick(nil)
        XCTAssertEqual(app.closedViews, ["shell:/work/api", "editor:/work/api/main.go"])
        XCTAssertEqual(workspace.viewSelection, "dashboard")
        XCTAssertEqual(app.ended, [], "closing a view ends no session")
    }

    func testATerminalOpensRightBelowItsSessionAndIsNotASession() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let rowsBefore = workspace.state.rows

        // Nothing to open one beside: a view, then a Kitty session.
        workspace.showView("dashboard")
        workspace.openTerminalBesideSession()
        workspace.select(rowsBefore[2].key)
        workspace.openTerminalBesideSession()
        XCTAssertEqual(app.madeViews, ["dashboard"])

        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.openTerminalBesideSession()
        let first = "terminal:\(F.runtimeID(1))#1"
        XCTAssertEqual(workspace.viewSelection, first)
        XCTAssertEqual(workspace.content, .view(id: first))
        XCTAssertTrue(workspace.window.firstResponder === workspace.utilityTerminalView(for: first))
        XCTAssertEqual(workspace.state.rows, rowsBefore, "a terminal adds no session")
        XCTAssertEqual(workspace.attachedRuntimeSessionIDs, [F.runtimeID(1)], "it is not a second attachment")

        // It is listed right under its session; another follows it.
        workspace.openTerminalBesideSession()
        let second = "terminal:\(F.runtimeID(1))#2"
        let session = try XCTUnwrap(workspace.items.firstIndex { item in
            if case let .row(row) = item { return row.embeddedRuntimeSessionID == F.runtimeID(1) }
            return false
        })
        XCTAssertEqual(try index(ofView: first), session + 1)
        XCTAssertEqual(try index(ofView: second), session + 2)
        XCTAssertEqual(workspace.sidebar.selectedRow, session + 2)
        let cell = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: session + 2) as? WorkspaceRowCellView)
        XCTAssertEqual(cell.titleLabel.stringValue, "Terminal 2")

        // The digits count sessions only. Stepping stops at every session
        // and every terminal under one, in sidebar order, and wraps.
        workspace.selectSession(at: 1)
        XCTAssertEqual(workspace.state.selection, rowsBefore[1].key)
        XCTAssertEqual(rowsBefore[1].embeddedRuntimeSessionID, F.runtimeID(1))
        workspace.selectNeighbor(1)
        XCTAssertEqual(workspace.viewSelection, first)
        XCTAssertTrue(workspace.window.firstResponder === workspace.utilityTerminalView(for: first))
        workspace.selectNeighbor(1)
        XCTAssertEqual(workspace.viewSelection, second)
        workspace.selectNeighbor(1)
        XCTAssertNil(workspace.viewSelection)
        XCTAssertEqual(workspace.state.selection, rowsBefore[2].key)
        workspace.selectNeighbor(1)
        XCTAssertEqual(workspace.state.selection, rowsBefore[0].key, "past the last one is the first")
        workspace.selectNeighbor(-1)
        workspace.selectNeighbor(-1)
        XCTAssertEqual(workspace.viewSelection, second)
        XCTAssertEqual(workspace.state.rows, rowsBefore)

        // With Shift the step is from session to session. From a terminal,
        // the next session is the one after its own and the previous its own.
        workspace.selectNeighbor(1, raising: true)
        XCTAssertNil(workspace.viewSelection)
        XCTAssertEqual(workspace.state.selection, rowsBefore[2].key)
        workspace.showView(second)
        workspace.selectNeighbor(-1, raising: true)
        XCTAssertNil(workspace.viewSelection)
        XCTAssertEqual(workspace.state.selection, rowsBefore[1].key)
        workspace.selectNeighbor(1, raising: true)
        XCTAssertEqual(workspace.state.selection, rowsBefore[2].key, "the terminals under it are passed over")
        workspace.selectNeighbor(-1, raising: true)
        workspace.selectNeighbor(-1, raising: true)
        XCTAssertNil(workspace.viewSelection)
        XCTAssertEqual(workspace.state.selection, rowsBefore[0].key)

        // Its shell exits: it is unlisted and the pane goes back.
        workspace.showView(second)
        workspace.terminalEnded(try XCTUnwrap(workspace.utilityTerminalView(for: second)))
        XCTAssertNil(workspace.views.view(second))
        XCTAssertEqual(app.closedViews, [second])

        // Its session ends: the shell keeps running, among the workspace's views.
        app.store.apply(snapshot: F.snapshot([sessions[1], sessions[2]]))
        XCTAssertEqual(app.closedViews, [second])
        XCTAssertEqual(try index(ofView: first), 3)
    }

    func testUtilityViewsAreNeverSessionsAndNeverWait() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let waitingBefore = workspace.state.groups.map(\.attention)
        let rowsBefore = workspace.state.rows

        workspace.showView("dashboard")
        workspace.showView("overview")
        workspace.open(WorkspaceViewRequest(kind: "shell", directory: "/work/api"))
        workspace.open(WorkspaceViewRequest(kind: "editor", path: "/work/api/main.go"))
        workspace.open(WorkspaceViewRequest(kind: "pager", runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(workspace.runningViewIDs.count, 5)

        // Five utility programs later the session list is what the daemon said.
        XCTAssertEqual(workspace.state.rows, rowsBefore)
        XCTAssertEqual(workspace.state.groups.map(\.attention), waitingBefore)
        XCTAssertEqual(workspace.state.embeddedRuntimeSessionIDs, [F.runtimeID(1), F.runtimeID(2)])
        XCTAssertEqual(workspace.attachedRuntimeSessionIDs, [], "no view attached to a session, not even the pager of one")
        XCTAssertEqual(app.store.sessions.count, 3)

        // The attention shortcut walks sessions only.
        var cycler = AttentionCycler()
        var visited: [String] = []
        for _ in 0..<4 {
            guard let next = cycler.advance(in: app.store.sessions, snapshotSocket: "unix:/tmp/kitty-1") else { break }
            visited.append(next.project)
        }
        XCTAssertEqual(Set(visited), ["api", "web"])

        // Stepping through sessions never lands on a view, and a view is not
        // retired by a snapshot the way a session is.
        workspace.selectNeighbor(1)
        XCTAssertNotNil(workspace.state.selection)
        XCTAssertNil(workspace.viewSelection)
        app.store.apply(snapshot: F.snapshot([]))
        XCTAssertEqual(workspace.runningViewIDs.count, 5, "sessions ending ends no utility view")
        XCTAssertEqual(app.closedViews, [])
        for case let .view(view) in workspace.items {
            let cell = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: try index(ofView: view.id)) as? WorkspaceRowCellView)
            XCTAssertFalse(["🛑", "🔑", "⚡"].contains(cell.glyphLabel.stringValue), "a view shows no session state")
        }
    }

    func testAQuitDashboardSaysSoAndReopens() throws {
        workspace.showView("dashboard")
        let first = try XCTUnwrap(workspace.utilityTerminalView(for: "dashboard"))

        workspace.terminalEnded(first)
        XCTAssertEqual(workspace.content, .viewEnded(id: "dashboard"))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Dashboard closed")
        XCTAssertNotNil(workspace.views.view("dashboard"), "it stays listed")
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.madeViews, ["dashboard"], "it is not restarted behind the operator's back")

        workspace.placeholder.press()
        XCTAssertEqual(app.madeViews, ["dashboard", "dashboard"])
        XCTAssertEqual(workspace.content, .view(id: "dashboard"))
    }

    func testAViewThatCannotStartSaysWhyAndIsRetriedOnlyWhenAsked() throws {
        app.viewProblem = "amx is still being resolved."
        workspace.showView("dashboard")
        XCTAssertEqual(workspace.content, .viewUnavailable(id: "dashboard", "amx is still being resolved."))
        XCTAssertEqual(workspace.placeholder.messageLabel.stringValue, "amx is still being resolved.")
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.madeViews, [])

        app.viewProblem = nil
        workspace.retryTerminal()
        XCTAssertEqual(app.madeViews, ["dashboard"])
        XCTAssertEqual(workspace.content, .view(id: "dashboard"))
    }

    func testClosingTheWindowKeepsUtilityProgramsAndDetachesSessions() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.open(WorkspaceViewRequest(kind: "shell", directory: "/work/api"))
        workspace.showView("dashboard")

        workspace.window.close()
        XCTAssertEqual(app.closed, [F.runtimeID(1)], "the attachment is let go")
        XCTAssertEqual(app.closedViews, [], "a shell and the Dashboard keep running")
        XCTAssertEqual(workspace.runningViewIDs, ["dashboard", "shell:/work/api"])

        workspace.openForTesting()
        XCTAssertEqual(workspace.content, .view(id: "dashboard"), "the window comes back where it was")
        XCTAssertEqual(app.madeViews, ["shell:/work/api", "dashboard"], "and starts nothing twice")
    }

    func testTheEmptyPaneOffersTheDashboard() throws {
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "No live sessions")
        XCTAssertEqual(workspace.placeholder.button.title, "Open Dashboard")
        workspace.placeholder.press()
        XCTAssertEqual(workspace.viewSelection, "dashboard")
        XCTAssertEqual(app.madeViews, ["dashboard"])

        // The arrow keys reach a view: it is shown, and the sidebar keeps the keyboard.
        workspace.sidebar.selectRowIndexes([try index(ofView: "overview")], byExtendingSelection: false)
        XCTAssertEqual(workspace.viewSelection, "overview")
        XCTAssertEqual(workspace.content, .view(id: "overview"))
    }
}

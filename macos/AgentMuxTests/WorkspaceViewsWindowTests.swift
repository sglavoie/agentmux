import AppKit
import XCTest
@testable import AgentMux

/// The workspace window's utility views: the Dashboard, the Overview, and
/// the shells, editors, and pagers they ask for, beside the sessions.
@MainActor
final class WorkspaceViewsWindowTests: XCTestCase {
    typealias F = WorkspaceFixture
    var app: FakeWorkspaceApp!
    var workspace: WorkspaceWindowController!
    var clock = Date(timeIntervalSince1970: 1_800_000_000)

    /// Every turn ended, so all three are listed under Done.
    let sessions = [
        F.tmux(1, project: "api", profile: "work", icon: "stop"),
        F.tmux(2, project: "api", profile: "personal", icon: "stop", attention: true, changedAtMS: 2000),
        F.tmux(4, project: "web", icon: "stop", attention: true, changedAtMS: 1000),
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

    func index(ofView id: String) throws -> Int {
        try XCTUnwrap(workspace.items.firstIndex { item in
            if case let .view(view) = item { return view.id == id }
            return false
        })
    }

    func testSessionsAreDraggedWithinTheirProjectAndTerminalsWithinTheirSession() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let (work, personal) = (F.runtimeID(1), F.runtimeID(2))
        workspace.select(.embedded(personal), focus: true)
        workspace.openTerminalBesideSession()
        workspace.openTerminalBesideSession()
        let (first, second) = ("terminal:\(personal)#1", "terminal:\(personal)#2")
        func sessionIndex(_ id: String) throws -> Int {
            try XCTUnwrap(workspace.items.firstIndex { item in
                if case let .row(row) = item { return row.embeddedRuntimeSessionID == id }
                return false
            })
        }
        // api, personal, its two terminals, work, web, the tmux session.
        let top = try sessionIndex(personal)
        XCTAssertEqual(try sessionIndex(work), top + 3)
        XCTAssertEqual(workspace.dropSlots(forItemAt: top), [top, top + 3, top + 4])
        XCTAssertEqual(workspace.dropSlots(forItemAt: top + 1), [top + 1, top + 2, top + 3])
        XCTAssertTrue(workspace.dropSlots(forItemAt: 1).isEmpty, "the Dashboard stays where it is")
        XCTAssertTrue(workspace.dropSlots(forItemAt: top - 1).isEmpty, "a project line is not dragged")
        XCTAssertNotNil(workspace.tableView(workspace.sidebar, pasteboardWriterForRow: top))
        XCTAssertNotNil(workspace.tableView(workspace.sidebar, pasteboardWriterForRow: top + 1))
        XCTAssertNil(workspace.tableView(workspace.sidebar, pasteboardWriterForRow: top + 6),
                     "a project's only session has nowhere to go")

        let ordersBeforeDragging = app.orders
        XCTAssertFalse(workspace.moveItem(at: top, to: top - 1), "not above its project")
        XCTAssertFalse(workspace.moveItem(at: top, to: top + 2), "among its own terminals is where it is")
        XCTAssertFalse(workspace.moveItem(at: top, to: top + 5), "not into another project")
        XCTAssertEqual(app.orders, ordersBeforeDragging)

        XCTAssertTrue(workspace.moveItem(at: top, to: top + 4))
        XCTAssertEqual(workspace.state.rows.map(\.embeddedRuntimeSessionID), [work, personal, F.runtimeID(4)])
        XCTAssertEqual(try sessionIndex(work), top)
        XCTAssertEqual(try index(ofView: first), top + 2, "a session takes its terminals with it")
        XCTAssertEqual(try index(ofView: second), top + 3)
        XCTAssertEqual(app.orders, ordersBeforeDragging + [workspace.order])
        XCTAssertEqual(workspace.viewSelection, second, "the pane stays where it was")
        XCTAssertEqual(workspace.sidebar.selectedRow, top + 3)

        // The order holds over snapshots, and a new session follows it.
        app.store.apply(snapshot: F.snapshot(sessions + [F.tmux(3, project: "api", profile: "alpha", icon: "stop")]))
        XCTAssertEqual(workspace.state.rows.map(\.embeddedRuntimeSessionID), [work, personal, F.runtimeID(3), F.runtimeID(4)])

        XCTAssertFalse(workspace.moveItem(at: top + 3, to: top + 5), "a terminal does not leave its session")
        XCTAssertTrue(workspace.moveItem(at: top + 3, to: top + 2))
        XCTAssertEqual(workspace.views.terminals(beside: personal).map(\.id), [second, first])
        XCTAssertEqual(try index(ofView: second), top + 2)
        XCTAssertTrue(workspace.moveItem(at: top + 2, to: top + 4))
        XCTAssertEqual(workspace.views.terminals(beside: personal).map(\.id), [first, second])
        XCTAssertEqual(app.orders.count, ordersBeforeDragging.count + 1, "moving a terminal changes no session order")
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
        XCTAssertEqual(cell.detailLabel.stringValue, "", "a shell running nothing has a blank line")

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

        // Nothing to pick for: a view that is about no session.
        workspace.showView("dashboard")
        XCTAssertNil(workspace.shownSessionRow)
        XCTAssertFalse(workspace.openPicker(.iconPicker))
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
        XCTAssertTrue(app.external.isEmpty, "a tmux session is selected here")

        // The terminals under a session are passed over.
        let embedded = try XCTUnwrap(rows.first { $0.embeddedRuntimeSessionID == F.runtimeID(1) })
        workspace.select(embedded.key, focus: true)
        workspace.openTerminalBesideSession()
        workspace.selectNeighbor(-1, sessionsOnly: true)
        XCTAssertEqual(workspace.state.selection, embedded.key, "up from a terminal is its own session")
    }

    func testTheCloseButtonClosesAnOpenedViewWhereverThePaneIs() throws {
        workspace.setShowOverviewInSidebar(true)
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
}

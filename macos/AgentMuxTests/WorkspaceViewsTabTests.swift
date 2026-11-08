import AppKit
import XCTest
@testable import AgentMux

extension WorkspaceViewsWindowTests {
    func testProjectTabsCycleSessionsAndTerminalsInBothDirections() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let personal = AttentionCycler.Key.embedded(F.runtimeID(2))
        let work = AttentionCycler.Key.embedded(F.runtimeID(1))
        workspace.select(personal, focus: true)
        workspace.openTerminalBesideSession()
        let personalTerminal = try XCTUnwrap(workspace.viewSelection)
        workspace.select(work, focus: true)
        workspace.openTerminalBesideSession()
        let first = try XCTUnwrap(workspace.viewSelection)
        workspace.openTerminalBesideSession()
        let second = try XCTUnwrap(workspace.viewSelection)

        // Start at the last terminal: wrap without entering web's session.
        workspace.selectProjectTab(1)
        XCTAssertEqual(workspace.state.selection, personal)
        XCTAssertNil(workspace.viewSelection)
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(2)))
        workspace.selectProjectTab(1)
        XCTAssertEqual(workspace.viewSelection, personalTerminal)
        workspace.selectProjectTab(1)
        XCTAssertEqual(workspace.state.selection, work)
        XCTAssertNil(workspace.viewSelection)
        workspace.selectProjectTab(1)
        XCTAssertEqual(workspace.viewSelection, first)
        workspace.selectProjectTab(1)
        XCTAssertEqual(workspace.viewSelection, second)
        XCTAssertTrue(workspace.window.firstResponder === workspace.utilityTerminalView(for: second))

        workspace.selectProjectTab(-1)
        XCTAssertEqual(workspace.viewSelection, first)
        workspace.selectProjectTab(-1)
        XCTAssertEqual(workspace.state.selection, work)
        XCTAssertNil(workspace.viewSelection)
        workspace.selectProjectTab(-1)
        XCTAssertEqual(workspace.viewSelection, personalTerminal)
        workspace.selectProjectTab(-1)
        XCTAssertEqual(workspace.state.selection, personal)
        workspace.selectProjectTab(-1)
        XCTAssertEqual(workspace.viewSelection, second)
        XCTAssertEqual(app.external, [])

        // Navigation follows a terminal drag, rather than its creation ordinal.
        XCTAssertTrue(workspace.moveItem(at: try index(ofView: second), to: try index(ofView: first)))
        workspace.select(work, focus: true)
        workspace.selectProjectTab(1)
        XCTAssertEqual(workspace.viewSelection, second)
        workspace.selectProjectTab(1)
        XCTAssertEqual(workspace.viewSelection, first)
    }

    func testProjectTabsIncludePinnedAndCollapsedSessionsInArrangedOrder() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let personal = AttentionCycler.Key.embedded(F.runtimeID(2))
        let work = AttentionCycler.Key.embedded(F.runtimeID(1))
        workspace.setPinned(try XCTUnwrap(workspace.state.rows.first { $0.project == "web" }).key, true)
        workspace.select(work, focus: true)
        workspace.openTerminalBesideSession()
        let terminal = try XCTUnwrap(workspace.viewSelection)
        workspace.select(work)
        workspace.togglePinOfShownSession()
        workspace.setCollapsed(project: WorkspaceSidebar.pinnedSection, pinned: true, true)
        workspace.setCollapsed(project: "api", pinned: false, true)

        workspace.selectProjectTab(1)
        XCTAssertEqual(workspace.viewSelection, terminal)
        XCTAssertFalse(try XCTUnwrap(workspace.state.groups.first { $0.pinned }).collapsed)
        workspace.selectProjectTab(1)
        XCTAssertEqual(workspace.state.selection, personal)
        XCTAssertNil(workspace.viewSelection)
        XCTAssertFalse(try XCTUnwrap(workspace.state.groups.first { $0.project == "api" }).collapsed)
        workspace.selectProjectTab(1)
        XCTAssertEqual(workspace.state.selection, work, "wrap to the pin in the same project")
        workspace.selectProjectTab(-1)
        XCTAssertEqual(workspace.state.selection, personal)
    }

    func testProjectTabsAreInertWithoutAProjectAndStayOnASingleTab() throws {
        XCTAssertFalse(workspace.canSelectProjectTab)
        workspace.selectProjectTab(1)
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.selectSession(at: 2)
        let only = workspace.state.selection
        XCTAssertTrue(workspace.canSelectProjectTab)
        workspace.selectProjectTab(1)
        workspace.selectProjectTab(-1)
        XCTAssertEqual(workspace.state.selection, only)
        XCTAssertEqual(app.external, [])
        workspace.showView("dashboard")
        XCTAssertFalse(workspace.canSelectProjectTab)
        workspace.selectProjectTab(1)
        workspace.selectProjectTab(-1)
        XCTAssertEqual(workspace.viewSelection, "dashboard")
    }

    func testATerminalOpensRightBelowItsSessionAndIsNotASession() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let rowsBefore = workspace.state.rows

        // Nothing to open one beside while the Dashboard is selected.
        workspace.showView("dashboard")
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

        // Its session ends: its remaining shell closes too.
        app.store.apply(snapshot: F.snapshot([sessions[1], sessions[2]]))
        XCTAssertEqual(app.closedViews, [second, first])
        XCTAssertNil(workspace.views.view(first))
        XCTAssertNil(workspace.utilityTerminalView(for: first))
    }
}

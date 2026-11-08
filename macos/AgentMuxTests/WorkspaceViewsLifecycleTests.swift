import AppKit
import XCTest
@testable import AgentMux

extension WorkspaceViewsWindowTests {
    func testEndingSessionClosesAllItsTerminalsOnlyAfterSuccess() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.select(.embedded(F.runtimeID(2)), focus: true)
        workspace.openTerminalBesideSession()
        let unrelated = try XCTUnwrap(workspace.viewSelection)
        workspace.openNewShell(in: "/work/api")
        let shell = try XCTUnwrap(workspace.viewSelection)
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.openTerminalBesideSession()
        workspace.openTerminalBesideSession()
        let children = workspace.views.terminals(beside: F.runtimeID(1)).map(\.id)
        let made = app.madeViews
        app.runningViews = Set(children)
        workspace.confirmViewClose = { _, _ in XCTFail("ending the session already authorizes its terminal cleanup") }
        workspace.confirmClose = { _, answer in answer(true) }
        app.holdsCloses = true

        workspace.closeSession(.embedded(F.runtimeID(1)))
        XCTAssertTrue(app.closedViews.isEmpty, "keep shells until the close succeeds")
        XCTAssertFalse(workspace.openTerminalBesideSession(), "do not add terminals while ending")
        app.finishClosing()

        XCTAssertEqual(Set(app.closedViews), Set(children))
        XCTAssertTrue(workspace.views.terminals(beside: F.runtimeID(1)).isEmpty)
        XCTAssertTrue(children.allSatisfy { workspace.utilityTerminalView(for: $0) == nil })
        XCTAssertNotNil(workspace.utilityTerminalView(for: unrelated))
        XCTAssertNotNil(workspace.utilityTerminalView(for: shell))
        XCTAssertEqual(workspace.content, .view(id: "dashboard"))
        XCTAssertEqual(app.madeViews, made + ["dashboard"], "cleanup cannot recreate a sibling terminal")
        app.store.apply(snapshot: F.snapshot(Array(sessions.dropFirst())))
        XCTAssertEqual(app.closedViews.count, children.count, "snapshot cleanup is idempotent")
    }

    func testCancelledAndFailedSessionClosePreserveItsTerminals() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.openTerminalBesideSession()
        let child = try XCTUnwrap(workspace.viewSelection)
        let terminal = try XCTUnwrap(workspace.utilityTerminalView(for: child))
        workspace.confirmClose = { _, answer in answer(false) }
        workspace.closeSession(.embedded(F.runtimeID(1)))
        XCTAssertTrue(app.ended.isEmpty)
        XCTAssertTrue(app.closedViews.isEmpty)

        workspace.confirmClose = { _, answer in answer(true) }
        app.closeProblem = "server unavailable"
        var problems: [String] = []
        workspace.reportCloseProblem = { _, problem in problems.append(problem) }
        workspace.closeSession(.embedded(F.runtimeID(1)))
        XCTAssertEqual(problems, ["server unavailable"])
        XCTAssertTrue(app.closedViews.isEmpty)
        XCTAssertTrue(workspace.utilityTerminalView(for: child) === terminal)
        XCTAssertEqual(workspace.viewSelection, child)
        XCTAssertTrue(workspace.canOpenTerminalBesideSession)
    }

    func testLiveRemovalClosesChildTerminalsAcrossPanesButDisconnectionAndDetachKeepThem() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.openTerminalBesideSession()
        let first = try XCTUnwrap(workspace.viewSelection)
        workspace.split(with: .embedded(F.runtimeID(1)))
        workspace.openTerminalBesideSession()
        let second = try XCTUnwrap(workspace.viewSelection)
        XCTAssertEqual(workspace.otherPlace, .view(first))
        workspace.detachSession(.embedded(F.runtimeID(1)))
        XCTAssertTrue(app.closedViews.isEmpty)
        app.store.apply(.disconnected(.notRunning, retryIn: 0.5))
        XCTAssertTrue(app.closedViews.isEmpty)
        XCTAssertEqual(workspace.viewSelection, second)

        app.store.apply(snapshot: F.snapshot(Array(sessions.dropFirst())))
        XCTAssertEqual(Set(app.closedViews), [first, second])
        XCTAssertFalse(workspace.isSplit)
        XCTAssertEqual(workspace.content, .view(id: "dashboard"))
        XCTAssertTrue(workspace.views.terminals(beside: F.runtimeID(1)).isEmpty)
        XCTAssertTrue(app.ended.isEmpty, "the snapshot already reports the session gone")
    }

    func testEndingBackgroundSessionPreservesShownTerminal() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.openTerminalBesideSession()
        let child = try XCTUnwrap(workspace.viewSelection)
        workspace.select(.embedded(F.runtimeID(2)), focus: true)
        workspace.confirmClose = { _, answer in answer(true) }

        workspace.closeSession(.embedded(F.runtimeID(1)))
        XCTAssertEqual(app.closedViews, [child])
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(2)))
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(2)))
    }

    func testSnapshotBeforeCloseReplyCleansTerminalsEvenWithWindowClosed() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.openTerminalBesideSession()
        let child = try XCTUnwrap(workspace.viewSelection)
        workspace.confirmClose = { _, answer in answer(true) }
        app.holdsCloses = true
        workspace.closeSession(.embedded(F.runtimeID(1)))
        workspace.window.close()
        XCTAssertTrue(app.closedViews.isEmpty)

        app.store.apply(snapshot: F.snapshot(Array(sessions.dropFirst())))
        XCTAssertEqual(app.closedViews, [child])
        XCTAssertNil(workspace.views.view(child))
        XCTAssertFalse(workspace.isOpen)
        app.finishClosing()
        XCTAssertEqual(app.closedViews, [child])
        workspace.openForTesting()
        XCTAssertEqual(workspace.content, .view(id: "dashboard"))
        XCTAssertEqual(app.madeViews, [child, "dashboard"])
    }

    func testSavedLayoutDoesNotRestoreOrphanTerminals() {
        let first = WorkspaceUtilityView(kind: .terminal, target: F.runtimeID(1), title: "Terminal", ordinal: 1)
        let second = WorkspaceUtilityView(kind: .terminal, target: F.runtimeID(1), title: "Terminal 2", ordinal: 2)
        workspace.restoreLayout(.init(active: .utility(second), other: .utility(first), activeOnRight: true))
        app.store.apply(snapshot: F.snapshot(Array(sessions.dropFirst())))
        XCTAssertTrue(workspace.views.terminals(beside: F.runtimeID(1)).isEmpty)
        XCTAssertFalse(workspace.isSplit)
        XCTAssertEqual(workspace.content, .view(id: "dashboard"))
        XCTAssertEqual(app.madeViews, ["dashboard"])
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
        XCTAssertEqual(workspace.state.embeddedRuntimeSessionIDs, [F.runtimeID(1), F.runtimeID(4), F.runtimeID(2)])
        XCTAssertEqual(workspace.attachedRuntimeSessionIDs, [], "no view attached to a session, not even the pager of one")
        XCTAssertEqual(app.store.sessions.count, 3)

        // The attention shortcut walks sessions only.
        var cycler = AttentionCycler()
        var visited: [String] = []
        for _ in 0..<4 {
            guard let next = cycler.advance(in: app.store.sessions) else { break }
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

    func testClosingTheDashboardTheWayItQuitsOffersToReopenIt() throws {
        workspace.showView("dashboard")
        workspace.closeView("dashboard")
        XCTAssertEqual(workspace.viewSelection, "dashboard", "the pane stays where it was")
        XCTAssertEqual(workspace.content, .viewEnded(id: "dashboard"))
        XCTAssertEqual(workspace.placeholder.button.title, "Reopen")
        workspace.placeholder.press()
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

    func testTheEmptyPaneOffersLaunchHistoryAndDashboard() throws {
        workspace.setShowOverviewInSidebar(true)
        app.store.apply(snapshot: F.snapshot([]))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "No live sessions")
        XCTAssertEqual(workspace.placeholder.button.title, "Launch Project…")
        workspace.placeholder.press()
        XCTAssertEqual(app.launchMenuRequests, 1)
        XCTAssertTrue(app.madeViews.isEmpty)
        let buttons = workspace.placeholder.secondaryActions.arrangedSubviews.compactMap { $0 as? NSButton }
        XCTAssertEqual(buttons.map(\.title), ["Conversation History", "Open Dashboard"])
        buttons[0].performClick(nil)
        XCTAssertEqual(app.performedCommands, [.history])
        buttons[1].performClick(nil)
        XCTAssertEqual(workspace.viewSelection, "dashboard")
        XCTAssertEqual(app.madeViews, ["dashboard"])

        // The arrow keys reach a view: it is shown, and the sidebar keeps the keyboard.
        workspace.sidebar.selectRowIndexes([try index(ofView: "overview")], byExtendingSelection: false)
        XCTAssertEqual(workspace.viewSelection, "overview")
        XCTAssertEqual(workspace.content, .view(id: "overview"))
    }
}

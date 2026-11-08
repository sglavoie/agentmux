import AppKit
import GhosttyKit
import ScreenCaptureKit
import XCTest
@testable import AgentMux

extension WorkspaceWindowTests {
    func testClosingAViewWhoseProgramIsRunningAsksFirst() throws {
        let shell = WorkspaceUtilityView(kind: .shell, target: "/work/api", title: "Shell")
        workspace.open(shell)
        var asked: [(String, @MainActor (Bool) -> Void)] = []
        workspace.confirmViewClose = { title, answer in asked.append((title, answer)) }

        // Nothing running: ⌘W closes at once.
        XCTAssertTrue(workspace.closeShown())
        XCTAssertEqual(asked.count, 0)
        XCTAssertEqual(app.closedViews, [shell.id])

        // Running: ⌘W, the row's ×, and the menu's Close all ask by name.
        workspace.open(shell)
        app.runningViews = [shell.id]
        XCTAssertTrue(workspace.closeShown())
        XCTAssertEqual(asked.map(\.0), ["Shell"])
        XCTAssertEqual(app.closedViews, [shell.id], "nothing ends before the answer")
        asked[0].1(false)
        XCTAssertEqual(app.closedViews, [shell.id])
        XCTAssertEqual(workspace.content, .view(id: shell.id))
        workspace.closeView(shell.id)
        asked[1].1(true)
        XCTAssertEqual(app.closedViews, [shell.id, shell.id])
        XCTAssertNil(workspace.views.view(shell.id))

        // The Dashboard and Past Sessions never ask, whatever Ghostty says.
        let history = WorkspaceUtilityView.history(ConversationIdentity(harness: "claude", sessionID: "0b6f-1c2e"))
        workspace.open(history)
        app.runningViews = [WorkspaceUtilityView.dashboard.id, history.id]
        workspace.closeView(history.id)
        XCTAssertNil(workspace.views.view(history.id))
        workspace.showView(WorkspaceUtilityView.dashboard.id)
        let dashboard = try XCTUnwrap(workspace.utilityTerminalView(for: WorkspaceUtilityView.dashboard.id))
        workspace.terminalAskedToClose(dashboard, needsConfirm: true)
        workspace.closeView(WorkspaceUtilityView.dashboard.id)
        XCTAssertEqual(asked.count, 2)
    }

    func testQuittingAsksOnceNamingEveryViewWhoseProgramIsRunning() throws {
        var asked: [([String], @MainActor (Bool) -> Void)] = []
        workspace.confirmQuitWithViews = { titles, answer in asked.append((titles, answer)) }
        XCTAssertFalse(workspace.confirmQuit { _ in XCTFail("nothing to ask") })

        let shell = WorkspaceUtilityView(kind: .shell, target: "/work/api", title: "Shell")
        let editor = WorkspaceUtilityView(kind: .editor, target: "/work/api/main.go", title: "main.go")
        let pager = WorkspaceUtilityView(kind: .pager, target: F.runtimeID(2), title: "api")
        for view in [shell, editor, pager] { workspace.open(view) }
        workspace.showView(WorkspaceUtilityView.dashboard.id)
        app.runningViews = [shell.id, pager.id, WorkspaceUtilityView.dashboard.id]
        // A session's terminal only detaches, so it is never named.
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))

        var answers: [Bool] = []
        XCTAssertTrue(workspace.confirmQuit { answers.append($0) })
        XCTAssertEqual(asked.map(\.0), [["Shell", "api"]])
        asked[0].1(false)
        XCTAssertEqual(answers, [false])
        XCTAssertNotNil(workspace.views.view(shell.id), "a cancelled quit closes nothing")
    }

    func testQuittingWithTheWindowClosedStillAsksAboutViewsWhoseProgramIsRunning() throws {
        var asked: [([String], @MainActor (Bool) -> Void)] = []
        workspace.confirmQuitWithViews = { titles, answer in asked.append((titles, answer)) }
        let shell = WorkspaceUtilityView(kind: .shell, target: "/work/api", title: "Shell")
        workspace.open(shell)
        app.runningViews = [shell.id]
        workspace.window.close()
        XCTAssertFalse(workspace.isOpen)
        XCTAssertNotNil(workspace.utilityTerminalView(for: shell.id), "a closed window keeps the shell running")
        XCTAssertEqual(app.closedViews, [])

        var answers: [Bool] = []
        XCTAssertTrue(workspace.confirmQuit { answers.append($0) })
        XCTAssertEqual(asked.map(\.0), [["Shell"]])
        XCTAssertFalse(workspace.isOpen, "asking does not open the window, so the next launch keeps it closed")
        asked[0].1(false)
        XCTAssertEqual(answers, [false])
        XCTAssertEqual(app.closedViews, [], "a cancelled quit ends nothing")

        // Once its program has exited, there is nothing to ask about.
        app.runningViews = []
        XCTAssertFalse(workspace.confirmQuit { _ in XCTFail("nothing to ask") })
    }

    func testATerminalThatCannotBeMadeSaysWhyAndIsRetriedOnlyWhenAsked() throws {
        app.problem = "amx is still being resolved."
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        XCTAssertEqual(workspace.content, .unavailable(runtimeSessionID: F.runtimeID(1), "amx is still being resolved."))
        XCTAssertEqual(workspace.placeholder.messageLabel.stringValue, "amx is still being resolved.")

        app.problem = nil
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.made, [], "a snapshot alone does not ask again")
        workspace.retryTerminal()
        XCTAssertEqual(app.made, [F.runtimeID(1)], "the app does, once amx is resolved")
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
    }

    func testClosingTheWindowDetachesEveryTerminalAndReopeningReattaches() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        workspace.activate(try row("api", "personal"))

        workspace.window.close()
        XCTAssertFalse(workspace.isOpen)
        XCTAssertEqual(Set(app.closed), [F.runtimeID(1), F.runtimeID(2)])
        XCTAssertEqual(workspace.attachedRuntimeSessionIDs, [])
        XCTAssertEqual(app.visibility, [false], "the app hears the window closed")

        // Snapshots while closed keep the rows current and attach nothing.
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.made.count, 2)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)), "the selection is remembered")

        workspace.openForTesting()
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(app.made.count, 3, "only the selected session is attached again")
    }

    func testReopeningNeverTakesASessionAnotherTerminalAttachedMeanwhile() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "personal"))
        workspace.window.close()

        // `amx attach` in another terminal took the session while closed.
        app.statuses[F.runtimeID(2)] = .live(clients: 1)
        workspace.openForTesting()
        XCTAssertEqual(app.asked.last, F.runtimeID(2), "amx is asked before anything attaches")
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(app.made.count, 1, "the other terminal keeps the session")
    }

    /// A session's zoom is the terminal's, and the terminal is let go with
    /// the window: the next one for the session is made at the same size.
    func testASessionsZoomSurvivesItsTerminalBeingMadeAgain() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        workspace.activate(try row("api", "personal"))
        XCTAssertEqual(app.madeFontSizes, [nil, nil], "a first terminal takes the app's size")

        app.reportedFontSizes = [F.runtimeID(1): 18, F.runtimeID(2): 9.5]
        workspace.window.close()
        XCTAssertEqual(workspace.fontSizes, [F.runtimeID(1): 18, F.runtimeID(2): 9.5])

        workspace.openForTesting()
        XCTAssertEqual(app.madeFontSizes.last, 9.5, "the shown session comes back at its zoom")
        workspace.activate(try row("api", "work"))
        XCTAssertEqual(app.madeFontSizes.last, 18, "each session keeps its own")
    }

    /// Typing in the list matches a line's name, not the glyphs its label
    /// starts with; headings are never matched.
    func testTypingInTheListMatchesNames() {
        app.store.apply(snapshot: F.snapshot(sessions))
        let strings = workspace.items.indices.map { workspace.tableView(NSTableView(), typeSelectStringFor: nil, row: $0) }
        for (item, string) in zip(workspace.items, strings) {
            switch item {
            case let .row(row): XCTAssertEqual(string, row.displayName)
            case let .view(view): XCTAssertEqual(string, view.title)
            case .navigation, .section, .overview, .group: XCTAssertNil(string)
            }
        }
        XCTAssertTrue(strings.contains("Dashboard"))
        XCTAssertTrue(workspace.items.contains { if case .row = $0 { return true } else { return false } })
    }

    /// Full screen is kept for the next launch while the window is open; a
    /// window closed in full screen keeps it.
    func testFullScreenIsReportedOnlyWhileOpen() {
        let note = Notification(name: NSWindow.didEnterFullScreenNotification, object: workspace.window)
        workspace.windowDidEnterFullScreen(note)
        workspace.windowDidExitFullScreen(note)
        XCTAssertEqual(app.fullScreenChanges, [true, false])
        workspace.window.close()
        workspace.windowDidExitFullScreen(note)
        XCTAssertEqual(app.fullScreenChanges, [true, false], "closing is not leaving full screen")
    }

    /// A new default zoom reaches the sessions whose terminal was let go:
    /// they come back at the default, not at the zoom they were closed with.
    func testANewDefaultZoomForgetsTheZoomOfClosedTerminals() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        app.reportedFontSizes = [F.runtimeID(1): 18]
        workspace.window.close()
        XCTAssertEqual(workspace.fontSizes, [F.runtimeID(1): 18])

        workspace.forgetFontSizes()
        workspace.openForTesting()
        XCTAssertEqual(app.madeFontSizes.last, .some(nil), "the session takes the app's size again")
    }
}

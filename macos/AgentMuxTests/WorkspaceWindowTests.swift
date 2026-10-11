import AppKit
import GhosttyKit
import XCTest
@testable import AgentMux

/// The workspace window against a store fed by hand: what selecting,
/// focusing, and losing the daemon do to the sidebar and the terminals.
@MainActor
final class WorkspaceWindowTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!

    private let sessions = [
        F.tmux(1, project: "api", profile: "work"),
        F.tmux(2, project: "api", profile: "personal", icon: "stop", attention: true, changedAtMS: 2000),
        F.kitty(4, project: "web", icon: "key", attention: true, changedAtMS: 1000),
    ]

    override func setUp() async throws {
        app = FakeWorkspaceApp()
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
        workspace.openForTesting()
    }

    override func tearDown() async throws {
        workspace.window.close()
        workspace = nil
        app = nil
    }

    private func row(_ project: String, _ label: String) throws -> WorkspaceRow {
        try XCTUnwrap(workspace.state.rows.first { $0.project == project && $0.label == label })
    }

    private func index(of key: AttentionCycler.Key) throws -> Int {
        try XCTUnwrap(workspace.items.firstIndex { item in
            if case let .row(row) = item { return row.key == key }
            return false
        })
    }

    func testSidebarListsProjectsThenTheirSessions() throws {
        // The workspace's own views are always listed, above the projects.
        XCTAssertEqual(workspace.items, [.section("Workspace"), .view(.dashboard), .view(.overview)])
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "No live sessions")

        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(workspace.sidebar.numberOfRows, 8)
        guard case let .group(api) = workspace.items[3], case let .group(web) = workspace.items[6] else {
            return XCTFail("expected a project line before its sessions: \(workspace.items)")
        }
        XCTAssertEqual([api.project, web.project], ["api", "web"])
        XCTAssertTrue(workspace.tableView(workspace.sidebar, isGroupRow: 3))
        XCTAssertFalse(workspace.tableView(workspace.sidebar, shouldSelectRow: 3), "a project line is not a session")
        XCTAssertTrue(workspace.tableView(workspace.sidebar, shouldSelectRow: 4))

        let header = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: 3) as? WorkspaceGroupCellView)
        XCTAssertEqual(header.nameLabel.stringValue, "api")
        XCTAssertEqual(header.countLabel.stringValue, "1", "one of the project's sessions waits")
        let cell = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: 4) as? WorkspaceRowCellView)
        XCTAssertEqual(cell.titleLabel.stringValue, "personal")
        XCTAssertEqual(cell.glyphLabel.stringValue, "🛑")
        let kitty = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: 7) as? WorkspaceRowCellView)
        XCTAssertEqual(kitty.titleLabel.stringValue, "work")
        XCTAssertTrue(kitty.detailLabel.stringValue.hasSuffix("Kitty"))

        XCTAssertNil(workspace.state.selection, "nothing is selected until the operator selects")
        XCTAssertEqual(app.made, [], "listing a session attaches nothing")
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "No session selected")
    }

    func testASessionLineIsDrawnOnItsColorUnlessSelected() throws {
        var colored = sessions
        colored[0].tabColor = "#1a3655"
        app.store.apply(snapshot: F.snapshot(colored))
        let work = try index(of: .embedded(F.runtimeID(1)))
        let personal = try index(of: .embedded(F.runtimeID(2)))

        func line(_ index: Int) throws -> (WorkspaceRowView, WorkspaceRowCellView) {
            let rowView = try XCTUnwrap(workspace.sidebar.rowView(atRow: index, makeIfNecessary: true) as? WorkspaceRowView)
            return (rowView, try XCTUnwrap(rowView.view(atColumn: 0) as? WorkspaceRowCellView))
        }
        var (rowView, cell) = try line(work)
        XCTAssertEqual(rowView.tabColor, NSColor(srgbRed: 0x1a / 255.0, green: 0x36 / 255.0, blue: 0x55 / 255.0, alpha: 1))
        XCTAssertTrue(rowView.showsTabColor)
        XCTAssertEqual(cell.titleLabel.textColor, .black, "the text is black on the color")
        let (plainView, plainCell) = try line(personal)
        XCTAssertNil(plainView.tabColor)
        XCTAssertEqual(plainCell.titleLabel.textColor, .labelColor)
        for own in [1, 2] {
            let ownView = try XCTUnwrap(workspace.sidebar.rowView(atRow: own, makeIfNecessary: true) as? WorkspaceRowView)
            XCTAssertNil(ownView.tabColor, "the Dashboard and the Overview are not a session's and take no color")
        }

        // The selected line shows the list's selection, as the active tab
        // shows Kitty's own colors.
        workspace.select(.embedded(F.runtimeID(1)))
        (rowView, cell) = try line(work)
        XCTAssertNotNil(rowView.tabColor)
        XCTAssertFalse(rowView.showsTabColor)
        rowView.layoutSubtreeIfNeeded()
        XCTAssertTrue(rowView.subviews.last === rowView.selectionBorder, "the border is over the line")
        XCTAssertFalse(rowView.selectionBorder.isHidden, "the selected line has a border")
        let unselected = try line(personal).0
        unselected.layoutSubtreeIfNeeded()
        XCTAssertTrue(unselected.selectionBorder.isHidden)
        XCTAssertEqual(cell.titleLabel.textColor, .labelColor)
        workspace.select(.embedded(F.runtimeID(2)))
        (rowView, cell) = try line(work)
        XCTAssertTrue(rowView.showsTabColor)
        XCTAssertEqual(cell.titleLabel.textColor, .black)

        // Another pick repaints the line; a pick taken back leaves none.
        colored[0].tabColor = "#c8c848"
        app.store.apply(snapshot: F.snapshot(colored))
        (rowView, cell) = try line(work)
        XCTAssertEqual(rowView.tabColor, NSColor(srgbRed: 0xc8 / 255.0, green: 0xc8 / 255.0, blue: 0x48 / 255.0, alpha: 1))
        XCTAssertEqual(cell.titleLabel.textColor, .black)
        colored[0].tabColor = ""
        app.store.apply(snapshot: F.snapshot(colored))
        (rowView, cell) = try line(work)
        XCTAssertNil(rowView.tabColor)
        XCTAssertEqual(cell.titleLabel.textColor, .labelColor)
    }

    func testSelectingATmuxSessionShowsItsTerminalAndKeepsTheOthers() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let (work, personal) = (try row("api", "work"), try row("api", "personal"))

        workspace.activate(work)
        XCTAssertEqual(app.made, [F.runtimeID(1)])
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        let first = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        XCTAssertFalse(first.isHidden)
        XCTAssertTrue(workspace.placeholder.isHidden)
        XCTAssertTrue(workspace.window.firstResponder === first, "a click gives the terminal the keyboard")
        XCTAssertEqual(workspace.sidebar.selectedRow, try index(of: work.key))
        XCTAssertEqual(workspace.window.title, "api — work")

        workspace.activate(personal)
        let second = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(2)))
        XCTAssertTrue(first.isHidden, "the unselected terminal is hidden, not closed")
        XCTAssertFalse(second.isHidden)
        XCTAssertTrue(workspace.window.firstResponder === second)
        XCTAssertEqual(app.closed, [])

        workspace.activate(work)
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(2)], "one terminal per session: coming back makes no second one")
        XCTAssertTrue(workspace.terminalView(for: F.runtimeID(1)) === first)
        XCTAssertTrue(workspace.window.firstResponder === first)
        XCTAssertEqual(app.external, [])
    }

    /// The session list keeps the keys of a list: Return goes into the
    /// selected row, and nothing typed there is a terminal's.
    func testReturnInTheSessionListFocusesTheSelectedRow() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let key = try row("api", "work").key
        workspace.select(key)
        workspace.focusSidebar()
        XCTAssertTrue(workspace.window.firstResponder === workspace.sidebar)
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))

        let returnKey = try XCTUnwrap(NSEvent.keyEvent(
            with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0, windowNumber: 0, context: nil,
            characters: "\r", charactersIgnoringModifiers: "\r", isARepeat: false, keyCode: 36))
        workspace.sidebar.keyDown(with: returnKey)
        XCTAssertTrue(workspace.window.firstResponder === terminal, "Return gives the selected session the keyboard")

        workspace.focusSidebar()
        XCTAssertTrue(workspace.window.firstResponder === workspace.sidebar)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)), "the terminal stays shown")

        workspace.showView(WorkspaceUtilityView.dashboard.id, focus: false)
        workspace.focusSidebar()
        workspace.sidebar.keyDown(with: returnKey)
        XCTAssertTrue(workspace.window.firstResponder === workspace.utilityTerminalView(for: WorkspaceUtilityView.dashboard.id))
    }

    /// Hidden, the sidebar takes no room and gives up the keyboard; the title
    /// bar's button brings it back, and asking for the list shows it again.
    func testTheSidebarCollapsesToNothingAndComesBack() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.select(try row("api", "work").key)
        workspace.focusSidebar()
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        XCTAssertFalse(workspace.isSidebarCollapsed)

        workspace.sidebarToggle.performClick(nil)
        workspace.window.layoutIfNeeded()
        XCTAssertTrue(workspace.isSidebarCollapsed)
        XCTAssertTrue(workspace.sidebar.isHiddenOrHasHiddenAncestor)
        XCTAssertEqual(terminal.frame.width, workspace.window.contentLayoutRect.width, "the pane has the whole window")
        XCTAssertTrue(workspace.window.firstResponder === terminal, "a hidden list does not keep the keyboard")
        XCTAssertEqual(app.sidebarCollapses, [true])

        workspace.sidebarToggle.performClick(nil)
        XCTAssertFalse(workspace.isSidebarCollapsed)

        workspace.setSidebarCollapsed(true)
        workspace.focusSidebar()
        XCTAssertFalse(workspace.isSidebarCollapsed, "Session List shows the list it goes to")
        XCTAssertTrue(workspace.window.firstResponder === workspace.sidebar)
        XCTAssertEqual(app.sidebarCollapses, [true, false, true, false])
    }

    func testAKittySessionIsRaisedInKittyOnlyWhenFocused() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let (work, kitty) = (try row("api", "work"), try row("web", "work"))
        workspace.activate(work)
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))

        // The arrow keys reach the Kitty row: it is shown, and Kitty is left alone.
        workspace.sidebar.selectRowIndexes([try index(of: kitty.key)], byExtendingSelection: false)
        XCTAssertEqual(workspace.state.selection, kitty.key)
        XCTAssertEqual(workspace.content, .external(kitty.key))
        XCTAssertEqual(app.external, [], "selecting never leaves the app")
        XCTAssertEqual(app.made, [F.runtimeID(1)], "a Kitty session has no terminal here")
        XCTAssertTrue(terminal.isHidden)
        XCTAssertFalse(workspace.placeholder.isHidden)
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "web runs in Kitty")
        XCTAssertTrue(workspace.window.firstResponder === workspace.sidebar, "the keyboard left the hidden terminal")

        // A click focuses it where it is.
        workspace.activate(kitty)
        XCTAssertEqual(app.external.map(\.target), [.kitty(socket: "", windowID: 4)])
        workspace.placeholder.press()
        XCTAssertEqual(app.external.count, 2, "Show in Kitty does the same")
        XCTAssertEqual(app.made, [F.runtimeID(1)])
    }

    func testFocusFromOutsideSelectsTheSessionAndStepsStayInside() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        // What the menu bar row, the next-attention shortcut, and a banner call.
        guard let listed = workspace.state.row(.embedded(F.runtimeID(2))) else { return XCTFail("not listed") }
        workspace.activate(listed)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(2)))

        // Sidebar order: api/personal, api/work, web/work, and around again.
        workspace.selectNeighbor(1)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(1)))
        workspace.selectNeighbor(1)
        XCTAssertEqual(workspace.content, .external(try row("web", "work").key))
        XCTAssertEqual(app.external, [], "stepping onto a Kitty session does not raise Kitty")
        workspace.selectNeighbor(1)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        workspace.selectNeighbor(-1)
        XCTAssertEqual(workspace.content, .external(try row("web", "work").key))
    }

    func testADigitGoesToTheSessionAtThatPlaceInTheSidebar() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        // Sidebar order: api/personal, api/work, web/work.
        workspace.selectSession(at: 1)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(1)))
        workspace.selectSession(at: 0)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        // A Kitty session is shown here: the digit alone never leaves the app.
        workspace.selectSession(at: 2)
        XCTAssertEqual(workspace.content, .external(try row("web", "work").key))
        XCTAssertEqual(app.external, [])
        // No session is listed there: nothing moves.
        workspace.selectSession(at: 3)
        XCTAssertEqual(workspace.content, .external(try row("web", "work").key))
        // A view is not a place among the sessions.
        workspace.showView(WorkspaceUtilityView.dashboard.id)
        workspace.selectSession(at: 0)
        XCTAssertNil(workspace.viewSelection)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
    }

    func testShiftRaisesAKittySessionInKitty() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        // An embedded session has nowhere else to go: it is focused here.
        workspace.selectSession(at: 1, raising: true)
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(1)))
        XCTAssertEqual(app.external, [])
        workspace.selectSession(at: 2, raising: true)
        XCTAssertEqual(workspace.content, .external(try row("web", "work").key))
        XCTAssertEqual(app.external.map(\.target), [.kitty(socket: "", windowID: 4)])
        workspace.selectSession(at: 3, raising: true)
        XCTAssertEqual(app.external.count, 1)

        // Stepping with Shift does the same: api/personal, api/work, web/work.
        workspace.selectNeighbor(1, raising: true)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        XCTAssertEqual(app.external.count, 1)
        workspace.selectNeighbor(-1, raising: true)
        XCTAssertEqual(workspace.content, .external(try row("web", "work").key))
        XCTAssertEqual(app.external.count, 2)
    }

    func testRowsSelectionAndTerminalsSurviveTheDaemonGoingAway() throws {
        app.store.apply(.subscribed(SubscriptionAck()))
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "personal"))
        workspace.activate(try row("api", "work"))
        let items = workspace.items
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        XCTAssertTrue(workspace.feedLabel.isHidden)

        // The daemon restarts: the socket is gone and the store drops its snapshot.
        app.store.apply(.disconnected(.notRunning, retryIn: 0.5))
        XCTAssertNil(app.store.snapshot)
        XCTAssertFalse(workspace.state.current)
        XCTAssertEqual(workspace.items, items, "the sessions stay listed")
        XCTAssertEqual(workspace.sidebar.alphaValue, 0.5, "and are marked as not current")
        XCTAssertEqual(workspace.feedLabel.stringValue, "Daemon: not running")
        XCTAssertFalse(workspace.feedLabel.isHidden)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertEqual(workspace.sidebar.selectedRow, try index(of: .embedded(F.runtimeID(1))))
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertTrue(workspace.terminalView(for: F.runtimeID(1)) === terminal)
        XCTAssertFalse(terminal.isHidden)
        XCTAssertEqual(workspace.attachedRuntimeSessionIDs, [F.runtimeID(1), F.runtimeID(2)])
        XCTAssertEqual(app.closed, [], "a missing daemon closes no terminal")

        // Other sessions can still be selected from the held rows.
        workspace.activate(try row("api", "personal"))
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(2)))
        workspace.activate(try row("api", "work"))

        // It is back, before its first snapshot: still nothing is decided.
        app.store.apply(.subscribed(SubscriptionAck()))
        XCTAssertFalse(workspace.state.current)
        XCTAssertEqual(app.closed, [])

        // The first snapshot lists the same sessions.
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertTrue(workspace.state.current)
        XCTAssertEqual(workspace.sidebar.alphaValue, 1)
        XCTAssertTrue(workspace.feedLabel.isHidden)
        XCTAssertEqual(workspace.items, items)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertTrue(workspace.terminalView(for: F.runtimeID(1)) === terminal)
        XCTAssertEqual(app.made, [F.runtimeID(2), F.runtimeID(1)], "no terminal was made again")
        XCTAssertEqual(app.closed, [])
    }

    func testAHalfOpenFeedKeepsItsSnapshotAndTheWorkspace() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        app.store.apply(.disconnected(.heartbeatLost, retryIn: 0.5))
        XCTAssertFalse(workspace.state.current)
        XCTAssertEqual(workspace.feedLabel.stringValue, "Daemon: reconnecting · no heartbeat")
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        app.store.apply(.subscribed(SubscriptionAck()))
        XCTAssertTrue(workspace.state.current, "the kept snapshot is the daemon's last word until the next one")
        XCTAssertEqual(app.closed, [])
    }

    func testOnlyALiveSnapshotRetiresASession() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "personal"))
        workspace.activate(try row("api", "work"))

        // The unselected session ended: its terminal goes, nothing else moves.
        app.store.apply(snapshot: F.snapshot([sessions[0], sessions[2]]))
        XCTAssertEqual(app.closed, [F.runtimeID(2)])
        XCTAssertEqual(workspace.attachedRuntimeSessionIDs, [F.runtimeID(1)])
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))

        // The selected one ended: nothing is selected, and no other session
        // is picked for the operator. The pane says what became of it.
        app.statuses[F.runtimeID(1)] = .ended("runtime session is ended")
        app.store.apply(snapshot: F.snapshot([sessions[2]]))
        XCTAssertEqual(app.closed, [F.runtimeID(2), F.runtimeID(1)])
        XCTAssertNil(workspace.state.selection)
        XCTAssertEqual(workspace.sidebar.selectedRow, -1)
        XCTAssertEqual(workspace.content, .gone(runtimeSessionID: F.runtimeID(1), .ended("runtime session is ended")))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Session ended")
        XCTAssertTrue(workspace.window.firstResponder === workspace.sidebar)
        XCTAssertEqual(workspace.window.title, "Agent Mux")
    }

    func testAnEndedAttachmentIsNotRemadeUntilAskedFor() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))

        // `amx attach` ended: another view took the session, or F12.
        workspace.terminalEnded(terminal)
        XCTAssertEqual(app.closed, [F.runtimeID(1)])
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(1)))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Detached")
        XCTAssertNil(terminal.superview)

        // Snapshots keep arriving and do not take the session back.
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        XCTAssertEqual(app.made, [F.runtimeID(1)], "attaching again would take it from whoever has it")

        workspace.placeholder.press()
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(1)])
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(1)))
    }

    private func cell(_ key: AttentionCycler.Key) throws -> WorkspaceRowCellView {
        try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: try index(of: key), makeIfNecessary: true) as? WorkspaceRowCellView)
    }

    func testTheCloseButtonEndsAnIdleSessionAtOnce() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.activate(work)
        app.holdsCloses = true
        workspace.confirmClose = { _, _ in XCTFail("an idle session is closed without asking") }

        XCTAssertFalse(try cell(work.key).closeButton.isHidden)
        XCTAssertTrue(try cell(try row("web", "work").key).closeButton.isHidden, "a Kitty window is not the workspace's to close")
        try cell(work.key).closeButton.performClick(nil)
        XCTAssertEqual(app.ended, [F.runtimeID(1)])
        XCTAssertEqual(app.closed, [F.runtimeID(1)], "its terminal is let go at once")
        XCTAssertEqual(workspace.content, .closing(runtimeSessionID: F.runtimeID(1)))
        XCTAssertTrue(try cell(work.key).closeButton.isHidden)
        XCTAssertEqual(try cell(work.key).detailLabel.stringValue, "closing…")

        // A second click, and snapshots that still list it, change nothing.
        workspace.closeSession(work.key)
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.ended, [F.runtimeID(1)])
        XCTAssertEqual(app.made, [F.runtimeID(1)], "a session being closed is not attached again")

        // The daemon drops the row before amx answers: nothing is reported
        // as gone, and the pane ends up on the Dashboard.
        app.store.apply(snapshot: F.snapshot(Array(sessions.dropFirst())))
        XCTAssertEqual(workspace.content, .empty)
        XCTAssertEqual(app.asked, [], "a session the operator closed is not asked about")
        app.finishClosing()
        XCTAssertEqual(workspace.content, .view(id: "dashboard"))
        XCTAssertEqual(workspace.state.rows.count, 2)
    }

    func testClosingASessionThatIsBusyAsksFirst() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        let busy = try row("api", "personal")
        var asked: [(WorkspaceRow, @MainActor (Bool) -> Void)] = []
        workspace.confirmClose = { row, answer in asked.append((row, answer)) }

        try cell(busy.key).closeButton.performClick(nil)
        XCTAssertEqual(asked.map(\.0), [busy])
        XCTAssertEqual(app.ended, [], "nothing ends before the answer")
        asked[0].1(false)
        XCTAssertEqual(app.ended, [])
        XCTAssertFalse(try cell(busy.key).closeButton.isHidden)

        try cell(busy.key).closeButton.performClick(nil)
        asked[1].1(true)
        XCTAssertEqual(app.ended, [F.runtimeID(2)])
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)), "the pane was elsewhere and stays there")
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
    }

    func testCloseShownEndsTheSessionThePaneIsOnAsItsButtonDoes() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let busy = try row("api", "personal")
        workspace.activate(busy)
        var asked: [(WorkspaceRow, @MainActor (Bool) -> Void)] = []
        workspace.confirmClose = { row, answer in asked.append((row, answer)) }

        XCTAssertTrue(workspace.closeShown())
        XCTAssertEqual(asked.map(\.0), [busy])
        XCTAssertEqual(app.ended, [], "nothing ends before the answer")
        asked[0].1(true)
        XCTAssertEqual(app.ended, [F.runtimeID(2)])

        // An idle one ends at once; a Kitty window is not the workspace's.
        workspace.activate(try row("api", "work"))
        XCTAssertTrue(workspace.closeShown())
        XCTAssertEqual(asked.count, 1)
        XCTAssertEqual(app.ended, [F.runtimeID(2), F.runtimeID(1)])
        workspace.select(try row("web", "work").key, focus: false)
        XCTAssertFalse(workspace.closeShown())
        XCTAssertEqual(app.ended, [F.runtimeID(2), F.runtimeID(1)])
    }

    func testASessionThatCouldNotBeClosedComesBack() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.activate(work)
        app.closeProblem = "tmux backend unavailable"
        var reported: [String] = []
        workspace.reportCloseProblem = { name, message in reported.append("\(name): \(message)") }

        try cell(work.key).closeButton.performClick(nil)
        XCTAssertEqual(reported, ["api — work: tmux backend unavailable"])
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(1)], "its terminal is attached again")
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertFalse(try cell(work.key).closeButton.isHidden)
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
}

/// The whole path a snapshot travels — socket, subscription, store, sidebar —
/// with real Ghostty terminals in the pane, across a daemon that drops the
/// stream and comes back.
@MainActor
final class WorkspaceStreamTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private var directory: URL!
    private var server: FakeStreamServer?
    private var client: SubscriptionClient?
    private var ghostty: Ghostty.App!
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!

    override func setUp() async throws {
        directory = URL(fileURLWithPath: "/tmp/amx-\(UUID().uuidString.prefix(8))")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        ghostty = Ghostty.App { Ghostty.Config(userConfig: nil, overrides: Ghostty.ConfigOverrides()) }
        XCTAssertEqual(ghostty.readiness, .ready)
        app = FakeWorkspaceApp()
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
    }

    override func tearDown() async throws {
        client?.stop()
        server?.shutdown()
        workspace.window.close()
        ghostty.shutdown()
        try? FileManager.default.removeItem(at: directory)
    }

    /// A terminal whose program records its pid and then waits, as an
    /// attached agent would.
    private func terminal(_ id: String) -> NSView? {
        let out = directory.appendingPathComponent("\(id).pid").path
        let view = ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", #"echo $$ > "$OUT.tmp" && mv "$OUT.tmp" "$OUT"; exec cat"#],
            environment: ["OUT": out]
        ))
        view?.delegate = workspace
        return view
    }

    private func pid(_ id: String) -> pid_t? {
        let url = directory.appendingPathComponent("\(id).pid")
        ghosttyWait("\(id) to start") { FileManager.default.fileExists(atPath: url.path) }
        return (try? String(contentsOf: url, encoding: .utf8)).flatMap { pid_t($0.trimmingCharacters(in: .whitespacesAndNewlines)) }
    }

    private func alive(_ pid: pid_t) -> Bool { kill(pid, 0) == 0 }

    func testSidebarAndTerminalsStayConsistentAcrossAReconnect() throws {
        let sessions = [
            F.tmux(1, project: "api", profile: "work"),
            F.tmux(2, project: "api", profile: "personal", icon: "stop", attention: true, changedAtMS: 2000),
            F.kitty(4, project: "web", icon: "key", attention: true, changedAtMS: 1000),
        ]
        let subscribe = Locked<String?>(nil)
        let drop = DispatchSemaphore(value: 0)
        let comeBack = DispatchSemaphore(value: 0)
        let finish = DispatchSemaphore(value: 0)
        server = try FakeStreamServer(directory: directory, scripts: [
            { conn in
                subscribe.set(conn.readLine())
                conn.write(F.ack)
                conn.write(F.frame(seq: 1, sessions))
                drop.wait()
                conn.close()
            },
            { conn in
                _ = conn.readLine()
                comeBack.wait()
                conn.write(F.ack)
                conn.write(F.frame(seq: 1, sessions))
                finish.wait()
                conn.write(F.frame(seq: 2, [sessions[0], sessions[2]]))
                Thread.sleep(forTimeInterval: 2)
                conn.close()
            },
        ])
        defer { [drop, comeBack, finish].forEach { $0.signal() } }

        app.factory = { [unowned self] id in self.terminal(id) }
        workspace.openForTesting()
        let store = app.store
        let client = SubscriptionClient(
            socketPath: try XCTUnwrap(server).path,
            backoff: ReconnectBackoff(initial: 0.05, maximum: 0.2),
            request: { SubscribeParams(clientVersion: "test") },
            onEvent: { event in DispatchQueue.main.async { MainActor.assumeIsolated { store.apply(event) } } }
        )
        self.client = client
        client.start()

        ghosttyWait("the first snapshot") { self.workspace.state.rows.count == 3 }
        XCTAssertTrue(try XCTUnwrap(subscribe.get()).contains(#""features":["runtime_sessions","workspace_views"]"#))
        XCTAssertTrue(workspace.state.current)

        let (work, personal) = (F.runtimeID(1), F.runtimeID(2))
        workspace.activate(try XCTUnwrap(workspace.state.row(.embedded(personal))))
        workspace.activate(try XCTUnwrap(workspace.state.row(.embedded(work))))
        let surface = try XCTUnwrap(workspace.terminalView(for: work) as? Ghostty.SurfaceView)
        let hidden = try XCTUnwrap(workspace.terminalView(for: personal) as? Ghostty.SurfaceView)
        let workPID = try XCTUnwrap(pid(work))
        let personalPID = try XCTUnwrap(pid(personal))
        XCTAssertTrue(workspace.window.firstResponder === surface)
        XCTAssertEqual([surface.isHidden, hidden.isHidden], [false, true])
        let items = workspace.items

        // The daemon drops the stream.
        drop.signal()
        ghosttyWait("the feed to drop") { self.app.store.feed != .live }
        XCTAssertFalse(workspace.state.current)
        XCTAssertEqual(workspace.items, items)
        XCTAssertEqual(workspace.state.selection, .embedded(work))
        XCTAssertFalse(workspace.feedLabel.isHidden)
        XCTAssertTrue(workspace.terminalView(for: work) === surface)
        XCTAssertNotNil(surface.surface, "the terminal is still open")
        XCTAssertTrue(workspace.window.firstResponder === surface, "and still has the keyboard")
        XCTAssertTrue(alive(workPID) && alive(personalPID), "both programs are still running")

        // It comes back and lists the same sessions.
        comeBack.signal()
        ghosttyWait("the feed to return") { self.workspace.state.current }
        XCTAssertEqual(workspace.items, items)
        XCTAssertEqual(workspace.state.selection, .embedded(work))
        XCTAssertTrue(workspace.terminalView(for: work) === surface)
        XCTAssertTrue(workspace.terminalView(for: personal) === hidden)
        XCTAssertTrue(workspace.window.firstResponder === surface)
        XCTAssertEqual(app.made.count, 2, "nothing was attached a second time")
        XCTAssertTrue(alive(workPID) && alive(personalPID))
        XCTAssertTrue(workspace.feedLabel.isHidden)

        // Then one session ends: only its terminal goes.
        finish.signal()
        ghosttyWait("the ended session to leave") { self.workspace.state.rows.count == 2 }
        XCTAssertEqual(app.closed, [personal])
        XCTAssertNil(hidden.surface)
        ghosttyWait("its program to end") { !self.alive(personalPID) }
        XCTAssertTrue(alive(workPID))
        XCTAssertTrue(workspace.window.firstResponder === surface)

        // The program in the shown terminal exits: the view detaches itself.
        kill(workPID, SIGTERM)
        ghosttyWait("the workspace to notice") { self.workspace.content == .detached(runtimeSessionID: work) }
        XCTAssertNil(surface.surface)
        XCTAssertEqual(workspace.state.selection, .embedded(work), "the session is still listed and selected")
    }
}

import AppKit
import XCTest
@testable import AgentMux

/// What the workspace does with what its terminals report: a bell, a title,
/// a hovered link, a failed renderer, and a desktop notification.
@MainActor
final class WorkspaceTerminalEventsTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!
    private var beeps = 0
    private var attention = 0
    private var active = true

    private let sessions = [
        F.tmux(1, project: "api", profile: "work"),
        F.tmux(2, project: "api", profile: "personal"),
    ]

    override func setUp() async throws {
        app = FakeWorkspaceApp()
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
        workspace.openForTesting()
        beeps = 0
        attention = 0
        active = true
        workspace.isAppActive = { [unowned self] in self.active }
        workspace.beep = { [unowned self] in self.beeps += 1 }
        workspace.requestAttention = { [unowned self] in self.attention += 1 }
        app.store.apply(snapshot: F.snapshot(sessions))
    }

    override func tearDown() async throws {
        workspace.window.close()
        workspace = nil
        app = nil
    }

    private func row(_ label: String) throws -> WorkspaceRow {
        try XCTUnwrap(workspace.state.rows.first { $0.label == label })
    }

    private func cell(at index: Int) throws -> WorkspaceRowCellView {
        try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: index) as? WorkspaceRowCellView)
    }

    private func cell(_ label: String) throws -> WorkspaceRowCellView {
        let key = try row(label).key
        return try cell(at: try XCTUnwrap(workspace.items.firstIndex { item in
            if case let .row(row) = item { return row.key == key }
            return false
        }))
    }

    private func viewCell(_ id: String) throws -> WorkspaceRowCellView {
        try cell(at: try XCTUnwrap(workspace.items.firstIndex { item in
            if case let .view(view) = item { return view.id == id }
            return false
        }))
    }

    func testABellIsHeardOnlyInTheTerminalShownAndAsksForAttentionFromBehind() {
        typealias R = WorkspaceWindowController.BellResponse
        XCTAssertEqual(R.of(shown: true, appActive: true, systemBell: true), R(mark: false, beep: true, requestAttention: false))
        XCTAssertEqual(R.of(shown: true, appActive: true, systemBell: false), R(mark: false, beep: false, requestAttention: false),
                       "bell-features without system: silent")
        XCTAssertEqual(R.of(shown: true, appActive: false, systemBell: true), R(mark: false, beep: false, requestAttention: true))
        XCTAssertEqual(R.of(shown: false, appActive: true, systemBell: true), R(mark: true, beep: false, requestAttention: false),
                       "a hidden terminal never beeps")
        XCTAssertEqual(R.of(shown: false, appActive: false, systemBell: false), R(mark: true, beep: false, requestAttention: true))
    }

    func testABellInAHiddenSessionMarksItsLineUntilItIsShown() throws {
        workspace.activate(try row("work"))
        let work = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        workspace.activate(try row("personal"))
        let personal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(2)))

        workspace.terminalRangBell(work, systemBell: true)
        XCTAssertEqual(beeps, 0, "a session out of view is not heard")
        XCTAssertEqual(attention, 0)
        XCTAssertEqual(workspace.ringingSessions, [F.runtimeID(1)])
        XCTAssertTrue(try cell("work").titleLabel.stringValue.hasSuffix(WorkspaceRowCellView.bellGlyph))
        XCTAssertFalse(try cell("personal").titleLabel.stringValue.hasSuffix(WorkspaceRowCellView.bellGlyph))
        XCTAssertTrue(try XCTUnwrap(cell("work").accessibilityLabel()).contains("bell"))

        workspace.terminalRangBell(personal, systemBell: false)
        XCTAssertEqual(beeps, 0, "bell-features leaves the beep out")
        workspace.terminalRangBell(personal, systemBell: true)
        XCTAssertEqual(beeps, 1)
        XCTAssertEqual(workspace.ringingSessions, [F.runtimeID(1)], "the terminal shown is never marked")

        active = false
        workspace.terminalRangBell(personal, systemBell: true)
        XCTAssertEqual(beeps, 1, "nothing beeps behind another app")
        XCTAssertEqual(attention, 1)

        workspace.activate(try row("work"))
        XCTAssertEqual(workspace.ringingSessions, [], "shown, the bell has been seen")
        XCTAssertFalse(try cell("work").titleLabel.stringValue.hasSuffix(WorkspaceRowCellView.bellGlyph))
    }

    func testABellInAHiddenViewMarksItsLine() throws {
        XCTAssertTrue(workspace.showView(WorkspaceUtilityView.dashboard.id))
        let dashboard = try XCTUnwrap(workspace.utilityTerminalView(for: WorkspaceUtilityView.dashboard.id))
        workspace.activate(try row("work"))
        workspace.terminalRangBell(dashboard, systemBell: true)
        XCTAssertEqual(workspace.ringingViews, [WorkspaceUtilityView.dashboard.id])
        XCTAssertTrue(try viewCell(WorkspaceUtilityView.dashboard.id).titleLabel.stringValue.hasSuffix(WorkspaceRowCellView.bellGlyph))
        XCTAssertTrue(workspace.showView(WorkspaceUtilityView.dashboard.id))
        XCTAssertEqual(workspace.ringingViews, [])
    }

    func testAViewsProgramTitleIsShownOnItsLineAndInTheWindowTitle() throws {
        let id = WorkspaceUtilityView.dashboard.id
        XCTAssertTrue(workspace.showView(id))
        let dashboard = try XCTUnwrap(workspace.utilityTerminalView(for: id))
        XCTAssertEqual(workspace.window.title, "Dashboard")

        workspace.terminalTitleChanged(dashboard, title: "amx · 2 waiting")
        XCTAssertEqual(workspace.window.title, "Dashboard — amx · 2 waiting")
        XCTAssertEqual(try viewCell(id).detailLabel.stringValue, "amx · 2 waiting")
        XCTAssertEqual(try viewCell(id).titleLabel.stringValue, "Dashboard", "the line keeps the view's own name")

        // A session's terminal is titled by tmux; its line and window keep
        // the session's name.
        workspace.activate(try row("work"))
        let work = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        let title = workspace.window.title
        workspace.terminalTitleChanged(work, title: "tmux says")
        XCTAssertEqual(workspace.window.title, title)

        workspace.terminalTitleChanged(dashboard, title: "")
        XCTAssertEqual(try viewCell(id).detailLabel.stringValue, WorkspaceUtilityView.dashboard.detail)
    }

    func testAHoveredLinkIsShownOnlyForTheTerminalShown() throws {
        workspace.activate(try row("work"))
        let work = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        workspace.activate(try row("personal"))
        let personal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(2)))

        workspace.terminalHoveredLink(work, url: "https://hidden.example")
        XCTAssertTrue(workspace.linkLabel.isHidden)
        workspace.terminalHoveredLink(personal, url: "https://example.com/pr/1")
        XCTAssertFalse(workspace.linkLabel.isHidden)
        XCTAssertEqual(workspace.linkLabel.stringValue, "https://example.com/pr/1")
        workspace.terminalHoveredLink(personal, url: nil)
        XCTAssertTrue(workspace.linkLabel.isHidden)

        workspace.terminalHoveredLink(personal, url: "https://example.com/pr/1")
        workspace.activate(try row("work"))
        XCTAssertTrue(workspace.linkLabel.isHidden, "another terminal shown has no link under the pointer")
    }

    func testARendererFailureOffersToReattach() throws {
        workspace.activate(try row("work"))
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))

        workspace.terminalRendererHealthChanged(terminal, healthy: false)
        XCTAssertEqual(workspace.content, .rendererFailed(runtimeSessionID: F.runtimeID(1)))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "The terminal stopped drawing")
        XCTAssertTrue(terminal.isHidden)
        XCTAssertFalse(workspace.placeholder.isHidden)

        // It recovered on its own: back to the same terminal.
        workspace.terminalRendererHealthChanged(terminal, healthy: true)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertFalse(terminal.isHidden)
        XCTAssertEqual(app.made, [F.runtimeID(1)])

        workspace.terminalRendererHealthChanged(terminal, healthy: false)
        workspace.placeholder.press()
        XCTAssertEqual(app.closed, [F.runtimeID(1)], "the broken terminal is let go")
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(1)], "and a new one attached")
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertFalse(try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1))) === terminal)
        XCTAssertEqual(workspace.rendererFailed, [])
    }

    func testAUtilityRendererFailureOffersRecovery() throws {
        // The Dashboard: it stays listed, and comes back by itself.
        let dashboardID = WorkspaceUtilityView.dashboard.id
        XCTAssertTrue(workspace.showView(dashboardID))
        let dashboard = try XCTUnwrap(workspace.utilityTerminalView(for: dashboardID))
        workspace.terminalRendererHealthChanged(dashboard, healthy: false)
        XCTAssertEqual(workspace.content, .viewRendererFailed(id: dashboardID))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "The terminal stopped drawing")
        XCTAssertEqual(workspace.placeholder.button.title, "Restart")
        XCTAssertFalse(workspace.placeholder.button.isHidden)
        XCTAssertTrue(dashboard.isHidden)
        XCTAssertFalse(workspace.placeholder.isHidden)
        XCTAssertEqual(workspace.rendererFailed, [], "a view is not a session")

        workspace.terminalRendererHealthChanged(dashboard, healthy: true)
        XCTAssertEqual(workspace.content, .view(id: dashboardID))
        XCTAssertFalse(dashboard.isHidden)
        XCTAssertEqual(app.madeViews, [dashboardID])

        // Restarting ends the broken terminal and starts the program again.
        workspace.terminalRendererHealthChanged(dashboard, healthy: false)
        workspace.placeholder.press()
        XCTAssertEqual(app.closedViews, [dashboardID])
        XCTAssertEqual(app.madeViews, [dashboardID, dashboardID])
        XCTAssertEqual(workspace.content, .view(id: dashboardID))
        let restarted = try XCTUnwrap(workspace.utilityTerminalView(for: dashboardID))
        XCTAssertFalse(restarted === dashboard)
        XCTAssertFalse(restarted.isHidden)
        XCTAssertTrue(workspace.placeholder.isHidden)
        XCTAssertEqual(workspace.rendererFailedViews, [])

        // The Overview, the other permanent view.
        let overviewID = WorkspaceUtilityView.overview.id
        XCTAssertTrue(workspace.showView(overviewID))
        let overview = try XCTUnwrap(workspace.utilityTerminalView(for: overviewID))
        workspace.terminalRendererHealthChanged(overview, healthy: false)
        XCTAssertEqual(workspace.content, .viewRendererFailed(id: overviewID))
        workspace.placeholder.press()
        XCTAssertEqual(workspace.content, .view(id: overviewID))
        XCTAssertFalse(try XCTUnwrap(workspace.utilityTerminalView(for: overviewID)) === overview)

        // A shell, which is not permanent: it stays listed and restarts.
        workspace.open(WorkspaceUtilityView(kind: .shell, target: "/tmp", title: "Shell"))
        guard case let .view(shellID) = workspace.content else { return XCTFail("the shell is shown") }
        let shell = try XCTUnwrap(workspace.utilityTerminalView(for: shellID))
        workspace.terminalRendererHealthChanged(shell, healthy: false)
        XCTAssertEqual(workspace.content, .viewRendererFailed(id: shellID))
        XCTAssertNotNil(try? viewCell(shellID), "the failed view stays listed")
        workspace.placeholder.press()
        XCTAssertEqual(workspace.content, .view(id: shellID))
        let newShell = try XCTUnwrap(workspace.utilityTerminalView(for: shellID))
        XCTAssertFalse(newShell === shell)
        XCTAssertFalse(newShell.isHidden)
        XCTAssertEqual(workspace.rendererFailedViews, [])
        XCTAssertEqual(workspace.rendererFailed, [])
        XCTAssertEqual(app.made, [], "no session terminal was made for a view")
    }

    func testATerminalsNotificationNamesWhereItCameFrom() throws {
        workspace.activate(try row("work"))
        let work = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        let name = try row("work").displayName
        workspace.terminalNotification(work, title: "Build", body: "done")
        XCTAssertTrue(workspace.showView(WorkspaceUtilityView.dashboard.id))
        let dashboard = try XCTUnwrap(workspace.utilityTerminalView(for: WorkspaceUtilityView.dashboard.id))
        workspace.terminalNotification(dashboard, title: "", body: "tests passed")
        workspace.terminalNotification(NSView(), title: "x", body: "not one of ours")
        XCTAssertEqual(app.terminalNotices.map(\.source), [name, "Dashboard"])
        XCTAssertEqual(app.terminalNotices.map(\.runtimeSessionID), [F.runtimeID(1), nil])
        XCTAssertEqual(app.terminalNotices.map(\.body), ["done", "tests passed"])
    }
}

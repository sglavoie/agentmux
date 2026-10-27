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

    func testAnIdleShellsLineIsBlankAndARunningCommandIsShown() throws {
        workspace.open(WorkspaceUtilityView(kind: .shell, target: "/tmp", title: "Shell"))
        guard case let .view(id) = workspace.content else { return XCTFail("the shell is shown") }
        let shell = try XCTUnwrap(workspace.utilityTerminalView(for: id))
        XCTAssertEqual(try viewCell(id).detailLabel.stringValue, "", "nothing runs yet")

        workspace.terminalTitleChanged(shell, title: "me@host:/tmp")
        XCTAssertEqual(try viewCell(id).detailLabel.stringValue, "", "at its prompt")
        XCTAssertEqual(workspace.window.title, "Shell")

        workspace.terminalTitleChanged(shell, title: "make test", directory: "/tmp")
        XCTAssertEqual(try viewCell(id).detailLabel.stringValue, "make test")
        XCTAssertEqual(workspace.window.title, "Shell — make test")

        // After a cd the title names the new directory before the shell
        // reports it: still a prompt.
        workspace.terminalTitleChanged(shell, title: "me@host:/var", directory: "/tmp")
        XCTAssertEqual(try viewCell(id).detailLabel.stringValue, "")
    }

    func testProgramTitlePreservesSessionName() throws {
        var named = sessions
        named[0].name = "release notes"
        app.store.apply(snapshot: F.snapshot(named))

        // A named session.
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.openTerminalBesideSession()
        let first = "terminal:\(F.runtimeID(1))#1"
        let firstTerminal = try XCTUnwrap(workspace.utilityTerminalView(for: first))
        XCTAssertEqual(try viewCell(first).detailLabel.stringValue, "")
        workspace.terminalTitleChanged(firstTerminal, title: "zsh")
        XCTAssertEqual(try viewCell(first).detailLabel.stringValue, "zsh · release notes")
        XCTAssertEqual(try viewCell(first).toolTip, "Shell · release notes")

        // A session with no name of its own is named by project and profile.
        workspace.select(.embedded(F.runtimeID(2)), focus: true)
        workspace.openTerminalBesideSession()
        let second = "terminal:\(F.runtimeID(2))#1"
        let secondTerminal = try XCTUnwrap(workspace.utilityTerminalView(for: second))
        workspace.terminalTitleChanged(secondTerminal, title: "zsh")
        XCTAssertEqual(try viewCell(second).detailLabel.stringValue, "zsh · api — personal")
        XCTAssertEqual(try viewCell(second).toolTip, "Shell · api — personal")

        // The title cleared, the line is blank again.
        workspace.terminalTitleChanged(firstTerminal, title: "")
        XCTAssertEqual(try viewCell(first).detailLabel.stringValue, "")

        // Its session ends: neither its terminal nor its program title remains.
        app.store.apply(snapshot: F.snapshot([named[1]]))
        XCTAssertNil(workspace.views.view(first))
        XCTAssertNil(workspace.programTitles[first])
        XCTAssertEqual(try viewCell(second).detailLabel.stringValue, "zsh · api — personal")
        workspace.toggleSwitcher()
        workspace.switcher.enter("release")
        XCTAssertTrue(workspace.switcher.shown.isEmpty)

        // A late event from the discarded terminal cannot restore its title.
        workspace.terminalTitleChanged(firstTerminal, title: "late title")
        XCTAssertNil(workspace.programTitles[first])
    }

    /// tmux copies a mouse selection when the button is let go: the link
    /// line says so for a moment, for the terminal shown only.
    func testACopyIsConfirmedForTheTerminalShown() throws {
        workspace.activate(try row("work"))
        let work = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        workspace.activate(try row("personal"))
        let personal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(2)))

        workspace.terminalCopied(work)
        XCTAssertTrue(workspace.linkLabel.isHidden, "a hidden terminal's copy is not announced")
        workspace.terminalCopied(personal)
        XCTAssertFalse(workspace.linkLabel.isHidden)
        XCTAssertEqual(workspace.linkLabel.stringValue, WorkspaceWindowController.copiedNotice)
        workspace.endCopiedNotice()
        XCTAssertTrue(workspace.linkLabel.isHidden)
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

    func testHoveredLinkOverlayDoesNotInterceptClicks() throws {
        let window = workspace.window
        workspace.activate(try row("work"))
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        workspace.terminalHoveredLink(terminal, url: "https://example.com/pr/1")
        window.layoutIfNeeded()
        let label = workspace.linkLabel
        XCTAssertFalse(label.isHidden)
        XCTAssertEqual(label.stringValue, "https://example.com/pr/1", "the link is still shown")

        let root = try XCTUnwrap(window.contentView)
        func hit(_ view: NSView, _ local: NSPoint) throws -> NSView {
            let point = view.convert(local, to: nil)
            return try XCTUnwrap(root.hitTest(root.superview!.convert(point, from: nil)))
        }
        let overLink = NSPoint(x: label.bounds.midX, y: label.bounds.midY)
        XCTAssertTrue(terminal.frame.contains(terminal.superview!.convert(overLink, from: label)), "the link lies over the terminal")
        let under = try hit(label, overLink)
        XCTAssertFalse(under.isDescendant(of: label), "the overlay takes no click")
        XCTAssertTrue(under.isDescendant(of: terminal), "the click reaches the terminal beneath")

        // The Go to… list, above the overlay, still takes its own clicks.
        workspace.toggleSwitcher()
        window.layoutIfNeeded()
        XCTAssertTrue(workspace.isSwitching)
        let field = workspace.switcher.field
        let onField = try hit(field, NSPoint(x: field.bounds.midX, y: field.bounds.midY))
        XCTAssertTrue(onField.isDescendant(of: workspace.switcher), "the list takes its own clicks")
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
        XCTAssertEqual(app.terminalNotices.map(\.notice.source), [name, "Dashboard"])
        XCTAssertEqual(app.terminalNotices.map(\.notice.origin), [.session(runtimeSessionID: F.runtimeID(1)), .view(id: WorkspaceUtilityView.dashboard.id)])
        XCTAssertEqual(app.terminalNotices.map(\.notice.body), ["done", "tests passed"])
        XCTAssertEqual(app.terminalNotices.map(\.onScreen), [false, false], "the test window is never key")
    }

    /// The app hears which terminal the pane shows each time that changes,
    /// so it can withdraw that terminal's notices.
    func testThePaneReportsTheTerminalItShows() throws {
        workspace.activate(try row("work"))
        XCTAssertTrue(workspace.showView(WorkspaceUtilityView.dashboard.id))
        workspace.activate(try row("work"))
        workspace.activate(try row("work"))
        let work = TerminalNotice.Origin.session(runtimeSessionID: F.runtimeID(1))
        let dashboard = TerminalNotice.Origin.view(id: WorkspaceUtilityView.dashboard.id)
        XCTAssertEqual(Array(app.showing.suffix(3)), [work, dashboard, work])
        XCTAssertEqual(app.showing.filter { $0 == work }.count, 2, "showing the same terminal again says nothing")
        XCTAssertEqual(workspace.showing, work)
    }

    func testANoticeIsOnScreenOnlyInTheKeyWindowOnItsTerminal() {
        let work = TerminalNotice.Origin.session(runtimeSessionID: "rs-1")
        XCTAssertTrue(WorkspaceWindowController.onScreen(work, showing: work, key: true))
        XCTAssertFalse(WorkspaceWindowController.onScreen(work, showing: work, key: false))
        XCTAssertFalse(WorkspaceWindowController.onScreen(work, showing: .view(id: "dashboard"), key: true))
        XCTAssertFalse(WorkspaceWindowController.onScreen(work, showing: nil, key: true))
    }
}

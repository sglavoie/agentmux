import AppKit
import XCTest
@testable import AgentMux

/// What the workspace says when the daemon is away, what it calls sessions
/// and the views about them, and what a right-click on a line offers.
@MainActor
final class WorkspacePolishTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!

    private let sessions = [
        F.tmux(1, project: "api", profile: "work"),
        F.tmux(2, project: "api", profile: "personal"),
        F.kitty(4, project: "web"),
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

    private func index(of key: AttentionCycler.Key) throws -> Int {
        try XCTUnwrap(workspace.items.firstIndex { item in
            if case let .row(row) = item { return row.key == key }
            return false
        })
    }

    private func index(ofView id: String) throws -> Int {
        try XCTUnwrap(workspace.items.firstIndex { item in
            if case let .view(view) = item { return view.id == id }
            return false
        })
    }

    private func perform(_ item: NSMenuItem) throws {
        _ = try XCTUnwrap(item.target).perform(try XCTUnwrap(item.action), with: item)
    }

    private var waitingNote: String? {
        (workspace.inspector.lines.first as? NSTextField)?.stringValue
    }

    // MARK: A daemon that is away

    func testADownDaemonIsWhatThePaneAndTheInspectorSay() throws {
        app.store.apply(.disconnected(.notRunning, retryIn: 0.5))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Daemon not running")
        XCTAssertEqual(workspace.placeholder.button.title, WorkspaceWindowController.startDaemonTitle)
        XCTAssertFalse(workspace.placeholder.button.isHidden)
        XCTAssertEqual(waitingNote, "Daemon not running", "not that nothing is waiting")
        workspace.placeholder.press()
        XCTAssertEqual(app.daemonStarts, 1)
        XCTAssertEqual(app.made, [], "starting the daemon starts no agent")

        app.store.apply(.disconnected(.unsupported, retryIn: 0.5))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Daemon too old — run amx daemon restart")
        XCTAssertTrue(workspace.placeholder.button.isHidden, "a restart is the operator's, from a terminal")

        app.store.apply(.disconnected(.heartbeatLost, retryIn: 0.5))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Reconnecting…")
        XCTAssertEqual(waitingNote, "Reconnecting… (no heartbeat)")

        app.store.apply(snapshot: F.snapshot([]))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "No live sessions")
        XCTAssertEqual(workspace.placeholder.button.title, "Open Dashboard")
        XCTAssertEqual(waitingNote, WorkspaceInspectorView.nothingWaiting)
    }

    // MARK: Names

    func testTheSessionNameIsUsedInTheTitleAndTheAlerts() throws {
        var named = sessions
        named[0].name = "release notes"
        app.store.apply(snapshot: F.snapshot(named))
        workspace.select(.embedded(F.runtimeID(1)))
        XCTAssertEqual(workspace.window.title, "release notes")
        workspace.select(.embedded(F.runtimeID(2)))
        XCTAssertEqual(workspace.window.title, "api — personal", "one with no name is its project and profile")

        var asked: [String] = []
        workspace.confirmClose = { row, answer in
            asked.append(row.displayName)
            answer(false)
        }
        workspace.closeSession(.embedded(F.runtimeID(1)))
        XCTAssertEqual(asked, ["release notes"])
        let cell = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil,
                                                     row: try index(of: .embedded(F.runtimeID(1)))) as? WorkspaceRowCellView)
        XCTAssertEqual(cell.closeButton.toolTip, "End release notes")
    }

    func testAViewAboutASessionNamesIt() throws {
        var named = sessions
        named[0].name = "release notes"
        app.store.apply(snapshot: F.snapshot(named))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.openTerminalBesideSession()
        let id = "terminal:\(F.runtimeID(1))#1"
        func cell() throws -> WorkspaceRowCellView {
            try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: try index(ofView: id)) as? WorkspaceRowCellView)
        }
        XCTAssertEqual(try cell().detailLabel.stringValue, "Shell · release notes")
        XCTAssertEqual(try cell().toolTip, "Shell · release notes")

        // Its session ends: the terminal keeps the name it was last listed
        // under, never the raw runtime session ID.
        app.store.apply(snapshot: F.snapshot([named[1], named[2]]))
        XCTAssertNotNil(workspace.views.view(id))
        XCTAssertEqual(try cell().toolTip, "Shell · release notes")
        workspace.toggleSwitcher()
        workspace.switcher.enter("release")
        XCTAssertEqual(workspace.switcher.shown.map(\.detail), ["Shell · release notes"])
    }

    // MARK: Right-click

    func testTheRightClickMenuOfAnEmbeddedSession() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let key = AttentionCycler.Key.embedded(F.runtimeID(1))
        let items = workspace.contextMenuItems(forRow: try index(of: key))
        func item(_ title: String) throws -> NSMenuItem { try XCTUnwrap(items.first { $0.title == title }) }

        var copied: [String] = []
        workspace.copyToPasteboard = { copied.append($0) }
        try perform(item(WorkspaceWindowController.copyRuntimeSessionIDTitle))
        XCTAssertEqual(copied, [F.runtimeID(1)])

        try perform(item(WorkspaceWindowController.newTerminalTitle))
        XCTAssertEqual(workspace.viewSelection, "terminal:\(F.runtimeID(1))#1")

        var asked = 0
        workspace.confirmClose = { _, answer in
            asked += 1
            answer(false)
        }
        try perform(item(WorkspaceWindowController.endSessionTitle))
        XCTAssertEqual(asked, 1, "always asked first")
        XCTAssertEqual(app.ended, [])
    }

    func testAViewOffersCloseAndAHeadingNoMenu() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        // A heading, and the Dashboard while nothing runs in it, have nothing.
        XCTAssertTrue(workspace.contextMenuItems(forRow: 0).isEmpty)
        XCTAssertTrue(workspace.contextMenuItems(forRow: try index(ofView: "dashboard")).isEmpty)
        XCTAssertEqual(workspace.sidebar.hasMenu?(0), false, "no menu rather than an empty one")
        XCTAssertEqual(workspace.sidebar.hasMenu?(try index(of: .embedded(F.runtimeID(1)))), true)

        workspace.open(WorkspaceViewRequest(kind: "shell", directory: "/work/api"))
        let items = workspace.contextMenuItems(forRow: try index(ofView: "shell:/work/api"))
        XCTAssertEqual(items.map(\.title), ["Close"])
        workspace.confirmViewClose = { _, answer in answer(true) }
        try perform(items[0])
        XCTAssertNil(workspace.views.view("shell:/work/api"))
    }

    // MARK: Small things

    func testHiddenTrailingViewsLeaveTheTitleTheLine() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let kitty = try XCTUnwrap(workspace.state.rows.first { $0.embeddedRuntimeSessionID == nil })
        let cell = WorkspaceRowCellView()
        cell.show(kitty, muteGlyph: "", sessionKey: nil)
        XCTAssertFalse(cell.titleClearsClose, "a Kitty session has no ×")
        XCTAssertFalse(cell.titleClearsKey)
        let tmux = try XCTUnwrap(workspace.state.row(.embedded(F.runtimeID(1))))
        cell.show(tmux, muteGlyph: "", sessionKey: "1")
        XCTAssertTrue(cell.titleClearsClose)
        XCTAssertTrue(cell.titleClearsKey)
        cell.show(WorkspaceUtilityView.dashboard)
        XCTAssertFalse(cell.titleClearsClose)
        XCTAssertFalse(cell.titleClearsKey)
    }

    func testTheSidebarToggleIsTheSidebarSymbol() {
        XCTAssertEqual(workspace.sidebarToggle.image?.accessibilityDescription, "Toggle Sidebar")
        XCTAssertNotNil(NSImage(systemSymbolName: "sidebar.left", accessibilityDescription: nil))
    }

    func testAWaitingRowIsTintedUnderThePointerAndWhilePressed() {
        let row = WorkspaceWaitingRowView(WorkspaceWaitingRow(
            key: .embedded(F.runtimeID(1)), title: "api — work", detail: "", glyph: "", toolTip: ""
        ))
        XCTAssertEqual(row.fillColor, WorkspaceWaitingRowView.restingFill)
        row.isHovered = true
        XCTAssertEqual(row.fillColor, WorkspaceWaitingRowView.hoveredFill)
        row.isPressed = true
        XCTAssertEqual(row.fillColor, WorkspaceWaitingRowView.pressedFill)
        row.isPressed = false
        row.isHovered = false
        XCTAssertEqual(row.fillColor, WorkspaceWaitingRowView.restingFill)
    }
}

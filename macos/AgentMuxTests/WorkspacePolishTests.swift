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
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Daemon too old — choose Restart Daemon")
        XCTAssertEqual(workspace.placeholder.button.title, WorkspaceWindowController.restartDaemonTitle)
        XCTAssertFalse(workspace.placeholder.button.isHidden, "the Restart Daemon the notice names")
        workspace.placeholder.press()
        XCTAssertEqual(app.daemonRestarts, 1)
        XCTAssertEqual(app.daemonStarts, 1)

        app.store.apply(.disconnected(.heartbeatLost, retryIn: 0.5))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Reconnecting…")
        XCTAssertFalse(workspace.placeholder.messageLabel.stringValue.contains("listed are"),
                       "nothing is listed, so it does not speak of the sessions listed")
        XCTAssertFalse(workspace.placeholder.button.isHidden, "a daemon that stopped answering is not waited on for good")
        XCTAssertEqual(workspace.placeholder.button.title, WorkspaceWindowController.restartDaemonTitle)
        XCTAssertEqual(waitingNote, "Reconnecting… (the daemon is not answering)")

        app.store.apply(.disconnected(.bye("restart"), retryIn: 0.5))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Reconnecting…")
        XCTAssertTrue(workspace.placeholder.button.isHidden, "a restart under way: nothing to do but wait")

        app.store.apply(snapshot: F.snapshot([]))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "No live sessions")
        XCTAssertEqual(workspace.placeholder.button.title, "Launch Project…")
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
        XCTAssertEqual(workspace.window.subtitle, try XCTUnwrap(workspace.state.selectedRow).detail,
                       "the sidebar's line for the session, there with the sidebar hidden")
        XCTAssertFalse(workspace.window.subtitle.isEmpty)

        var asked: [String] = []
        workspace.confirmClose = { row, answer in
            asked.append(row.displayName)
            answer(false)
        }
        workspace.closeSession(.embedded(F.runtimeID(1)))
        XCTAssertEqual(asked, ["release notes"])
        let cell = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil,
                                                     row: try index(of: .embedded(F.runtimeID(1)))) as? WorkspaceRowCellView)
        XCTAssertEqual(cell.closeButton.toolTip, "Detach release notes; agent keeps running")
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

        workspace.toggleSwitcher()
        workspace.switcher.enter("release")
        XCTAssertTrue(workspace.switcher.shown.map(\.detail).contains("Shell · release notes"))

        // Its session ends: the terminal leaves the sidebar and switcher too.
        app.store.apply(snapshot: F.snapshot([named[1], named[2]]))
        XCTAssertNil(workspace.views.view(id))
        XCTAssertTrue(workspace.isSwitching, "session cleanup preserves an open switcher")
        XCTAssertTrue(workspace.switcher.shown.isEmpty)
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

    /// The inspector's Color…, Icon…, and Mute act on the line under the
    /// pointer, wherever the pane is; a Kitty session has the pickers in
    /// Kitty but is muted from here.
    func testTheRightClickMenuColorsIconsAndMutesTheClickedSession() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try XCTUnwrap(workspace.state.row(.embedded(F.runtimeID(1))))
        let personal = try XCTUnwrap(workspace.state.row(.embedded(F.runtimeID(2))))
        workspace.activate(work)
        let items = workspace.contextMenuItems(forRow: try index(of: personal.key))
        func item(_ title: String) throws -> NSMenuItem { try XCTUnwrap(items.first { $0.title == title }) }

        try perform(item("Color…"))
        XCTAssertEqual(workspace.viewSelection, WorkspaceUtilityView.picker(.colorPicker, runtimeSessionID: F.runtimeID(2))?.id)
        try perform(item("Icon…"))
        XCTAssertEqual(workspace.viewSelection, WorkspaceUtilityView.picker(.iconPicker, runtimeSessionID: F.runtimeID(2))?.id)
        try perform(item("Mute"))
        XCTAssertEqual(app.muteToggles, [personal])

        let kitty = try XCTUnwrap(workspace.state.rows.first { $0.embeddedRuntimeSessionID == nil })
        let kittyItems = workspace.contextMenuItems(forRow: try index(of: kitty.key))
        for title in ["Color…", "Icon…"] {
            let picker = try XCTUnwrap(kittyItems.first { $0.title == title })
            XCTAssertFalse(picker.isEnabled, "\(title) is a Kitty session's in Kitty")
            XCTAssertEqual(picker.toolTip, WorkspaceInspectorView.kittyOnly)
        }
        let mute = try XCTUnwrap(kittyItems.first { $0.title == "Mute" })
        try perform(mute)
        XCTAssertEqual(app.muteToggles, [personal, kitty])
    }

    /// A right-click on the list's empty space offers where to go and what
    /// to open, sent to the app as the main menu's items are.
    func testTheListsEmptySpaceOffersGoToAndNewViews() {
        let items = workspace.contextMenuItems(forRow: -1)
        XCTAssertEqual(items.map(\.title), ["Go to…", "New Shell", "Dashboard", "Overview"])
        XCTAssertTrue(items.allSatisfy { $0.target === app })
        XCTAssertEqual(workspace.sidebar.hasMenu?(-1), true)
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
        XCTAssertEqual(items.map(\.title), ["Name Terminal…", "Close"])
        workspace.confirmViewClose = { _, answer in answer(true) }
        try perform(items[1])
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

    /// The source list restyles a cell's textField, which drew the selected
    /// line's title dark on its dark pill.
    func testASessionTitleKeepsTheColorItsLineSets() {
        let cell = WorkspaceRowCellView()
        XCTAssertNil(cell.textField)
        cell.setTabText(nil)
        XCTAssertEqual(cell.titleLabel.textColor, .labelColor)
        XCTAssertEqual(cell.detailLabel.textColor, .secondaryLabelColor)
    }

    /// The list's grays are too faint on the selected line's opaque pill.
    func testASelectedLineMutesItsDetailLessThanTheList() {
        let cell = WorkspaceRowCellView()
        cell.setTabText(nil, selected: true)
        XCTAssertEqual(cell.titleLabel.textColor, .labelColor)
        XCTAssertEqual(cell.detailLabel.textColor, WorkspaceRowCellView.selectedDetailColor)
        XCTAssertEqual(cell.keyLabel.textColor, WorkspaceRowCellView.selectedDetailColor)
    }

    /// Muting the label color once fixed it to the appearance current then,
    /// which drew the selected line's detail black on the dark pill.
    func testASelectedLinesDetailIsLightInTheDarkAppearance() {
        func resolved(_ name: NSAppearance.Name) -> CGFloat {
            var white: CGFloat = -1
            NSAppearance(named: name)!.performAsCurrentDrawingAppearance {
                let color = WorkspaceRowCellView.selectedDetailColor.usingColorSpace(.genericGray)!
                white = color.whiteComponent
                XCTAssertEqual(color.alphaComponent, 0.8, accuracy: 0.01)
            }
            return white
        }
        XCTAssertEqual(resolved(.darkAqua), 1, accuracy: 0.01)
        XCTAssertEqual(resolved(.aqua), 0, accuracy: 0.01)
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

import AppKit
import XCTest
@testable import AgentMux

/// The pane keeps its place under clicks and keys that used to blank it,
/// ages keep moving while nothing else does, the daemon can be started or
/// restarted whatever is listed, held lines are not acted on, and the menu,
/// the Go to… list, and the scrollbar say what VoiceOver needs.
@MainActor
final class WorkspaceRoughEdgesTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!
    private var clock = Date(timeIntervalSince1970: 600)

    private let sessions = [
        F.tmux(1, project: "api", profile: "work", changedAtMS: 540_000),
        F.tmux(2, project: "api", profile: "personal", icon: "key", attention: true, changedAtMS: 300_000),
        F.kitty(4, project: "web"),
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

    private func row(_ project: String, _ label: String) throws -> WorkspaceRow {
        try XCTUnwrap(workspace.state.rows.first { $0.project == project && $0.label == label })
    }

    private func index(of key: AttentionCycler.Key) throws -> Int {
        try XCTUnwrap(workspace.items.firstIndex { item in
            if case let .row(row) = item { return row.key == key }
            return false
        })
    }

    // MARK: The pane keeps its place

    func testEmptySpaceOrACommandClickKeepsTheSelectedLine() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.activate(work)
        let selected = IndexSet(integer: try index(of: work.key))
        XCTAssertEqual(workspace.sidebar.selectedRowIndexes, selected)

        // A click on empty space, or ⌘-click on the selected line, proposes nothing.
        XCTAssertEqual(workspace.tableView(workspace.sidebar, selectionIndexesForProposedSelection: IndexSet()), selected)
        // A project's header is no line to select.
        let header = try XCTUnwrap(workspace.items.firstIndex { if case .group = $0 { return true } else { return false } })
        XCTAssertEqual(workspace.tableView(workspace.sidebar, selectionIndexesForProposedSelection: IndexSet(integer: header)), selected)
        // Another session's line is.
        let personal = try index(of: try row("api", "personal").key)
        XCTAssertEqual(workspace.tableView(workspace.sidebar, selectionIndexesForProposedSelection: IndexSet(integer: personal)),
                       IndexSet(integer: personal))

        // The table's own empty selection, were it to happen, blanks nothing.
        workspace.sidebar.deselectAll(nil)
        XCTAssertEqual(workspace.state.selection, work.key)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertEqual(app.places.last ?? nil, .session(runtimeSessionID: F.runtimeID(1)), "the place restored stays")

        // The workspace still empties the selection itself.
        workspace.setCollapsed(project: "api", pinned: false, true)
        XCTAssertEqual(workspace.sidebar.selectedRowIndexes, IndexSet())
        XCTAssertEqual(workspace.state.selection, work.key)
    }

    func testTheArrowsGoFromTheSessionACollapseHid() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.activate(work)
        workspace.focusSidebar()
        workspace.sidebar.keyDown(with: TestKey.left.event())
        XCTAssertEqual(workspace.sidebar.selectedRowIndexes, IndexSet(), "its line was collapsed away")

        workspace.sidebar.keyDown(with: TestKey.function(NSDownArrowFunctionKey, keyCode: 125).event())
        XCTAssertNil(workspace.viewSelection, "not the Dashboard at the top")
        XCTAssertEqual(workspace.state.selection, try row("web", "work").key, "the session after api/work")
        XCTAssertTrue(workspace.window.firstResponder === workspace.sidebar, "an arrow key stays in the list")

        workspace.activate(work)
        workspace.focusSidebar()
        workspace.sidebar.keyDown(with: TestKey.function(NSUpArrowFunctionKey, keyCode: 126).event())
        XCTAssertNil(workspace.viewSelection)
        XCTAssertEqual(workspace.state.selection, try row("web", "work").key, "the session before api/work, wrapping")
    }

    func testTheArrowsAreTheListsWhenThePaneIsOnNothing() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.closeSelectedView()
        workspace.select(nil)
        XCTAssertFalse(workspace.stepFromUnlistedPlace(1), "nothing to step from: the list moves as it does")
    }

    // MARK: Ages

    func testAgesMoveWhileTheDaemonSaysNothing() throws {
        XCTAssertNotNil(workspace.ageTimer, "it runs while the window is open")
        XCTAssertEqual(workspace.ageTimer?.timeInterval, WorkspaceWindowController.ageRefreshInterval)
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.setInspectorTab(.session)
        let work = try row("api", "work")
        workspace.activate(work)
        XCTAssertTrue(work.detail.contains("1m"), work.detail)
        let terminal = workspace.window.firstResponder

        clock = clock.addingTimeInterval(4 * 60)
        workspace.ageTimer?.fire()
        let later = try row("api", "work").detail
        XCTAssertTrue(later.contains("5m"), later)
        XCTAssertTrue(workspace.inspector.model.waiting.first?.detail.contains("9m") ?? false)
        XCTAssertEqual(workspace.state.selection, work.key, "the selection stays")
        XCTAssertTrue(workspace.window.firstResponder === terminal, "and so does the keyboard")

        workspace.window.close()
        XCTAssertNil(workspace.ageTimer, "a closed window says nothing")
    }

    // MARK: The daemon

    func testTheSidebarOffersToStartOrRestartTheDaemonWhateverIsListed() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        XCTAssertTrue(workspace.feedButton.isHidden)

        app.store.apply(.disconnected(.notRunning, retryIn: 0.5))
        XCTAssertFalse(workspace.state.rows.isEmpty, "the rows are held")
        XCTAssertFalse(workspace.feedButton.isHidden)
        XCTAssertEqual(workspace.feedButton.title, "Start Daemon")
        workspace.feedButton.performClick(nil)
        XCTAssertEqual(app.daemonStarts, 1)
        XCTAssertEqual(app.made, [F.runtimeID(1)], "starting the daemon starts no agent")

        app.store.apply(.disconnected(.unsupported, retryIn: 0.5))
        XCTAssertEqual(workspace.feedLabel.stringValue, "Daemon too old — choose Restart Daemon")
        XCTAssertEqual(workspace.feedButton.title, "Restart Daemon")
        workspace.feedButton.performClick(nil)
        XCTAssertEqual(app.daemonRestarts, 1)

        app.store.apply(.disconnected(.heartbeatLost, retryIn: 0.5))
        XCTAssertTrue(workspace.feedButton.isHidden, "nothing to do but wait")
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertTrue(workspace.feedButton.isHidden)
    }

    // MARK: Held lines

    func testHeldLinesSayTheFeedIsDownAndAnswerNothing() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.setInspectorCollapsed(false)
        func answer() throws -> NSButton {
            let lines = workspace.inspector.lines.compactMap { $0 as? WorkspaceWaitingRowView }
            return try XCTUnwrap(lines.first?.answerButton)
        }
        XCTAssertTrue(try answer().isEnabled)

        app.store.apply(.disconnected(.heartbeatLost, retryIn: 0.5))
        XCTAssertFalse(workspace.state.current)
        XCTAssertEqual((workspace.inspector.lines.first as? NSTextField)?.stringValue,
                       "Reconnecting… (the daemon is not answering)", "said above the held lines")
        XCTAssertEqual(workspace.inspector.lines.compactMap { $0 as? WorkspaceWaitingRowView }.count, 1)
        XCTAssertFalse(try answer().isEnabled)
        XCTAssertEqual(try answer().toolTip, WorkspaceInspectorView.waitForDaemon)

        workspace.setInspectorTab(.session)
        workspace.select(try row("api", "personal").key)
        XCTAssertEqual((workspace.inspector.lines.first as? NSTextField)?.stringValue,
                       "Reconnecting… (the daemon is not answering)")
        XCTAssertFalse(workspace.inspector.actionButtons.isEmpty)
        XCTAssertTrue(workspace.inspector.actionButtons.allSatisfy { !$0.isEnabled })

        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertTrue(workspace.inspector.actionButtons.allSatisfy(\.isEnabled))
        workspace.setInspectorTab(.waiting)
        XCTAssertTrue(try answer().isEnabled)
        XCTAssertNil(try answer().toolTip)
    }

    // MARK: Menus and words

    func testEndSessionIsGreyedOutOnASessionAlreadyClosing() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(workspace.sidebar.menu?.autoenablesItems, false)
        let work = try row("api", "work")
        func end() throws -> NSMenuItem {
            try XCTUnwrap(workspace.contextMenuItems(forRow: try index(of: work.key))
                .first { $0.title == WorkspaceWindowController.endSessionTitle })
        }
        XCTAssertTrue(try end().isEnabled)
        app.holdsCloses = true
        workspace.confirmClose = { _, answer in answer(true) }
        workspace.closeSession(work.key)
        XCTAssertFalse(try end().isEnabled)

        let kitty = workspace.contextMenuItems(forRow: try index(of: try row("web", "work").key))
        XCTAssertEqual(kitty.first { $0.title == WorkspaceWindowController.nameSessionTitle }?.isEnabled, false,
                       "a Kitty session is named in Kitty")
        XCTAssertEqual(kitty.first { $0.title == WorkspaceWindowController.showInKittyTitle }?.isEnabled, true)
    }

    func testTheWordsNameTheWindowAndPastSessions() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.activate(work)
        workspace.terminalEnded(try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1))))
        XCTAssertTrue(workspace.placeholder.messageLabel.stringValue.hasPrefix("This window let go of the session"))
        XCTAssertTrue(WorkspaceWindowController.conversationKept.hasSuffix("resumed from Past Sessions."))
    }

    func testTheMainMenuHasTheStandardItems() throws {
        let savedServices = NSApp.servicesMenu
        let savedWindows = NSApp.windowsMenu
        defer {
            NSApp.servicesMenu = savedServices
            NSApp.windowsMenu = savedWindows
        }
        let delegate = try XCTUnwrap(NSApp.delegate as? AppDelegate)
        let menu = WorkspaceMenu.make(target: delegate)
        let appMenu = try XCTUnwrap(menu.item(withTitle: "Agent Mux")?.submenu)
        XCTAssertEqual(appMenu.item(withTitle: "About Agent Mux")?.action, #selector(NSApplication.orderFrontStandardAboutPanel(_:)))
        let services = try XCTUnwrap(appMenu.item(withTitle: "Services")?.submenu)
        XCTAssertTrue(NSApp.servicesMenu === services)
        let others = try XCTUnwrap(appMenu.item(withTitle: "Hide Others"))
        XCTAssertEqual(others.action, #selector(NSApplication.hideOtherApplications(_:)))
        XCTAssertEqual(others.keyEquivalent, "", "⌥⌘H would take Option from the program")
        XCTAssertEqual(appMenu.item(withTitle: "Show All")?.action, #selector(NSApplication.unhideAllApplications(_:)))

        let window = try XCTUnwrap(menu.item(withTitle: "Window")?.submenu)
        XCTAssertEqual(window.item(withTitle: "Zoom")?.action, #selector(NSWindow.performZoom(_:)))
        XCTAssertEqual(window.item(withTitle: "Bring All to Front")?.action, #selector(NSApplication.arrangeInFront(_:)))

        let view = try XCTUnwrap(menu.item(withTitle: "View")?.submenu)
        XCTAssertEqual(view.items.first { $0.identifier == WorkspaceMenu.toggleSidebarIdentifier }?.action,
                       #selector(AppDelegate.toggleWorkspaceSidebar(_:)))
        XCTAssertEqual(view.items.first { $0.identifier == WorkspaceMenu.toggleInspectorIdentifier }?.action,
                       #selector(AppDelegate.toggleWorkspaceInspector(_:)))

        // No two items claim the same key.
        func keys(_ menu: NSMenu) -> [String] {
            menu.items.flatMap { item -> [String] in
                let own = item.keyEquivalent.isEmpty ? [] : ["\(item.keyEquivalentModifierMask.rawValue):\(item.keyEquivalent)"]
                return own + (item.submenu.map(keys) ?? [])
            }
        }
        let all = keys(menu)
        XCTAssertEqual(all.count, Set(all).count, "a key equivalent is listed twice")
    }

    // MARK: VoiceOver

    func testTheGoToListSaysWhereTheSelectionMoved() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.toggleSwitcher()
        let switcher = workspace.switcher
        XCTAssertNil(switcher.lastAnnouncement, "nothing is said until it moves")
        switcher.move(1)
        let selected = try XCTUnwrap(switcher.selected)
        XCTAssertEqual(switcher.lastAnnouncement, WorkspaceSwitcherView.spoken(selected))
        XCTAssertTrue(switcher.lastAnnouncement?.hasPrefix(selected.title) ?? false)

        // Typing filters the list under a field that keeps the keyboard:
        // what is left is said too.
        switcher.enter("personal")
        let top = try XCTUnwrap(switcher.selected)
        XCTAssertEqual(switcher.lastAnnouncement,
                       WorkspaceSwitcherView.spokenMatches(switcher.shown.count, top: top))
        XCTAssertEqual(WorkspaceSwitcherView.spokenMatches(1, top: top), "1 match, \(WorkspaceSwitcherView.spoken(top))")
        switcher.enter("nothing here")
        XCTAssertEqual(switcher.lastAnnouncement, WorkspaceSwitcherView.noMatches)

        let cell = WorkspaceSwitcherCellView()
        cell.show(WorkspaceSwitcher.Entry(place: .view("dashboard"), glyph: "", title: "Dashboard", detail: "The TUI"))
        XCTAssertEqual(cell.accessibilityLabel(), "Dashboard, The TUI")
        cell.show(WorkspaceSwitcher.Entry(place: .view("overview"), glyph: "", title: "Overview", detail: ""))
        XCTAssertEqual(cell.accessibilityLabel(), "Overview")
    }

    func testTheScrollbarSaysWhereItIsAndPages() throws {
        let bar = WorkspaceScrollbarView()
        var rows: [UInt64] = []
        bar.onScroll = { rows.append($0) }
        XCTAssertNil(bar.accessibilityValue())
        XCTAssertFalse(bar.accessibilityPerformIncrement(), "nothing to page")

        bar.scrollbar = Ghostty.Scrollbar(total: 1000, offset: 450, len: 100)
        XCTAssertEqual((bar.accessibilityValue() as? NSNumber)?.doubleValue ?? -1, 0.5, accuracy: 0.001)
        XCTAssertEqual(bar.accessibilityValueDescription(), "50 percent")
        XCTAssertTrue(bar.accessibilityPerformIncrement())
        XCTAssertTrue(bar.accessibilityPerformDecrement())
        XCTAssertEqual(rows, [550, 350], "a page down and a page up")

        let top = Ghostty.Scrollbar(total: 1000, offset: 40, len: 100)
        XCTAssertEqual(WorkspaceScrollbarView.pagedRow(top, pages: -1), 0, "not above the top")
        let bottom = Ghostty.Scrollbar(total: 1000, offset: 860, len: 100)
        XCTAssertEqual(WorkspaceScrollbarView.pagedRow(bottom, pages: 1), 900, "not below the bottom")
    }
}

import AppKit
import GhosttyKit
import ScreenCaptureKit
import XCTest
@testable import AgentMux

extension WorkspaceWindowTests {
    func testNamingTerminalsFromMenuAndKeyboardPreservesTheRunningProgram() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.activate(work)
        XCTAssertTrue(workspace.openTerminalBesideSession())
        let terminal = try XCTUnwrap(workspace.views.terminals(beside: F.runtimeID(1)).first)
        let surface = try XCTUnwrap(workspace.utilityTerminalView(for: terminal.id))
        var asked: [(WorkspaceUtilityView, @MainActor (String?) -> Void)] = []
        workspace.askTerminalName = { view, answer in asked.append((view, answer)) }
        var layout: WorkspaceRestoration.Layout?
        workspace.onLayoutChanged = { layout = $0 }

        // A context menu names its own row even when a session is selected.
        workspace.activate(work)
        let index = try XCTUnwrap(workspace.items.firstIndex { $0 == .view(terminal) })
        let items = workspace.contextMenuItems(forRow: index)
        XCTAssertEqual(items.map(\.title), ["Name Terminal…", "Close"])
        let naming = try XCTUnwrap(items.first { $0.title == "Name Terminal…" })
        _ = try XCTUnwrap(naming.target).perform(try XCTUnwrap(naming.action), with: naming)
        XCTAssertEqual(asked.first?.0, terminal)
        asked[0].1("  build logs  ")
        XCTAssertEqual(workspace.views.view(terminal.id)?.displayName, "build logs")
        XCTAssertEqual(workspace.state.selection, work.key)
        XCTAssertTrue(workspace.utilityTerminalView(for: terminal.id) === surface)
        XCTAssertTrue(app.named.isEmpty, "a terminal name never writes to its agent session")

        workspace.showView(terminal.id)
        XCTAssertTrue(workspace.canNameShown)
        XCTAssertEqual(workspace.nameShownTitle, "Name Terminal…")
        XCTAssertEqual(workspace.window.title, "build logs")
        XCTAssertTrue(workspace.nameShownSession())
        XCTAssertEqual(asked.last?.0.name, "build logs")
        asked[1].1(nil)
        XCTAssertEqual(workspace.window.title, "build logs", "cancel leaves the name intact")
        XCTAssertTrue(workspace.nameShownSession())
        asked[2].1("deploy")
        XCTAssertEqual(workspace.window.title, "deploy")
        let named = try XCTUnwrap(workspace.views.view(terminal.id))
        XCTAssertEqual(layout?.active, .utility(named))
        let entries = WorkspaceSwitcher.entries(rows: workspace.state.rows, views: workspace.views.all)
        XCTAssertEqual(WorkspaceSwitcher.matches("deploy", in: entries).map(\.place), [.view(terminal.id)])
        let cell = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: index, makeIfNecessary: true) as? WorkspaceRowCellView)
        XCTAssertEqual(cell.titleLabel.stringValue, "deploy")
        workspace.terminalTitleChanged(surface, title: "zsh")
        XCTAssertEqual(workspace.window.title, "deploy — zsh")
        XCTAssertTrue(workspace.nameShownSession())
        asked[3].1(" \n ")
        XCTAssertEqual(workspace.views.view(terminal.id), terminal)
        XCTAssertEqual(workspace.window.title, "Terminal — zsh")
        XCTAssertTrue(workspace.utilityTerminalView(for: terminal.id) === surface)

        // Standalone shells have the same shortcut; permanent views do not.
        workspace.openNewShell(in: "/work/api")
        XCTAssertTrue(workspace.nameShownSession())
        XCTAssertEqual(asked.last?.0.kind, .shell)
        asked[4].1("server")
        XCTAssertEqual(workspace.window.title, "server")
        workspace.showView(WorkspaceUtilityView.dashboard.id)
        XCTAssertFalse(workspace.canNameShown)
        XCTAssertFalse(workspace.nameShownSession())
    }

    func testTerminalNamingAnswerCannotRenameAReplacementTerminal() throws {
        workspace.openNewShell(in: "/work/api")
        var answer: (@MainActor (String?) -> Void)?
        workspace.askTerminalName = { _, reply in answer = reply }
        XCTAssertTrue(workspace.nameShownSession())
        workspace.confirmBeforeClosing = false
        XCTAssertTrue(workspace.closeSelectedView())
        workspace.openNewShell(in: "/work/api")
        answer?("old terminal")
        XCTAssertEqual(workspace.window.title, "Shell")
    }

    func testPinningASessionListsItAboveEveryProject() throws {
        var pinSessions = sessions
        pinSessions[2] = F.tmux(4, project: "web", icon: "stop", attention: true, changedAtMS: 1000)
        app.store.apply(snapshot: F.snapshot(pinSessions))
        let work = try row("api", "work")
        let other = try row("web", "work")
        workspace.activate(work)
        func lines() -> [String] {
            workspace.items.compactMap { item in
                switch item {
                case let .group(group): return "[\(group.project)]"
                case let .row(row): return "\(row.project)/\(row.label)"
                case .navigation, .section, .overview, .view: return nil
                }
            }
        }
        XCTAssertEqual(lines(), ["[Done]", "[api]", "api/personal", "api/work", "[web]", "web/work"])

        // The right-click menu pins the session under the pointer.
        var items = workspace.contextMenuItems(forRow: try index(of: other.key))
        _ = try XCTUnwrap(items[5].target).perform(try XCTUnwrap(items[5].action), with: items[5])
        XCTAssertEqual(lines(), ["[Pinned]", "web/work", "[Done]", "[api]", "api/personal", "api/work"],
                       "a project with nothing left under it is not listed")
        XCTAssertEqual(workspace.sessionKey(of: other.key), "1", "the digits follow the list")
        XCTAssertEqual(workspace.state.selection, work.key, "the pane stays where it is")
        XCTAssertEqual(app.orders.last?.pinned, [WorkspaceOrder.token(other.key)])

        // The menu pins the one the pane is on, below the ones pinned before.
        XCTAssertTrue(workspace.togglePinOfShownSession())
        XCTAssertEqual(lines(), ["[Pinned]", "web/work", "api/work", "[Done]", "[api]", "api/personal"])

        // Pinned sessions are dragged among themselves and nowhere else.
        let top = try index(of: other.key)
        XCTAssertEqual(workspace.dropSlots(forItemAt: top), [top, top + 1, top + 2])
        XCTAssertTrue(workspace.moveItem(at: top, to: top + 2))
        XCTAssertEqual(lines(), ["[Pinned]", "api/work", "web/work", "[Done]", "[api]", "api/personal"])
        XCTAssertEqual(app.orders.last?.pinned, [WorkspaceOrder.token(work.key), WorkspaceOrder.token(other.key)])

        // A snapshot keeps them there, and unpinning puts one back.
        app.store.apply(snapshot: F.snapshot(pinSessions))
        workspace.refresh()
        XCTAssertEqual(lines(), ["[Pinned]", "api/work", "web/work", "[Done]", "[api]", "api/personal"])
        items = workspace.contextMenuItems(forRow: try index(of: other.key))
        XCTAssertEqual(items[5].title, "Unpin")
        _ = try XCTUnwrap(items[5].target).perform(try XCTUnwrap(items[5].action), with: items[5])
        XCTAssertEqual(lines(), ["[Pinned]", "api/work", "[Done]", "[api]", "api/personal", "[web]", "web/work"])
        XCTAssertTrue(workspace.togglePinOfShownSession())
        XCTAssertEqual(lines(), ["[Done]", "[api]", "api/personal", "api/work", "[web]", "web/work"])
        XCTAssertEqual(app.orders.last?.pinned, [])
    }

    /// The orders dragged into forget a session that ended and a project
    /// that is gone, once a live snapshot says so, and only then.
    func testALiveSnapshotPrunesTheDraggedOrders() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let (work, personal) = (try row("api", "work"), try row("api", "personal"))
        let top = try index(of: personal.key)
        XCTAssertTrue(workspace.moveItem(at: top, to: top + 2))
        workspace.setPinned(try row("web", "work").key, true)
        let dragged = app.orders.count
        XCTAssertEqual(app.orders.last?.projects["api"], [work, personal].map { WorkspaceOrder.token($0.key) })

        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.orders.count, dragged, "nothing gone, nothing written")

        // Held rows say nothing of what ended.
        app.store.apply(.disconnected(.notRunning, retryIn: 0.5))
        XCTAssertFalse(workspace.state.current)
        XCTAssertEqual(app.orders.count, dragged)

        app.store.apply(snapshot: F.snapshot([sessions[0]]))
        XCTAssertEqual(app.orders.count, dragged + 1)
        XCTAssertEqual(app.orders.last?.projects, ["api": [WorkspaceOrder.token(work.key)]])
        XCTAssertEqual(app.orders.last?.pinned, [], "the tmux session ended, and its pin with it")
        XCTAssertEqual(workspace.order, app.orders.last)
    }

    /// The sidebar and the inspector open at the widths they were left at,
    /// and the delegate hears of a new width only while one is shown.
    func testTheSidebarAndInspectorOpenAtTheirSavedWidths() throws {
        XCTAssertEqual(WorkspaceWindowController.width(nil, default: 240, in: 180...420), 240)
        XCTAssertEqual(WorkspaceWindowController.width(300, default: 240, in: 180...420), 300)
        XCTAssertEqual(WorkspaceWindowController.width(9000, default: 240, in: 180...420), 420)
        XCTAssertEqual(WorkspaceWindowController.width(12, default: 240, in: 180...420), 180)
        XCTAssertEqual(WorkspaceWindowController.width(.nan, default: 240, in: 180...420), 240)

        func widths(_ controller: WorkspaceWindowController) throws -> (CGFloat, CGFloat) {
            let split = try XCTUnwrap(controller.window.contentViewController as? NSSplitViewController)
            return (split.splitViewItems[0].viewController.view.frame.width, split.splitViewItems[2].viewController.view.frame.width)
        }
        XCTAssertEqual(try widths(workspace).0, WorkspaceWindowController.defaultSidebarWidth, "nothing saved: the default")

        // Both fit beside the pane, which keeps room for the Go to… list.
        let other = FakeWorkspaceApp()
        let restored = WorkspaceWindowController(delegate: other, glyphs: .fallback)
        defer { restored.window.close() }
        restored.sidebarWidth = 260
        restored.inspectorWidth = 280
        restored.openForTesting()
        XCTAssertEqual(try widths(restored).0, 260)
        XCTAssertTrue(restored.isInspectorCollapsed, "hidden until asked for")
        XCTAssertTrue(other.layoutChanges.isEmpty, "opening at the saved widths reports nothing")
        restored.setInspectorCollapsed(false)
        restored.window.layoutIfNeeded()
        XCTAssertEqual(try widths(restored).1, 280, "shown, the inspector has its saved width")
        XCTAssertEqual(try widths(restored).0, 260)

        let split = try XCTUnwrap(restored.window.contentViewController as? NSSplitViewController)
        split.splitView.setPosition(220, ofDividerAt: 0)
        restored.window.layoutIfNeeded()
        XCTAssertEqual(other.layoutChanges.last?.sidebar, 220)
        XCTAssertNil(other.layoutChanges.last?.inspector, "the inspector kept its width")

        // Hidden and shown again, the sidebar keeps the width it had.
        let reported = other.layoutChanges.count
        restored.setSidebarCollapsed(true)
        restored.window.layoutIfNeeded()
        restored.setSidebarCollapsed(false)
        restored.window.layoutIfNeeded()
        XCTAssertEqual(try widths(restored).0, 220)
        XCTAssertFalse(other.layoutChanges.dropFirst(reported).contains { $0.sidebar != nil && $0.sidebar != 220 })
    }

    /// The window opens at the saved widths and zoom, with its saved frame,
    /// and saves no other width over them.
    func testSavedWidthsSurviveASavedZoomAndFrame() throws {
        let name = "AgentMuxWorkspaceTest-\(UUID().uuidString)"
        let key = "NSWindow Frame \(name)"
        UserDefaults.standard.set("0 0 1500 900 0 0 1500 900 ", forKey: key)
        defer { UserDefaults.standard.removeObject(forKey: key) }
        let other = FakeWorkspaceApp()
        let restored = WorkspaceWindowController(delegate: other, glyphs: .fallback)
        defer { restored.window.close() }
        restored.frameAutosaveName = name
        // The app gives the saved zoom before the widths; that must not
        // make the window at the default widths.
        restored.setSidebarZoom(percent: 140)
        restored.sidebarWidth = 494.5
        restored.inspectorWidth = 420
        restored.openForTesting()
        let split = try XCTUnwrap(restored.window.contentViewController as? NSSplitViewController)
        restored.window.layoutIfNeeded()
        XCTAssertEqual(restored.window.frame.width, 1500, accuracy: 1, "the saved frame is applied")
        XCTAssertEqual(restored.sidebar.convert(NSSize(width: 10, height: 10), to: nil).height, 14, accuracy: 0.01)
        XCTAssertEqual(split.splitViewItems[0].viewController.view.frame.width, 494.5, accuracy: 0.5)
        restored.setInspectorCollapsed(false)
        restored.window.layoutIfNeeded()
        XCTAssertEqual(split.splitViewItems[2].viewController.view.frame.width, 420, accuracy: 0.5)
        XCTAssertEqual(split.splitViewItems[0].viewController.view.frame.width, 494.5, accuracy: 0.5)
        XCTAssertFalse(other.layoutChanges.contains { $0.sidebar != nil && abs($0.sidebar! - 494.5) > 0.5 },
                       "no narrower sidebar is saved: \(other.layoutChanges)")
    }

    /// In a window too narrow for both saved widths beside the pane, the
    /// hidden inspector takes no room from the sidebar.
    func testAWideSavedSidebarSurvivesAWideSavedInspectorInANarrowWindow() throws {
        let other = FakeWorkspaceApp()
        let restored = WorkspaceWindowController(delegate: other, glyphs: .fallback)
        defer { restored.window.close() }
        restored.sidebarWidth = 600
        restored.inspectorWidth = 400
        restored.openForTesting()
        let split = try XCTUnwrap(restored.window.contentViewController as? NSSplitViewController)
        restored.window.layoutIfNeeded()
        XCTAssertEqual(restored.window.frame.width, 1100, accuracy: 1, "the default frame")
        XCTAssertTrue(restored.isInspectorCollapsed)
        XCTAssertEqual(split.splitViewItems[0].viewController.view.frame.width, 600, accuracy: 0.5)
        XCTAssertTrue(other.layoutChanges.isEmpty, "opening at the saved widths reports nothing: \(other.layoutChanges)")

        // Shown, it shares what the pane can spare with the sidebar.
        restored.setInspectorCollapsed(false)
        restored.window.layoutIfNeeded()
        let shown = split.splitViewItems[2].viewController.view.frame.width
        XCTAssertTrue(WorkspaceWindowController.inspectorWidths.contains(shown), "shown, the inspector has room: \(shown)")
        XCTAssertTrue(WorkspaceWindowController.sidebarWidths.contains(split.splitViewItems[0].viewController.view.frame.width))
        XCTAssertGreaterThanOrEqual(split.splitViewItems[1].viewController.view.frame.width, 319.5, "the pane keeps its minimum")
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
}

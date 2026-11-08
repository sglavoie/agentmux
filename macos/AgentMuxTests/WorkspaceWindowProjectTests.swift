import AppKit
import GhosttyKit
import ScreenCaptureKit
import XCTest
@testable import AgentMux

extension WorkspaceWindowTests {
    /// A project header's menu collapses the project to its header, which
    /// still counts what waits under it, and the collapse is kept with the
    /// order so it outlasts the app.
    func testAProjectCollapsesToItsHeaderFromItsMenu() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.activate(work)
        func lines() -> [String] {
            workspace.items.compactMap { item in
                switch item {
                case let .group(group): return group.collapsed ? "[\(group.project)]+" : "[\(group.project)]"
                case let .row(row): return "\(row.project)/\(row.label)"
                case .navigation, .section, .overview, .view: return nil
                }
            }
        }
        func header(_ project: String) throws -> Int {
            try XCTUnwrap(workspace.items.firstIndex { if case let .group(group) = $0 { return group.project == project } else { return false } })
        }
        var items = workspace.contextMenuItems(forRow: try header("api"))
        XCTAssertEqual(items.map(\.title), ["Collapse Project", "", "Mark Accounted For", "Edit Project Note…", "Undo Annotation"])
        _ = try XCTUnwrap(items[0].target).perform(try XCTUnwrap(items[0].action), with: items[0])
        XCTAssertEqual(lines(), ["[Done]", "[api]+", "[web]", "web/work"])
        XCTAssertEqual(app.orders.last?.collapsed, [WorkspaceOrder.groupToken(project: "api", pinned: false)])
        XCTAssertEqual(workspace.state.selection, work.key, "the pane stays where it is")
        XCTAssertEqual(workspace.sidebar.selectedRowIndexes, IndexSet(), "with no line to select")
        let cell = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: try header("api")) as? WorkspaceGroupCellView)
        XCTAssertEqual(cell.countLabel.stringValue, "1", "the waiting session is still counted")
        XCTAssertFalse(cell.disclosureLabel.isHidden)

        // A new snapshot keeps it collapsed.
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(lines(), ["[Done]", "[api]+", "[web]", "web/work"])

        // The pinned group collapses apart from the projects.
        workspace.setPinned(try row("web", "work").key, true)
        items = workspace.contextMenuItems(forRow: try header("Pinned"))
        XCTAssertEqual(items.map(\.title), ["Collapse Pinned"])
        _ = try XCTUnwrap(items[0].target).perform(try XCTUnwrap(items[0].action), with: items[0])
        XCTAssertEqual(lines(), ["[Pinned]+", "[Done]", "[api]+"])

        items = workspace.contextMenuItems(forRow: try header("api"))
        XCTAssertEqual(items.map(\.title), ["Expand Project", "", "Mark Accounted For", "Edit Project Note…", "Undo Annotation"])
        _ = try XCTUnwrap(items[0].target).perform(try XCTUnwrap(items[0].action), with: items[0])
        XCTAssertEqual(lines(), ["[Pinned]+", "[Done]", "[api]", "api/personal", "api/work"])
        XCTAssertEqual(workspace.sidebar.selectedRowIndexes, IndexSet(integer: try index(of: work.key)))
        XCTAssertEqual(app.orders.last?.collapsed, [WorkspaceOrder.groupToken(project: "Pinned", pinned: true)])
        XCTAssertEqual(workspace.order, app.orders.last)

        // A state section collapses to its heading, which counts its sessions.
        items = workspace.contextMenuItems(forRow: try header("Done"))
        XCTAssertEqual(items.map(\.title), ["Collapse Section"])
        _ = try XCTUnwrap(items[0].target).perform(try XCTUnwrap(items[0].action), with: items[0])
        XCTAssertEqual(lines(), ["[Pinned]+", "[Done]+"])
        XCTAssertEqual(app.orders.last?.collapsed, [WorkspaceOrder.groupToken(project: "Pinned", pinned: true),
                                                    WorkspaceOrder.groupToken(section: .done)])
        let section = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: try header("Done")) as? WorkspaceGroupCellView)
        XCTAssertEqual(section.countLabel.stringValue, "2")
        XCTAssertEqual(section.accessibilityLabel(), "Done, 2 sessions, collapsed")
        XCTAssertEqual(workspace.state.selection, work.key, "the pane stays where it is")

        // Going to a session in it expands the section.
        XCTAssertTrue(workspace.revealInSidebar())
        XCTAssertEqual(lines(), ["[Pinned]+", "[Done]", "[api]", "api/personal", "api/work"])
    }

    /// The overview above the sessions counts every section listed, Needs
    /// attention and Pinned included, and a click on one goes to its first
    /// session, expanding what holds it.
    func testTheOverviewCountsEachSectionAndGoesToItsFirstSession() throws {
        let live = [
            F.tmux(1, project: "api", profile: "work", icon: "bolt"),
            F.tmux(2, project: "web", profile: "work", icon: "bolt"),
            F.tmux(3, project: "api", profile: "personal", icon: "stop"),
            F.tmux(4, project: "docs", profile: "work"),
            F.tmux(5, project: "api", profile: "other", icon: "key", attention: true, changedAtMS: 1000),
        ]
        app.store.apply(snapshot: F.snapshot(live))
        func overview() throws -> (index: Int, counts: [WorkspaceSectionCount]) {
            let index = try XCTUnwrap(workspace.items.firstIndex { if case .overview = $0 { return true } else { return false } })
            guard case let .overview(counts) = workspace.items[index] else { throw XCTSkip("unreachable") }
            return (index, counts)
        }
        var (index, counts) = try overview()
        XCTAssertEqual(counts.map { "\($0.title) \($0.count)" }, ["Needs attention 1", "Done 1", "Working 2", "Idle 1"])
        guard case .group = workspace.items[index + 1] else { return XCTFail("the overview leads the sessions") }
        XCTAssertEqual(counts[2].first, .embedded(F.runtimeID(1)), "a section's first session as listed")

        workspace.setCollapsed(project: "api", pinned: false, true)
        if let working = workspace.state.groups.first(where: { $0.isSectionHeading && $0.stateSection == .working }) {
            workspace.setCollapsed(working, true)
        }
        (index, counts) = try overview()
        let cell = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: index) as? WorkspaceOverviewCellView)
        XCTAssertEqual(cell.chips.count, 4)
        XCTAssertEqual(cell.chips[2].accessibilityLabel(), "Working, 2 sessions")
        workspace.sidebar.scrollRowToVisible(0)
        cell.chips[2].performClick(nil)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        let line = try XCTUnwrap(workspace.items.firstIndex { if case let .row(row) = $0 { return row.key == .embedded(F.runtimeID(1)) } else { return false } })
        XCTAssertEqual(workspace.sidebar.selectedRow, line, "its line is selected, as ⌘J leaves it")
        XCTAssertTrue(workspace.sidebar.visibleRect.contains(workspace.sidebar.rect(ofRow: line)), "and scrolled into view")
        XCTAssertFalse(workspace.state.groups.contains { $0.collapsed && ($0.project == "api" || $0.stateSection == .working && $0.isSectionHeading) },
                       "going to the session expands its project and its section")

        XCTAssertGreaterThan(WorkspaceOverviewCellView.height(for: counts, width: 120),
                             WorkspaceOverviewCellView.height(for: counts, width: 600), "narrow, the chips wrap")
    }

    /// Digits reach collapsed sessions after the visible ones and expand
    /// their group; ⌘] steps from the session the pane is on even after its
    /// row was collapsed away.
    func testDigitsAndStepsAgreeAboutACollapsedProject() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.activate(work)
        workspace.setCollapsed(project: "api", pinned: false, true)
        XCTAssertNil(workspace.items.firstIndex { $0 == .row(work) }, "the row is collapsed away")

        workspace.selectNeighbor(1)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(4)), "the step is from api/work, not from the top")
        XCTAssertTrue(workspace.state.groups.first { $0.project == "api" }?.collapsed == true, "a step expands nothing")

        workspace.activate(work)
        workspace.selectNeighbor(-1, sessionsOnly: true)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(4)), "F7 wraps from api/work as well")

        XCTAssertEqual(workspace.sessionName(at: 0), "web — work")
        XCTAssertEqual(workspace.sessionName(at: 1), "api — personal")
        workspace.selectSession(at: 1)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        XCTAssertTrue(workspace.state.groups.first { $0.project == "api" }?.collapsed == false, "going to a session expands its project")
        XCTAssertEqual(workspace.sidebar.selectedRowIndexes, IndexSet(integer: try index(of: .embedded(F.runtimeID(2)))))
        XCTAssertEqual(workspace.sessionKey(of: work.key), "2")
    }

    /// A project's header collapses and expands by a click, by its
    /// accessibility action, and by ← and → in the list, and shows which it
    /// is.
    func testAProjectHeaderTogglesByClickKeyboardAndAccessibility() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        func header() throws -> Int {
            try XCTUnwrap(workspace.items.firstIndex { if case let .group(group) = $0 { return group.project == "api" } else { return false } })
        }
        func collapsed() -> Bool? { workspace.state.groups.first { $0.project == "api" }?.collapsed }
        func cell() throws -> WorkspaceGroupCellView {
            try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: try header()) as? WorkspaceGroupCellView)
        }

        var shown = try cell()
        XCTAssertEqual(shown.nameLabel.textColor, .labelColor, "a project's name reads as a heading")
        XCTAssertFalse(shown.disclosureLabel.isHidden, "an expanded project shows its disclosure too")
        XCTAssertEqual(shown.disclosureLabel.stringValue, WorkspaceGroupCellView.expandedDisclosure)
        XCTAssertTrue(shown.isAccessibilityDisclosed())
        XCTAssertEqual(shown.accessibilityLabel(), "api, 1 waiting, expanded")
        XCTAssertEqual(shown.accessibilityCustomActions()?.map(\.name), [WorkspaceGroupCellView.collapseAction])

        // A click on the header.
        workspace.focusItem(at: try header())
        XCTAssertEqual(collapsed(), true)
        shown = try cell()
        XCTAssertEqual(shown.disclosureLabel.stringValue, WorkspaceGroupCellView.collapsedDisclosure)
        XCTAssertFalse(shown.isAccessibilityDisclosed())
        XCTAssertEqual(shown.accessibilityLabel(), "api, 1 waiting, collapsed")
        workspace.focusItem(at: try header())
        XCTAssertEqual(collapsed(), false)
        XCTAssertEqual(workspace.state.selection, nil, "a header is not a session to go to")

        // Its accessibility action.
        let action = try XCTUnwrap(try cell().accessibilityCustomActions()?.first)
        XCTAssertEqual(action.handler?(), true)
        XCTAssertEqual(collapsed(), true)
        let expand = try XCTUnwrap(try cell().accessibilityCustomActions()?.first)
        XCTAssertEqual(expand.name, WorkspaceGroupCellView.expandAction)
        XCTAssertEqual(expand.handler?(), true)
        XCTAssertEqual(collapsed(), false)

        // ← and → in the list, on one of the project's sessions.
        let work = try row("api", "work")
        workspace.activate(work)
        workspace.focusSidebar()
        workspace.sidebar.keyDown(with: TestKey.left.event())
        XCTAssertEqual(collapsed(), true)
        XCTAssertEqual(workspace.state.selection, work.key, "the pane stays where it is")
        workspace.sidebar.keyDown(with: TestKey.function(NSRightArrowFunctionKey, keyCode: 124).event())
        XCTAssertEqual(collapsed(), false, "→ expands the project of the session the pane is on")
        XCTAssertEqual(workspace.sidebar.selectedRowIndexes, IndexSet(integer: try index(of: work.key)))
        workspace.sidebar.keyDown(with: TestKey.function(NSRightArrowFunctionKey, keyCode: 124).event())
        XCTAssertEqual(collapsed(), false, "→ on an expanded project leaves it so")
        XCTAssertTrue(workspace.state.groups.first { $0.project == "web" }?.collapsed == false, "other projects are left alone")
    }

    func testReturnOrAClickOnADetachedSessionReattachesIt() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.activate(work)
        workspace.terminalEnded(try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1))))
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(1)))
        XCTAssertEqual(workspace.placeholder.button.keyEquivalent, "\r", "Reattach is the default button")

        // Return in the session list, on the detached session's line.
        workspace.focusSidebar()
        workspace.sidebar.keyDown(with: TestKey(keyCode: 36, characters: "\r", unmodified: "\r").event())
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(1)])
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(1)))

        // Go to… goes where a click would, and reattaches the same way.
        workspace.terminalEnded(try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1))))
        workspace.toggleSwitcher()
        workspace.switcher.enter("api work")
        workspace.switcher.choose()
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(1), F.runtimeID(1)])
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))

        // Any other button is not the default one.
        workspace.select(try row("web", "work").key)
        XCTAssertEqual(workspace.placeholder.button.keyEquivalent, "\r")
    }

    func testAHiddenSidebarNeverTakesTheKeyboard() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        workspace.setSidebarCollapsed(true)
        workspace.window.layoutIfNeeded()
        func inTheSidebar() -> Bool {
            (workspace.window.firstResponder as? NSView)?.isDescendant(of: workspace.sidebar) == true
        }

        // The shown terminal ends while the list is hidden.
        workspace.terminalEnded(try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1))))
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(1)))
        XCTAssertFalse(inTheSidebar(), "the arrows must not switch sessions in a list that cannot be seen")

        // The list is hidden while it has the keyboard and the pane shows
        // no terminal.
        workspace.setSidebarCollapsed(false)
        workspace.focusSidebar()
        XCTAssertTrue(inTheSidebar())
        workspace.setSidebarCollapsed(true)
        XCTAssertFalse(inTheSidebar())

        // A view that ends while the list is hidden.
        workspace.showView(WorkspaceUtilityView.dashboard.id)
        workspace.terminalEnded(try XCTUnwrap(workspace.utilityTerminalView(for: WorkspaceUtilityView.dashboard.id)))
        XCTAssertEqual(workspace.content, .viewEnded(id: WorkspaceUtilityView.dashboard.id))
        XCTAssertFalse(inTheSidebar())

        // Shown again, the list takes it as before.
        workspace.focusSidebar()
        XCTAssertTrue(inTheSidebar())
    }

    func testAViewWhoseProgramIsStillRunningClosesOnlyWhenTheOperatorAgrees() throws {
        let shell = WorkspaceUtilityView(kind: .shell, target: "/work/api", title: "Shell")
        workspace.open(shell)
        let view = try XCTUnwrap(workspace.utilityTerminalView(for: shell.id))
        var asked: [(String, @MainActor (Bool) -> Void)] = []
        workspace.confirmViewClose = { title, answer in asked.append((title, answer)) }

        workspace.terminalAskedToClose(view, needsConfirm: true)
        XCTAssertEqual(asked.map(\.0), ["Shell"])
        XCTAssertEqual(app.closedViews, [], "nothing ends before the answer")
        asked[0].1(false)
        XCTAssertEqual(app.closedViews, [])
        XCTAssertEqual(workspace.content, .view(id: shell.id))

        workspace.terminalAskedToClose(view, needsConfirm: true)
        asked[1].1(true)
        XCTAssertEqual(app.closedViews, [shell.id])
        XCTAssertNil(workspace.views.view(shell.id))

        // Nothing running: it closes at once.
        workspace.open(shell)
        workspace.terminalAskedToClose(try XCTUnwrap(workspace.utilityTerminalView(for: shell.id)), needsConfirm: false)
        XCTAssertEqual(asked.count, 2)
        XCTAssertEqual(app.closedViews, [shell.id, shell.id])

        // An attachment's close only detaches, so it is never asked about.
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        workspace.terminalAskedToClose(try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1))), needsConfirm: true)
        XCTAssertEqual(asked.count, 2)
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(1)))
    }

    func testCloseShownCanSkipConfirmationAndRestoreItImmediately() throws {
        let shell = WorkspaceUtilityView(kind: .shell, target: "/work/api", title: "Shell")
        workspace.open(shell)
        app.runningViews = [shell.id]
        var asked: [String] = []
        workspace.confirmViewClose = { title, answer in asked.append(title); answer(false) }

        workspace.confirmBeforeClosing = false
        XCTAssertTrue(workspace.closeShown())
        XCTAssertTrue(asked.isEmpty)
        XCTAssertEqual(app.closedViews, [shell.id])
        XCTAssertNil(workspace.views.view(shell.id))

        workspace.open(shell)
        workspace.confirmBeforeClosing = true
        XCTAssertTrue(workspace.closeShown())
        XCTAssertEqual(asked, ["Shell"])
        XCTAssertEqual(app.closedViews, [shell.id])
        XCTAssertNotNil(workspace.views.view(shell.id))
    }

    func testGhosttyCloseCanSkipConfirmationWithoutDisablingQuitConfirmation() throws {
        let shell = WorkspaceUtilityView(kind: .shell, target: "/work/api", title: "Shell")
        workspace.open(shell)
        app.runningViews = [shell.id]
        workspace.confirmBeforeClosing = false
        workspace.confirmViewClose = { _, _ in XCTFail("close confirmation is disabled") }
        var quitTitles: [String] = []
        workspace.confirmQuitWithViews = { titles, answer in quitTitles = titles; answer(false) }
        XCTAssertTrue(workspace.confirmQuit { XCTAssertFalse($0) })
        XCTAssertEqual(quitTitles, ["Shell"])
        XCTAssertTrue(app.closedViews.isEmpty)

        workspace.terminalAskedToClose(try XCTUnwrap(workspace.utilityTerminalView(for: shell.id)), needsConfirm: true)
        XCTAssertEqual(app.closedViews, [shell.id])
        XCTAssertNil(workspace.views.view(shell.id))
    }
}

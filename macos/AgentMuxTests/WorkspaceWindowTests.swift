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

    /// Every turn ended, so all three are listed under Done, below its
    /// heading, and the layout tests have one state section.
    private let sessions = [
        F.tmux(1, project: "api", profile: "work", icon: "stop"),
        F.tmux(2, project: "api", profile: "personal", icon: "stop", attention: true, changedAtMS: 2000),
        // A seen completion stays in its project; attention transitions are
        // exercised separately below so layout tests have a fixed baseline.
        F.tmux(4, project: "web", icon: "stop", attention: true, changedAtMS: 1000),
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

    private func chooseGroup(at index: Int) {
        workspace.groupFilter.pills[index].performClick(nil)
    }

    func testProjectFirstDragsPinnedAndWaitingRowsWithinTheirState() throws {
        var live = [
            F.tmux(1, project: "api", profile: "a", icon: "key", attention: true),
            F.tmux(2, project: "api", profile: "b", icon: "key", attention: true),
            F.tmux(3, project: "api", profile: "c", icon: "bolt"),
        ]
        live[1].pinned = true
        workspace.setSidebarGrouping(.projectThenState)
        app.store.apply(snapshot: F.snapshot(live))
        let pinned = try index(of: .embedded(F.runtimeID(2)))
        let first = try index(of: .embedded(F.runtimeID(1)))
        let cell = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: pinned, makeIfNecessary: true) as? WorkspaceRowCellView)
        XCTAssertTrue(cell.titleLabel.stringValue.hasPrefix("★ "))
        XCTAssertTrue(workspace.moveItem(at: pinned, to: first))
        XCTAssertEqual(workspace.state.rows.map(\.key), [2, 1, 3].map { .embedded(F.runtimeID($0)) })
        XCTAssertEqual(workspace.order.projects["api"], [2, 1, 3].map { F.runtimeID($0) })
        let moved = try index(of: .embedded(F.runtimeID(2)))
        let running = try index(of: .embedded(F.runtimeID(3)))
        XCTAssertFalse(workspace.moveItem(at: moved, to: running), "dragging cannot move a session into a different state")
        XCTAssertEqual(workspace.state.groups.map(\.title), ["api", "Waiting", "Working"])
    }

    func testProjectGroupingChangesLiveAndPreservesSelectionRevealAndDragging() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let shown = try row("api", "work")
        workspace.activate(shown)
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        workspace.setSidebarGrouping(.projectThenState)
        XCTAssertEqual(workspace.state.selection, shown.key)
        XCTAssertTrue(workspace.terminalView(for: F.runtimeID(1)) === terminal)
        XCTAssertEqual(workspace.state.groups.map(\.title), ["api", "Done", "web", "Done"])
        let nested = try XCTUnwrap(workspace.state.groups.first { $0.isNestedState && $0.project == "api" })
        workspace.setCollapsed(nested, true)
        XCTAssertFalse(workspace.items.contains { if case let .row(row) = $0 { return row.key == shown.key }; return false })
        workspace.setCollapsed(project: "api", pinned: false, true)
        XCTAssertTrue(workspace.revealInSidebar())
        XCTAssertEqual(workspace.sidebar.selectedRow, try index(of: shown.key))
        XCTAssertFalse(try XCTUnwrap(workspace.state.groups.first { $0.isNestedState && $0.project == "api" }).collapsed)
        let from = try index(of: shown.key)
        let before = try index(of: .embedded(F.runtimeID(2)))
        XCTAssertTrue(workspace.moveItem(at: from, to: before))
        XCTAssertEqual(workspace.state.rows.filter { $0.project == "api" }.map(\.key), [shown.key, .embedded(F.runtimeID(2))])
        workspace.setSidebarGrouping(.stateThenProject)
        XCTAssertFalse(workspace.state.groups.contains(where: \.isProjectHeading))
        XCTAssertEqual(workspace.state.selection, shown.key)
        workspace.setSidebarGrouping(.projectThenState)
        var changed = sessions
        changed[0].iconState = "bolt"
        app.store.apply(snapshot: F.snapshot(changed))
        XCTAssertEqual(workspace.state.groups.map(\.title), ["api", "Done", "Working", "web", "Done"])
        XCTAssertEqual(workspace.state.selection, shown.key)
    }

    func testWorkspaceNavigationSharesALineAndRestacksWithZoomAndResize() async throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.window.setContentSize(NSSize(width: 1500, height: 900))
        let split = try XCTUnwrap(workspace.window.contentViewController as? NSSplitViewController)

        func settleLayout() async throws {
            workspace.window.contentView?.layoutSubtreeIfNeeded()
            try await Task.sleep(for: .milliseconds(50))
            workspace.window.contentView?.layoutSubtreeIfNeeded()
        }

        split.splitView.setPosition(600, ofDividerAt: 0)
        try await settleLayout()
        let line = try XCTUnwrap(workspace.items.firstIndex(of: .navigation))
        XCTAssertFalse(workspace.items.contains(.view(.dashboard)))
        XCTAssertFalse(workspace.items.contains(.view(.overview)))
        let cell = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: line, makeIfNecessary: true) as? WorkspaceNavigationCellView)
        cell.layoutSubtreeIfNeeded()
        XCTAssertEqual(cell.buttons[0].frame.minY, cell.buttons[1].frame.minY)
        XCTAssertLessThan(cell.buttons[0].frame.maxX, cell.buttons[1].frame.minX)
        for button in cell.buttons {
            XCTAssertGreaterThanOrEqual(button.bounds.width, WorkspaceNavigationButton.width(for: button.view))
        }
        cell.buttons[1].performClick(nil)
        XCTAssertEqual(workspace.viewSelection, WorkspaceUtilityView.overview.id)
        XCTAssertEqual(workspace.sidebar.selectedRow, line)
        XCTAssertEqual(cell.buttons.map(\.state), [.off, .on])
        workspace.sidebar.keyDown(with: TestKey.left.event())
        XCTAssertEqual(workspace.viewSelection, WorkspaceUtilityView.dashboard.id)
        XCTAssertEqual(cell.buttons.map(\.state), [.on, .off])
        workspace.sidebar.keyDown(with: TestKey.function(NSRightArrowFunctionKey, keyCode: 124).event())
        workspace.focusSelectedItem()
        XCTAssertEqual(workspace.viewSelection, WorkspaceUtilityView.overview.id)

        workspace.setSidebarZoom(percent: 200)
        try await settleLayout()
        XCTAssertFalse(workspace.items.contains(.navigation))
        XCTAssertEqual(workspace.items[workspace.sidebar.selectedRow], .view(.overview))
        workspace.setSidebarZoom(percent: 100)
        try await settleLayout()
        XCTAssertEqual(workspace.items[workspace.sidebar.selectedRow], .navigation)

        split.splitView.setPosition(240, ofDividerAt: 0)
        try await settleLayout()
        XCTAssertFalse(workspace.items.contains(.navigation))
        XCTAssertTrue(workspace.items.contains(.view(.dashboard)))
        XCTAssertEqual(workspace.items[workspace.sidebar.selectedRow], .view(.overview))
    }

    func testGroupScopeIncludesSessionsLaunchedWithAnotherProfileOrHarness() {
        app.workspaceProjects = [
            ProjectEntry(profile: "personal-codex", title: "Garden", harness: "codex", groups: ["personal", "workflow"]),
        ]
        let live = [
            F.tmux(1, project: "Garden", profile: "personal-claude", harness: "claude"),
            F.tmux(2, project: "Garden", profile: "personal-codex", harness: "codex"),
            F.tmux(3, project: "Unconfigured", profile: "personal-claude", harness: "claude"),
        ]
        app.store.apply(snapshot: F.snapshot(live))
        workspace.select(.embedded(F.runtimeID(1)))
        for index in [1, 2] {
            chooseGroup(at: index)
            XCTAssertEqual(Set(workspace.sessionShortcutRows.map(\.key)), [.embedded(F.runtimeID(1)), .embedded(F.runtimeID(2))])
            XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
            XCTAssertEqual(workspace.scopedSidebar.sectionCounts.map(\.count), [2])
        }
        app.store.apply(.disconnected(.notRunning, retryIn: 0.5))
        XCTAssertEqual(workspace.sessionShortcutRows.count, 2, "cached rows keep their project membership")
        XCTAssertEqual(workspace.selectedGroup, "workflow")
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertEqual(workspace.sessionShortcutRows.count, 2)

        // An explicitly configured profile still owns its own tags, even
        // when another profile of the same project declares different ones.
        app.workspaceProjects.append(ProjectEntry(profile: "personal-claude", title: "Garden", groups: ["personal"]))
        workspace.reloadSidebar(allowReorder: true)
        XCTAssertEqual(workspace.sessionShortcutRows.map(\.key), [.embedded(F.runtimeID(2))])
        chooseGroup(at: 1)
        XCTAssertEqual(workspace.sessionShortcutRows.count, 2)
    }

    func testGroupPillsMatchEveryTagRegardlessOfItsPositionAndToggleOff() {
        app.workspaceProjects = [
            ProjectEntry(profile: "work", title: "api", groups: ["work", "tradition"]),
            ProjectEntry(profile: "personal", title: "api"),
            ProjectEntry(profile: "work", title: "web", groups: ["tradition", "work"]),
        ]
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(workspace.groupFilter.pills.map { $0.group ?? "All" }, ["All", "tradition", "work"])
        for index in [2, 1, 2, 1] {
            chooseGroup(at: index)
            XCTAssertEqual(Set(workspace.sessionShortcutRows.map(\.key)), [.embedded(F.runtimeID(1)), .embedded(F.runtimeID(4))])
            XCTAssertEqual(workspace.groupFilter.pills.map(\.state), (0...2).map { $0 == index ? .on : .off })
            workspace.reloadSidebar()
            XCTAssertEqual(workspace.selectedGroup, workspace.groupFilter.pills[index].group)
        }
        chooseGroup(at: 1)
        XCTAssertNil(workspace.selectedGroup, "clicking the active pill clears the scope")
        XCTAssertEqual(workspace.sessionShortcutRows.count, 3)
        XCTAssertEqual(workspace.groupFilter.pills.map(\.state), [.on, .off, .off])
        chooseGroup(at: 2)
        chooseGroup(at: 0)
        XCTAssertNil(workspace.selectedGroup)
    }

    func testGroupPillsWrapAndKeepTheSelectionWhenTheSidebarResizes() {
        let filter = WorkspaceGroupFilter()
        filter.show(groups: ["personal", "tradition", "work", String(repeating: "long", count: 20)], selected: "tradition")
        for width: CGFloat in [200, 120, 400] {
            filter.setFrameSize(NSSize(width: width, height: 300))
            filter.layoutSubtreeIfNeeded()
            XCTAssertGreaterThan(filter.pills.last!.frame.minY, filter.pills.first!.frame.minY)
            for pill in filter.pills {
                XCTAssertGreaterThanOrEqual(pill.frame.minX, 0)
                XCTAssertLessThanOrEqual(pill.frame.maxX, width)
                XCTAssertLessThanOrEqual(pill.frame.maxY, filter.intrinsicContentSize.height)
            }
            for (previous, next) in zip(filter.pills, filter.pills.dropFirst()) {
                XCTAssertFalse(previous.frame.intersects(next.frame))
            }
            XCTAssertEqual(filter.pills.filter { $0.state == .on }.map(\.group), ["tradition"])
        }
    }

    func testGroupScopeUsesConfiguredTagsAndPreservesHiddenTerminals() throws {
        app.workspaceProjects = [
            ProjectEntry(profile: "work", title: "api", groups: ["work"]),
            ProjectEntry(profile: "personal", title: "api", groups: ["personal", "shared"]),
            ProjectEntry(profile: "work", title: "web", groups: ["shared"]),
            ProjectEntry(profile: "work", title: "offline", groups: ["empty", "personal"]),
        ]
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(workspace.groupFilter.pills.map { $0.group ?? "All" }, ["All", "empty", "personal", "shared", "work"])
        let hidden = try row("api", "work")
        workspace.select(hidden.key)
        XCTAssertTrue(workspace.openTerminalBesideSession())
        let terminal = try XCTUnwrap(workspace.views.terminals(beside: hidden.embeddedRuntimeSessionID).first)
        workspace.select(hidden.key)
        let made = app.made

        chooseGroup(at: 2)
        XCTAssertEqual(workspace.selectedGroup, "personal")
        XCTAssertEqual(workspace.sessionShortcutRows.map(\.key), [.embedded(F.runtimeID(2))])
        XCTAssertEqual(workspace.scopedSidebar.sectionCounts.map(\.count), [1])
        XCTAssertFalse(workspace.items.contains(.view(terminal)))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        XCTAssertNotNil(workspace.terminals[F.runtimeID(1)])
        XCTAssertNotNil(workspace.views.view(terminal.id))
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.made, made + [F.runtimeID(2)])
        XCTAssertTrue(app.closed.isEmpty)
        XCTAssertEqual(workspace.state.rows.count, 3)
        XCTAssertEqual(workspace.selectedGroup, "personal")

        chooseGroup(at: 3)
        XCTAssertEqual(Set(workspace.sessionShortcutRows.map(\.key)), [.embedded(F.runtimeID(2)), .embedded(F.runtimeID(4))])
        chooseGroup(at: 1)
        XCTAssertTrue(workspace.sessionShortcutRows.isEmpty, "a configured group can have no running sessions")
        XCTAssertTrue(workspace.scopedSidebar.listedGroups.isEmpty)
        chooseGroup(at: 0)
        XCTAssertEqual(workspace.sessionShortcutRows.count, 3)
        XCTAssertTrue(workspace.items.contains(.view(terminal)))
    }

    func testGroupScopeRefreshAndLiteralAllGroup() {
        app.workspaceProjects = [
            ProjectEntry(profile: "work", title: "api", groups: ["All", "personal", "personal"]),
            ProjectEntry(profile: "personal", title: "api"),
        ]
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(workspace.groupFilter.pills.map { $0.group ?? "All" }, ["All", "All", "personal"])
        chooseGroup(at: 1)
        XCTAssertEqual(workspace.selectedGroup, "All")
        XCTAssertEqual(workspace.sessionShortcutRows.map(\.key), [.embedded(F.runtimeID(1))])

        app.workspaceProjects.append(ProjectEntry(profile: "work", title: "web", groups: ["All", "new"]))
        workspace.reloadSidebar(allowReorder: true)
        XCTAssertEqual(workspace.selectedGroup, "All")
        XCTAssertEqual(workspace.groupFilter.pills.map { $0.group ?? "All" }, ["All", "All", "new", "personal"])
        XCTAssertEqual(workspace.sessionShortcutRows.count, 2)
        app.workspaceProjects = []
        workspace.reloadSidebar(allowReorder: true)
        XCTAssertNil(workspace.selectedGroup)
        XCTAssertEqual(workspace.groupFilter.pills.map { $0.group ?? "All" }, ["All"])
        XCTAssertEqual(workspace.sessionShortcutRows.count, 3)
    }

    func testGroupScopeIncludesPinnedAndAttentionRowsAndRevealsOutsideScope() {
        app.workspaceProjects = [
            ProjectEntry(profile: "work", title: "api", groups: ["personal"]),
            ProjectEntry(profile: "personal", title: "api"),
        ]
        var live = sessions
        live[0].pinned = true
        live[1].iconState = "question"
        app.store.apply(snapshot: F.snapshot(live))
        chooseGroup(at: 1)
        XCTAssertEqual(workspace.scopedSidebar.groups.first?.pinned, true)
        XCTAssertEqual(workspace.sessionShortcutRows.map(\.key), [.embedded(F.runtimeID(1))])
        workspace.selectSession(at: 0)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        workspace.selectNeighbor(1)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        workspace.select(.embedded(F.runtimeID(2)))
        XCTAssertTrue(workspace.revealInSidebar())
        XCTAssertNil(workspace.selectedGroup)
        XCTAssertEqual(workspace.groupFilter.pills.first?.state, .on)
        XCTAssertEqual(workspace.sessionShortcutRows.count, 3)
    }

    func testDraggingWithinGroupScopePreservesHiddenSiblingOrder() throws {
        app.workspaceProjects = [
            ProjectEntry(profile: "work", title: "api", groups: ["personal"]),
            ProjectEntry(profile: "other", title: "api"),
        ]
        let live = [
            F.tmux(1, project: "api", profile: "work"),
            F.tmux(2, project: "api", profile: "other"),
            F.tmux(3, project: "api", profile: "work"),
        ]
        workspace.order.set((1...3).map { .embedded(F.runtimeID($0)) }, for: "api")
        app.store.apply(snapshot: F.snapshot(live))
        chooseGroup(at: 1)
        let first = try index(of: .embedded(F.runtimeID(1)))
        let last = try index(of: .embedded(F.runtimeID(3)))
        XCTAssertTrue(workspace.moveItem(at: last, to: first))
        XCTAssertEqual(workspace.sessionShortcutRows.map(\.key), [.embedded(F.runtimeID(3)), .embedded(F.runtimeID(1))])
        chooseGroup(at: 0)
        XCTAssertEqual(workspace.sessionShortcutRows.map(\.key), [.embedded(F.runtimeID(3)), .embedded(F.runtimeID(2)), .embedded(F.runtimeID(1))])
    }

    /// A refusal is said at the top of the pane for a moment, where the
    /// operator is looking; a failure is an alert that offers the fix.
    func testNoticesShowInThePaneAndErrorsOfferAFix() {
        XCTAssertNil(workspace.shownNotice)
        workspace.showNotice(AppDelegate.noSessionShown)
        XCTAssertEqual(workspace.shownNotice, AppDelegate.noSessionShown)
        XCTAssertTrue(workspace.noticeBanner.isDescendant(of: workspace.pane))
        XCTAssertNil(workspace.noticeBanner.hitTest(.zero), "clicks reach the terminal beneath")
        workspace.showNotice("Terminal is not running.")
        XCTAssertEqual(workspace.shownNotice, "Terminal is not running.", "a newer notice replaces it")
        workspace.endNotice()
        XCTAssertNil(workspace.shownNotice)

        var errors: [WorkspaceError] = []
        workspace.reportError = { errors.append($0) }
        workspace.showError(title: "api could not be named", message: AMXWording.notFound)
        workspace.showError(title: "api could not be ended", message: "exit status 1", details: "error: boom")
        XCTAssertEqual(errors.map(\.recovery), [.openSettings, nil])
        XCTAssertEqual(errors.last?.details, "error: boom")
    }

    func testOptionZoomScalesBothSidebarsAndResetsWithoutChangingFocusOrPaneWidth() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.setInspectorCollapsed(false, animated: false)
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        workspace.window.makeFirstResponder(workspace.sidebar)
        let responder = workspace.window.firstResponder
        let originalWidth = workspace.inspector.convert(workspace.inspector.bounds, to: nil).width
        let originalSize = workspace.sidebar.convert(NSSize(width: 10, height: 10), to: nil)
        func key(_ code: UInt16, _ text: String, _ flags: NSEvent.ModifierFlags = .option) throws {
            let event = try XCTUnwrap(NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: flags,
                timestamp: 0, windowNumber: workspace.window.windowNumber, context: nil, characters: text,
                charactersIgnoringModifiers: text, isARepeat: false, keyCode: code))
            XCTAssertTrue(workspace.window.performKeyEquivalent(with: event))
        }
        for (code, text, flags) in [(UInt16(24), "≠", NSEvent.ModifierFlags.option), (24, "±", [.option, .shift])] {
            try key(code, text, flags)
            XCTAssertGreaterThan(workspace.sidebarZoom, 1)
            for view: NSView in [workspace.sidebar, workspace.inspector] {
                XCTAssertEqual(view.convert(NSSize(width: 10, height: 10), to: nil).height,
                               originalSize.height * workspace.sidebarZoom, accuracy: 0.01)
            }
            XCTAssertEqual(workspace.inspector.convert(workspace.inspector.bounds, to: nil).width, originalWidth, accuracy: 0.01)
            XCTAssertTrue(workspace.window.firstResponder === responder)
            try key(29, "º")
            XCTAssertEqual(workspace.sidebarZoom, 1)
        }
        workspace.setInspectorCollapsed(true, animated: false)
        try key(27, "–")
        workspace.setInspectorCollapsed(false, animated: false)
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        XCTAssertEqual(workspace.sidebarZoom, 0.9, accuracy: 0.01)
        XCTAssertEqual(workspace.inspector.convert(NSSize(width: 10, height: 10), to: nil).height, 9, accuracy: 0.01)
        try key(29, "º")
        XCTAssertEqual(workspace.sidebar.convert(NSSize(width: 10, height: 10), to: nil), originalSize)
    }

    /// Keyboard zoom is reported for Settings to keep; a zoom Settings
    /// applies is not reported back, and steps stay whole percents.
    func testSidebarZoomReportsKeyboardChangesOnlyAndClamps() {
        var reported: [Int] = []
        workspace.onSidebarZoomChanged = { reported.append($0) }
        workspace.setSidebarZoom(percent: 190)
        XCTAssertEqual(reported, [])
        workspace.zoomSidebars(.bigger)
        workspace.zoomSidebars(.bigger)
        XCTAssertEqual(workspace.sidebarZoomPercent, 200)
        workspace.zoomSidebars(.reset)
        for _ in 0..<5 { workspace.zoomSidebars(.smaller) }
        XCTAssertEqual(workspace.sidebarZoomPercent, 70)
        XCTAssertEqual(reported, [200, 100, 90, 80, 70])
    }

    func testCompactSidebarPlacesTerminalTabsBesideSessionAndKeepsSelection() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.setCompactSidebar(true)
        workspace.activate(work)
        XCTAssertTrue(workspace.openTerminalBesideSession())
        XCTAssertTrue(workspace.openTerminalBesideSession())
        let terminals = workspace.views.terminals(beside: F.runtimeID(1))
        XCTAssertEqual(terminals.count, 2)
        workspace.programTitles[terminals[0].id] = "make test"
        workspace.sidebar.reloadData()
        let sessionIndex = try index(of: work.key)
        let cell = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: sessionIndex, makeIfNecessary: true) as? WorkspaceRowCellView)
        XCTAssertEqual(cell.compactTerminalButtons.map(\.terminalID), terminals.map(\.id))
        XCTAssertEqual(cell.compactTerminalButtons[0].displayedDetail, "make test")
        XCTAssertEqual(cell.compactTerminalButtons[1].displayedDetail, "")
        let menu = try XCTUnwrap(cell.compactTerminalButtons[0].onMenu?())
        XCTAssertEqual(menu.items.map(\.title), ["Name Terminal…", "Move Terminal Left", "Move Terminal Right", "Close"])
        XCTAssertFalse(menu.items[1].isEnabled)
        XCTAssertTrue(menu.items[2].isEnabled)
        workspace.window.layoutIfNeeded()
        for (index, button) in cell.compactTerminalButtons.enumerated() {
            let frame = button.convert(button.bounds, to: cell)
            XCTAssertGreaterThan(frame.minX, cell.bounds.minX + cell.bounds.width * 0.4)
            XCTAssertGreaterThan(frame.width, 0)
            XCTAssertLessThanOrEqual(frame.maxX, cell.bounds.maxX)
            XCTAssertFalse(button.closeButton.isHidden)
            XCTAssertEqual(button.closeButton.accessibilityLabel(), "Close Terminal: \(terminals[index].displayName)")
            XCTAssertEqual(button.displayedTitle, "T\(terminals[index].ordinal)")
        }
        for terminal in terminals {
            let line = try XCTUnwrap(workspace.items.firstIndex(of: .view(terminal)))
            XCTAssertEqual(workspace.tableView(workspace.sidebar, heightOfRow: line), 1)
        }
        cell.compactTerminalButtons[0].performClick(nil)
        XCTAssertEqual(workspace.viewSelection, terminals[0].id)
        XCTAssertTrue(cell.compactTerminalButtons[0].isCurrent)
        XCTAssertFalse(cell.compactTerminalButtons[1].isCurrent)
        _ = try XCTUnwrap(menu.items[2].target).perform(try XCTUnwrap(menu.items[2].action), with: menu.items[2])
        XCTAssertEqual(workspace.views.terminals(beside: F.runtimeID(1)).map(\.id), terminals.reversed().map(\.id))
        let reordered = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: sessionIndex, makeIfNecessary: true) as? WorkspaceRowCellView)
        XCTAssertEqual(reordered.compactTerminalButtons.map(\.terminalID), terminals.reversed().map(\.id))
        workspace.programTitles[terminals[0].id] = "swift build"
        workspace.progresses[terminals[0].id] = Ghostty.Progress(state: .normal, percent: 45)
        workspace.reload(IndexSet(integer: sessionIndex))
        let updated = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: sessionIndex, makeIfNecessary: true) as? WorkspaceRowCellView)
        XCTAssertEqual(updated.compactTerminalButtons[1].displayedDetail, "45% · swift build")
        workspace.confirmBeforeClosing = false
        updated.compactTerminalButtons[0].closeButton.performClick(nil)
        XCTAssertEqual(workspace.views.terminals(beside: F.runtimeID(1)).map(\.id), [terminals[0].id])
        XCTAssertNotNil(workspace.state.row(work.key), "closing a tab must leave its session running")
        workspace.setCompactSidebar(false)
        let line = try XCTUnwrap(workspace.items.firstIndex(of: .view(terminals[0])))
        XCTAssertEqual(workspace.tableView(workspace.sidebar, heightOfRow: line), 38)
        let expanded = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: sessionIndex, makeIfNecessary: true) as? WorkspaceRowCellView)
        XCTAssertTrue(expanded.compactTerminalButtons.isEmpty)
    }

    func testCompactTerminalTabsReceiveClicksAcrossSessionStateFlashes() throws {
        workspace.setCompactSidebar(true)
        workspace.window.orderFront(nil)
        // Return to Idle as well: the overlay stays installed after any transition.
        for icon in ["", "working", "stop", "question", ""] {
            app.store.apply(snapshot: F.snapshot([F.tmux(1, project: "api", profile: "work", icon: icon)]))
            let session = try row("api", "work")
            workspace.activate(session)
            if workspace.views.terminals(beside: F.runtimeID(1)).isEmpty {
                XCTAssertTrue(workspace.openTerminalBesideSession())
                XCTAssertTrue(workspace.openTerminalBesideSession())
            }
            let terminals = workspace.views.terminals(beside: F.runtimeID(1))
            let line = try index(of: session.key)
            let rowView = try XCTUnwrap(workspace.sidebar.rowView(atRow: line, makeIfNecessary: true) as? WorkspaceRowView)
            rowView.flash()
            for flashing in [true, false] {
                if !flashing {
                    // Completing the animation leaves its transparent view installed.
                    for view in rowView.subviews { view.layer?.removeAnimation(forKey: WorkspaceRowView.flashKey) }
                }
                XCTAssertEqual(rowView.isFlashing, flashing)
                for terminal in terminals {
                    workspace.activate(session)
                    workspace.sidebar.scrollRowToVisible(line)
                    workspace.window.layoutIfNeeded()
                    let cell = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: line, makeIfNecessary: true) as? WorkspaceRowCellView)
                    let button = try XCTUnwrap(cell.compactTerminalButtons.first { $0.terminalID == terminal.id })
                    let root = try XCTUnwrap(workspace.window.contentView)
                    let point = button.convert(NSPoint(x: 4, y: button.bounds.midY), to: nil)
                    let hit = root.hitTest(root.superview!.convert(point, from: nil))
                    XCTAssertTrue(hit === button, "Tab must receive clicks in state \(icon), flashing: \(flashing)")
                    let close = button.closeButton
                    let closePoint = close.convert(NSPoint(x: close.bounds.midX, y: close.bounds.midY), to: root.superview)
                    XCTAssertTrue(root.hitTest(closePoint) === close, "Close button must remain reachable")
                    let down = try XCTUnwrap(NSEvent.mouseEvent(with: .leftMouseDown, location: point, modifierFlags: [], timestamp: 0,
                        windowNumber: workspace.window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1))
                    // Route through the window hierarchy, without requiring the test app to be active.
                    if hit === button { hit?.mouseDown(with: down) }
                    XCTAssertEqual(workspace.viewSelection, terminal.id)
                    XCTAssertTrue(workspace.window.firstResponder === workspace.utilityTerminalView(for: terminal.id))
                }
            }
        }
    }

    func testCompactTerminalTabUsesFullTitleWhenItFitsAndOrdinalWhenItDoesNot() throws {
        let button = WorkspaceCompactTerminalButton(terminalID: "terminal:example#2")
        button.title = "Terminal 2"
        button.shortTitle = "T2"
        button.detail = "swift build"
        button.frame = NSRect(x: 0, y: 0, width: 46, height: 34)
        button.layoutSubtreeIfNeeded()
        XCTAssertEqual(button.displayedTitle, "T2")
        XCTAssertEqual(button.displayedDetail, "swift build")
        XCTAssertFalse(button.closeButton.isHidden)
        let detail = try XCTUnwrap(button.subviews.compactMap { $0 as? NSTextField }.first { $0.stringValue == "swift build" })
        XCTAssertFalse(detail.frame.intersects(button.closeButton.frame))
        XCTAssertGreaterThan(detail.frame.maxX, button.closeButton.frame.minX,
                             "the command line uses the space beneath the close button")
        button.frame.size.width = 150
        button.layoutSubtreeIfNeeded()
        XCTAssertEqual(button.displayedTitle, "Terminal 2")
    }

    private func index(of key: AttentionCycler.Key) throws -> Int {
        try XCTUnwrap(workspace.items.firstIndex { item in
            if case let .row(row) = item { return row.key == key }
            return false
        })
    }

    func testSidebarFontsSurviveSourceListStyling() async throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.window.orderFront(nil)
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        workspace.window.displayIfNeeded()
        try await Task.sleep(for: .milliseconds(200))
        for (index, item) in workspace.items.enumerated() {
            switch item {
            case .section, .group:
                let cell = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: index, makeIfNecessary: true) as? WorkspaceGroupCellView)
                XCTAssertEqual(cell.nameLabel.font?.pointSize, 13, "Heading: \(cell.nameLabel.stringValue)")
                let drawnFont = cell.nameLabel.attributedStringValue.attribute(.font, at: 0, effectiveRange: nil) as? NSFont
                XCTAssertEqual(drawnFont?.pointSize, 13, "Rendered heading: \(cell.nameLabel.stringValue)")
            case .row:
                let cell = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: index, makeIfNecessary: true) as? WorkspaceRowCellView)
                let drawnFont = cell.titleLabel.attributedStringValue.attribute(.font, at: 0, effectiveRange: nil) as? NSFont
                XCTAssertEqual(drawnFont?.pointSize, 12, "Rendered session: \(cell.titleLabel.stringValue)")
            case .overview:
                let cell = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: index, makeIfNecessary: true) as? WorkspaceOverviewCellView)
                XCTAssertFalse(cell.chips.isEmpty)
            case .navigation, .view: break
            }
        }
    }

    func testSidebarHoldsTargetsDuringHoverAndMenuWhileUpdatingAttention() throws {
        var live = sessions
        app.store.apply(snapshot: F.snapshot(live))
        let key = AttentionCycler.Key.embedded(F.runtimeID(2))
        let before = try index(of: key)
        let entered = try XCTUnwrap(NSEvent.enterExitEvent(with: .mouseEntered, location: .zero,
            modifierFlags: [], timestamp: 0, windowNumber: workspace.window.windowNumber,
            context: nil, eventNumber: 0, trackingNumber: 0, userData: nil))
        workspace.sidebar.mouseEntered(with: entered)
        live[1].unseen = true
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertEqual(try index(of: key), before)
        guard case let .row(updated) = workspace.items[before] else { return XCTFail("Lost held row") }
        XCTAssertTrue(updated.needsAttention, "Held position must still show current attention")
        XCTAssertEqual(workspace.sessionKey(of: key), "1", "hover keeps the digit with the physical row")
        XCTAssertEqual(workspace.sessionName(at: 0), updated.displayName)
        workspace.menuWillOpen(NSMenu())
        workspace.sidebar.mouseExited(with: entered)
        XCTAssertEqual(try index(of: key), before, "Open menu must retain its target")
        workspace.menuDidClose(NSMenu())
        let promoted = try index(of: key)
        guard case let .group(group) = workspace.items[promoted - 1] else { return XCTFail("Missing attention group") }
        XCTAssertTrue(group.needsAttention, "Promotion should apply when interaction ends")
    }

    func testWorkingSessionLeavesAttentionAfterBriefHoverHold() async throws {
        var live = sessions
        live[1].unseen = true
        app.store.apply(snapshot: F.snapshot(live))
        let key = AttentionCycler.Key.embedded(F.runtimeID(2))
        let before = try index(of: key)
        let entered = try XCTUnwrap(NSEvent.enterExitEvent(with: .mouseEntered, location: .zero,
            modifierFlags: [], timestamp: 0, windowNumber: workspace.window.windowNumber,
            context: nil, eventNumber: 0, trackingNumber: 0, userData: nil))
        workspace.sidebar.mouseEntered(with: entered)

        live[1].iconState = "bolt"
        live[1].unseen = false
        live[1].attention = false
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertEqual(try index(of: key), before, "Hover briefly keeps the click target in place")
        XCTAssertFalse(try XCTUnwrap(workspace.state.row(key)).needsAttention)

        try await Task.sleep(for: .milliseconds(700))
        let moved = try index(of: key)
        let group = try XCTUnwrap(workspace.items[..<moved].last { if case .group = $0 { return true }; return false })
        guard case let .group(working) = group else { return XCTFail("Missing working group") }
        XCTAssertEqual(working.stateSection, .working)
        XCTAssertFalse(working.needsAttention)
    }

    func testHeldLinesNameTheirProjectByWhereTheyAreListed() throws {
        var live = sessions
        live[1].unseen = true
        app.store.apply(snapshot: F.snapshot(live))
        let key = AttentionCycler.Key.embedded(F.runtimeID(2))
        func title() throws -> String {
            let line = try index(of: key)
            let cell = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: line, makeIfNecessary: true) as? WorkspaceRowCellView)
            return cell.titleLabel.stringValue
        }
        XCTAssertEqual(try title(), "api — personal")
        let entered = try XCTUnwrap(NSEvent.enterExitEvent(with: .mouseEntered, location: .zero,
            modifierFlags: [], timestamp: 0, windowNumber: workspace.window.windowNumber,
            context: nil, eventNumber: 0, trackingNumber: 0, userData: nil))
        workspace.sidebar.mouseEntered(with: entered)
        live[1].unseen = false
        app.store.apply(snapshot: F.snapshot(live))
        guard case let .row(held) = workspace.items[try index(of: key)] else { return XCTFail("Lost held row") }
        XCTAssertFalse(held.needsAttention)
        XCTAssertEqual(try title(), "api — personal", "a line held in Needs attention still names its project")
        workspace.sidebar.mouseExited(with: entered)
        XCTAssertEqual(try title(), "personal", "back under its project it needs no project name")
    }

    func testNewSessionsAppearWhilePointerRemainsInSidebar() throws {
        app.store.apply(snapshot: F.snapshot([]))
        let entered = try XCTUnwrap(NSEvent.enterExitEvent(with: .mouseEntered, location: .zero,
            modifierFlags: [], timestamp: 0, windowNumber: workspace.window.windowNumber,
            context: nil, eventNumber: 0, trackingNumber: 0, userData: nil))
        workspace.sidebar.mouseEntered(with: entered)
        app.store.apply(snapshot: F.snapshot([sessions[0]]))
        _ = try index(of: .embedded(F.runtimeID(1)))
        app.store.apply(snapshot: F.snapshot(sessions))
        _ = try index(of: .embedded(F.runtimeID(2)))
        XCTAssertTrue(workspace.sidebar.pointerInside)
    }

    func testEndedHeldSessionCannotBeOpenedOrActedOn() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let key = AttentionCycler.Key.embedded(F.runtimeID(2))
        let before = try index(of: key)
        workspace.menuWillOpen(NSMenu())
        app.store.apply(snapshot: F.snapshot([sessions[0], sessions[2]]))
        XCTAssertEqual(try index(of: key), before)
        XCTAssertTrue(workspace.contextMenuItems(forRow: before).isEmpty)
        let made = app.made
        workspace.focusItem(at: before)
        XCTAssertEqual(app.made, made)
        workspace.menuDidClose(NSMenu())
        XCTAssertFalse(workspace.items.contains { if case let .row(row) = $0 { return row.key == key }; return false })
    }

    func testAttentionSnapshotMovesRowsAndAcknowledgmentRestoresThemWithoutChangingSelection() throws {
        var live = sessions
        app.store.apply(snapshot: F.snapshot(live))
        let selected = AttentionCycler.Key.embedded(F.runtimeID(1))
        let waiting = AttentionCycler.Key.embedded(F.runtimeID(2))
        workspace.select(waiting)
        workspace.openTerminalBesideSession()
        let companion = try XCTUnwrap(workspace.views.terminals(beside: F.runtimeID(2)).first)
        workspace.select(selected)
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        let made = app.made
        // Both finished unseen: under Needs attention they keep the order
        // of their sections and projects.
        live[1].unseen = true
        live[2].unseen = true
        app.store.apply(snapshot: F.snapshot(live))

        XCTAssertEqual(workspace.state.rows.map(\.key), [waiting, AttentionCycler.key(live[2]), selected])
        XCTAssertEqual(workspace.sessionKey(of: waiting), "1")
        XCTAssertEqual(workspace.items[try index(of: waiting) + 1], .view(companion))
        XCTAssertEqual(workspace.state.selection, selected)
        XCTAssertEqual(workspace.sidebar.selectedRow, try index(of: selected))
        XCTAssertTrue(workspace.terminalView(for: F.runtimeID(1)) === terminal)
        XCTAssertEqual(app.made, made, "reordering must not attach or acknowledge sessions")
        let cell = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: try index(of: waiting)) as? WorkspaceRowCellView)
        XCTAssertEqual(cell.titleLabel.stringValue, "api — personal")
        let header = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: 4) as? WorkspaceGroupCellView)
        XCTAssertEqual(header.nameLabel.stringValue, "Needs attention")
        XCTAssertEqual(header.countLabel.stringValue, "2")
        XCTAssertTrue(header.disclosureLabel.isHidden)
        XCTAssertTrue(workspace.contextMenuItems(forRow: 3).isEmpty, "the overview has no menu")
        XCTAssertTrue(workspace.contextMenuItems(forRow: 4).isEmpty)
        XCTAssertEqual(workspace.dropSlots(forItemAt: try index(of: waiting)), [])
        workspace.focusItem(at: 4)
        XCTAssertTrue(app.orders.isEmpty, "attention cannot be collapsed")

        live[1].unseen = false
        live[2].unseen = false
        live[2].iconState = "bolt"
        live[2].attention = false
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertFalse(workspace.state.groups.contains(where: \.needsAttention))
        XCTAssertEqual(workspace.state.rows.map(\.project), ["api", "api", "web"])
        XCTAssertEqual(workspace.items[try index(of: waiting) + 1], .view(companion))
        XCTAssertEqual(workspace.state.selection, selected)
        XCTAssertEqual(workspace.sidebar.selectedRow, try index(of: selected))
        XCTAssertEqual(app.made, made)
    }

    func testDraggingKeepsTheSavedPositionOfPromotedSiblings() throws {
        // Only a project's rows are promoted while they can be dragged: an
        // open Pinned group keeps its waiting rows.
        // All three finished, so they share a state section once seen.
        var live = (1...3).map { F.tmux($0, project: "api", profile: String($0), icon: "stop") }
        let keys = (1...3).map { AttentionCycler.Key.embedded(F.runtimeID($0)) }
        live[1].unseen = true
        live[1].attention = true
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertTrue(workspace.moveItem(at: try index(of: keys[2]), to: try index(of: keys[0])))
        let saved = try XCTUnwrap(app.orders.last)
        XCTAssertEqual(saved.projects["api"], [keys[2], keys[1], keys[0]].map(WorkspaceOrder.token))
        live[1].unseen = false
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertEqual(workspace.state.rows.map(\.key), [keys[2], keys[1], keys[0]])
    }

    func testSidebarListsProjectsThenTheirSessions() throws {
        // The workspace's own views are always listed, above the projects.
        XCTAssertEqual(workspace.items, [.section("Workspace"), .view(.dashboard), .view(.overview)])
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Connecting to the daemon…")

        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(workspace.sidebar.numberOfRows, 10)
        guard case let .overview(counts) = workspace.items[3], case let .group(done) = workspace.items[4],
              case let .group(api) = workspace.items[5], case let .group(web) = workspace.items[8] else {
            return XCTFail("expected the overview, then a project line before its sessions: \(workspace.items)")
        }
        XCTAssertEqual(counts.map(\.title), ["Done"], "the overview counts the sections listed below it")
        XCTAssertFalse(workspace.tableView(workspace.sidebar, isGroupRow: 3))
        XCTAssertFalse(workspace.tableView(workspace.sidebar, shouldSelectRow: 3), "the overview is not a session")
        XCTAssertTrue(done.isSectionHeading && done.stateSection == .done, "the projects are listed under their state")
        XCTAssertEqual([api.project, web.project], ["api", "web"])
        XCTAssertTrue(workspace.tableView(workspace.sidebar, isGroupRow: 4))
        XCTAssertFalse(workspace.tableView(workspace.sidebar, shouldSelectRow: 4), "a section line is not a session")
        XCTAssertTrue(workspace.tableView(workspace.sidebar, isGroupRow: 5))
        XCTAssertFalse(workspace.tableView(workspace.sidebar, shouldSelectRow: 5), "a project line is not a session")
        XCTAssertTrue(workspace.tableView(workspace.sidebar, shouldSelectRow: 6))

        let section = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: 4) as? WorkspaceGroupCellView)
        XCTAssertEqual(section.nameLabel.stringValue, "Done")
        XCTAssertEqual(section.countLabel.stringValue, "3", "a section counts its sessions")
        XCTAssertFalse(section.disclosureLabel.isHidden, "and collapses")
        let header = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: 5) as? WorkspaceGroupCellView)
        XCTAssertEqual(header.nameLabel.stringValue, "api")
        XCTAssertEqual(header.countLabel.stringValue, "1", "one of the project's sessions waits")
        let cell = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: 6) as? WorkspaceRowCellView)
        XCTAssertEqual(cell.titleLabel.stringValue, "personal")
        XCTAssertEqual(cell.glyphLabel.stringValue, "✓")
        let webCell = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: 9) as? WorkspaceRowCellView)
        XCTAssertEqual(webCell.titleLabel.stringValue, "work")
        XCTAssertFalse(webCell.detailLabel.stringValue.isEmpty)

        XCTAssertNil(workspace.state.selection, "nothing is selected until the operator selects")
        XCTAssertEqual(app.made, [], "listing a session attaches nothing")
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "No session selected")
    }

    func testSessionColorIsAMarkerAndSelectionUsesTheSystemAccent() throws {
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
        XCTAssertEqual(cell.titleLabel.textColor, .labelColor, "text uses the system color on a neutral background")
        let (plainView, plainCell) = try line(personal)
        XCTAssertNil(plainView.tabColor)
        XCTAssertEqual(plainCell.titleLabel.textColor, .labelColor)
        for own in [1, 2] {
            let ownView = try XCTUnwrap(workspace.sidebar.rowView(atRow: own, makeIfNecessary: true) as? WorkspaceRowView)
            XCTAssertNil(ownView.tabColor, "the Dashboard and the Overview are not a session's and take no color")
        }

        // The selected line shows the list's selection, as the active tab
        // shows the terminal's own colors.
        workspace.select(.embedded(F.runtimeID(1)))
        (rowView, cell) = try line(work)
        XCTAssertNotNil(rowView.tabColor)
        XCTAssertTrue(rowView.showsTabColor)
        rowView.layoutSubtreeIfNeeded()
        XCTAssertTrue(rowView.subviews.last === rowView.selectionBorder, "the border is over the line")
        XCTAssertFalse(rowView.selectionBorder.isHidden, "the selected line has a border")
        XCTAssertEqual(rowView.selectionBorder.color, .controlAccentColor, "selection is independent of profile color")
        let unselected = try line(personal).0
        unselected.layoutSubtreeIfNeeded()
        XCTAssertTrue(unselected.selectionBorder.isHidden)
        XCTAssertEqual(cell.titleLabel.textColor, .labelColor)
        workspace.select(.embedded(F.runtimeID(2)))
        (rowView, cell) = try line(work)
        XCTAssertTrue(rowView.showsTabColor)
        XCTAssertEqual(cell.titleLabel.textColor, .labelColor)

        // Another pick repaints the line; a pick taken back leaves none.
        colored[0].tabColor = "#c8c848"
        app.store.apply(snapshot: F.snapshot(colored))
        (rowView, cell) = try line(work)
        XCTAssertEqual(rowView.tabColor, NSColor(srgbRed: 0xc8 / 255.0, green: 0xc8 / 255.0, blue: 0x48 / 255.0, alpha: 1))
        XCTAssertEqual(cell.titleLabel.textColor, .labelColor)
        colored[0].tabColor = ""
        app.store.apply(snapshot: F.snapshot(colored))
        (rowView, cell) = try line(work)
        XCTAssertNil(rowView.tabColor)
        XCTAssertEqual(cell.titleLabel.textColor, .labelColor)
    }

    func testASessionShowsItsHarnessIconAheadOfItsProfile() throws {
        var badged = sessions
        badged[0].harnessIcon = "🦊"
        app.store.apply(snapshot: F.snapshot(badged))

        func line(_ key: AttentionCycler.Key) throws -> WorkspaceRowCellView {
            let rowView = try XCTUnwrap(workspace.sidebar.rowView(atRow: try index(of: key), makeIfNecessary: true) as? WorkspaceRowView)
            return try XCTUnwrap(rowView.view(atColumn: 0) as? WorkspaceRowCellView)
        }
        XCTAssertEqual(try line(.embedded(F.runtimeID(1))).titleLabel.stringValue, "🦊 work")
        XCTAssertEqual(try line(.embedded(F.runtimeID(2))).titleLabel.stringValue, "personal", "a session with no icon looks as it did")

        // The icon picker cleared it: the next snapshot takes it off the line.
        badged[0].harnessIcon = ""
        app.store.apply(snapshot: F.snapshot(badged))
        XCTAssertEqual(try line(.embedded(F.runtimeID(1))).titleLabel.stringValue, "work")
    }

    func testANamedSessionShowsItsNameAboveItsProfile() throws {
        var named = sessions
        named[0].name = "billing fix"
        named[0].tabColor = "#c8c848"
        app.store.apply(snapshot: F.snapshot(named))
        let work = try index(of: .embedded(F.runtimeID(1)))
        let personal = try index(of: .embedded(F.runtimeID(2)))

        func line(_ index: Int) throws -> WorkspaceRowCellView {
            let rowView = try XCTUnwrap(workspace.sidebar.rowView(atRow: index, makeIfNecessary: true) as? WorkspaceRowView)
            return try XCTUnwrap(rowView.view(atColumn: 0) as? WorkspaceRowCellView)
        }
        var cell = try line(work)
        XCTAssertFalse(cell.nameLabel.isHidden)
        XCTAssertEqual(cell.nameLabel.stringValue, "billing fix")
        XCTAssertEqual(cell.titleLabel.stringValue, "work", "the profile stays on its own line")
        XCTAssertTrue(cell.nameLabel.font?.fontDescriptor.symbolicTraits.contains(.bold) == true)
        XCTAssertEqual(cell.nameLabel.textColor, .labelColor, "name follows the current appearance")
        XCTAssertTrue((cell.nameLabel.superview as? NSStackView)?.arrangedSubviews.first === cell.nameLabel, "the name is the first line")
        XCTAssertGreaterThan(workspace.sidebar.rect(ofRow: work).height, workspace.sidebar.rect(ofRow: personal).height,
                             "the named line is taller by its first line")

        // A session with no name looks as it did.
        let plain = try line(personal)
        XCTAssertTrue(plain.nameLabel.isHidden)
        XCTAssertEqual(plain.titleLabel.stringValue, "personal")
        XCTAssertEqual(workspace.sidebar.rect(ofRow: personal).height, 38)

        // Selected, the line is on the list's own selection.
        workspace.select(.embedded(F.runtimeID(1)))
        cell = try line(work)
        XCTAssertEqual(cell.nameLabel.textColor, .labelColor)

        // The name taken back leaves the line as it was.
        named[0].name = ""
        app.store.apply(snapshot: F.snapshot(named))
        cell = try line(work)
        XCTAssertTrue(cell.nameLabel.isHidden)
        XCTAssertEqual(workspace.sidebar.rect(ofRow: work).height, 38)
    }

    func testNamingASessionFromItsMenuAndFromTheKeyboard() throws {
        var named = sessions
        named[1].name = "old name"
        app.store.apply(snapshot: F.snapshot(named))
        let work = try row("api", "work")
        let personal = try row("api", "personal")
        var asked: [(WorkspaceRow, @MainActor (String?) -> Void)] = []
        workspace.askName = { row, answer in asked.append((row, answer)) }
        var problems: [String] = []
        workspace.reportNameProblem = { _, message in problems.append(message) }

        // The right-click menu of a session's line names that session,
        // wherever the pane is.
        workspace.activate(work)
        var items = workspace.contextMenuItems(forRow: try index(of: personal.key))
        XCTAssertEqual(items.map(\.title), ["Name Session…", "Color…", "Icon…", "Mute", "Pin to Top", "", "New Terminal Below",
                                            "New Session Below…", "Continue with Another Agent…", "Copy Runtime Session ID", "", "End Session…"])
        _ = try XCTUnwrap(items[0].target).perform(try XCTUnwrap(items[0].action), with: items[0])
        XCTAssertEqual(asked.map(\.0), [personal])
        XCTAssertTrue(app.named.isEmpty, "nothing is written before the answer")
        asked[0].1("  release notes ")
        XCTAssertEqual(app.named.map(\.id), [F.runtimeID(2)])
        XCTAssertEqual(app.named.map(\.name), ["release notes"])

        // Cancelled, or left as it was, it writes nothing; empty removes it.
        workspace.nameSession(personal.key)
        asked[1].1(nil)
        workspace.nameSession(personal.key)
        asked[2].1("old name")
        XCTAssertEqual(app.named.count, 1)
        workspace.nameSession(personal.key)
        asked[3].1("")
        XCTAssertEqual(app.named.map(\.name), ["release notes", ""])

        // The shortcut names the session the pane is on.
        XCTAssertTrue(workspace.nameShownSession())
        XCTAssertEqual(asked.last?.0, work)
        app.nameProblem = "tmux backend unavailable"
        asked[4].1("x")
        XCTAssertEqual(problems, ["tmux backend unavailable"])

        let web = try row("web", "work")
        items = workspace.contextMenuItems(forRow: try index(of: web.key))
        XCTAssertFalse(items.isEmpty)
        XCTAssertTrue(workspace.contextMenuItems(forRow: 0).isEmpty)
        XCTAssertEqual(workspace.contextMenuItems(forRow: -1).first?.title, "Go to…", "the list's empty space has its own menu")
        workspace.select(web.key)
        XCTAssertTrue(workspace.nameShownSession())
        XCTAssertEqual(asked.count, 6)
    }

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
        _ = try XCTUnwrap(items[0].target).perform(try XCTUnwrap(items[0].action), with: items[0])
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
        _ = try XCTUnwrap(items[4].target).perform(try XCTUnwrap(items[4].action), with: items[4])
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
        XCTAssertEqual(items[4].title, "Unpin")
        _ = try XCTUnwrap(items[4].target).perform(try XCTUnwrap(items[4].action), with: items[4])
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

    func testStateChangesFlashWithTheirSectionColorAfterReordering() throws {
        var live = sessions
        var pinned = F.tmux(6, project: "api", icon: "stop")
        pinned.pinned = true
        live += [pinned, F.tmux(7, project: "web", icon: "key", attention: true)]
        app.store.apply(snapshot: F.snapshot(live))
        let keys = workspace.state.rows.map(\.key)
        func rowView(_ key: AttentionCycler.Key) throws -> WorkspaceRowView {
            try XCTUnwrap(workspace.sidebar.rowView(atRow: index(of: key), makeIfNecessary: true) as? WorkspaceRowView)
        }
        for key in keys { XCTAssertFalse(try rowView(key).isFlashing, "first sightings do not flash") }

        live[0].name = "Renamed"
        live[0].presentationChangedAtMS = 3000
        live.append(F.tmux(5, project: "api", icon: "bolt"))
        app.store.apply(snapshot: F.snapshot(live))
        for row in workspace.state.rows {
            XCTAssertFalse(try rowView(row.key).isFlashing, "metadata and new sessions do not flash")
        }

        let shown = try row("api", "personal")
        workspace.activate(shown)
        let responder = workspace.window.firstResponder
        let position = workspace.sidebar.enclosingScrollView?.contentView.bounds.origin
        // Both sessions move from Done to Working.
        live[0].iconState = "bolt"
        live[2].iconState = "bolt"
        live[3].iconState = "bolt" // Still in Pinned: purple, not Working blue.
        live[4].iconState = "question" // Still in Needs attention: amber.
        app.store.apply(snapshot: F.snapshot(live))
        for row in workspace.state.rows {
            let view = try rowView(row.key)
            let changed = [1, 4, 6, 7].contains { row.key == .embedded(F.runtimeID($0)) }
            XCTAssertEqual(view.isFlashing, changed)
            if changed {
                let layer = try XCTUnwrap(view.subviews.first { $0.layer?.animation(forKey: WorkspaceRowView.flashKey) != nil }?.layer)
                let animation = try XCTUnwrap(layer.animation(forKey: WorkspaceRowView.flashKey) as? CAKeyframeAnimation)
                XCTAssertEqual(animation.duration, 0.35, accuracy: 0.001)
                XCTAssertEqual(animation.values?.count, 3, "One brief state-change highlight")
                let band: WorkspaceSidebarBand = row.key == .embedded(F.runtimeID(6)) ? .pinned
                    : row.key == .embedded(F.runtimeID(7)) ? .attention : .running
                view.effectiveAppearance.performAsCurrentDrawingAppearance {
                    XCTAssertEqual(layer.backgroundColor, band.color.cgColor)
                }
            }
        }
        XCTAssertEqual(workspace.state.selection, shown.key)
        XCTAssertTrue(workspace.window.firstResponder === responder)
        XCTAssertEqual(workspace.sidebar.enclosingScrollView?.contentView.bounds.origin, position)
    }

    func testStateChangesLeaveCollapsedGroupsAndSidebarClosed() throws {
        var live = sessions
        app.store.apply(snapshot: F.snapshot(live))
        workspace.setCollapsed(project: "api", pinned: false, true)
        workspace.setSidebarCollapsed(true)
        live[0].iconState = "bolt"
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertTrue(workspace.isSidebarCollapsed)
        XCTAssertFalse(workspace.items.contains { if case let .row(row) = $0 { return row.project == "api" }; return false })
        workspace.setCollapsed(project: "api", pinned: false, false)
        let line = try index(of: .embedded(F.runtimeID(1)))
        let view = try XCTUnwrap(workspace.sidebar.rowView(atRow: line, makeIfNecessary: true) as? WorkspaceRowView)
        XCTAssertFalse(view.isFlashing, "expanding does not replay an old transition")
    }

    /// ⌘J finds the shown session in the list: a hidden sidebar comes back,
    /// a collapsed project opens, and the line flashes, while the keyboard
    /// stays with the terminal.
    func testRevealInSidebarShowsAndFlashesTheShownLine() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let shown = try row("api", "work")
        workspace.activate(shown)
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        workspace.window.makeFirstResponder(terminal)
        workspace.setCollapsed(project: "api", pinned: false, true)
        workspace.setSidebarCollapsed(true)
        XCTAssertFalse(workspace.items.contains { if case let .row(row) = $0 { return row.key == shown.key }; return false },
                       "the collapse took the line off the list")

        XCTAssertTrue(workspace.revealInSidebar())
        XCTAssertFalse(workspace.isSidebarCollapsed)
        let line = try index(of: shown.key)
        XCTAssertEqual(workspace.sidebar.selectedRow, line)
        let rowView = try XCTUnwrap(workspace.sidebar.rowView(atRow: line, makeIfNecessary: false) as? WorkspaceRowView)
        XCTAssertTrue(rowView.isFlashing)
        let flashLayer = try XCTUnwrap(rowView.subviews.first { $0.layer?.animation(forKey: WorkspaceRowView.flashKey) != nil }?.layer)
        let animation = try XCTUnwrap(flashLayer.animation(forKey: WorkspaceRowView.flashKey))
        let pulses = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion ? 1 : 2
        XCTAssertEqual(animation.duration, 0.4 * Double(pulses), accuracy: 0.001)
        rowView.effectiveAppearance.performAsCurrentDrawingAppearance {
            XCTAssertEqual(flashLayer.backgroundColor, WorkspaceSidebarBand.stopped.color.cgColor)
        }
        XCTAssertTrue(workspace.window.firstResponder === terminal, "revealing does not take the keyboard")
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
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
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(4)))
        XCTAssertEqual(app.external, [], "stepping between sessions stays in the workspace")
        workspace.selectNeighbor(1)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        workspace.selectNeighbor(-1)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(4)))
    }

    func testADigitGoesToTheSessionAtThatPlaceInTheSidebar() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        // Sidebar order: api/personal, api/work, web/work.
        workspace.selectSession(at: 1)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(1)))
        workspace.selectSession(at: 0)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        // The digit selects the third session in the workspace.
        workspace.selectSession(at: 2)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(4)))
        XCTAssertEqual(app.external, [])
        // No session is listed there: nothing moves.
        workspace.selectSession(at: 3)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(4)))
        // A view is not a place among the sessions.
        workspace.showView(WorkspaceUtilityView.dashboard.id)
        workspace.selectSession(at: 0)
        XCTAssertNil(workspace.viewSelection)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
    }

    func testASessionLineShowsTheDigitThatGoesToIt() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        // Sidebar order: api/personal, api/work, web/work.
        let keys = try workspace.state.rows.map { try cell($0.key).keyLabel.stringValue }
        XCTAssertEqual(keys, ["⌘1", "⌘2", "⌘3"])
        // A view is not a place among the sessions.
        let dashboard = try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: 1) as? WorkspaceRowCellView)
        XCTAssertEqual(dashboard.keyLabel.stringValue, "")

        // The number is the place, not the session: it follows a drag.
        let api = workspace.state.rows.filter { $0.project == "api" }
        XCTAssertTrue(workspace.moveItem(at: try index(of: api[1].key), to: try index(of: api[0].key)))
        XCTAssertEqual(try cell(api[0].key).keyLabel.stringValue, "⌘2")
        XCTAssertEqual(try cell(api[1].key).keyLabel.stringValue, "⌘1")

        // The tenth is ⌘0, and past it a session has no key.
        XCTAssertEqual(WorkspaceMenu.sessionKeys.count, 10)
        XCTAssertNil(workspace.sessionKey(of: .embedded("rs-not-listed")))
    }

    func testDigitsFollowHeldRowsThroughStateChangesAndUseZeroForTheTenth() throws {
        var live = (1...11).map { F.tmux($0, project: String(format: "project-%02d", $0), icon: "bolt") }
        app.store.apply(snapshot: F.snapshot(live))
        let original = workspace.items.compactMap { item -> AttentionCycler.Key? in
            if case let .row(row) = item { return row.key }
            return nil
        }
        let entered = try XCTUnwrap(NSEvent.enterExitEvent(with: .mouseEntered, location: .zero,
            modifierFlags: [], timestamp: 0, windowNumber: workspace.window.windowNumber,
            context: nil, eventNumber: 0, trackingNumber: 0, userData: nil))
        workspace.sidebar.mouseEntered(with: entered)
        live[10].unseen = true
        live[10].iconState = "stop"
        live[5].iconState = "stop"
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertNotEqual(workspace.state.rows.map(\.key), original, "the live state has reordered")

        func checkDisplayedShortcuts() throws {
            let visible = workspace.items.compactMap { item -> WorkspaceRow? in
                if case let .row(row) = item { return row }
                return nil
            }
            XCTAssertEqual(try visible.map { try cell($0.key).keyLabel.stringValue },
                           ["⌘1", "⌘2", "⌘3", "⌘4", "⌘5", "⌘6", "⌘7", "⌘8", "⌘9", "⌘0", ""])
            for (index, row) in visible.prefix(10).enumerated() {
                XCTAssertEqual(workspace.sessionName(at: index), row.displayName)
                workspace.selectSession(at: index)
                XCTAssertEqual(workspace.state.selection, row.key, "the digit must open the row carrying its label")
            }
            XCTAssertNil(workspace.sessionKey(of: visible[10].key))
        }
        XCTAssertEqual(workspace.items.compactMap { item -> AttentionCycler.Key? in
            if case let .row(row) = item { return row.key }
            return nil
        }, original)
        try checkDisplayedShortcuts()
        workspace.menuWillOpen(NSMenu())
        workspace.sidebar.mouseExited(with: entered)
        try checkDisplayedShortcuts()
        workspace.dragged = .session(original[0])
        workspace.menuDidClose(NSMenu())
        try checkDisplayedShortcuts()
        workspace.dragged = nil
        workspace.reloadSidebar()
        XCTAssertEqual(workspace.sessionKey(of: .embedded(F.runtimeID(11))), "1", "released rows follow promotion")
        try checkDisplayedShortcuts()
    }

    func testCollapsedSessionsDoNotLeaveGapsInVisibleDigits() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let web = try row("web", "work").key
        let line = try cell(web)
        XCTAssertEqual(line.keyLabel.stringValue, "⌘3")
        workspace.setCollapsed(project: "api", pinned: false, true)
        XCTAssertEqual(try cell(web).keyLabel.stringValue, "⌘1")

        // Starting a hidden session must not consume a visible digit.
        app.store.apply(snapshot: F.snapshot(sessions + [F.tmux(3, project: "api", profile: "spare", icon: "stop")]))
        XCTAssertEqual(workspace.sessionKey(of: web), "1")
        XCTAssertEqual(try cell(web).keyLabel.stringValue, "⌘1")
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
        XCTAssertEqual(workspace.feedLabel.stringValue, "Daemon not running")
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

    func testTheFeedNoticeWrapsAndSaysItAllInItsTooltip() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertNil(workspace.feedLabel.toolTip)
        XCTAssertEqual(workspace.feedLabel.maximumNumberOfLines, 2)
        XCTAssertFalse(workspace.feedLabel.usesSingleLineMode)
        app.store.apply(.disconnected(.heartbeatLost, retryIn: 0.5))
        XCTAssertEqual(workspace.feedLabel.toolTip, "Reconnecting… (the daemon is not answering)")
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertNil(workspace.feedLabel.toolTip)
    }

    func testNeedsAttentionAndPinnedTintOnlyTheirHeaders() throws {
        typealias Item = WorkspaceWindowController.Item
        let view = WorkspaceUtilityView(kind: .shell, target: "/repo", title: "Shell")
        let items: [Item] = [
            .section("Views"), .view(view),
            .group(WorkspaceGroup(project: WorkspaceSidebar.pinnedSection, rows: [], pinned: true)), .view(view), .view(view),
            .group(WorkspaceGroup(project: WorkspaceSidebar.attentionSection, rows: [], needsAttention: true)), .view(view),
            .group(WorkspaceGroup(project: "api", rows: [])), .view(view),
            .group(WorkspaceGroup(project: WorkspaceSidebar.attentionSection, rows: [], needsAttention: true)),
        ]
        let bands = Item.bands(in: items)
        XCTAssertEqual(bands.map(\.lines), [2..<3, 5..<6, 9..<10])
        XCTAssertEqual(bands.map(\.band), [.pinned, .attention, .attention])
        XCTAssertTrue(Item.bands(in: [.section("Views"), .group(WorkspaceGroup(project: "api", rows: []))]).isEmpty)
    }

    func testStateHeaderColorsLeaveProjectsSessionsAndTerminalsNeutral() throws {
        typealias Item = WorkspaceWindowController.Item
        func row(_ id: String, _ state: String) -> WorkspaceRow {
            var row = WorkspaceRow(key: .embedded(id), target: .embedded(runtimeSessionID: id), project: "api", label: "work",
                                   detail: "", glyph: "", attention: false, working: false, muted: false, toolTip: "")
            row.iconState = state
            return row
        }
        let beside = WorkspaceUtilityView(kind: .terminal, target: "rs-1", title: "Terminal")
        let items: [Item] = [
            .group(WorkspaceGroup(project: WorkspaceSidebar.pinnedSection, rows: [], pinned: true)), .row(row("p", "stop")),
            .group(WorkspaceGroup(project: "api", rows: [])),
            .row(row("rs-1", "stop")), .view(beside),
            .row(row("rs-2", "bolt")), .row(row("rs-3", "subagent")), .row(row("rs-4", "compact")),
            .row(row("rs-5", "")), .row(row("rs-6", "key")), .row(row("rs-7", "question")),
            .group(WorkspaceGroup(project: "Waiting", rows: [], stateSection: .waiting, isSectionHeading: true)),
            .group(WorkspaceGroup(project: "Done", rows: [], stateSection: .done, isSectionHeading: true)),
            .group(WorkspaceGroup(project: "Working", rows: [], stateSection: .working, isSectionHeading: true)),
            .group(WorkspaceGroup(project: "Idle", rows: [], collapsed: true, stateSection: .idle, isSectionHeading: true)),
        ]
        let bands = Item.bands(in: items)
        XCTAssertEqual(bands.map(\.lines), [0..<1, 11..<12, 12..<13, 13..<14, 14..<15])
        XCTAssertEqual(bands.map(\.band), [.pinned, .attention, .stopped, .running, .idle])
        XCTAssertEqual(WorkspaceSidebarBand(state: "warning"), .attention)
        XCTAssertEqual(WorkspaceSidebarBand(state: "error"), .attention)
    }

    func testOnlyTheSidebarLinesThatChangedAreDrawnAgain() throws {
        typealias Item = WorkspaceWindowController.Item
        app.store.apply(snapshot: F.snapshot(sessions))
        let items = workspace.items
        XCTAssertEqual(WorkspaceWindowController.changedLines(from: items, to: items), IndexSet())

        // Muted in place: a new state would move the line to another section.
        var changed = sessions
        changed[1].muted = true
        app.store.apply(snapshot: F.snapshot(changed))
        let moved = try index(of: .embedded(F.runtimeID(2)))
        XCTAssertEqual(WorkspaceWindowController.changedLines(from: items, to: workspace.items).map(Array.init),
                       items.indices.filter { items[$0] != workspace.items[$0] })
        XCTAssertTrue(WorkspaceWindowController.changedLines(from: items, to: workspace.items)?.contains(moved) ?? false)

        // Another order, another line, or a name, which makes a line taller: all of it again.
        XCTAssertNil(WorkspaceWindowController.changedLines(from: items, to: items.reversed()))
        XCTAssertNil(WorkspaceWindowController.changedLines(from: items, to: Array(items.dropLast())))
        var named = changed
        named[1].name = "migrations"
        app.store.apply(snapshot: F.snapshot(named))
        XCTAssertNil(WorkspaceWindowController.changedLines(from: items, to: workspace.items))
        XCTAssertNotNil(WorkspaceWindowController.changedLines(from: [Item.section("A")], to: [Item.section("A")]))
    }

    func testTheLineUnderThePointerKeepsItsCloseButtonWhileAnotherChanges() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try cell(.embedded(F.runtimeID(1)))
        work.isHovered = true
        // Muted in place: a new state would move the line to another section.
        var changed = sessions
        changed[1].muted = true
        app.store.apply(snapshot: F.snapshot(changed))
        let line = try index(of: .embedded(F.runtimeID(1)))
        XCTAssertTrue(workspace.sidebar.view(atColumn: 0, row: line, makeIfNecessary: false) === work, "the line was not drawn again")
        XCTAssertTrue(work.showsCloseButton)
    }

    func testALineDrawnAgainIsHoveredWhereThePointerIs() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.window.layoutIfNeeded()
        let first = try index(of: .embedded(F.runtimeID(2)))
        let second = try index(of: .embedded(F.runtimeID(1)))
        let over = try cell(.embedded(F.runtimeID(2)))
        let other = try cell(.embedded(F.runtimeID(1)))
        other.isHovered = true
        let point = over.convert(NSPoint(x: over.bounds.midX, y: over.bounds.midY), to: nil)
        workspace.syncHover(at: point, lines: IndexSet([first, second]))
        XCTAssertTrue(over.isHovered)
        XCTAssertFalse(other.isHovered)
        workspace.syncHover(at: NSPoint(x: -100, y: -100), lines: IndexSet([first, second]))
        XCTAssertFalse(over.isHovered, "a pointer outside the list hovers nothing")
    }

    func testAHalfOpenFeedKeepsItsSnapshotAndTheWorkspace() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        app.store.apply(.disconnected(.heartbeatLost, retryIn: 0.5))
        XCTAssertFalse(workspace.state.current)
        XCTAssertEqual(workspace.feedLabel.stringValue, "Reconnecting… (the daemon is not answering)")
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

    func testEndSessionCanSkipConfirmationAndRestoreItImmediately() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let busy = try row("api", "personal")
        workspace.activate(busy)
        app.holdsCloses = true
        var asked: [WorkspaceRow] = []
        workspace.confirmClose = { row, answer in asked.append(row); answer(false) }

        workspace.confirmBeforeClosing = false
        workspace.closeSession(busy.key)
        XCTAssertTrue(asked.isEmpty)
        XCTAssertEqual(app.ended, [F.runtimeID(2)])
        XCTAssertEqual(app.closed, [F.runtimeID(2)])
        workspace.closeSession(busy.key)
        XCTAssertEqual(app.ended, [F.runtimeID(2)], "an in-flight close cannot be duplicated")

        workspace.confirmBeforeClosing = true
        let idle = try row("api", "work")
        workspace.closeSession(idle.key)
        XCTAssertEqual(asked, [idle])
        XCTAssertEqual(app.ended, [F.runtimeID(2)], "cancelling still keeps the agent running")
    }

    func testExplicitEndSessionAsksEvenForAnIdleSession() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.activate(work)
        app.holdsCloses = true
        var asked: [(WorkspaceRow, @MainActor (Bool) -> Void)] = []
        workspace.confirmClose = { row, answer in asked.append((row, answer)) }

        XCTAssertFalse(try cell(work.key).closeButton.isHidden)
        XCTAssertFalse(try cell(try row("web", "work").key).closeButton.isHidden)
        workspace.closeSession(work.key)
        XCTAssertEqual(asked.map(\.0), [work], "an idle agent is ended only once the operator agrees")
        XCTAssertEqual(app.ended, [], "nothing ends before the answer")
        asked[0].1(true)
        XCTAssertEqual(app.ended, [F.runtimeID(1)])
        XCTAssertEqual(app.closed, [F.runtimeID(1)], "its terminal is let go at once")
        XCTAssertEqual(workspace.content, .closing(runtimeSessionID: F.runtimeID(1)))
        XCTAssertTrue(try cell(work.key).closeButton.isHidden)
        XCTAssertEqual(try cell(work.key).detailLabel.stringValue, "ending…")
        XCTAssertTrue(try XCTUnwrap(cell(work.key).accessibilityLabel()).contains(", ending session"),
                      "VoiceOver hears what the line shows")

        // A second click, and snapshots that still list it, change nothing.
        workspace.closeSession(work.key)
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(asked.count, 1, "a session being closed is not asked about again")
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

        workspace.closeSession(busy.key)
        XCTAssertEqual(asked.map(\.0), [busy])
        XCTAssertEqual(app.ended, [], "nothing ends before the answer")
        asked[0].1(false)
        XCTAssertEqual(app.ended, [])
        XCTAssertFalse(try cell(busy.key).closeButton.isHidden)

        workspace.closeSession(busy.key)
        asked[1].1(true)
        XCTAssertEqual(app.ended, [F.runtimeID(2)])
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)), "the pane was elsewhere and stays there")
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
    }

    func testClosingAnAttachmentKeepsTheAgentAndCanReattach() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let busy = try row("api", "personal")
        workspace.activate(busy, reattaching: true)
        workspace.confirmClose = { _, _ in XCTFail("detaching must not ask to end the agent") }
        XCTAssertTrue(workspace.closeShown())
        XCTAssertEqual(app.ended, [])
        XCTAssertEqual(app.closed, [F.runtimeID(2)])
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(2)))
        XCTAssertNotNil(workspace.state.row(busy.key))
        workspace.activate(busy, reattaching: true)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(2)))
    }

    func testSessionRowCloseButtonEndsItsOwnSessionThroughConfirmation() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        let busy = try row("api", "personal")
        var asked: [(WorkspaceRow, @MainActor (Bool) -> Void)] = []
        workspace.confirmClose = { row, answer in asked.append((row, answer)) }

        let button = try cell(busy.key).closeButton
        XCTAssertEqual(button.toolTip, "End \(busy.displayName)…")
        XCTAssertEqual(button.accessibilityLabel(), "End session \(busy.displayName)")
        button.performClick(nil)
        XCTAssertEqual(asked.map(\.0), [busy])
        XCTAssertEqual(app.ended, [], "clicking does not end the agent before confirmation")
        asked[0].1(false)
        XCTAssertEqual(app.ended, [], "cancelling keeps the session running")

        try cell(busy.key).closeButton.performClick(nil)
        XCTAssertEqual(asked.map(\.0), [busy, busy])
        asked[1].1(true)
        XCTAssertEqual(app.ended, [F.runtimeID(2)], "the row under the pointer is ended")
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)),
                       "ending another row does not change the shown session")
    }

    func testASessionThatCouldNotBeClosedComesBack() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.activate(work)
        app.closeProblem = "tmux backend unavailable"
        var reported: [String] = []
        workspace.reportCloseProblem = { name, message in reported.append("\(name): \(message)") }
        workspace.confirmClose = { _, answer in answer(true) }

        workspace.closeSession(work.key)
        XCTAssertEqual(reported, ["api — work: tmux backend unavailable"])
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(1)], "its terminal is attached again")
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertFalse(try cell(work.key).closeButton.isHidden)
    }

    func testTheCloseButtonIsDrawnOnlyUnderThePointerWithARoomyTarget() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let line = try cell(try row("api", "work").key)
        XCTAssertFalse(line.closeButton.isHidden, "the line can be closed")
        XCTAssertFalse(line.showsCloseButton, "but the × is not drawn until the pointer is over it")
        XCTAssertEqual(line.closeButton.alphaValue, 0)
        line.isHovered = true
        XCTAssertTrue(line.showsCloseButton)
        XCTAssertEqual(line.closeButton.alphaValue, 1)
        line.isHovered = false
        XCTAssertFalse(line.showsCloseButton)
        XCTAssertFalse(try cell(try row("web", "work").key).showsCloseButton, "the terminal surface has none to draw")

        line.layoutSubtreeIfNeeded()
        XCTAssertGreaterThanOrEqual(line.closeButton.frame.width, 18)
        XCTAssertGreaterThanOrEqual(line.closeButton.frame.height, 18)
    }

    func testTheCloseItemSaysWhatItCloses() throws {
        XCTAssertEqual(workspace.closeShownTitle, "Close")
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        XCTAssertEqual(workspace.closeShownTitle, "Detach Terminal")
        workspace.select(try row("web", "work").key)
        XCTAssertEqual(workspace.closeShownTitle, "Detach Terminal")
        workspace.showView(WorkspaceUtilityView.dashboard.id)
        XCTAssertEqual(workspace.closeShownTitle, "Close View")
    }

    func testHandoffTargetsClickedConversationAndRetainsSourceNavigation() throws {
        var fourth = F.tmux(4, project: "api")
        fourth.conversation = .init(harness: "copilot", sessionID: "fourth-source")
        let initial = [F.tmux(1, project: "api", conversation: "first"), F.tmux(2, project: "api", conversation: "second"), F.tmux(3, project: "api"), fourth]
        app.store.apply(snapshot: F.snapshot(initial))
        workspace.select(.embedded(F.runtimeID(1)))
        let menu = workspace.contextMenuItems(forRow: try index(of: .embedded(F.runtimeID(2))))
        let handoff = try XCTUnwrap(menu.first { $0.title == WorkspaceHandoffModel.title })
        XCTAssertTrue(handoff.isEnabled)
        XCTAssertTrue(NSApp.sendAction(try XCTUnwrap(handoff.action), to: handoff.target, from: handoff))
        XCTAssertEqual(app.handoffRequests.map(\.conversation), [ConversationIdentity(harness: "claude", sessionID: "second")])
        let withoutIdentity = workspace.contextMenuItems(forRow: try index(of: .embedded(F.runtimeID(3))))
        XCTAssertFalse(try XCTUnwrap(withoutIdentity.first { $0.title == WorkspaceHandoffModel.title }).isEnabled)
        let fourthKey = AttentionCycler.key(fourth)
        XCTAssertFalse(workspace.contextMenuItems(forRow: try index(of: fourthKey)).isEmpty)
        let source = ConversationIdentity(harness: "codex", sessionID: "original")
        app.handoffSource = source
        workspace.setInspectorTab(.session)
        workspace.reloadInspector()
        XCTAssertEqual(workspace.inspector.model.sourceConversation, source)
        workspace.inspector.onSource?(source)
        XCTAssertEqual(app.openedHandoffSources, [source])
        app.store.apply(snapshot: F.snapshot([initial[0]]))
        XCTAssertTrue(NSApp.sendAction(try XCTUnwrap(handoff.action), to: handoff.target, from: handoff))
        XCTAssertEqual(app.handoffRequests.count, 1, "an ended target cannot be handed off from a stale menu")
    }

    func testNewSessionMenuTargetsClickedRowAndLaunchFocusWaitsForSnapshot() throws {
        let initial = [F.tmux(1, project: "api"), F.tmux(2, project: "api"), F.tmux(3, project: "api")]
        app.store.apply(snapshot: F.snapshot(initial))
        let parent = AttentionCycler.Key.embedded(F.runtimeID(2))
        workspace.select(.embedded(F.runtimeID(1)))
        let menu = workspace.contextMenuItems(forRow: try index(of: parent))
        let item = try XCTUnwrap(menu.first { $0.title == WorkspaceWindowController.newSessionTitle })
        _ = try XCTUnwrap(item.target).perform(try XCTUnwrap(item.action), with: item)
        XCTAssertEqual(app.newSessionRequests.map(\.key), [parent])
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)), "opening the dialog preserves the shown session")
        workspace.openNewSession(F.runtimeID(4), below: parent)
        XCTAssertEqual(workspace.awaited?.runtimeSessionID, F.runtimeID(4))
        app.store.apply(snapshot: F.snapshot(initial + [F.tmux(4, project: "api", profile: "codex", harness: "codex")]))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(4)))
        XCTAssertEqual(workspace.order.projects["api"], [F.runtimeID(1), F.runtimeID(2), F.runtimeID(4), F.runtimeID(3)])
        XCTAssertNil(workspace.awaited)
        XCTAssertNil(workspace.newSessionBelow)
        app.store.apply(snapshot: F.snapshot(initial + [F.tmux(4, project: "api", profile: "codex", harness: "codex")]))
        XCTAssertEqual(workspace.order.projects["api"], [F.runtimeID(1), F.runtimeID(2), F.runtimeID(4), F.runtimeID(3)])
    }

    func testTheSessionMenuFollowsTheSessions() throws {
        XCTAssertNil(workspace.sessionName(at: 0), "nothing listed, nothing to go to")
        XCTAssertEqual(WorkspaceWindowController.sessionItemTitle(at: 6, name: nil), "Session 7")
        XCTAssertEqual(workspace.pinShownTitle, "Pin Session")
        XCTAssertNil(workspace.shownSessionRow, "so the pin item is greyed out")
        XCTAssertFalse(workspace.canOpenTerminalBesideSession)
        XCTAssertFalse(workspace.openTerminalBesideSession(), "refused with nothing shown")

        app.store.apply(snapshot: F.snapshot(sessions))
        let rows = workspace.state.rows
        XCTAssertEqual(rows.count, 3)
        for (index, row) in rows.enumerated() {
            XCTAssertEqual(workspace.sessionName(at: index), row.displayName)
        }
        for index in rows.count..<WorkspaceMenu.sessionKeys.count {
            XCTAssertNil(workspace.sessionName(at: index), "⌘\(WorkspaceMenu.sessionKeys[index]) has no session to go to")
        }
        XCTAssertEqual(WorkspaceWindowController.sessionItemTitle(at: 0, name: rows[0].displayName),
                       "Session 1: \(rows[0].displayName)")

        let work = try row("api", "work")
        workspace.activate(work)
        XCTAssertEqual(workspace.pinShownTitle, "Pin Session")
        XCTAssertTrue(workspace.canOpenTerminalBesideSession)
        XCTAssertTrue(workspace.togglePinOfShownSession())
        XCTAssertEqual(workspace.pinShownTitle, "Unpin Session")
        XCTAssertEqual(workspace.sessionName(at: 0), work.displayName, "a pinned session is listed first")

        workspace.select(try row("web", "work").key)
        XCTAssertNotNil(workspace.shownSessionRow)
        XCTAssertEqual(workspace.pinShownTitle, "Pin Session")
        XCTAssertTrue(workspace.canOpenTerminalBesideSession)
    }

    /// Reattach is offered only on a detached session's line, and reattaches
    /// it even when the pane is somewhere else.
    func testDetachedLinesOfferReattach() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        let other = try row("web", "work")
        workspace.activate(work)
        XCTAssertFalse(workspace.contextMenuItems(forRow: try index(of: work.key)).map(\.title).contains("Reattach"))
        workspace.terminalEnded(try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1))))
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(1)))
        workspace.select(other.key)
        let items = workspace.contextMenuItems(forRow: try index(of: work.key))
        XCTAssertEqual(items.map(\.title), ["Name Session…", "Color…", "Icon…", "Mute", "Pin to Top", "", "Reattach", "New Terminal Below",
                                            "New Session Below…", "Continue with Another Agent…", "Copy Runtime Session ID", "", "End Session…"])
        _ = try XCTUnwrap(items[6].target).perform(try XCTUnwrap(items[6].action), with: items[6])
        XCTAssertEqual(workspace.state.selection, work.key)
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(4), F.runtimeID(1)])
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertFalse(workspace.contextMenuItems(forRow: try index(of: work.key)).map(\.title).contains("Reattach"))
    }

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
        XCTAssertEqual(items.map(\.title), ["Collapse Project"])
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
        XCTAssertEqual(items.map(\.title), ["Expand Project"])
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

    func testWarmSessionSwitchResponsivenessWithGhostty() throws {
        app.factory = { [unowned self] id in self.terminal(id) }
        workspace.openForTesting()
        app.store.apply(snapshot: F.snapshot((1...4).map { F.tmux($0, project: "fixture", icon: "bolt") }))
        var processes: [pid_t] = []
        for n in 1...4 {
            let id = F.runtimeID(n)
            workspace.select(.embedded(id), focus: true)
            processes.append(try XCTUnwrap(pid(id)))
        }
        var samples: [Double] = []
        for n in 0..<100 {
            let id = F.runtimeID(n % 4 + 1)
            let start = ProcessInfo.processInfo.systemUptime
            workspace.select(.embedded(id), focus: true)
            workspace.window.contentView?.layoutSubtreeIfNeeded()
            samples.append((ProcessInfo.processInfo.systemUptime - start) * 1000)
            XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: id))
        }
        XCTAssertEqual(app.made.count, 4, "switching reuses each live terminal")
        XCTAssertTrue(processes.allSatisfy(alive))
        samples.sort()
        let report = ["median_ms": samples[50], "p95_ms": samples[94], "max_ms": samples[99]]
        let directory = URL(fileURLWithPath: "/tmp/agentmux-context-previews", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try JSONSerialization.data(withJSONObject: report, options: [.prettyPrinted, .sortedKeys])
            .write(to: directory.appendingPathComponent("ghostty-responsiveness.json"))
        print("Warm Ghostty switch (4 terminals, 100 samples, controller + layout, excluding GPU presentation): \(report)")
    }

    func testSidebarAndTerminalsStayConsistentAcrossAReconnect() throws {
        let sessions = [
            F.tmux(1, project: "api", profile: "work"),
            F.tmux(2, project: "api", profile: "personal", icon: "stop", attention: true, changedAtMS: 2000),
            F.tmux(4, project: "web", icon: "key", attention: true, changedAtMS: 1000),
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

extension WorkspaceWindowTests {
    func testClosingRechecksPolicyAndTargetAfterAsynchronousRead() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let key = AttentionCycler.Key.embedded(F.runtimeID(1))
        var reads: [(Bool) -> Void] = []
        var asked: [WorkspaceRow] = []
        workspace.readConfirmation = { reads.append($0) }
        workspace.confirmClose = { row, answer in asked.append(row); answer(false) }
        workspace.closeSession(key)
        workspace.closeSession(key)
        XCTAssertEqual(reads.count, 1, "duplicate clicks share the pending policy check")
        XCTAssertTrue(asked.isEmpty)
        reads.removeFirst()(true)
        XCTAssertEqual(asked.map(\.key), [key])
        workspace.closeSession(key)
        app.store.apply(snapshot: F.snapshot([]))
        reads.removeFirst()(false)
        XCTAssertEqual(asked.count, 1, "a late policy response cannot act on an ended target")
    }
}

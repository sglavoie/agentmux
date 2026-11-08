import AppKit
import GhosttyKit
import ScreenCaptureKit
import XCTest
@testable import AgentMux

/// The workspace window against a store fed by hand: what selecting,
/// focusing, and losing the daemon do to the sidebar and the terminals.
@MainActor
final class WorkspaceWindowTests: XCTestCase {
    typealias F = WorkspaceFixture
    var app: FakeWorkspaceApp!
    var workspace: WorkspaceWindowController!

    /// Every turn ended, so all three are listed under Done, below its
    /// heading, and the layout tests have one state section.
    let sessions = [
        F.tmux(1, project: "api", profile: "work", icon: "stop"),
        F.tmux(2, project: "api", profile: "personal", icon: "stop", attention: true, changedAtMS: 2000),
        // A seen completion stays in its project; attention transitions are
        // exercised separately below so layout tests have a fixed baseline.
        F.tmux(4, project: "web", icon: "stop", attention: true, changedAtMS: 1000),
    ]

    override func setUp() async throws {
        app = FakeWorkspaceApp()
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        // Keep both utility entries visible for the shared layout fixtures.
        workspace.setShowOverviewInSidebar(true)
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
        workspace.openForTesting()
    }

    override func tearDown() async throws {
        workspace.window.close()
        workspace = nil
        app = nil
    }

    func row(_ project: String, _ label: String) throws -> WorkspaceRow {
        try XCTUnwrap(workspace.state.rows.first { $0.project == project && $0.label == label })
    }

    func chooseGroup(at index: Int) {
        workspace.groupFilter.pills[index].performClick(nil)
    }

    func testPinsDragAcrossProjectsInEveryLayoutAndKeepTheirOrderWhenSwitching() throws {
        var live = [
            F.tmux(1, project: "api", profile: "a", icon: "key", attention: true),
            F.tmux(2, project: "api", profile: "b", icon: "key", attention: true),
            F.tmux(3, project: "web", profile: "c", icon: "bolt"),
        ]
        live[1].pinned = true
        live[2].pinned = true
        app.store.apply(snapshot: F.snapshot(live))
        var expected = [2, 3, 1].map { AttentionCycler.Key.embedded(F.runtimeID($0)) }
        for grouping in WorkspaceSidebarGrouping.allCases {
            workspace.setSidebarGrouping(grouping)
            XCTAssertEqual(workspace.state.rows.map(\.key), expected)
            let first = try index(of: expected[0])
            let second = try index(of: expected[1])
            let cell = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: first, makeIfNecessary: true) as? WorkspaceRowCellView)
            XCTAssertTrue(cell.titleLabel.stringValue.hasPrefix("★ "))
            XCTAssertTrue(workspace.moveItem(at: second, to: first), "\(grouping)")
            expected.swapAt(0, 1)
            XCTAssertEqual(workspace.state.rows.map(\.key), expected)
            XCTAssertEqual(workspace.order.pinned, expected.prefix(2).map(WorkspaceOrder.token))
            XCTAssertTrue(workspace.order.projects.isEmpty)
            XCTAssertTrue(workspace.order.flat.isEmpty)
            XCTAssertFalse(workspace.moveItem(at: try index(of: expected[0]), to: try index(of: expected[2])), "pins stay above unpinned rows")
        }
    }

    func testProjectsDragAcrossStatesAndKeepSelectedTerminalOnStateChanges() throws {
        workspace.setSidebarGrouping(.project)
        var live = sessions
        live[0].iconState = "bolt"
        app.store.apply(snapshot: F.snapshot(live))
        let selected = try row("api", "work")
        workspace.activate(selected)
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        XCTAssertTrue(workspace.moveItem(at: try index(of: selected.key), to: try index(of: .embedded(F.runtimeID(2)))))
        XCTAssertEqual(workspace.order.projects["api"], [1, 2].map(F.runtimeID))
        live[0].iconState = "key"
        live[0].attention = true
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertEqual(workspace.state.rows.map(\.key), [1, 2, 4].map { .embedded(F.runtimeID($0)) })
        XCTAssertEqual(workspace.state.selection, selected.key)
        XCTAssertTrue(workspace.terminalView(for: F.runtimeID(1)) === terminal)
        let group = try XCTUnwrap(workspace.state.groups.first)
        let cell = WorkspaceGroupCellView()
        cell.show(group)
        XCTAssertEqual(cell.countLabel.stringValue, "2")
        XCTAssertTrue(cell.accessibilityLabel()?.contains("2 waiting") == true)
    }

    func testBranchesRevealExpandsParentAndBranchAndDragsOnlySiblings() throws {
        workspace.setSidebarGrouping(.projectThenBranch)
        var live = sessions
        live[0].branch = "feature"
        live[1].branch = "main"
        live.append(F.tmux(5, project: "api", profile: "other"))
        live[3].branch = "feature"
        app.store.apply(snapshot: F.snapshot(live))
        let selected = try row("api", "work")
        workspace.activate(selected)
        let feature = try XCTUnwrap(workspace.state.groups.first { $0.branch == "feature" })
        workspace.setCollapsed(feature, true)
        workspace.setCollapsed(project: "api", pinned: false, true)
        XCTAssertTrue(workspace.revealInSidebar())
        XCTAssertEqual(workspace.sidebar.selectedRow, try index(of: selected.key))
        XCTAssertTrue(workspace.moveItem(at: try index(of: selected.key), to: try index(of: .embedded(F.runtimeID(5)))))
        XCTAssertFalse(workspace.moveItem(at: try index(of: selected.key), to: try index(of: .embedded(F.runtimeID(2)))))
        XCTAssertEqual(workspace.state.rows.filter { $0.branch == "feature" }.map(\.key), [1, 5].map { .embedded(F.runtimeID($0)) })
        workspace.setSidebarGrouping(.project)
        XCTAssertEqual(workspace.state.selection, selected.key)
    }

    func testFlatListLabelsProjectsAndKeepsPinsFirstWhenFiltered() throws {
        workspace.setSidebarGrouping(.compact)
        var live = sessions
        live[0].name = "Build API"
        live[2].pinned = true
        app.store.apply(snapshot: F.snapshot(live))
        workspace.select(.embedded(F.runtimeID(1)))
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        let pin = try index(of: .embedded(F.runtimeID(4)))
        let cell = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: pin, makeIfNecessary: true) as? WorkspaceRowCellView)
        XCTAssertEqual(cell.titleLabel.stringValue, "★ web — work")
        XCTAssertFalse(cell.detailLabel.stringValue.contains("web"))
        let named = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: try index(of: .embedded(F.runtimeID(1))), makeIfNecessary: true) as? WorkspaceRowCellView)
        XCTAssertEqual(named.nameLabel.stringValue, "api — Build API")
        XCTAssertEqual(named.titleLabel.stringValue, "work")
        XCTAssertFalse(named.detailLabel.stringValue.contains("api"))
        workspace.groupFilter.waitingOnlyButton.performClick(nil)
        XCTAssertTrue(workspace.sidebarWaitingOnly)
        XCTAssertEqual(workspace.sessionShortcutRows.map(\.key), [4, 2].map { .embedded(F.runtimeID($0)) })
        XCTAssertEqual(workspace.state.rows.count, 3)
        XCTAssertTrue(workspace.terminalView(for: F.runtimeID(1)) === terminal)
        XCTAssertFalse(workspace.moveItem(at: try index(of: .embedded(F.runtimeID(4))), to: try index(of: .embedded(F.runtimeID(2)))), "pins cannot be dragged below unpinned rows")
        XCTAssertTrue(workspace.order.flat.isEmpty)
        XCTAssertTrue(workspace.revealInSidebar())
        XCTAssertFalse(workspace.sidebarWaitingOnly)
        XCTAssertEqual(workspace.sessionShortcutRows.map(\.key), [4, 2, 1].map { .embedded(F.runtimeID($0)) })
        workspace.setSidebarWaitingOnly(true)
        live[1].attention = false
        live[2].attention = false
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertTrue(workspace.items.contains(.section("No waiting sessions")))
        XCTAssertEqual(workspace.state.rows.count, 3)
    }

    func testRecentSortTracksVisitsAndRestoresManualOrder() throws {
        workspace.setSidebarGrouping(.compact)
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.select(.embedded(F.runtimeID(4)))
        workspace.select(.embedded(F.runtimeID(1)))
        workspace.setSidebarSort(.recentlyVisited)
        XCTAssertEqual(workspace.state.rows.map(\.key), [1, 4, 2].map { .embedded(F.runtimeID($0)) })
        XCTAssertTrue(workspace.dropSlots(forItemAt: try index(of: .embedded(F.runtimeID(4)))).isEmpty)
        workspace.select(.embedded(F.runtimeID(2)))
        XCTAssertEqual(workspace.state.rows.map(\.key), [2, 1, 4].map { .embedded(F.runtimeID($0)) })
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(workspace.state.rows.map(\.key), [2, 1, 4].map { .embedded(F.runtimeID($0)) })
        workspace.select(.embedded(F.runtimeID(4)))
        XCTAssertEqual(workspace.state.rows.map(\.key), [4, 2, 1].map { .embedded(F.runtimeID($0)) })
        XCTAssertTrue(workspace.openTerminalBesideSession())
        let attached = try XCTUnwrap(workspace.viewSelection)
        workspace.select(.embedded(F.runtimeID(1)))
        XCTAssertTrue(workspace.showView(attached))
        XCTAssertEqual(workspace.state.rows.first?.key, .embedded(F.runtimeID(4)), "opening an attached terminal visits its session")
        workspace.setSidebarSort(.manual)
        XCTAssertEqual(workspace.state.rows.map(\.key), [2, 1, 4].map { .embedded(F.runtimeID($0)) })
        XCTAssertFalse(workspace.dropSlots(forItemAt: try index(of: .embedded(F.runtimeID(1)))).isEmpty)
    }

    func testWaitingFilterCombinesWithProjectScopeInEveryLayoutAndKeepsOfflineRows() throws {
        app.workspaceProjects = [ProjectEntry(profile: "work", title: "api", harness: "claude", groups: ["backend"])]
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.selectGroup("backend")
        for grouping in WorkspaceSidebarGrouping.allCases {
            workspace.setSidebarGrouping(grouping)
            for sorting in WorkspaceSidebarSort.allCases {
                workspace.setSidebarSort(sorting)
                workspace.setSidebarWaitingOnly(true)
                XCTAssertEqual(workspace.sessionShortcutRows.map(\.key), [.embedded(F.runtimeID(2))])
                XCTAssertEqual(workspace.state.rows.count, 3)
            }
        }
        app.store.apply(.disconnected(.notRunning, retryIn: 0.5))
        XCTAssertFalse(workspace.scopedSidebar.current)
        XCTAssertEqual(workspace.sessionShortcutRows.map(\.key), [.embedded(F.runtimeID(2))])
        workspace.setSidebarWaitingOnly(false)
        XCTAssertEqual(Set(workspace.sessionShortcutRows.map(\.key)), Set([1, 2].map { .embedded(F.runtimeID($0)) }))
        XCTAssertEqual(workspace.selectedGroup, "backend")
    }

    func testNewSessionBelowInRecentFlatListPreservesManualOrder() {
        workspace.setSidebarGrouping(.compact)
        workspace.setSidebarSort(.recentlyVisited)
        workspace.order.setFlat([4, 2, 1].map { .embedded(F.runtimeID($0)) })
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.select(.embedded(F.runtimeID(1)))
        workspace.openNewSession(F.runtimeID(5), below: .embedded(F.runtimeID(2)))
        app.store.apply(snapshot: F.snapshot(sessions + [F.tmux(5, project: "api")]))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(5)))
        XCTAssertEqual(workspace.order.flat, [4, 2, 5, 1].map(F.runtimeID))
        workspace.setSidebarSort(.manual)
        XCTAssertEqual(workspace.state.rows.map(\.key), [4, 2, 5, 1].map { .embedded(F.runtimeID($0)) })
    }

    func testSidebarLayoutsInLightDarkAndNarrowWindows() async throws {
        guard #available(macOS 14.4, *) else { throw XCTSkip("Window captures require macOS 14.4") }
        var live = [F.tmux(1, project: "Agent Mux", profile: "work", icon: "bolt"),
                    F.tmux(2, project: "Agent Mux", profile: "review", icon: "key", attention: true),
                    F.tmux(3, project: "Docs", icon: "stop", attention: true)]
        live[0].name = "Sidebar layouts"
        live[0].branch = "feature/sidebar"
        live[1].branch = "main"
        live[1].pinned = true
        live[2].branch = "main"
        app.store.apply(snapshot: F.snapshot(live))
        workspace.select(.embedded(F.runtimeID(1)))
        workspace.setInspectorCollapsed(true)
        let directory = URL(fileURLWithPath: "/tmp/agentmux-sidebar-previews", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        for grouping in [WorkspaceSidebarGrouping.project, .compact, .projectThenBranch] {
            workspace.setSidebarGrouping(grouping)
            for (appearance, width) in [(NSAppearance.Name.aqua, CGFloat(1100)), (.darkAqua, 700)] {
                workspace.window.appearance = NSAppearance(named: appearance)
                workspace.window.setContentSize(NSSize(width: width, height: 760))
                workspace.window.makeKeyAndOrderFront(nil)
                workspace.window.contentView?.layoutSubtreeIfNeeded()
                workspace.window.displayIfNeeded()
                try await Task.sleep(for: .milliseconds(300))
                XCTAssertEqual(Set(workspace.sessionShortcutRows.map(\.key)).count, 3)
                let waiting = workspace.groupFilter.waitingOnlyButton
                XCTAssertTrue(workspace.groupFilter.bounds.contains(waiting.frame))
                XCTAssertGreaterThan(waiting.frame.width, 100)
                let content = try await SCShareableContent.currentProcess
                let target = try XCTUnwrap(content.windows.first { $0.windowID == CGWindowID(workspace.window.windowNumber) })
                let filter = SCContentFilter(desktopIndependentWindow: target)
                let configuration = SCStreamConfiguration()
                configuration.width = Int(filter.contentRect.width * CGFloat(filter.pointPixelScale))
                configuration.height = Int(filter.contentRect.height * CGFloat(filter.pointPixelScale))
                configuration.showsCursor = false
                let capture = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: configuration)
                let png = try XCTUnwrap(NSBitmapImageRep(cgImage: capture).representation(using: .png, properties: [:]))
                let name = "\(grouping.rawValue)-\(appearance == .aqua ? "light" : "dark-narrow")"
                try png.write(to: directory.appendingPathComponent(name + ".png"))
                let attachment = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
                attachment.name = name
                attachment.lifetime = .keepAlways
                add(attachment)
            }
        }
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

        workspace.setShowOverviewInSidebar(false)
        XCTAssertFalse(workspace.items.contains(.navigation))
        XCTAssertFalse(workspace.items.contains(.view(.overview)))
        XCTAssertTrue(workspace.items.contains(.view(.dashboard)))
        XCTAssertEqual(workspace.viewSelection, WorkspaceUtilityView.overview.id, "hiding the entry preserves the active view")
        workspace.setShowOverviewInSidebar(true)
        XCTAssertEqual(workspace.items[workspace.sidebar.selectedRow], .navigation)

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

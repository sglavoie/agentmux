import AppKit
import GhosttyKit
import ScreenCaptureKit
import XCTest
@testable import AgentMux

extension WorkspaceWindowTests {
    func testNewProjectsSortInEveryGroupingAndTerminalPresentation() throws {
        let initial = [F.tmux(1, project: "🧠 BrainNotes", profile: "a"),
                       F.tmux(2, project: "🧠 BrainNotes", profile: "b"),
                       F.tmux(3, project: "↑ ZoomFlow")]
        let added = [F.tmux(4, project: "🐱 amx"), F.tmux(5, project: "📝 Docket")]
        let manual = [4, 2, 1, 5, 3].map { AttentionCycler.Key.embedded(F.runtimeID($0)) }
        func displayedKeys() -> [AttentionCycler.Key] {
            workspace.items.compactMap { if case let .row(row) = $0 { return row.key }; return nil }
        }
        for compact in [false, true] {
            workspace.setCompactSidebar(compact)
            for grouping in WorkspaceSidebarGrouping.allCases {
                workspace.setSidebarSort(.manual)
                workspace.setSidebarGrouping(grouping)
                workspace.order = WorkspaceOrder()
                workspace.order.set([2, 1].map { .embedded(F.runtimeID($0)) }, for: "🧠 BrainNotes")
                workspace.order.setFlat([2, 1, 3].map { .embedded(F.runtimeID($0)) })
                app.store.apply(snapshot: F.snapshot(initial))
                for live in [initial + added, Array((initial + added).reversed())] {
                    app.store.apply(snapshot: F.snapshot(live))
                    XCTAssertEqual(displayedKeys(), manual, "\(grouping), compact terminals: \(compact)")
                    XCTAssertEqual(workspace.sessionShortcutRows.map(\.key), manual)
                }

                workspace.order.visit(.embedded(F.runtimeID(3)))
                workspace.setSidebarSort(.recentlyVisited)
                let recent = grouping == .compact
                    ? [3, 4, 2, 1, 5].map { AttentionCycler.Key.embedded(F.runtimeID($0)) } : manual
                XCTAssertEqual(displayedKeys(), recent, "visits reorder rows within their group")
                XCTAssertEqual(workspace.sessionShortcutRows.map(\.key), recent)
                workspace.setSidebarSort(.manual)
                XCTAssertEqual(displayedKeys(), manual, "returning to manual restores alphabetical project placement")
            }
        }
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
        XCTAssertEqual(animation.duration, 0.32, accuracy: 0.001)
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
}

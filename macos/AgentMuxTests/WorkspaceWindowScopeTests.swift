import AppKit
import GhosttyKit
import ScreenCaptureKit
import XCTest
@testable import AgentMux

extension WorkspaceWindowTests {
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

    func index(of key: AttentionCycler.Key) throws -> Int {
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
}

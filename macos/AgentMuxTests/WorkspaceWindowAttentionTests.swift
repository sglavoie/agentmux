import AppKit
import GhosttyKit
import ScreenCaptureKit
import XCTest
@testable import AgentMux

extension WorkspaceWindowTests {
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
        let ordersBeforeClick = app.orders
        workspace.focusItem(at: 4)
        XCTAssertEqual(app.orders, ordersBeforeClick, "attention cannot be collapsed")

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
        // Dashboard is listed by default; Overview is an optional entry.
        workspace.setShowOverviewInSidebar(false)
        XCTAssertEqual(workspace.items, [.section("Workspace"), .view(.dashboard)])
        workspace.setShowOverviewInSidebar(true)
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
        XCTAssertEqual(cell.titleLabel.stringValue, "api · claude · work", "project and agent stay on the secondary line")
        XCTAssertTrue(cell.nameLabel.font?.fontDescriptor.symbolicTraits.contains(.bold) == true)
        XCTAssertEqual(cell.nameLabel.textColor, .labelColor, "name follows the current appearance")
        let nameBounds = cell.convert(cell.nameLabel.bounds, from: cell.nameLabel)
        let profileBounds = cell.convert(cell.titleLabel.bounds, from: cell.titleLabel)
        XCTAssertGreaterThanOrEqual(nameBounds.minY, profileBounds.maxY, "the name is the first line")
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
        XCTAssertEqual(items.map(\.title), ["Peek at Session", "Name Session…", "Color…", "Icon…", "Mute", "Pin to Top", "", "New Terminal Below",
                                            "New Session Below…", "Continue with Another Agent…", "Copy Runtime Session ID", "", "End Session…", "", "Mark Accounted For", "Edit Session Note…", "Undo Annotation"])
        let naming = try XCTUnwrap(items.first { $0.title == "Name Session…" })
        _ = try XCTUnwrap(naming.target).perform(try XCTUnwrap(naming.action), with: naming)
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
}

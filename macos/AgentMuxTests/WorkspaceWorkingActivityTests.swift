import AppKit
import XCTest
@testable import AgentMux

@MainActor
final class WorkspaceWorkingActivityTests: XCTestCase {
    typealias F = WorkspaceFixture

    func testToolDecodesAndWorkingRowsUseBusyStretch() throws {
        let decoded = try JSONDecoder().decode(CompanionSession.self, from: Data(#"{"active_tool":"Bash","busy_since_at":1000}"#.utf8))
        XCTAssertEqual(decoded.activeTool, "Bash")
        XCTAssertEqual(decoded.busySinceAt, 1000)
        XCTAssertEqual(try JSONDecoder().decode(CompanionSession.self, from: Data("{}".utf8)).activeTool, "")
        let now = Date(timeIntervalSince1970: 1095)
        for icon in ["bolt", "subagent", "compact"] {
            var session = F.tmux(1, project: "api", icon: icon, changedAtMS: 1094000)
            session.busySinceAt = decoded.busySinceAt
            session.activeTool = decoded.activeTool
            session.branch = "main"
            session.dirty = true
            let row = WorkspaceSidebar.row(session, now: now, glyphs: .fallback)
            XCTAssertEqual(row.sidebarDetail, "Bash · 1:35 · main*")
            XCTAssertEqual(row.sidebarDetail(now: now.addingTimeInterval(1)), "Bash · 1:36 · main*")
            session.activeTool = ""
            XCTAssertEqual(WorkspaceSidebar.row(session, now: now, glyphs: .fallback).sidebarDetail, "Working · 1:35 · main*")
            session.iconState = "stop"
            XCTAssertEqual(WorkspaceSidebar.row(session, now: now, glyphs: .fallback).sidebarDetail(now: now),
                           WorkspaceSidebar.row(session, now: now, glyphs: .fallback).sidebarDetail)
        }
    }

    func testElapsedBandsMatchDashboard() {
        let anchor: Int64 = 1000
        for (seconds, expected) in [(-1, "0:00"), (0, "0:00"), (95, "1:35"), (3599, "59:59"),
                                    (3600, "1h00"), (359999, "99h59"), (360000, "4d04"),
                                    (8639999, "99d23"), (8640000, "99d+")] {
            XCTAssertEqual(WorkspaceRow.runElapsed(since: anchor, now: Date(timeIntervalSince1970: Double(anchor) + Double(seconds))), expected)
        }
        XCTAssertEqual(WorkspaceRow.runElapsed(since: 0, now: Date()), "—")
    }

    func testWorkingTimerFollowsVisibilityListedRowsAndUpdatesOnlyExistingLabels() throws {
        let app = FakeWorkspaceApp()
        var now = Date(timeIntervalSince1970: 1095)
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback, now: { now })
        defer { workspace.window.close() }
        app.store.onChange = { [weak workspace] in workspace?.refresh() }
        workspace.openForTesting()
        var session = F.tmux(1, project: "api", icon: "bolt")
        session.activeTool = "Bash"
        session.busySinceAt = 1000
        app.store.apply(snapshot: F.snapshot([session]))
        XCTAssertNil(workspace.workingTimer, "an unordered testing window is not visible")
        workspace.window.orderFront(nil)
        workspace.updateWorkingTimer()
        let timer = try XCTUnwrap(workspace.workingTimer)
        XCTAssertEqual(timer.timeInterval, 1)
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        let index = try XCTUnwrap(workspace.items.firstIndex { if case .row = $0 { return true }; return false })
        let cell = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: index, makeIfNecessary: true) as? WorkspaceRowCellView)
        XCTAssertEqual(cell.detailLabel.stringValue, "Bash · 1:35")
        workspace.sidebar.selectRowIndexes(IndexSet(integer: index), byExtendingSelection: false)
        let selection = workspace.sidebar.selectedRowIndexes
        let responder = workspace.window.firstResponder
        let items = workspace.items
        now = now.addingTimeInterval(1)
        timer.fire()
        XCTAssertEqual(cell.detailLabel.stringValue, "Bash · 1:36")
        XCTAssertTrue(workspace.sidebar.view(atColumn: 0, row: index, makeIfNecessary: false) === cell)
        XCTAssertEqual(workspace.items, items, "ticks never replace or reorder table data")
        XCTAssertEqual(workspace.sidebar.selectedRowIndexes, selection)
        XCTAssertTrue(workspace.window.firstResponder === responder)
        XCTAssertNotNil(workspace.ageTimer, "the ordinary age refresh stays independent")
        workspace.window.orderOut(nil)
        workspace.windowDidChangeOcclusionState(Notification(name: NSWindow.didChangeOcclusionStateNotification))
        XCTAssertNil(workspace.workingTimer)
        XCTAssertFalse(timer.isValid)
        workspace.window.orderFront(nil)
        workspace.updateWorkingTimer()
        XCTAssertNotNil(workspace.workingTimer)
        workspace.setCollapsed(project: "api", pinned: false, true)
        XCTAssertNil(workspace.workingTimer, "collapsed working sessions are not listed")
        workspace.setCollapsed(project: "api", pinned: false, false)
        XCTAssertNotNil(workspace.workingTimer)
        session.iconState = "stop"
        app.store.apply(snapshot: F.snapshot([session]))
        XCTAssertNil(workspace.workingTimer)
        session.iconState = "bolt"
        app.store.apply(snapshot: F.snapshot([session]))
        XCTAssertNotNil(workspace.workingTimer)
        workspace.window.close()
        XCTAssertNil(workspace.workingTimer)
        XCTAssertNil(workspace.ageTimer)
    }
}

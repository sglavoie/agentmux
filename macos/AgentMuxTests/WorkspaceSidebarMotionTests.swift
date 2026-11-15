import AppKit
import XCTest
@testable import AgentMux

@MainActor
final class WorkspaceSidebarMotionTests: XCTestCase {
    typealias F = WorkspaceFixture
    typealias Item = WorkspaceSidebarItem
    var app: FakeWorkspaceApp!
    var workspace: WorkspaceWindowController!
    var table: MotionRecordingSidebar!

    override func setUp() async throws {
        app = FakeWorkspaceApp()
        table = MotionRecordingSidebar()
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback, sidebar: table)
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
        workspace.openForTesting()
    }

    override func tearDown() async throws {
        workspace.window.close()
        workspace = nil
        table = nil
        app = nil
    }

    private func line(_ id: Int) throws -> Int {
        try XCTUnwrap(workspace.items.firstIndex {
            if case let .row(row) = $0 { return row.key == .embedded(F.runtimeID(id)) }
            return false
        })
    }

    private func displayedLines() -> [String] {
        workspace.items.indices.map { index in
            let cell = table.view(atColumn: 0, row: index, makeIfNecessary: true)
            if let row = cell as? WorkspaceRowCellView {
                return [row.titleLabel.stringValue, row.detailLabel.stringValue, row.keyLabel.stringValue].joined(separator: " | ")
            }
            if let group = cell as? WorkspaceGroupCellView {
                return [group.nameLabel.stringValue, group.countLabel.stringValue].joined(separator: " | ")
            }
            return String(describing: type(of: cell))
        }
    }

    private func settle() async throws {
        for _ in 0..<100 {
            if workspace.sidebarAnimations == 0 { return }
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTFail("Sidebar animation did not complete")
    }

    func testStateMoveRetainsRowSelectionFocusScrollAndFlashesAfterwards() async throws {
        var live = [F.tmux(1, project: "api", icon: "stop"),
                    F.tmux(2, project: "api", icon: "stop"),
                    F.tmux(3, project: "api", icon: "bolt")]
        app.store.apply(snapshot: F.snapshot(live))
        let selected = try XCTUnwrap(workspace.state.row(.embedded(F.runtimeID(2))))
        workspace.activate(selected)
        let responder = workspace.window.firstResponder
        let position = table.enclosingScrollView?.contentView.bounds.origin
        let rowView = try XCTUnwrap(table.rowView(atRow: line(2), makeIfNecessary: true) as? WorkspaceRowView)
        workspace.sidebarMotionEnabled = { true }
        table.reset()
        live[1].iconState = "bolt"
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertEqual(table.fullReloads, 0)
        XCTAssertFalse(table.moves.isEmpty, "state transition uses actual NSTableView moves")
        XCTAssertGreaterThan(workspace.sidebarAnimations, 0)
        XCTAssertFalse(rowView.isFlashing, "flash waits for the move to finish")
        XCTAssertEqual(workspace.state.selection, selected.key)
        XCTAssertEqual(table.selectedRow, try line(2))
        XCTAssertTrue(workspace.window.firstResponder === responder)
        try await settle()
        XCTAssertTrue(table.rowView(atRow: try line(2), makeIfNecessary: true) === rowView)
        XCTAssertTrue(rowView.isFlashing)
        XCTAssertEqual(table.enclosingScrollView?.contentView.bounds.origin, position)
        XCTAssertEqual(table.selectedRow, try line(2))
        XCTAssertTrue(workspace.window.firstResponder === responder)
        XCTAssertEqual(table.numberOfRows, workspace.items.count)
        let animated = workspace.items
        let rendered = displayedLines()
        workspace.reloadSidebar(force: true, allowReorder: true)
        XCTAssertEqual(workspace.items, animated, "animated order equals a fresh model reload")
        XCTAssertEqual(displayedLines(), rendered, "AppKit's cells match the final model, including labels and digits")
    }

    func testAnimatedUpdateRedrawsChangedDecorations() async throws {
        var live = [F.tmux(1, project: "api", icon: "stop")]
        live[0].tabColor = "#112233"
        app.store.apply(snapshot: F.snapshot(live))
        workspace.select(.embedded(F.runtimeID(1)), focus: false)
        workspace.openTerminalBesideSession()
        workspace.open(WorkspaceViewRequest(kind: "pager", runtimeSessionID: F.runtimeID(1), title: "api"))
        let pager = try XCTUnwrap(workspace.items.firstIndex {
            if case let .view(view) = $0 { return view.id == "pager:\(F.runtimeID(1))" }
            return false
        })
        let terminal = try XCTUnwrap(workspace.items.firstIndex {
            if case let .view(view) = $0 { return view.besideRuntimeSessionID == F.runtimeID(1) }
            return false
        })
        func terminalColor() throws -> NSColor {
            let row = try XCTUnwrap(table.rowView(atRow: terminal, makeIfNecessary: true) as? WorkspaceRowView)
            return try XCTUnwrap(row.tabColor)
        }
        let oldColor = try terminalColor()
        let before = workspace.items[pager]
        let terminalBefore = workspace.items[terminal]
        let oldText = displayedLines()[pager]
        XCTAssertTrue(oldText.contains("api — work"))
        workspace.sidebarMotionEnabled = { true }
        table.reset()

        // Naming an unnamed session replaces its line (and changes its height),
        // while the pager's item and position stay unchanged.
        live[0].name = "release notes"
        live[0].tabColor = "#445566"
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertEqual(workspace.items[pager], before)
        XCTAssertEqual(workspace.items[terminal], terminalBefore)
        XCTAssertEqual(table.fullReloads, 0)
        XCTAssertGreaterThan(table.insertions, 0)
        XCTAssertGreaterThan(table.removals, 0)
        XCTAssertGreaterThan(workspace.sidebarAnimations, 0)
        try await settle()
        let rendered = displayedLines()
        let color = try terminalColor()
        XCTAssertNotEqual(color, oldColor)
        XCTAssertTrue(rendered[pager].contains("Scrollback · release notes"))
        XCTAssertNotEqual(rendered[pager], oldText)
        workspace.reloadSidebar(force: true, allowReorder: true)
        XCTAssertEqual(displayedLines(), rendered, "animated decorations match a forced reload")
        XCTAssertEqual(try terminalColor(), color, "inherited profile markers match a forced reload")
    }

    func testInsertRemoveAndRapidSnapshotsKeepTheLatestOrder() async throws {
        var live = [F.tmux(1, project: "api", icon: "stop"), F.tmux(2, project: "web", icon: "bolt")]
        app.store.apply(snapshot: F.snapshot(live))
        workspace.sidebarMotionEnabled = { true }
        table.reset()
        live.append(F.tmux(3, project: "cli", icon: "key", attention: true))
        app.store.apply(snapshot: F.snapshot(live))
        live.removeFirst()
        live[0].iconState = "stop"
        app.store.apply(snapshot: F.snapshot(live))
        live[0].iconState = "bolt"
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertEqual(table.fullReloads, 0)
        XCTAssertGreaterThan(table.insertions, 0)
        XCTAssertGreaterThan(table.removals, 0)
        try await settle()
        let animated = workspace.items
        let rendered = displayedLines()
        XCTAssertEqual(table.numberOfRows, animated.count)
        workspace.reloadSidebar(force: true, allowReorder: true)
        XCTAssertEqual(workspace.items, animated)
        XCTAssertEqual(displayedLines(), rendered)
        XCTAssertTrue(workspace.pendingSidebarFlashes.isEmpty)
    }

    func testFallbacksReloadForDisabledMotionForceLargeDiffAndDetachedTable() async throws {
        let initial = [F.tmux(1, project: "api", icon: "stop")]
        app.store.apply(snapshot: F.snapshot(initial))
        table.reset()
        app.store.apply(snapshot: F.snapshot(initial + [F.tmux(2, project: "web")]))
        XCTAssertEqual(table.fullReloads, 1, "motion is disabled by default in tests")
        workspace.sidebarMotionEnabled = { true }
        table.reset()
        workspace.reloadSidebar(force: true)
        XCTAssertEqual(table.fullReloads, 1)
        table.reset()
        app.store.apply(snapshot: F.snapshot((10...40).map { F.tmux($0, project: "api") }))
        XCTAssertEqual(table.fullReloads, 1, "large scope changes reload")
        table.removeFromSuperview()
        XCTAssertNil(table.window)
        table.reset()
        app.store.apply(snapshot: F.snapshot(initial))
        XCTAssertEqual(table.fullReloads, 1)
        XCTAssertTrue(table.moves.isEmpty)
        XCTAssertEqual(workspace.sidebarAnimations, 0)
    }

    func testMenuAndDragHoldsDelayAnimatedReordering() async throws {
        var live = [F.tmux(1, project: "api", icon: "stop"), F.tmux(2, project: "api", icon: "bolt")]
        app.store.apply(snapshot: F.snapshot(live))
        workspace.sidebarMotionEnabled = { true }
        let before = try line(1)
        workspace.menuWillOpen(NSMenu())
        workspace.dragged = .session(.embedded(F.runtimeID(1)))
        table.reset()
        live[0].iconState = "key"
        live[0].attention = true
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertEqual(try line(1), before)
        XCTAssertTrue(table.moves.isEmpty)
        workspace.menuDidClose(NSMenu())
        XCTAssertEqual(try line(1), before)
        workspace.dragged = nil
        workspace.reloadSidebar()
        XCTAssertGreaterThan(workspace.sidebarAnimations, 0)
        try await settle()
        let animated = workspace.items
        workspace.reloadSidebar(force: true, allowReorder: true)
        XCTAssertEqual(workspace.items, animated)
    }

    func testMovePreservesNonzeroScrollAndSidebarKeyboardFocus() async throws {
        var live = (1...30).map { F.tmux($0, project: "api", profile: String(format: "%02d", $0), icon: "stop") }
        live.append(F.tmux(31, project: "api", icon: "bolt"))
        app.store.apply(snapshot: F.snapshot(live))
        workspace.activate(try XCTUnwrap(workspace.state.row(.embedded(F.runtimeID(20)))))
        workspace.window.makeFirstResponder(table)
        table.scrollRowToVisible(try line(20))
        let position = try XCTUnwrap(table.enclosingScrollView?.contentView.bounds.origin)
        XCTAssertGreaterThan(position.y, 0)
        workspace.sidebarMotionEnabled = { true }
        table.reset()
        live[19].iconState = "bolt"
        app.store.apply(snapshot: F.snapshot(live))
        try await settle()
        XCTAssertEqual(table.fullReloads, 0)
        XCTAssertEqual(table.enclosingScrollView?.contentView.bounds.origin, position)
        XCTAssertTrue(workspace.window.firstResponder === table)
        XCTAssertEqual(table.selectedRow, try line(20))
    }

    func testMotionKeepsCollapsedGroupsClosed() async throws {
        var live = [F.tmux(1, project: "api", icon: "stop"), F.tmux(2, project: "web", icon: "bolt")]
        app.store.apply(snapshot: F.snapshot(live))
        workspace.setCollapsed(project: "api", pinned: false, true)
        workspace.sidebarMotionEnabled = { true }
        live[0].iconState = "bolt"
        app.store.apply(snapshot: F.snapshot(live))
        try await settle()
        XCTAssertFalse(workspace.items.contains {
            if case let .row(row) = $0 { return row.key == .embedded(F.runtimeID(1)) }
            return false
        })
        XCTAssertTrue(workspace.pendingSidebarFlashes.isEmpty)
    }

    func testFlashUsesMotionGateAndClearsStaticFeedback() async throws {
        let flash = WorkspaceRowFlashView()
        flash.motionEnabled = { true }
        flash.flash(pulseDuration: 0.35, pulses: 2)
        let animation = try XCTUnwrap(flash.layer?.animation(forKey: WorkspaceRowView.flashKey) as? CAKeyframeAnimation)
        XCTAssertEqual(animation.duration, 0.7, accuracy: 0.001)
        XCTAssertEqual(animation.values?.count, 5)
        flash.motionEnabled = { false }
        flash.flash(pulseDuration: 0.03, pulses: 2)
        XCTAssertNil(flash.layer?.animation(forKey: WorkspaceRowView.flashKey))
        XCTAssertEqual(flash.layer?.opacity, 1)
        XCTAssertNil(flash.hitTest(.zero))
        try await Task.sleep(for: .milliseconds(80))
        XCTAssertFalse(flash.isFlashing)
        XCTAssertEqual(flash.layer?.opacity, 0)
    }

    func testStructuralOperationsReproduceMixedIdentityChanges() throws {
        // Exhaust all orders and subsets, including insertions, of four lines.
        func arrangements(_ values: [Int]) -> [[Int]] {
            [[]] + values.flatMap { value in
                arrangements(values.filter { $0 != value }).map { [value] + $0 }
            }
        }
        let orders = arrangements([0, 1, 2, 3])
        for old in orders {
            for new in orders {
                var current = old.map { Item.section(String($0)) }
                let target = new.map { Item.section(String($0)) }
                for change in try XCTUnwrap(Item.structuralChanges(from: current, to: target)) {
                    switch change {
                    case let .remove(index): current.remove(at: index)
                    case let .insert(index, final): current.insert(target[final], at: index)
                    case let .move(source, destination): current.insert(current.remove(at: source), at: destination)
                    }
                }
                XCTAssertEqual(current, target, "\(old) → \(new)")
            }
        }
        XCTAssertNil(Item.structuralChanges(from: [.section("same"), .section("same")], to: []))
    }
}

@MainActor
final class MotionRecordingSidebar: WorkspaceSidebarView {
    var fullReloads = 0
    var moves: [(Int, Int)] = []
    var insertions = 0
    var removals = 0

    func reset() { fullReloads = 0; moves = []; insertions = 0; removals = 0 }
    override func reloadData() { fullReloads += 1; super.reloadData() }
    override func moveRow(at oldIndex: Int, to newIndex: Int) {
        moves.append((oldIndex, newIndex))
        super.moveRow(at: oldIndex, to: newIndex)
    }
    override func insertRows(at indexes: IndexSet, withAnimation animationOptions: NSTableView.AnimationOptions) {
        insertions += indexes.count
        super.insertRows(at: indexes, withAnimation: animationOptions)
    }
    override func removeRows(at indexes: IndexSet, withAnimation animationOptions: NSTableView.AnimationOptions) {
        removals += indexes.count
        super.removeRows(at: indexes, withAnimation: animationOptions)
    }
}

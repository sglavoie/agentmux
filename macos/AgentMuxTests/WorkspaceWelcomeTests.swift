import AppKit
import XCTest
@testable import AgentMux

private final class WelcomeRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release() { pending.removeFirst()() }
}

@MainActor
final class WorkspaceWelcomeTests: XCTestCase {
    private func command(_ body: @escaping ([String]) -> [String: Any]) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/fixture/amx"), launchEnvironment: LaunchEnvironment(home: "/fixture", path: "/bin"))
        command.runner = { _, args, _, _ in
            .finished(.init(status: 0, stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": body(args)]), stderr: Data()))
        }
        return command
    }

    private func report() -> [String: Any] {
        let entries = ["read", "new", "late"].map { id in
            ["id": id, "at": "2026-11-01T12:00:00Z", "kind": "completed", "title": "API", "detail": "Turn ended",
             "event": "Stop", "harness": "claude", "profile": "work", "session_id": "one", "runtime_session_id": "rs-one", "run_id": "", "trace_id": ""]
        }
        return ["entries": entries, "since": "2026-10-30T00:00:00Z", "earliest_available": "", "truncated": true, "corrupt_lines": 0, "matched": 8]
    }

    func testReturnSummaryReadsExactUnreadIDsWithoutMarkingOrOpeningTranscripts() {
        let runner = WelcomeRunner()
        var calls: [[String]] = []
        let amx = command { args in calls.append(args); return self.report() }
        let markers = WorkspaceActivityReadStore(load: { .init(read: ["read": Date()]) }, save: { _ in XCTFail("A glance must not acknowledge events") })
        let model = WorkspaceWelcomeModel(command: { amx }, runner: runner, readStore: markers)
        let start = Date()
        model.returned(at: start)
        runner.release()
        XCTAssertEqual(model.summary, "At least 2 unread updates in the past 7 days.")
        XCTAssertEqual(calls, [["session", "activity", "--json", "--days", "7", "--limit", "1000"]])
        model.dismiss()
        XCTAssertFalse(model.visible)
        model.leave(at: start)
        model.returned(at: start.addingTimeInterval(20))
        XCTAssertTrue(runner.pending.isEmpty, "Brief app switches do not resurrect a dismissed summary")
        model.leave(at: start)
        model.returned(at: start.addingTimeInterval(61))
        XCTAssertEqual(runner.pending.count, 1)
        model.close()
        runner.release()
        XCTAssertNil(model.summary, "A late read must not reopen a closed summary")
    }

    func testSentReceiptDoesNotClearAttentionAndReconnectNeverCelebrates() {
        let model = WorkspaceWelcomeModel(command: { nil })
        var expiries: [DispatchWorkItem] = []
        model.schedule = { _, work in expiries.append(work) }
        model.observe(waiting: 2, working: 1, live: true, celebrate: true)
        XCTAssertNil(model.receipt)
        model.answerSent()
        XCTAssertEqual(model.title, "Answer sent")
        XCTAssertEqual(model.waiting, 2)
        model.observe(waiting: 1, working: 2, live: true, celebrate: true)
        XCTAssertEqual(model.title, "Answer sent")
        model.observe(waiting: 0, working: 3, live: true, celebrate: true)
        XCTAssertEqual(model.title, "All caught up")
        XCTAssertEqual(model.detail, "3 agents are still working.")
        expiries[0].perform()
        XCTAssertEqual(model.title, "All caught up", "An older receipt cannot dismiss the newer one")
        model.observe(waiting: 1, working: 2, live: true, celebrate: true)
        XCTAssertNil(model.receipt, "New attention invalidates all caught up immediately")
        model.observe(waiting: 0, working: 0, live: false, celebrate: true)
        XCTAssertEqual(model.attentionText, "Waiting status unavailable")
        model.observe(waiting: 0, working: 2, live: true, celebrate: true)
        XCTAssertNil(model.receipt, "Reconnection is not evidence of resolving a question")
    }

    func testReadMarkerFailureIsNotPresentedAsAnExactUnreadCount() {
        let runner = WelcomeRunner()
        let amx = command { _ in self.report() }
        let model = WorkspaceWelcomeModel(command: { amx }, runner: runner,
            readStore: .init(load: { throw CocoaError(.fileReadNoPermission) }, save: { _ in XCTFail() }))
        model.refresh(); runner.release()
        XCTAssertTrue(model.detail.contains("Read status is unavailable"))
    }

    func testPaletteKeepsShortcutAndUnavailableReasonVisibleTogether() {
        let cell = WorkspaceSwitcherCellView()
        cell.show(.init(place: .command(.newShell), glyph: "›", title: "New Shell", detail: "⌘T", unavailableReason: "Session unavailable"))
        XCTAssertEqual(cell.shortcutLabel.stringValue, "⌘T")
        XCTAssertEqual(cell.detailLabel.stringValue, "Session unavailable")
        cell.show(.init(place: .view("shell"), glyph: "›", title: "Shell", detail: "API project"))
        XCTAssertTrue(cell.shortcutLabel.isHidden)
        XCTAssertEqual(cell.detailLabel.stringValue, "API project")
    }

    func testReceiptAndLiveCountsKeepTerminalFocusAndBounds() throws {
        let app = FakeWorkspaceApp()
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        app.store.onChange = { [weak workspace] in workspace?.refresh() }
        workspace.openForTesting()
        defer { workspace.window.close() }
        var session = WorkspaceFixture.tmux(1, project: "API", icon: "key", attention: true)
        app.store.apply(snapshot: WorkspaceFixture.snapshot([session]))
        workspace.select(.embedded(WorkspaceFixture.runtimeID(1)), focus: true)
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        let terminal = try XCTUnwrap(workspace.terminalView(for: WorkspaceFixture.runtimeID(1)))
        let bounds = terminal.bounds
        workspace.welcome.answerSent()
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        XCTAssertEqual(workspace.welcome.waiting, 1)
        XCTAssertEqual(terminal.bounds, bounds)
        XCTAssertTrue(workspace.window.firstResponder === terminal)
        workspace.welcome.onAction(.nextWaiting)
        XCTAssertEqual(app.performedCommands, [.nextWaiting])
        session.attention = false; session.iconState = "bolt"
        app.store.apply(snapshot: WorkspaceFixture.snapshot([session]))
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        XCTAssertEqual(workspace.welcome.waiting, 0)
        XCTAssertEqual(terminal.bounds, bounds)
        XCTAssertTrue(workspace.window.firstResponder === terminal)
    }

    func testSavedOutcomePreviewDebouncesNavigationAndNeverGenerates() throws {
        let runner = WelcomeRunner()
        var calls: [[String]] = []
        let amx = command { args in
            calls.append(args)
            return ["conversation": args.last!, "status": "ready", "recap": [
                "goal": ["text": "Fix authentication", "messages": [0]],
                "latest": ["text": "Added regression coverage", "messages": [4]],
                "outstanding": ["text": "Review changes", "messages": [4]],
                "generated_at": "2026-11-06T10:00:00Z", "profile": "work", "model": "fixture", "partial": false]]
        }
        let recap = WorkspaceRecapModel(command: { amx }, runner: runner, automaticallyGenerate: false)
        let view = WorkspaceSessionContextView(recap: recap)
        var pending: [DispatchWorkItem] = []
        view.schedulePreview = { pending.append($0) }
        func context(_ n: Int) -> WorkspaceSessionContext {
            let session = WorkspaceFixture.tmux(n, project: "API", conversation: "conversation-\(n)")
            let row = WorkspaceSidebar.row(session, now: Date(), glyphs: .fallback)
            return .init(key: row.key, row: row, session: session, content: .terminal(runtimeSessionID: row.key.runtimeSessionID), current: true)
        }
        view.show(context(1), canAnswer: false)
        view.show(context(2), canAnswer: false)
        pending[0].perform()
        XCTAssertTrue(runner.pending.isEmpty)
        pending[1].perform()
        runner.release()
        XCTAssertEqual(view.outcome.title, "Last: Added regression coverage")
        XCTAssertTrue(view.outcome.toolTip?.contains("Saved") == true)
        XCTAssertEqual(calls, [["session", "recap", "--cached", "--json", "--", "claude:conversation-2"]])
        view.show(context(3), canAnswer: false)
        XCTAssertEqual(view.outcome.title, "Last outcome", "Never show another conversation's result")
        pending[2].perform()
        view.show(nil, canAnswer: false)
        runner.release()
        XCTAssertNil(recap.report)
    }
}

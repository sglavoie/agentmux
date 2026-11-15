import AppKit
import XCTest
@testable import AgentMux

final class ExperienceRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func drain() { while !pending.isEmpty { pending.removeFirst()() } }
}

enum ExperienceFixture {
    static func command(_ body: @escaping ([String]) -> [String: Any]) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/fixture/amx"), launchEnvironment: LaunchEnvironment(home: "/fixture", path: "/bin"))
        command.runner = { _, args, _, _ in
            .finished(.init(status: 0, stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": body(args)]), stderr: Data()))
        }
        return command
    }
    static func event(_ id: String, runtime: String, at: String) -> [String: Any] {
        ["id": id, "at": at, "kind": "completed", "title": "API", "detail": "Turn finished", "event": "Stop", "harness": "claude", "profile": "work", "session_id": "one", "runtime_session_id": runtime, "run_id": "", "trace_id": ""]
    }
    static func report(_ entries: [[String: Any]]) -> [String: Any] {
        ["entries": entries, "since": "2026-11-01T00:00:00Z", "earliest_available": "", "truncated": false, "corrupt_lines": 0, "matched": entries.count]
    }
    static func recap(_ identity: String) -> [String: Any] {
        ["conversation": identity, "status": "ready", "recap": [
            "goal": ["text": "Fix login redirect", "messages": [0]],
            "latest": ["text": "Added regression coverage for expired sessions.", "messages": [2]],
            "outstanding": ["text": "Review the redirect behavior.", "messages": [2]],
            "generated_at": "2026-11-01T12:05:00Z", "profile": "work", "model": "fixture", "partial": false]]
    }
}

@MainActor
final class WorkspaceExperienceTests: XCTestCase {
    private typealias F = WorkspaceFixture

    func testPeekDiscardsLateQuestionAndRecapReplies() {
        let runner = ExperienceRunner()
        var calls: [[String]] = []
        let command = ExperienceFixture.command { args in calls.append(args); return ExperienceFixture.recap(args.last!) }
        let model = WorkspaceSessionPeekModel(command: { command }, runner: runner)
        let sessions = [F.tmux(1, project: "API", icon: "key", attention: true), F.tmux(2, project: "Web", icon: "question", attention: true)]
        let waiting = WorkspaceInspector.waiting(sessions, glyphs: .fallback)
        var first = WorkspaceSidebar.row(sessions[0], now: .now, glyphs: .fallback)
        first.conversation = .init(harness: "claude", sessionID: "one")
        var second = WorkspaceSidebar.row(sessions[1], now: .now, glyphs: .fallback)
        second.conversation = .init(harness: "codex", sessionID: "two")
        var replies: [(WorkspaceWaitingContext.Result) -> Void] = []
        model.readContext = { _, completion in replies.append(completion) }
        model.show(first, waiting: waiting.first { $0.key == first.key }, current: true)
        model.show(second, waiting: waiting.first { $0.key == second.key }, current: true)
        replies[0](.loaded(SecretText("Old question")))
        XCTAssertNil(model.reading.content)
        replies[1](.loaded(SecretText("Choose a layout")))
        runner.drain()
        XCTAssertEqual(model.reading.content?.reveal(), "Choose a layout")
        XCTAssertEqual(model.recap.report?.conversation, "codex:two")
        XCTAssertTrue(calls.allSatisfy { $0.contains("--cached") })
        model.show(second, waiting: waiting.first { $0.key == second.key }, current: false)
        XCTAssertNil(model.reading.content, "Disconnected questions must not look current")
        model.close()
        XCTAssertNil(model.row)
    }

    func testPeekingAtAnotherAgentKeepsTerminalFocusBoundsAndSelection() throws {
        let app = FakeWorkspaceApp()
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        workspace.openForTesting(); workspace.window.makeKeyAndOrderFront(nil)
        defer { workspace.window.close() }
        app.store.apply(snapshot: F.snapshot([F.tmux(1, project: "API"), F.tmux(2, project: "Web")]))
        workspace.refresh(); workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.window.layoutIfNeeded()
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        let bounds = terminal.bounds, place = workspace.place
        workspace.showSessionPeek(.embedded(F.runtimeID(2)))
        XCTAssertTrue(workspace.peekPopover.popover.isShown)
        XCTAssertEqual(workspace.sessionPeek.row?.key, .embedded(F.runtimeID(2)))
        XCTAssertEqual(workspace.place, place)
        XCTAssertTrue(workspace.window.firstResponder === terminal)
        XCTAssertEqual(terminal.bounds, bounds)
        XCTAssertNil(workspace.terminalView(for: F.runtimeID(2)), "A peek must never attach another terminal")
        workspace.peekPopover.close()
        XCTAssertTrue(workspace.window.firstResponder === terminal)
        workspace.toggleSwitcher()
        workspace.switcher.enter("Web")
        let editor = workspace.window.firstResponder
        workspace.switcher.peekSelected()
        XCTAssertTrue(workspace.isSwitching)
        XCTAssertTrue(workspace.peekPopover.popover.isShown)
        XCTAssertEqual(workspace.switcher.field.stringValue, "Web")
        XCTAssertTrue(workspace.window.firstResponder === editor)
        XCTAssertEqual(workspace.place, place)
        workspace.peekPopover.close()
        workspace.closeSwitcher(restoring: true)
        XCTAssertTrue(workspace.window.firstResponder === terminal)
    }

    func testSentAnswerIsHeldUntilQuestionChangesAndNextSkipsIt() throws {
        let inspector = WorkspaceInspectorView()
        let sessions = [F.tmux(1, project: "API", icon: "key", attention: true), F.tmux(2, project: "Web", icon: "question", attention: true)]
        let rows = WorkspaceInspector.waiting(sessions, glyphs: .fallback)
        var model = WorkspaceInspectorView.Model(tab: .waiting, waiting: rows, inspection: nil, current: true, feedNotice: nil)
        inspector.show(model)
        let first = rows[0]
        var opened: [AttentionCycler.Key] = []
        inspector.onWaiting = { opened.append($0) }
        inspector.markAnswerSent(first)
        XCTAssertTrue(opened.isEmpty, "With continuation off, marking an answer sent stays passive; only app orchestration may advance")
        let view = try XCTUnwrap(inspector.lines.compactMap { $0 as? WorkspaceWaitingRowView }.first { $0.row.key == first.key })
        XCTAssertEqual(view.answerButton?.title, "Answer sent")
        XCTAssertEqual(view.answerButton?.isEnabled, false)
        inspector.nextWaiting()
        XCTAssertEqual(opened, [rows[1].key])
        model.current = false; inspector.show(model)
        XCTAssertTrue(inspector.answerWasSent(first))
        model.current = true
        model.waiting[0].contextVersion += ":new-question"
        inspector.show(model)
        XCTAssertFalse(inspector.answerWasSent(model.waiting[0]))
        XCTAssertEqual(inspector.remainingWaiting.count, 2)
    }

    func testSuggestedTaskNameNeverOverwritesTypingAndRejectsWrongConversation() {
        let runner = ExperienceRunner()
        var row = WorkspaceSidebar.row(F.tmux(1, project: "API"), now: .now, glyphs: .fallback)
        row.conversation = .init(harness: "claude", sessionID: "one")
        row.branch = "fix/login-redirect"
        var identity = "one"
        let command = ExperienceFixture.command { _ in
            ["session": ["id": identity, "harness": "claude", "title": "Fix expired session redirect"], "messages": [], "total": 0, "start": 0]
        }
        let model = WorkspaceTaskName(row: row, command: { command }, runner: runner)
        model.load(); model.name = "My edited name"; runner.drain()
        XCTAssertEqual(model.name, "My edited name")
        XCTAssertEqual(model.suggestion, "Fix expired session redirect")
        identity = "other"
        let mismatched = WorkspaceTaskName(row: row, command: { command }, runner: runner)
        mismatched.load(); runner.drain()
        XCTAssertEqual(mismatched.suggestion, "Fix login redirect")
    }

    func testTaskIdentityIsConsistentAcrossSwitcherWaitingAndNotification() {
        var session = F.tmux(1, project: "API", icon: "key", attention: true)
        session.name = "Fix login redirect"
        let row = WorkspaceSidebar.row(session, now: .now, glyphs: .fallback)
        XCTAssertEqual(WorkspaceSwitcher.entries(rows: [row], views: []).first?.title, session.name)
        XCTAssertEqual(WorkspaceInspector.waiting([session], glyphs: .fallback).first?.title, session.name)
        XCTAssertEqual(row.project, "API", "Task names must not change project grouping")
        var notice = AppNotice(notificationID: "test", title: "Permission requested", subtitle: "Allow tests?")
        notice.runtimeSessionID = F.runtimeID(1); notice.backend = CompanionRuntime.tmuxBackend
        let named = notice.namingTask(from: [session])
        XCTAssertEqual(named.title, session.name)
        XCTAssertTrue(named.subtitle.contains("Permission requested"))
        XCTAssertEqual(named.runtimeSessionID, notice.runtimeSessionID)
        notice.runtimeSessionID = F.runtimeID(2)
        XCTAssertEqual(notice.namingTask(from: [session]).title, "Permission requested")
    }
}

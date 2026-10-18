import AppKit
import SwiftUI
import XCTest
@testable import AgentMux

private final class ClarityRunner: ASLWorkRunner {
    var hold = false
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) {
        let value = work()
        if hold { pending.append { then(value) } } else { then(value) }
    }
    func drain() { while !pending.isEmpty { pending.removeFirst()() } }
}

@MainActor
final class WorkspaceClarityPolishTests: XCTestCase {
    private func command(_ body: @escaping ([String]) -> [String: Any]?) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in
            guard let data = body(args) else { return .finished(.init(status: 1, stdout: Data(), stderr: Data("Read failed".utf8))) }
            return .finished(.init(status: 0, stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": data]), stderr: Data()))
        }
        return command
    }
    private func history(_ args: [String]) -> [String: Any] {
        if args[1] == "show" {
            return ["session": ["id": "one", "harness": "claude"], "messages_total": 50, "messages_start": 0,
                    "messages": (0..<50).map { ["role": "assistant", "text": "Reading message \($0) without losing my place."] }]
        }
        return ["sessions": [["id": "one", "harness": "claude", "project": "A very long project title for narrow windows", "cwd": "/repo"], ["id": "two", "harness": "claude", "project": "Second"]]]
    }
    private func views(_ view: NSView) -> [NSView] { [view] + view.subviews.flatMap(views) }
    private func key(_ text: String, window: NSWindow) -> NSEvent {
        NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [.command, .option], timestamp: 0,
                         windowNumber: window.windowNumber, context: nil, characters: text, charactersIgnoringModifiers: text,
                         isARepeat: false, keyCode: 1)!
    }

    func testHistoryLayoutRestoresAndClampsWithoutOverwritingPreferredWidth() throws {
        let suite = "amx.clarity.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let model = WorkspaceHistoryModel(command: { nil }, defaults: defaults)
        XCTAssertLessThan(model.layout.width(in: 696), 220)
        model.layout.resultsWidth = 380
        model.layout.collapsed = true
        let restored = WorkspaceHistoryModel(command: { nil }, defaults: defaults)
        XCTAssertTrue(restored.layout.collapsed)
        XCTAssertEqual(restored.layout.resultsWidth, 380)
        XCTAssertEqual(restored.layout.width(in: 696), 316)
        XCTAssertEqual(restored.layout.width(in: 1100), 380)
        restored.layout.resultsWidth = .nan
        XCTAssertTrue(restored.layout.width(in: 696).isFinite)
    }

    func testHistoryToggleKeepsSelectionAndReadingAnchorAndReturnsFocusToResults() async throws {
        let amx = command(history)
        let model = WorkspaceHistoryModel(command: { amx }, runner: ClarityRunner())
        model.search()
        let host = WorkspaceReviewHost(rootView: WorkspaceHistoryView(model: model))
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 720, height: 428), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host; window.makeKeyAndOrderFront(nil)
        defer { window.close() }
        try await Task.sleep(for: .milliseconds(200))
        let scroll = try XCTUnwrap(views(host).compactMap { $0 as? NSScrollView }.max { $0.convert(.zero, to: host).x < $1.convert(.zero, to: host).x })
        XCTAssertGreaterThan(scroll.frame.width, 440, "the transcript gets most of a narrow window")
        model.rememberVisibleMessage(12)
        let selection = model.selection
        XCTAssertTrue(host.performKeyEquivalent(with: key("s", window: window)))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertTrue(model.layout.collapsed)
        XCTAssertEqual(model.selection, selection)
        XCTAssertEqual(model.visibleMessage, 12)
        XCTAssertTrue(host.performKeyEquivalent(with: key("s", window: window)))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertFalse(model.layout.collapsed)
        XCTAssertEqual(model.selection, selection)
        let responder = try XCTUnwrap(window.firstResponder as? NSView)
        XCTAssertLessThan(responder.convert(.zero, to: host).x, 230, "reopening returns keyboard focus to the results")
    }

    func testIndependentHistoryFreshnessSurvivesFailedAndSupersededReads() throws {
        let runner = ClarityRunner()
        var failSearch = false, failTranscript = false
        let amx = command { args in
            if args[1] == "show" ? failTranscript : failSearch { return nil }
            return self.history(args)
        }
        let model = WorkspaceHistoryModel(command: { amx }, runner: runner)
        model.search()
        let searchTime = try XCTUnwrap(model.resultsRefreshedAt)
        let readTime = try XCTUnwrap(model.transcriptRefreshedAt)
        failSearch = true; model.search()
        XCTAssertEqual(model.resultsRefreshedAt, searchTime)
        XCTAssertEqual(model.transcriptRefreshedAt, readTime)
        failTranscript = true; model.refreshTranscript()
        XCTAssertEqual(model.transcriptRefreshedAt, readTime)
        failTranscript = false; runner.hold = true
        model.refreshTranscript(); model.select("claude:two")
        XCTAssertNil(model.transcriptRefreshedAt)
        model.close(); runner.drain()
        XCTAssertNil(model.transcriptRefreshedAt, "late reads cannot refresh a replacement selection")
    }

    func testFeedbackSurvivesAnotherActionAndRecoveryReadsOriginalConversation() throws {
        var requests: [[String]] = []
        let amx = command { args in requests.append(args); return self.history(args) }
        let history = WorkspaceFeedbackHistory()
        let model = WorkspaceHistoryModel(command: { amx }, runner: ClarityRunner(), feedbackHistory: history)
        var actions = 0
        model.onAction = { _, _, done in actions += 1; done("Outcome unconfirmed") }
        model.search(); model.act(fork: false)
        let first = try XCTUnwrap(history.entries.first)
        model.select("claude:two"); model.act(fork: true)
        XCTAssertEqual(history.entries.count, 2)
        XCTAssertTrue(first.feedback.target.contains("claude:one"))
        XCTAssertLessThanOrEqual(first.occurred, history.entries[0].occurred)
        requests = []; first.recover()
        XCTAssertEqual(actions, 2, "inspecting feedback must not repeat resume or fork")
        XCTAssertEqual(model.currentConversation?.encoded, "claude:one")
        XCTAssertTrue(requests[0].contains("--conversation"))
        XCTAssertTrue(requests[0].contains("claude:one"))
        history.dismiss(first.id)
        XCTAssertEqual(history.entries.count, 1)
    }

    func testFeedbackIsBoundedAndKeepsNewestExactTargets() {
        let history = WorkspaceFeedbackHistory()
        for i in 0..<40 { history.record(.init(target: "checkout-\(i)", message: "Failure \(i)")) }
        XCTAssertEqual(history.entries.count, 30)
        XCTAssertEqual(history.entries.first?.feedback.target, "checkout-39")
        XCTAssertEqual(history.entries.last?.feedback.target, "checkout-10")
    }

    func testFreshnessExplainsObservationAgeAndClampsClockSkew() {
        let at = Date(timeIntervalSince1970: 1000)
        XCTAssertEqual(WorkspaceFreshness.text(at: at, now: at.addingTimeInterval(125), retained: true), "Showing results from 2m ago")
        XCTAssertEqual(WorkspaceFreshness.text(at: at, now: at.addingTimeInterval(-60), retained: false), "Refreshed just now")
    }

    func testVisibleReasonsAgreeAndDiagnosticReasonStaysExact() {
        for (reason, icon, expected) in [("permission", "key", "Needs permission"), ("elicitation", "question", "Waiting for your answer"), ("done", "stop", "Turn complete")] {
            var session = WorkspaceFixture.tmux(1, project: "api", icon: icon, attention: true)
            session.reason = reason
            let sidebar = WorkspaceSidebar.row(session, snapshotSocket: "", now: Date(), glyphs: .fallback)
            let waiting = WorkspaceInspector.waiting([session], glyphs: .fallback)
            XCTAssertTrue(sidebar.detail.contains(expected))
            XCTAssertTrue(waiting[0].detail.contains(expected))
            XCTAssertEqual(session.reason, reason)
        }
        XCTAssertEqual(WorkspaceWording.status("new_protocol_state"), "New protocol state")
    }
}

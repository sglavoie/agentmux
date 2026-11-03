import AppKit
import SwiftUI
import XCTest
@testable import AgentMux

private final class RecapRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release(_ index: Int = 0) { pending.remove(at: index)() }
}

@MainActor
final class WorkspaceRecapTests: XCTestCase {
    private func payload(_ id: String, status: String, saved: Bool = false, warning: String? = nil) -> Data {
        var data: [String: Any] = ["conversation": id, "status": status]
        if saved {
            data["recap"] = ["goal": ["text": "Implement a feature", "messages": [0]],
                             "latest": ["text": "Agent reports tests passed", "messages": [42]],
                             "outstanding": ["text": "Check keyboard navigation", "messages": [42]],
                             "generated_at": "2026-10-31T12:00:00Z", "profile": "personal", "model": "haiku", "partial": true]
        }
        if let warning { data["warning"] = warning }
        return try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": data])
    }

    private func command(_ response: @escaping ([String]) -> Data) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/fixture", path: "/bin"))
        command.runner = { _, args, _, _ in .finished(.init(status: 0, stdout: response(args), stderr: Data())) }
        return command
    }

    func testReadsCacheFirstThenGeneratesAfterDebounceAndLinksEvidence() {
        let runner = RecapRunner()
        var calls: [[String]] = [], scheduled: [DispatchWorkItem] = []
        let command = command { args in
            calls.append(args)
            return self.payload("codex:a", status: args.contains("--cached") ? "missing" : "ready", saved: !args.contains("--cached"))
        }
        let model = WorkspaceRecapModel(command: { command }, runner: runner)
        model.schedule = { _, work in scheduled.append(work) }
        model.select(.init(harness: "codex", sessionID: "a"), revision: "1")
        runner.release()
        XCTAssertEqual(calls.count, 1); XCTAssertTrue(calls[0].contains("--cached"))
        XCTAssertEqual(scheduled.count, 1)
        scheduled.removeFirst().perform(); runner.release()
        XCTAssertEqual(model.report?.recap?.latest.messages, [42])
        XCTAssertEqual(model.report?.recap?.latest.text.reveal(), "Agent reports tests passed")
        var revealed: (ConversationIdentity, Int)?
        model.onEvidence = { revealed = ($0, $1) }
        model.reveal(42)
        XCTAssertEqual(revealed?.0.encoded, "codex:a"); XCTAssertEqual(revealed?.1, 42)
        model.select(.init(harness: "codex", sessionID: "a"), revision: "1")
        XCTAssertTrue(runner.pending.isEmpty, "unchanged selection does not make requests")
        model.check(); runner.release()
        XCTAssertEqual(model.report?.status, "missing")
        model.close()
        scheduled.forEach { $0.perform() }
        XCTAssertTrue(runner.pending.isEmpty, "closing cancels queued generation")
    }

    func testSavedRecapDisplaysImmediatelyAndRefreshUsesExplicitRetry() {
        let runner = RecapRunner()
        var calls: [[String]] = []
        let command = command { args in calls.append(args); return self.payload("claude:a", status: "ready", saved: true) }
        let model = WorkspaceRecapModel(command: { command }, runner: runner)
        model.select(.init(harness: "claude", sessionID: "a"), revision: "1"); runner.release()
        XCTAssertNotNil(model.report?.recap)
        XCTAssertTrue(runner.pending.isEmpty)
        model.refresh(); runner.release()
        XCTAssertTrue(calls.last!.contains("--refresh"))
        XCTAssertFalse(calls.last!.contains("--cached"))
    }

    func testSwitchingAndClosingDiscardLateRepliesAndDebouncedWork() {
        let runner = RecapRunner()
        let command = command { args in self.payload(args[args.count - 2], status: "ready", saved: true) }
        let model = WorkspaceRecapModel(command: { command }, runner: runner)
        model.select(.init(harness: "claude", sessionID: "a"), revision: "1")
        model.select(.init(harness: "codex", sessionID: "b"), revision: "1")
        runner.release()
        XCTAssertNil(model.report)
        runner.release()
        XCTAssertEqual(model.report?.conversation, "codex:b")
        model.check(); model.close(); runner.release()
        XCTAssertNil(model.report); XCTAssertNil(model.conversation)
    }

    func testWorkingAgentNeverStartsGenerationAndFailureRetainsSavedRecap() {
        let runner = RecapRunner()
        var scheduled: [DispatchWorkItem] = [], fail = false
        var command = command { _ in self.payload("claude:a", status: "stale", saved: true) }
        let successfulRunner = command.runner
        command.runner = { binary, args, env, timeout in
            if fail { return .timedOut }
            return successfulRunner(binary, args, env, timeout)
        }
        let model = WorkspaceRecapModel(command: { command }, runner: runner)
        model.schedule = { _, work in scheduled.append(work) }
        model.select(.init(harness: "claude", sessionID: "a"), revision: "1", canGenerate: false)
        runner.release()
        XCTAssertTrue(scheduled.isEmpty)
        model.select(.init(harness: "claude", sessionID: "a"), revision: "2", canGenerate: true)
        runner.release(); XCTAssertEqual(scheduled.count, 1)
        fail = true
        scheduled.removeFirst().perform(); runner.release()
        XCTAssertNotNil(model.error); XCTAssertNotNil(model.report?.recap)
        XCTAssertEqual(model.report?.status, "stale")
    }

    func testMismatchedConversationIsRejectedAndViewCanRender() {
        let runner = RecapRunner()
        let command = command { _ in self.payload("copilot:wrong", status: "ready", saved: true) }
        let model = WorkspaceRecapModel(command: { command }, runner: runner)
        model.select(.init(harness: "claude", sessionID: "a"), revision: "1"); runner.release()
        XCTAssertNil(model.report); XCTAssertNotNil(model.error)
        let view = NSHostingView(rootView: WorkspaceRecapView(model: model))
        view.frame = NSRect(x: 0, y: 0, width: 250, height: 400)
        view.layoutSubtreeIfNeeded()
        XCTAssertGreaterThan(view.fittingSize.height, 0)
    }

    func testInspectorReservesRecapHeightWhenTextWrapsAndContentChanges() async throws {
        let inspector = WorkspaceInspectorView()
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 280, height: 400),
                              styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.contentView = inspector
        window.orderFront(nil)
        defer { inspector.recap.close(); window.close() }
        inspector.show(.init(tab: .session, inspection: .init(key: .embedded("recap-test"), title: "Session details",
                                                             glyph: "", sections: [], embedded: true, muted: false)))
        let warning = "Error: indexed conversation codex:01a1a25a-27f6-7531-91ea-7c60c8c2801e was not found; to find a conversation by what was said in it, run `amx session search --json`"
        for saved in [false, true, false] {
            let loaded = expectation(description: "Recap content loaded")
            let subscription = inspector.recap.$report.sink { report in
                if (report?.recap != nil) == saved, report?.warning == warning { loaded.fulfill() }
            }
            let command = command { _ in self.payload("codex:a", status: "ready", saved: saved, warning: warning) }
            inspector.recap.command = { command }
            inspector.recap.select(.init(harness: "codex", sessionID: "a"), revision: UUID().uuidString)
            await fulfillment(of: [loaded], timeout: 3)
            subscription.cancel()

            for width in [CGFloat(280), 420, 280] {
                window.setContentSize(NSSize(width: width, height: 400))
                // Allow SwiftUI's observed update and AppKit's intrinsic-size
                // invalidation to reach the enclosing document stack.
                try await Task.sleep(for: .milliseconds(50))
                window.layoutIfNeeded()
                let title = try XCTUnwrap(inspector.lines.first)
                let stack = try XCTUnwrap(title.superview as? NSStackView)
                let host = try XCTUnwrap(stack.arrangedSubviews.compactMap { $0 as? NSHostingView<WorkspaceInspectorRecapView> }.first)
                let measurement = NSHostingController(rootView: WorkspaceRecapView(model: inspector.recap))
                let expected = measurement.sizeThatFits(in: NSSize(width: host.bounds.width, height: .greatestFiniteMagnitude))
                XCTAssertEqual(host.bounds.height, expected.height, accuracy: 1,
                               "The recap must reserve its full wrapped height at width \(width)")
                XCTAssertGreaterThanOrEqual(title.frame.minY, host.frame.maxY,
                                            "The session details must start below the recap")
            }
        }
    }

    func testHistoryOpensExactSupportingMessageFromInspectorAndWithinHistory() {
        let runner = RecapRunner()
        var calls: [[String]] = []
        let command = command { args in
            calls.append(args)
            let data: [String: Any]
            if args[1] == "show" {
                data = ["session": ["id": "a", "harness": "codex"], "messages_total": 60, "messages_start": 32,
                        "messages": (32..<60).map { ["role": "assistant", "text": "Message \($0)"] }]
            } else { data = ["sessions": [["id": "a", "harness": "codex"]]] }
            return try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": data])
        }
        let history = WorkspaceHistoryModel(command: { command }, runner: runner)
        history.open(project: "", conversation: .init(harness: "codex", sessionID: "a"), recapMessage: 42)
        runner.release(); runner.release()
        XCTAssertEqual(history.selection, "codex:a")
        XCTAssertEqual(history.highlightedMessage, 42)
        XCTAssertEqual(history.scrollRequest?.message, 42)
        XCTAssertEqual(calls.last, ["session", "show", "--json", "--from", "32", "--count", "50", "--", "codex:a"])
        history.revealRecapMessage(45); runner.release()
        XCTAssertEqual(history.highlightedMessage, 45)
        XCTAssertEqual(history.scrollRequest?.message, 45)
        XCTAssertTrue(calls.last!.contains("35"))
    }

    func testLateTranscriptWriteRetriesOnceAutomatically() {
        let runner = RecapRunner()
        var scheduled: [DispatchWorkItem] = [], generations = 0
        let command = command { args in
            if args.contains("--cached") { return self.payload("codex:a", status: "missing") }
            generations += 1
            return self.payload("codex:a", status: "stale", warning: "Conversation changed during generation")
        }
        let model = WorkspaceRecapModel(command: { command }, runner: runner)
        model.schedule = { _, item in scheduled.append(item) }
        model.select(.init(harness: "codex", sessionID: "a"), revision: "1")
        runner.release()
        scheduled.removeFirst().perform(); runner.release()
        XCTAssertEqual(generations, 1)
        XCTAssertEqual(scheduled.count, 1, "the late transcript write is retried automatically")
        scheduled.removeFirst().perform(); runner.release()
        scheduled.removeFirst().perform(); runner.release()
        XCTAssertEqual(generations, 2)
        XCTAssertTrue(scheduled.isEmpty, "a continuously changing source cannot cause an unbounded model loop")
    }

    func testBriefingPollsExistingWorkerWithoutGeneratingWhenResultIsMissing() {
        let runner = RecapRunner()
        var scheduled: [DispatchWorkItem] = [], calls: [[String]] = [], status = "pending"
        let command = command { args in
            calls.append(args)
            return self.payload("codex:a", status: status)
        }
        let model = WorkspaceRecapModel(command: { command }, runner: runner, automaticallyGenerate: false)
        model.schedule = { _, work in scheduled.append(work) }
        model.select(.init(harness: "codex", sessionID: "a"), revision: "1")
        runner.release()
        XCTAssertEqual(scheduled.count, 1, "a request already running in another view is polled")
        status = "missing"
        scheduled.removeFirst().perform(); runner.release()
        XCTAssertTrue(scheduled.isEmpty, "a missing result cannot turn a briefing read into a model request")
        XCTAssertTrue(calls.allSatisfy { $0.contains("--cached") })
        model.refresh(); runner.release()
        XCTAssertTrue(calls.last!.contains("--refresh"), "per-card generation remains explicit")
        model.close()
    }
}

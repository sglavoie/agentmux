import AppKit
import SwiftUI
import XCTest
@testable import AgentMux

private final class ConsistencyRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func finish() { pending.removeFirst()() }
    func drain() { while !pending.isEmpty { finish() } }
}

private final class ConsistencyASL: ASLService {
    var reads = 0
    var row: ASLRow = {
        var row = ASLRow(); row.rowID = "one"; row.runID = "run-one"; row.planDir = "/plan"
        row.primaryState = "paused"
        return row
    }()
    func snapshot() -> Result<ASLResult, ASLRequestError> {
        reads += 1
        return .success(ASLResult(status: ASLStatus(available: true, mutationsEnabled: true), snapshot: ASLSnapshot(rows: [row]), meta: ASLSnapshotMeta()))
    }
    func question(planDir: String, questionID: String) -> Result<ASLQuestionBody, ASLRequestError> { .failure(.unsupported) }
    func act(_ params: ASLRequestParams) -> Result<ASLOperation, ASLRequestError> { XCTFail("Review navigation must not mutate a run"); return .failure(.unsupported) }
}

@MainActor
final class WorkspaceConsistencyTests: XCTestCase {
    private func command(_ response: @escaping ([String]) -> [String: Any]) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in
            .finished(.init(status: 0, stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": response(args)]), stderr: Data()))
        }
        return command
    }
    private func descendants(_ view: NSView) -> [NSView] { [view] + view.subviews.flatMap(descendants) }
    private func settle(_ window: NSWindow) async throws {
        window.layoutIfNeeded(); try await Task.sleep(for: .milliseconds(450))
    }
    private func key(_ text: String, modifiers: NSEvent.ModifierFlags = .command, in window: NSWindow) throws -> NSEvent {
        try XCTUnwrap(NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: modifiers, timestamp: 0,
            windowNumber: window.windowNumber, context: nil, characters: text, charactersIgnoringModifiers: text, isARepeat: false, keyCode: 0))
    }

    func testHistoryReadRecoveryRemainsAvailableAfterSearchFailureAndTargetsOriginalAction() {
        let runner = ConsistencyRunner()
        var available = true
        let amx = command { args in
            if args[1] == "show" { return ["session": ["id": "one", "harness": "claude"], "messages_total": 1, "messages": [["role": "assistant", "text": "Previous output"]]] }
            return ["sessions": [["id": "one", "harness": "claude"], ["id": "two", "harness": "claude"]]]
        }
        let model = WorkspaceHistoryModel(command: { available ? amx : nil }, runner: runner)
        model.search(); runner.drain()
        var completion: ((String?) -> Void)?
        model.onAction = { _, _, done in completion = done }
        model.act(fork: true)
        model.select("claude:two"); runner.drain()
        completion?("Could not open the conversation")
        XCTAssertTrue(model.actionFeedback?.target.contains("claude:one") == true)
        XCTAssertEqual(model.actionFeedback?.kind, .error)
        XCTAssertEqual(model.actionFeedback?.recovery, .refresh)
        available = false; model.search()
        XCTAssertFalse(model.canAct)
        XCTAssertTrue(model.canRefreshTranscript)
        available = true; model.refreshTranscript()
        XCTAssertTrue(model.reading)
        runner.drain()
        XCTAssertNil(model.transcriptError)
        XCTAssertNotNil(model.error, "transcript recovery does not pretend the failed search recovered")
    }

    func testHistoryRefreshKeepsListAndTranscriptFramesThroughLoadingAndFailure() async throws {
        let runner = ConsistencyRunner()
        var available = true
        let amx = command { args in
            if args[1] == "show" { return ["session": ["id": "one", "harness": "claude"], "messages_total": 80,
                "messages": (0..<80).map { ["role": "assistant", "text": "Output \($0)"] }] }
            return ["sessions": [["id": "one", "harness": "claude", "cwd": "/checkout"]]]
        }
        let model = WorkspaceHistoryModel(command: { available ? amx : nil }, runner: runner)
        model.search(); runner.drain()
        let controller = WorkspaceHistoryWindow(model: model)
        controller.show(project: "", conversation: nil); runner.drain()
        let window = try XCTUnwrap(controller.window)
        defer { window.close() }
        try await settle(window)
        func frames() -> [NSRect] {
            descendants(window.contentView!).compactMap { $0 as? NSScrollView }
                .filter { $0.frame.height > 100 }.map { $0.convert($0.bounds, to: window.contentView) }
        }
        let baseline = frames()
        XCTAssertGreaterThanOrEqual(baseline.count, 2)
        model.refreshTranscript(); try await settle(window)
        XCTAssertEqual(frames(), baseline, "transcript loading must not push its scroll view")
        runner.drain()
        model.scheduleSearch(); try await settle(window)
        XCTAssertEqual(frames(), baseline, "searching must not push results or transcript")
        runner.drain()
        available = false; model.search(); try await settle(window)
        XCTAssertEqual(frames(), baseline, "a search failure must occupy the same toolbar slot")
    }

    func testConflictedWorktreeKeepsDiffSpaceAndCannotLandDuringReviewRefresh() async throws {
        let runner = ConsistencyRunner()
        let longPath = "sources/very-long-shared-directory/another-long-component/different-file.swift"
        let amx = command { args in
            if args[0] == "worktree" {
                if args[1] == "list" { return ["worktrees": [["id": "wt-one", "path": "/checkout"]]] }
                return ["review": ["id": "wt-one", "path": "/checkout", "branch": "feature/a-long-branch-name", "source_branch": "main",
                    "merge_base": "parent", "ahead": 2, "behind": 0, "dirty": false, "lands": "fast_forward",
                    "warnings": (0..<20).map { "Long warning \($0): review the checkout before landing" }, "conflicts": (0..<20).map { "sources/component-\($0)/file.swift" }]]
            }
            return ["root": "/checkout", "base": "abc", "files": [["path": longPath, "added": 100, "deleted": 0, "binary": false, "untracked": false]],
                "patch": "@@ -0,0 +1,100 @@\n" + (0..<100).map { "+source \($0)\n" }.joined(), "truncated": false]
        }
        let model = WorkspaceChangesModel(command: { amx }, runner: runner)
        model.open(directory: "/checkout"); runner.drain(); model.diffFontSize = 20
        let host = WorkspaceReviewHost(rootView: WorkspaceChangesView(model: model))
        let style: NSWindow.StyleMask = [.titled, .resizable]
        let window = NSWindow(contentRect: NSWindow.contentRect(forFrameRect: NSRect(x: 0, y: 0, width: 720, height: 450), styleMask: style), styleMask: style, backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host; window.makeKeyAndOrderFront(nil)
        defer { window.close() }
        try await settle(window)
        let scroll = try XCTUnwrap(descendants(host).compactMap { $0 as? NSScrollView }.first { $0.documentView is WorkspaceDiffTextView })
        let frame = scroll.convert(scroll.bounds, to: host)
        XCTAssertEqual(window.frame.size, NSSize(width: 720, height: 450))
        XCTAssertGreaterThanOrEqual(scroll.contentSize.height, 240, "warnings and conflicts belong in bounded details")
        model.refresh(); try await settle(window)
        XCTAssertNotNil(model.worktree, "the previous review stays in place")
        XCTAssertFalse(model.canLand)
        XCTAssertEqual(scroll.convert(scroll.bounds, to: host), frame)
        runner.finish() // refreshed file list; review is still in flight
        XCTAssertFalse(model.canLand, "retained review is not fresh authority to land")
        runner.drain(); try await settle(window)
        XCTAssertEqual(scroll.convert(scroll.bounds, to: host), frame)
    }

    func testActivityAndRunsKeyboardRefreshFocusAndClose() async throws {
        let runner = ConsistencyRunner()
        let amx = command { args in
            if args[1] == "show" { return ["session": ["id": "one", "harness": "claude"], "messages_total": 1,
                "messages": [["role": "assistant", "timestamp": "2026-10-16T12:00:00Z", "text": "# Review\n\n```swift\nlet value = 1\n```"]]] }
            return ["entries": [["id": "event", "at": "2026-10-16T12:01:00Z", "kind": "state", "title": "Finished", "detail": "Ready for review",
                "event": "state_changed", "harness": "claude", "profile": "work", "session_id": "one", "runtime_session_id": "rs-one", "run_id": "", "trace_id": ""]],
                "since": "", "earliest_available": "", "truncated": false, "corrupt_lines": 0, "matched": 1]
        }
        let activity = WorkspaceActivityModel(command: { amx }, runner: runner, readStore: .init(load: { .init() }, save: { _ in }))
        let controller = WorkspaceActivityWindow(model: activity)
        var focused = 0, opened = 0
        activity.onFocus = { _ in focused += 1 }; activity.onConversation = { _ in opened += 1 }
        controller.show(); runner.drain()
        let window = try XCTUnwrap(controller.window)
        defer { window.close() }
        try await settle(window)
        let host = try XCTUnwrap(window.contentView as? WorkspaceReviewHost<WorkspaceActivityView>)
        XCTAssertTrue(host.performKeyEquivalent(with: try key("r", in: window)))
        XCTAssertTrue(activity.loading); runner.drain()
        XCTAssertTrue(host.performKeyEquivalent(with: try key("\r", in: window)))
        XCTAssertEqual(focused, 1)
        XCTAssertTrue(host.performKeyEquivalent(with: try key("\r", modifiers: [.command, .shift], in: window)))
        XCTAssertEqual(opened, 1)
        XCTAssertTrue(host.performKeyEquivalent(with: try key("f", modifiers: [.command, .shift], in: window)))
        XCTAssertTrue(host.performKeyEquivalent(with: try key("w", in: window)))
        XCTAssertFalse(window.isVisible)

        let service = ConsistencyASL()
        let runs = WorkspaceRunsModel(service: service, command: { nil }, runner: runner)
        let runController = WorkspaceRunsWindow(model: runs, plan: WorkspaceRunPlanModel(command: { nil }))
        runs.onFocus = { _ in focused += 1 }
        runController.show(); runner.drain()
        let runWindow = try XCTUnwrap(runController.window)
        defer { runWindow.close() }
        try await settle(runWindow)
        let runHost = try XCTUnwrap(runWindow.contentView as? WorkspaceReviewHost<WorkspaceRunsView>)
        let reads = service.reads
        XCTAssertTrue(runHost.performKeyEquivalent(with: try key("r", in: runWindow))); runner.drain()
        XCTAssertEqual(service.reads, reads + 1)
        XCTAssertTrue(runHost.performKeyEquivalent(with: try key("\r", in: runWindow)))
        XCTAssertEqual(focused, 2)
        XCTAssertTrue(runHost.performKeyEquivalent(with: try key("w", in: runWindow)))
        XCTAssertFalse(runWindow.isVisible)
    }

    func testWrappingPreferenceUpdatesEveryVisibleCodeBlock() async throws {
        let preferences = WorkspaceReadingPreferences()
        let text = "```swift\n" + String(repeating: "let example = 1; ", count: 20) + "\n```\n\n```sh\n" + String(repeating: "echo example; ", count: 20) + "\n```"
        let host = NSHostingView(rootView: WorkspaceTranscriptText(text: text, terms: [], preferences: preferences))
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 600, height: 500), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host; window.orderFront(nil)
        defer { window.close() }
        try await settle(window)
        XCTAssertEqual(descendants(host).compactMap { $0 as? NSScrollView }.count, 2)
        preferences.wrapLines = true
        try await settle(window)
        XCTAssertTrue(descendants(host).compactMap { $0 as? NSScrollView }.isEmpty, "both code blocks wrap when the shared preference changes")
        preferences.wrapLines = false
        try await settle(window)
        XCTAssertEqual(descendants(host).compactMap { $0 as? NSScrollView }.count, 2)
    }

    func testPathLabelsKeepTheShortestDistinguishingSuffix() {
        let paths = ["src/network/client.swift", "tests/network/client.swift", "src/model.swift"]
        XCTAssertEqual(WorkspaceWording.uniqueSuffixes(paths)[paths[0]], "src/network/client.swift")
        XCTAssertEqual(WorkspaceWording.uniqueSuffixes(paths)[paths[2]], "model.swift")
        XCTAssertEqual(WorkspaceWording.uniqueSuffixes(["a/client.swift", "b/client.swift"])["a/client.swift"], "a/client.swift")
    }

    func testReadingPreferencesRestoreAndCopyKeepsLiteralUnicode() throws {
        let suite = "amx.consistency.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let model = WorkspaceChangesModel(command: { nil }, defaults: defaults)
        let preferences = model.readingPreferences
        preferences.wrapLines = true; preferences.fontSize = 20
        XCTAssertTrue(model.wrapDiffLines); XCTAssertEqual(model.diffFontSize, 20)
        let restored = WorkspaceReadingPreferences(defaults: defaults)
        XCTAssertTrue(restored.wrapLines); XCTAssertEqual(restored.effectiveFontSize, 20)
        let pasteboard = NSPasteboard.withUniqueName()
        defer { pasteboard.releaseGlobally() }
        var feedback = WorkspaceCopyFeedback()
        feedback.copy("let fox = \"🦊 café\"\n", to: pasteboard)
        XCTAssertEqual(feedback.message, "Copied")
        XCTAssertTrue(feedback.succeeded)
        XCTAssertEqual(pasteboard.string(forType: .string), "let fox = \"🦊 café\"\n")
    }
}

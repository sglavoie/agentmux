import AppKit
import SwiftUI
import XCTest
@testable import AgentMux

private final class NavigationPolishRunner: ASLWorkRunner {
    var hold = false
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) {
        let value = work()
        if hold { pending.append { then(value) } } else { then(value) }
    }
    func drain() { while !pending.isEmpty { pending.removeFirst()() } }
}

@MainActor
final class WorkspaceNavigationPolishTests: XCTestCase {
    private func command(_ response: @escaping ([String]) -> [String: Any]?) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in
            guard let data = response(args) else { return .finished(.init(status: 1, stdout: Data(), stderr: Data("Read failed".utf8))) }
            return .finished(.init(status: 0, stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": data]), stderr: Data()))
        }
        return command
    }
    private func response(_ args: [String], focus: Int? = 12) -> [String: Any] {
        let session: [String: Any] = ["id": "one", "harness": "claude", "project": "API"]
        if args[1] != "show" { return ["sessions": [session]] }
        var data: [String: Any] = ["session": session, "messages_total": 400, "messages_start": 0,
                                  "messages": (0..<50).map { ["role": "assistant", "text": "Message \($0)"] }]
        if let focus { data["focus_message"] = focus }
        return data
    }

    func testEventNavigationOverridesOldSearchAndPositionAndRetryKeepsTimestamp() throws {
        var calls: [[String]] = []
        var fail = true
        let amx = command { args in
            calls.append(args)
            return args[1] == "show" && fail ? nil : self.response(args)
        }
        let model = WorkspaceHistoryModel(command: { amx }, runner: NavigationPolishRunner())
        model.query = "unrelated old search"
        let at = "2026-10-17T12:00:00Z"
        model.open(project: "API", conversation: ConversationIdentity(encoded: "claude:one"), eventTime: at)
        XCTAssertEqual(model.query, "")
        XCTAssertEqual(model.scope, .conversation)
        XCTAssertTrue(model.includeAutomated)
        XCTAssertTrue(calls.last?.contains(at) == true)
        XCTAssertNotNil(model.transcriptError)
        fail = false
        model.refreshTranscript()
        XCTAssertTrue(calls.last?.contains("--at") == true)
        XCTAssertEqual(model.scrollRequest?.message, 12)
        XCTAssertEqual(model.highlightedMessage, 12)
        XCTAssertEqual(model.transcript?.total, 400)
        model.rememberVisibleMessage(12)
        model.refreshTranscript()
        XCTAssertFalse(calls.last?.contains("--at") == true, "ordinary refresh preserves the reader, not an old jump")
        XCTAssertEqual(model.scrollRequest?.message, 12)
    }

    func testEventJumpReplacesAnExistingReadingPositionInTheHostedReader() async throws {
        let amx = command { self.response($0) }
        let model = WorkspaceHistoryModel(command: { amx }, runner: NavigationPolishRunner())
        model.search()
        let controller = WorkspaceHistoryWindow(model: model)
        controller.show(project: "API", conversation: ConversationIdentity(encoded: "claude:one"))
        defer { controller.window?.close() }
        try await Task.sleep(for: .milliseconds(150))
        model.rememberVisibleMessage(40)
        controller.show(project: "API", conversation: ConversationIdentity(encoded: "claude:one"), eventTime: "2026-10-17T12:00:00Z")
        try await Task.sleep(for: .milliseconds(350))
        XCTAssertEqual(model.scrollRequest?.message, 12)
        XCTAssertEqual(model.highlightedMessage, 12)
        XCTAssertNotEqual(model.visibleMessage, 40)
        XCTAssertEqual(model.selection, "claude:one")
    }

    func testMissingTimestampEvidenceIsExplainedAndLateNavigationCannotReplaceSelection() {
        let runner = NavigationPolishRunner()
        let amx = command { self.response($0, focus: nil) }
        let model = WorkspaceHistoryModel(command: { amx }, runner: runner)
        model.open(project: "", conversation: ConversationIdentity(encoded: "claude:one"), eventTime: "2026-10-17T12:00:00Z")
        XCTAssertNotNil(model.locationNotice)
        XCTAssertNil(model.highlightedMessage)
        runner.hold = true
        model.open(project: "", conversation: ConversationIdentity(encoded: "claude:one"), eventTime: "2026-10-17T11:00:00Z")
        model.close()
        runner.drain()
        XCTAssertNil(model.transcript)
    }

    func testActivityNavigationCarriesSelectedEventAndDoesNotMarkItRead() throws {
        let amx = command { args in
            if args[1] == "activity" {
                return ["entries": [["id":"event", "at":"2026-10-17T12:00:00Z", "kind":"completed", "title":"Finished", "detail":"Done", "event":"Stop", "harness":"claude", "profile":"work", "session_id":"one", "runtime_session_id":"r", "run_id":"", "trace_id":""]], "since":"", "earliest_available":"", "truncated":false, "corrupt_lines":0, "matched":1]
            }
            return self.response(args)
        }
        let model = WorkspaceActivityModel(command: { amx }, runner: NavigationPolishRunner(), readStore: .init(load: { .init() }, save: { _ in XCTFail("Navigation cannot mark read") }))
        model.refresh()
        var target: String?, time: String?
        model.onConversation = { target = $0.encoded; time = $1 }
        model.openConversation()
        XCTAssertEqual(target, "claude:one")
        XCTAssertEqual(time, "2026-10-17T12:00:00Z")
        XCTAssertEqual(model.selection, "event")
        XCTAssertTrue(model.readState.read.isEmpty)
    }

    func testProgressAndFailedEvidenceIgnoreOtherSessionsAndSupersededAttempts() throws {
        func detail(_ records: [[String: Any]]) throws -> WorkspaceRunDetail {
            let data: [String: Any] = ["plan_dir":"/plan", "plan_title":"Plan", "counts":["complete":8, "pending":2, "unknown":0], "sessions":[],
                                      "runner_state":["run_id":"run", "paused":true, "reason":"verification-failed", "run_started_at":"", "run_finished_at":""],
                                      "verification":records, "verification_truncated":false]
            return try JSONDecoder().decode(WorkspaceRunDetail.self, from: JSONSerialization.data(withJSONObject: data))
        }
        let failed: [String: Any] = ["event":"verification_check_finished", "session_id":"9", "at":"now", "name":"Unit tests", "status":"failed", "returncode":1]
        let finish: [String: Any] = ["event":"verification_finished", "session_id":"9", "at":"now", "passed":false]
        let value = try detail([failed, finish])
        XCTAssertEqual(value.progressSummary, "8 of 10 sessions complete")
        XCTAssertEqual(value.blockingVerification(sessionID: "9").map(\.summary), ["Unit tests: failed · exit 1"])
        XCTAssertTrue(value.blockingVerification(sessionID: "8").isEmpty)
        let passed: [String: Any] = ["event":"verification_finished", "session_id":"9", "at":"later", "passed":true]
        XCTAssertTrue(try detail([failed, finish, passed]).blockingVerification(sessionID: "9").isEmpty)
        XCTAssertEqual(try detail([finish]).blockingVerification(sessionID: "9").count, 1, "keep an honest summary when bounded evidence lacks a named check")
    }

    func testLaunchSummaryNamesAllDestinationsSourcesAndUnknownState() {
        let rows = LaunchBackendPreference.workspace.destinationRows(cliBackend: "kitty", cliSource: "compatibility default")
        XCTAssertEqual(rows.map(\.destination), ["Workspace (tmux)", "Kitty", "Workspace (tmux)"])
        XCTAssertEqual(rows[0].source, "App preference")
        XCTAssertTrue(rows[1].source.contains("compatibility default"))
        XCTAssertTrue(LaunchBackendPreference.workspace.differsFromCLI("kitty"))
        XCTAssertFalse(LaunchBackendPreference.followCLI.differsFromCLI("kitty"))
        XCTAssertFalse(LaunchBackendPreference.workspace.differsFromCLI(nil))
        XCTAssertEqual(LaunchBackendPreference.followCLI.destinationRows(cliBackend: "kitty", cliSource: "config.toml")[0].destination, "Kitty")
        XCTAssertTrue(LaunchBackendPreference.followCLI.destinationRows(cliBackend: nil, cliSource: nil)[1].destination.contains("Unknown"))
    }

    func testReadStatusRetainsIndependentTimesAndLabelsFailure() {
        let at = Date(timeIntervalSince1970: 100)
        let search = WorkspaceReadObservation(title: "Search", retained: true, refreshedAt: at, feedback: .init(target: "Search", message: "Cannot read"))
        let transcript = WorkspaceReadObservation(title: "Transcript", retained: true, refreshedAt: at.addingTimeInterval(60))
        XCTAssertTrue(search.showingPrevious)
        XCTAssertTrue(search.description.contains("Search: Cannot read"))
        XCTAssertFalse(transcript.showingPrevious)
        XCTAssertEqual(search.refreshedAt, at)
        XCTAssertEqual(transcript.refreshedAt, at.addingTimeInterval(60))
    }
}

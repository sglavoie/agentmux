import XCTest
@testable import AgentMux

private final class HistoryWorkRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release(_ index: Int = 0) { pending.remove(at: index)() }
}

@MainActor
final class WorkspaceHistoryTests: XCTestCase {
    private func command(_ response: @escaping ([String]) -> String) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in
            .finished(ProcessOutput(status: 0, stdout: Data(response(args).utf8), stderr: Data()))
        }
        return command
    }

    func testQueriesCannotBecomeFlagsAndProjectIsOneCSVValue() {
        let args = WorkspaceHistoryModel.arguments(query: "--all", project: "API, \"new\"", includeAutomated: false)
        XCTAssertEqual(args, ["session", "search", "--json", "--project", "\"API, \"\"new\"\"\"", "--", "--all"])
        XCTAssertEqual(WorkspaceHistoryModel.arguments(query: " ", project: nil, includeAutomated: true), ["session", "list", "--json", "--all"])
    }

    func testEmptyTranscriptDecodesWithoutMessagesField() throws {
        let value = try JSONDecoder().decode(WorkspaceTranscript.self, from: Data(#"{"session":{"id":"a","harness":"claude"},"messages_total":0}"#.utf8))
        XCTAssertTrue(value.messages.isEmpty)
        XCTAssertEqual(value.total, 0)
    }

    func testConversationScopeUsesHarnessAndIDAndLoadsTheSelectedTranscript() {
        let runner = HistoryWorkRunner()
        var calls: [[String]] = []
        let amx = command { args in
            calls.append(args)
            if args[1] == "show" {
                return #"{"schema_version":1,"data":{"session":{"id":"a","harness":"claude"},"messages_total":1,"messages":[{"role":"assistant","text":"done"}]}}"#
            }
            return #"{"schema_version":1,"data":{"sessions":[{"id":"a","harness":"claude"},{"id":"a","harness":"codex"}]}}"#
        }
        let model = WorkspaceHistoryModel(command: { amx }, runner: runner)
        model.scope = .conversation
        model.open(project: "api", conversation: ConversationIdentity(harness: "claude", sessionID: "a"))
        runner.release()
        XCTAssertEqual(model.entries.map(\.id), ["claude:a"])
        runner.release()
        XCTAssertEqual(model.transcript?.messages.first?.text.reveal(), "done")
        XCTAssertEqual(calls[1], ["session", "show", "--json", "--last", "50", "--", "claude:a"])
    }

    func testLateSearchCannotReplaceNewResultsAndCloseDiscardsTranscript() {
        let runner = HistoryWorkRunner()
        let amx = command { args in
            let id = args.last == "new" ? "new" : "old"
            return "{\"schema_version\":1,\"data\":{\"results\":[{\"id\":\"\(id)\",\"harness\":\"claude\",\"preview\":\"match\",\"matches\":2}]}}"
        }
        let model = WorkspaceHistoryModel(command: { amx }, runner: runner)
        model.query = "old"
        model.search()
        model.query = "new"
        model.search()
        runner.release(1)
        XCTAssertEqual(model.entries.map(\.id), ["claude:new"])
        runner.release(0)
        XCTAssertEqual(model.entries.map(\.id), ["claude:new"])
        model.close()
        runner.release()
        XCTAssertNil(model.transcript)
        XCTAssertTrue(model.entries.isEmpty)
    }

    func testFailedSearchRetainsResultsButDisablesTheirActions() {
        let runner = HistoryWorkRunner()
        var fail = false
        let amx = command { args in
            if fail { return #"{"schema_version":1,"error":{"code":"unavailable","message":"Index unavailable"}}"# }
            if args[1] == "show" { return #"{"schema_version":1,"data":{"session":{"id":"a","harness":"claude"},"messages_total":0}}"# }
            return #"{"schema_version":1,"data":{"sessions":[{"id":"a","harness":"claude"}]}}"#
        }
        let model = WorkspaceHistoryModel(command: { amx }, runner: runner)
        model.search()
        runner.release()
        runner.release()
        XCTAssertTrue(model.canAct)
        fail = true
        model.search()
        runner.release()
        XCTAssertEqual(model.entries.count, 1)
        XCTAssertEqual(model.error, "Index unavailable")
        XCTAssertFalse(model.canAct)
    }

    func testDoubleClickDoesNotLaunchTwiceAndUnknownOutcomeIsShown() {
        let runner = HistoryWorkRunner()
        let amx = command { _ in #"{"schema_version":1,"data":{"sessions":[{"id":"a","harness":"claude"}]}}"# }
        let model = WorkspaceHistoryModel(command: { amx }, runner: runner)
        model.search()
        runner.release()
        var actions = 0
        var finish: ((String?) -> Void)?
        model.onAction = { conversation, fork, completion in
            XCTAssertEqual(conversation.selector, "claude:a")
            XCTAssertTrue(fork)
            actions += 1
            finish = completion
        }
        model.act(fork: true)
        model.act(fork: true)
        XCTAssertEqual(actions, 1)
        finish?("Outcome unknown; check the workspace")
        XCTAssertEqual(model.actionNotice, "Outcome unknown; check the workspace")
        XCTAssertFalse(model.acting)
    }
}

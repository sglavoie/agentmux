import AppKit
import XCTest
@testable import AgentMux

private final class CatchUpRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release(_ index: Int = 0) { pending.remove(at: index)() }
    func drain() { while !pending.isEmpty { release() } }
}

@MainActor
final class WorkspaceCatchUpTests: XCTestCase {
    private func entry(_ id: String, runtime: String = "api", conversation: String = "shared", kind: String = "completed") -> [String: Any] {
        ["id": id, "at": "2026-11-14T12:00:00Z", "kind": kind, "title": "Turn finished", "detail": "Working → Turn finished",
         "event": "Stop", "harness": "codex", "profile": "work", "session_id": conversation,
         "runtime_session_id": runtime, "run_id": "", "trace_id": "trace"]
    }
    private func command(entries: [[String: Any]], calls: @escaping ([String]) -> Void = { _ in }) -> AMXCommand {
        BriefingFixture.command { args in
            calls(args)
            if args[1] == "activity" {
                return ["entries": entries, "since": "", "earliest_available": "", "truncated": false, "corrupt_lines": 0, "matched": entries.count]
            }
            if args[1] == "recap" { return BriefingFixture.recap(args.last!) }
            if args[1] == "show" { return ["session": ["harness": "codex", "id": String(args.last!.split(separator: ":").last!)], "messages_total": 0, "messages": []] }
            let other = args.contains("\"Other\"")
            let offset = args[args.firstIndex(of: "--offset")! + 1]
            let membership = args.contains("200")
            return ["sessions": [["id": other ? "other" : (offset == "0" ? "shared" : "archived"), "harness": "codex", "project": other ? "Other" : "API"]],
                    "has_more": membership && !other && offset == "0", "total": other ? 1 : 2]
        }
    }
    private func model(_ command: AMXCommand, runner: CatchUpRunner, store: WorkspaceActivityReadStore? = nil) -> WorkspaceCatchUpModel {
        let model = WorkspaceCatchUpModel(briefing: .init(command: { command }, runner: runner),
            activity: .init(command: { command }, runner: runner, readStore: store ?? .init(load: { .init() }, save: { _ in })),
            summary: .init(command: { command }, runner: runner))
        model.update(projects: [.init(profile: "work", title: "API"), .init(profile: "work", title: "Other")],
                     sessions: [BriefingFixture.session("api", icon: "question"), BriefingFixture.session("other", project: "Other", icon: "key", conversation: "other")],
                     asl: [], live: true, order: WorkspaceOrder())
        return model
    }

    func testAllProjectsSectionsGroupBySessionAndReadOnlySavedOutcomes() throws {
        let runner = CatchUpRunner()
        var calls: [[String]] = []
        let model = model(command(entries: [entry("a"), entry("b"), entry("c", runtime: "other", conversation: "other")], calls: { calls.append($0) }), runner: runner)
        model.open(); model.refresh(); runner.drain()
        XCTAssertEqual(model.briefing.project, "")
        XCTAssertEqual(model.briefing.waiting.count, 2)
        XCTAssertTrue(model.briefing.conversations.isEmpty)
        XCTAssertEqual(model.activity.groups.map { $0.entries.count }, [2, 1])
        XCTAssertEqual(model.summary.recaps["runtime:api"]?.report?.recap?.latest.text.reveal(), "Agent reports the migration tests passed")
        XCTAssertTrue(calls.filter { $0[1] == "recap" }.allSatisfy { $0.contains("--cached") })
        XCTAssertTrue(model.activity.readState.read.isEmpty)
        var answered: WorkspaceWaitingAnswer?
        model.briefing.onAnswer = { answered = $0 }
        model.briefing.answer(.embedded("api"))
        XCTAssertEqual(answered, .prompt(runtimeSessionID: "api"))
        model.close(); runner.drain()
    }

    func testProjectScopeIncludesArchivedPagesAndMarkReadTouchesOnlyVisibleIDs() throws {
        let runner = CatchUpRunner()
        var saved = WorkspaceActivityReadState()
        let store = WorkspaceActivityReadStore(load: { saved }, save: { saved = $0 })
        let model = model(command(entries: [entry("a"), entry("old", runtime: "ended", conversation: "archived"), entry("other", runtime: "other", conversation: "other"), entry("unknown", runtime: "unknown", conversation: "")]), runner: runner, store: store)
        model.open(); model.refresh(); runner.drain()
        model.select("API"); runner.drain()
        XCTAssertEqual(model.briefing.waiting.count, 1)
        XCTAssertEqual(model.briefing.recent.first?.selector, "codex:shared")
        XCTAssertEqual(model.activity.visible.map(\.id), ["a", "old"])
        XCTAssertTrue(saved.read.isEmpty)
        model.activity.markVisibleRead()
        XCTAssertEqual(Set(saved.read.keys), ["a", "old"])
        XCTAssertTrue(model.activity.visible.isEmpty)
        model.select("Other"); runner.drain()
        XCTAssertEqual(model.activity.visible.map(\.id), ["other"])
        model.activity.undoMarkRead()
        XCTAssertTrue(saved.read.isEmpty)
        model.select(""); runner.drain()
        XCTAssertEqual(model.activity.visible.count, 4)
        XCTAssertTrue(model.briefing.conversations.isEmpty)
        model.close(); runner.drain()
    }

    func testLegacyAndMergedReadersPreserveEachOthersAcknowledgementsAndUndo() {
        let runner = CatchUpRunner(), amx = command(entries: [entry("one"), entry("two")])
        var saved = WorkspaceActivityReadState()
        let store = WorkspaceActivityReadStore(load: { saved }, save: { saved = $0 })
        let first = WorkspaceActivityModel(command: { amx }, runner: runner, readStore: store)
        let second = WorkspaceActivityModel(command: { amx }, runner: runner, readStore: store)
        first.refresh(); second.refresh(); runner.drain()
        first.select("one"); first.markSelectedRead(now: Date(timeIntervalSince1970: 100))
        second.select("two"); second.markSelectedRead(now: Date(timeIntervalSince1970: 101))
        XCTAssertEqual(Set(saved.read.keys), ["one", "two"])
        first.undoMarkRead()
        XCTAssertEqual(Set(saved.read.keys), ["two"], "Undo must preserve acknowledgements from the other reader")
        second.refresh(); runner.drain()
        XCTAssertEqual(second.visible.map(\.id), ["one"])
        second.undoMarkRead()
        XCTAssertTrue(saved.read.isEmpty)
        first.close(); second.close(); runner.drain()
    }

    func testScopeSwitchAndCloseDiscardLateMembershipAndTranscriptReplies() {
        let runner = CatchUpRunner()
        let model = model(command(entries: [entry("a"), entry("other", runtime: "other", conversation: "other")]), runner: runner)
        model.open(); model.refresh(); runner.drain()
        model.select("API"); model.select("Other")
        runner.drain()
        XCTAssertEqual(model.activity.visible.map(\.id), ["other"])
        XCTAssertEqual(model.briefing.recent.first?.id, "other")
        model.select("API"); model.close(); runner.drain()
        XCTAssertFalse(model.activity.scopeLoading)
        XCTAssertNil(model.activity.transcript)
        XCTAssertTrue(model.briefing.recent.isEmpty)
    }

    func testFlightLogDeepLinkRetainsKindNinetyDaysAndReadEventsThroughPresentation() throws {
        let session = try XCTUnwrap(JSONDecoder().decode(FlightReport.self, from: FlightLogFixture.data()).sessions.first)
        let runner = CatchUpRunner()
        let model = model(command(entries: [entry("question", runtime: session.runtimeSessionID, kind: "question"), entry("done", runtime: session.runtimeSessionID), entry("different", kind: "question")]), runner: runner)
        let controller = WorkspaceBriefingWindow(model: model)
        controller.show(project: "API"); runner.drain()
        controller.showFlight(session, kind: "question"); runner.drain()
        XCTAssertEqual(controller.window?.title, "Briefing")
        XCTAssertEqual(model.activity.project, "")
        XCTAssertEqual(model.activity.flightKind, "question")
        XCTAssertEqual(model.activity.days, 90)
        XCTAssertFalse(model.activity.unreadOnly)
        XCTAssertEqual(model.activity.visible.map(\.id), ["question"])
        model.activity.markVisibleRead()
        XCTAssertEqual(model.activity.visible.map(\.id), ["question"], "retained-history links include acknowledged events")
        model.activity.clearFlightFilter()
        XCTAssertEqual(model.activity.visible.count, 3)
        controller.window?.close(); runner.drain()
        XCTAssertNil(model.activity.selection)
    }

    func testEmptySectionsAndExplicitForkKeepExactIdentityAndPendingAnswersStayExcluded() throws {
        let runner = CatchUpRunner(), amx = command(entries: [])
        let model = model(amx, runner: runner)
        model.open(project: "API"); model.refresh(); runner.drain()
        let pending = try XCTUnwrap(model.briefing.waiting.first)
        model.briefing.excludePendingAnswers([pending])
        model.select(""); runner.drain(); model.select("API"); runner.drain()
        XCTAssertTrue(model.briefing.waiting.isEmpty)
        var forked: String?
        model.briefing.onFork = { conversation, done in forked = conversation.selector; done(nil) }
        model.briefing.fork(try XCTUnwrap(model.briefing.conversations.first))
        XCTAssertEqual(forked, "codex:shared")
        XCTAssertFalse(model.briefing.acting)
        XCTAssertTrue(model.activity.visible.isEmpty)
        let empty = BriefingFixture.command { args in
            if args[1] == "activity" { return ["entries": [], "since": "", "earliest_available": "", "truncated": false, "corrupt_lines": 0, "matched": 0] }
            return ["sessions": []]
        }
        let emptyModel = self.model(empty, runner: runner)
        emptyModel.update(projects: [.init(profile: "work", title: "API")], sessions: [], asl: [], live: true, order: WorkspaceOrder())
        emptyModel.open(project: "API"); emptyModel.refresh(); runner.drain()
        XCTAssertTrue(emptyModel.briefing.waiting.isEmpty)
        XCTAssertTrue(emptyModel.activity.groups.isEmpty)
        XCTAssertTrue(emptyModel.briefing.conversations.isEmpty)
        model.close(); emptyModel.close(); runner.drain()
    }
}

import AppKit
import XCTest
@testable import AgentMux

private final class BriefingRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release(_ index: Int = 0) { pending.remove(at: index)() }
    func drain() { while !pending.isEmpty { release() } }
}

enum BriefingFixture {
    static func session(_ id: String, harness: String = "codex", project: String = "API", icon: String = "bolt",
                        conversation: String? = "shared") -> CompanionSession {
        var session = CompanionSession()
        session.runtime = CompanionRuntime(sessionID: id, backend: "tmux")
        session.harness = harness; session.profile = harness + "-work"; session.project = project
        session.iconState = icon; session.attention = ["key", "question", "error"].contains(icon)
        session.conversation = conversation.map { ConversationIdentity(harness: harness, sessionID: $0) }
        return session
    }

    static func command(_ response: @escaping ([String]) -> [String: Any] = { args in
        if args[1] == "recap" {
            return recap(args[args.firstIndex(of: "--")! + 1])
        }
        return ["sessions": [["id": "shared", "harness": "codex", "project": "API", "profile": "codex-work", "last_at": "2026-10-31T12:00:00Z"],
                             ["id": "earlier", "harness": "claude", "project": "API", "profile": "claude-work", "last_at": "2026-10-30T12:00:00Z"]],
                "total": 25, "hidden_automated": 4, "has_more": true]
    }) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/fixture", path: "/bin"))
        command.runner = { _, args, _, _ in
            .finished(.init(status: 0, stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": response(args)]), stderr: Data()))
        }
        return command
    }

    static func recap(_ selector: String, status: String = "ready", saved: Bool = true) -> [String: Any] {
        var value: [String: Any] = ["conversation": selector, "status": status]
        if saved {
            value["recap"] = ["goal": ["text": "Finish the API migration", "messages": [0]],
                              "latest": ["text": "Agent reports the migration tests passed", "messages": [8]],
                              "outstanding": ["text": "Review the deployment configuration", "messages": [9]],
                              "generated_at": "2026-10-31T12:00:00Z", "profile": "personal", "model": "haiku", "partial": true]
        }
        return value
    }
}

@MainActor
final class WorkspaceBriefingTests: XCTestCase {
    func testCombinesAllLiveWithTenRecentAndKeepsHarnessQualifiedIdentities() throws {
        let runner = BriefingRunner()
        var calls: [[String]] = []
        let command = BriefingFixture.command { args in
            calls.append(args)
            if args[1] == "recap" { return BriefingFixture.recap(args[args.firstIndex(of: "--")! + 1]) }
            return ["sessions": (0..<15).map { ["id": "\($0)", "harness": "claude", "project": "API"] }, "total": 15]
        }
        let model = WorkspaceBriefingModel(command: { command }, runner: runner)
        let sessions = [BriefingFixture.session("rs-a"), BriefingFixture.session("rs-b", harness: "claude"),
                        BriefingFixture.session("rs-c", project: "Other"), BriefingFixture.session("rs-d", conversation: nil)]
        model.update(projects: [.init(profile: "a", title: "API"), .init(profile: "b", title: "API")], sessions: sessions, live: true)
        model.open(preferredProject: "API"); model.refresh(); runner.drain()
        XCTAssertEqual(model.projects, ["API", "Other"])
        XCTAssertEqual(model.rows.count, 3, "live sessions without conversation identity stay visible")
        XCTAssertEqual(model.recent.count, 10)
        XCTAssertEqual(model.conversations.count, 12, "all live conversations remain, even outside the recent ten")
        XCTAssertEqual(Set(model.conversations.prefix(2).map(\.selector)), Set(["codex:shared", "claude:shared"]))
        XCTAssertEqual(model.recaps.count, 12)
        XCTAssertEqual(calls.first, ["session", "list", "--json", "--all", "--project", "\"API\"", "--offset", "0", "--limit", "10"])
        XCTAssertTrue(calls.dropFirst().allSatisfy { $0.contains("--cached") })
        model.close()
    }

    func testLiveAndHistoryDeduplicateAndWaitingExcludesCompletedTurns() {
        let runner = BriefingRunner(), command = BriefingFixture.command()
        let model = WorkspaceBriefingModel(command: { command }, runner: runner)
        let sessions = [BriefingFixture.session("permission", icon: "key"),
                        BriefingFixture.session("question", icon: "question", conversation: "question"),
                        BriefingFixture.session("done", icon: "stop", conversation: "done"),
                        BriefingFixture.session("other", project: "Other", icon: "key")]
        model.update(projects: [], sessions: sessions, live: true)
        model.open(preferredProject: "API"); model.refresh(); runner.drain()
        XCTAssertEqual(model.conversations.filter { $0.selector == "codex:shared" }.count, 1)
        XCTAssertEqual(model.waiting.map(\.key), [.embedded("permission"), .embedded("question")])
        XCTAssertEqual(model.total, 25)
        var focused: AttentionCycler.Key?
        model.onFocus = { focused = $0 }
        model.focus(.embedded("other")); XCTAssertNil(focused)
        model.focus(.embedded("permission")); XCTAssertEqual(focused, .embedded("permission"))
        model.close()
    }

    func testSwitchingProjectsAndClosingRejectLateListAndRecapReplies() {
        let runner = BriefingRunner()
        let command = BriefingFixture.command { args in
            if args[1] == "recap" { return BriefingFixture.recap(args[args.firstIndex(of: "--")! + 1]) }
            let project = args[args.firstIndex(of: "--project")! + 1]
            return ["sessions": [["harness": "codex", "id": project.contains("Other") ? "other" : "api"]]]
        }
        let model = WorkspaceBriefingModel(command: { command }, runner: runner)
        model.update(projects: [.init(profile: "p", title: "API"), .init(profile: "p", title: "Other")], sessions: [], live: true)
        model.open(preferredProject: "API"); model.refresh()
        model.select("Other")
        runner.release(1) // newer list first, then the superseded API list
        runner.release()
        XCTAssertEqual(model.recent.map(\.id), ["other"])
        XCTAssertEqual(model.recaps.count, 1)
        let discarded = model.recaps["codex:other"]!
        model.select("API")
        runner.release() // old recap
        XCTAssertNil(discarded.report)
        model.close(); runner.drain()
        XCTAssertTrue(model.recent.isEmpty); XCTAssertTrue(model.recaps.isEmpty)
        XCTAssertFalse(model.loading)
    }

    func testRefreshReadsOnlySavedRecapsAndExplicitGenerationWaitsForIdle() throws {
        let runner = BriefingRunner()
        var calls: [[String]] = []
        let command = BriefingFixture.command { args in
            calls.append(args)
            if args[1] == "recap" {
                return BriefingFixture.recap(args[args.firstIndex(of: "--")! + 1], status: args.contains("--cached") ? "stale" : "ready")
            }
            return ["sessions": [["id": "shared", "harness": "codex"]]]
        }
        let model = WorkspaceBriefingModel(command: { command }, runner: runner)
        var session = BriefingFixture.session("rs-a")
        model.update(projects: [], sessions: [session], live: true)
        model.open(preferredProject: "API"); model.refresh(); runner.drain()
        let recap = try XCTUnwrap(model.recaps["codex:shared"])
        var scheduled = 0
        recap.schedule = { _, _ in scheduled += 1 }
        recap.refresh(); XCTAssertTrue(runner.pending.isEmpty, "cannot generate while an agent is working")
        model.refresh(); runner.drain()
        XCTAssertEqual(scheduled, 0)
        XCTAssertEqual(recap.report?.status, "stale"); XCTAssertNotNil(recap.report?.recap)
        XCTAssertTrue(calls.filter { $0[1] == "recap" }.allSatisfy { $0.contains("--cached") })
        session.iconState = "stop"
        model.update(projects: [], sessions: [session], live: true); runner.drain()
        XCTAssertTrue(recap.canGenerate)
        recap.refresh(); runner.drain()
        XCTAssertEqual(calls.filter { $0.contains("--refresh") }.count, 1)
        XCTAssertEqual(recap.report?.status, "ready")
        var evidence: (ConversationIdentity, Int?)?
        model.onConversation = { evidence = ($0, $1) }
        recap.reveal(9)
        XCTAssertEqual(evidence?.0.encoded, "codex:shared"); XCTAssertEqual(evidence?.1, 9)
        model.close()
    }

    func testFailedRefreshRetainsHistoryAndSavedRecapsAndMissingRecapNeverGenerates() throws {
        let runner = BriefingRunner()
        var fail = false
        var command = BriefingFixture.command { args in
            if args[1] == "recap" { return BriefingFixture.recap(args[args.firstIndex(of: "--")! + 1], status: "missing", saved: false) }
            return ["sessions": [["id": "archived", "harness": "copilot"]]]
        }
        let success = command.runner
        command.runner = { binary, args, env, timeout in fail ? .timedOut : success(binary, args, env, timeout) }
        let model = WorkspaceBriefingModel(command: { command }, runner: runner)
        model.update(projects: [.init(profile: "p", title: "API, \"work\"")], sessions: [], live: true)
        model.open(preferredProject: nil); model.refresh(); runner.drain()
        let recap = try XCTUnwrap(model.recaps["copilot:archived"])
        XCTAssertEqual(recap.report?.status, "missing")
        XCTAssertFalse(recap.automaticallyGenerate)
        XCTAssertEqual(WorkspaceHistoryModel.arguments(query: "", project: model.project, includeAutomated: false, limit: 10)[4], "\"API, \"\"work\"\"\"")
        let at = model.updatedAt
        fail = true; model.refresh(); runner.drain()
        XCTAssertNotNil(model.error); XCTAssertEqual(model.recent.map(\.id), ["archived"])
        XCTAssertEqual(model.updatedAt, at); XCTAssertTrue(model.recaps["copilot:archived"] === recap)
        XCTAssertNotNil(recap.error)
        model.close()
    }

    func testFocusAndResumeUseExactConversationAndOfflineDisablesActions() throws {
        let runner = BriefingRunner(), command = BriefingFixture.command()
        let model = WorkspaceBriefingModel(command: { command }, runner: runner)
        let sessions = [BriefingFixture.session("rs-a", icon: "stop")]
        model.update(projects: [], sessions: sessions, live: true)
        model.open(preferredProject: nil); model.refresh(); runner.drain()
        var focused: AttentionCycler.Key?, resumed: RecentConversation?, finish: ((String?) -> Void)?
        model.onFocus = { focused = $0 }
        model.onContinue = { resumed = $0; finish = $1 }
        model.continueConversation(model.conversations[0])
        XCTAssertEqual(focused, .embedded("rs-a")); XCTAssertNil(resumed)
        let archived = try XCTUnwrap(model.conversations.first { $0.id == "earlier" })
        model.continueConversation(archived)
        XCTAssertEqual(resumed?.selector, "claude:earlier"); XCTAssertTrue(model.acting)
        finish?("Resume failed. Retry after checking the workspace.")
        XCTAssertFalse(model.acting); XCTAssertNotNil(model.actionError)
        focused = nil; resumed = nil
        model.update(projects: [], sessions: sessions, live: false); runner.drain()
        model.focus(.embedded("rs-a")); model.continueConversation(archived)
        XCTAssertNil(focused); XCTAssertNil(resumed)
        XCTAssertTrue(model.recaps.values.allSatisfy { !$0.canGenerate })
        model.close()
    }

    func testEmptyInitialCatalogCanArriveLaterAndMenuAndPaletteExposeBriefing() throws {
        let runner = BriefingRunner(), command = BriefingFixture.command()
        let model = WorkspaceBriefingModel(command: { command }, runner: runner)
        model.open(preferredProject: nil); model.refresh()
        XCTAssertTrue(runner.pending.isEmpty)
        model.update(projects: [.init(profile: "p", title: "API")], sessions: [], live: true)
        XCTAssertEqual(model.project, "API"); runner.drain()
        XCTAssertFalse(model.recent.isEmpty)
        let app = AppDelegate()
        let entries = app.workspaceCommands
        XCTAssertTrue(WorkspaceSwitcher.matches(">project briefing", in: entries).contains { $0.place == .command(.briefing) })
        XCTAssertEqual(WorkspaceShortcut.briefing.action, #selector(AppDelegate.showBriefing(_:)))
        XCTAssertNil(WorkspaceShortcut.briefing.defaultCombo)
        model.close()
    }
}

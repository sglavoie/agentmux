import AppKit
import XCTest
@testable import AgentMux

private final class HandoffRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release() { pending.removeFirst()() }
}

@MainActor
final class WorkspaceHandoffTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private let source = ConversationIdentity(harness: "claude", sessionID: "source-one")
    private func reply(_ data: String, status: Int32 = 0) -> ProcessRun {
        .finished(.init(status: status, stdout: Data(data.utf8), stderr: Data()))
    }
    private func command(_ calls: @escaping ([String]) -> ProcessRun) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in calls(args) }
        return command
    }
    private func preview(_ args: [String]) -> ProcessRun {
        if args.starts(with: ["config", "profiles"]) {
            return reply(#"{"schema_version":1,"data":[{"name":"work","harness":"claude"},{"name":"review","harness":"codex"},{"name":"copilot","harness":"copilot"}]}"#)
        }
        return reply(#"{"schema_version":1,"data":{"changed":false,"data":{"content":"Source context\nOriginal goal and checkout status.\n","directory":"/project/worktree","source_harness":"claude","source_session_id":"source-one"}}}"#)
    }

    func testPreviewIsReadOnlyAndOffersAllProfiles() {
        let runner = HandoffRunner()
        var calls: [[String]] = []
        let command = command { args in calls.append(args); return self.preview(args) }
        let model = WorkspaceHandoffModel(source: source, preferredProfile: "work", command: { command }, runner: runner)
        XCTAssertFalse(model.canLaunch)
        model.load(); model.load()
        XCTAssertEqual(runner.pending.count, 1)
        runner.release()
        XCTAssertEqual(model.profiles.map(\.name), ["work", "review", "copilot"])
        XCTAssertEqual(model.profile, "review")
        XCTAssertEqual(model.directory, "/project/worktree")
        XCTAssertTrue(model.content.contains("Original goal"))
        XCTAssertTrue(model.canLaunch)
        XCTAssertEqual(calls, [["config", "profiles", "--json"], ["session", "handoff", "--preview", "--json", "--", source.encoded]])
        model.profile = "unknown"; XCTAssertFalse(model.canLaunch)
        model.profile = "copilot"; model.content = " \n"; XCTAssertFalse(model.canLaunch)
        model.content = "Edited"; model.instruction = String(repeating: "a", count: 16 * 1024 + 1); XCTAssertFalse(model.canLaunch)
    }

    func testLaunchUsesExactEditsPrivateFileAndOpensReportedSessionOnce() throws {
        let runner = HandoffRunner()
        var launches = 0, contextPath = ""
        let content = "Edited context: `quotes` $(literal) café\n"
        let command = command { args in
            if args.contains("--content-file") {
                launches += 1
                XCTAssertLessThan(args.firstIndex(of: "--json")!, args.firstIndex(of: "--")!, "JSON is an option, not a conversation selector")
                contextPath = args[args.firstIndex(of: "--content-file")! + 1]
                XCTAssertEqual(try? String(contentsOfFile: contextPath, encoding: .utf8), content)
                XCTAssertEqual(try? FileManager.default.attributesOfItem(atPath: contextPath)[.posixPermissions] as? Int, 0o600)
                let parent = URL(fileURLWithPath: contextPath).deletingLastPathComponent().path
                XCTAssertEqual(try? FileManager.default.attributesOfItem(atPath: parent)[.posixPermissions] as? Int, 0o700)
                XCTAssertEqual(args[args.firstIndex(of: "--instruction")! + 1], "Review for concurrency bugs")
                XCTAssertTrue(args.contains("copilot"))
                return self.reply(#"{"schema_version":1,"data":{"data":{"launched":true,"runtime_session_id":"\#(F.runtimeID(2))","source_harness":"claude","source_session_id":"source-one","path":"/saved/handoff.md"}}}"#)
            }
            return self.preview(args)
        }
        let model = WorkspaceHandoffModel(source: source, command: { command }, runner: runner)
        var opened: [String] = []
        model.onLaunched = { id, path in opened.append(id); XCTAssertEqual(path, "/saved/handoff.md") }
        model.load(); runner.release()
        model.content = content; model.instruction = "Review for concurrency bugs"; model.profile = "copilot"
        model.launch(); model.launch()
        XCTAssertEqual(runner.pending.count, 1)
        runner.release()
        XCTAssertEqual(launches, 1)
        XCTAssertEqual(opened, [F.runtimeID(2)])
        XCTAssertFalse(model.canLaunch)
        XCTAssertFalse(FileManager.default.fileExists(atPath: contextPath), "temporary context is removed after amx returns")
    }

    func testHandoffCarriesEditedTaskNameOnlyAfterConfirmedLaunch() throws {
        for confirmed in [true, false] {
            let runner = HandoffRunner()
            var renamed: [[String]] = []
            let command = command { args in
                if args.first == "name-session" {
                    renamed.append(args)
                    return self.reply(#"{"schema_version":1,"data":{"name":"Fix login redirect","changed":true}}"#)
                }
                if args.contains("--content-file") {
                    let id = confirmed ? F.runtimeID(2) : ""
                    return self.reply(#"{"schema_version":1,"data":{"data":{"launched":true,"runtime_session_id":"\#(id)","source_harness":"claude","source_session_id":"source-one"}}}"#)
                }
                return self.preview(args)
            }
            let model = WorkspaceHandoffModel(source: source, command: { command }, runner: runner)
            model.taskName = "Fix login redirect"
            var opened: [String] = []
            model.onLaunched = { id, _ in opened.append(id) }
            model.load(); runner.release(); model.launch(); runner.release()
            if confirmed {
                XCTAssertTrue(model.launching, "Keep submission locked while its name is saved")
                runner.release()
                XCTAssertEqual(renamed, [NameSessionResult.arguments(runtimeSessionID: F.runtimeID(2), name: "Fix login redirect")])
                XCTAssertEqual(opened, [F.runtimeID(2)])
            } else {
                XCTAssertTrue(renamed.isEmpty)
                XCTAssertTrue(opened.isEmpty)
            }
        }
    }

    func testClosedOrMismatchedPreviewCannotEnableLaunch() {
        let runner = HandoffRunner()
        let command = command { args in self.preview(args) }
        let model = WorkspaceHandoffModel(source: source, command: { command }, runner: runner)
        model.load(); model.close(); runner.release()
        XCTAssertFalse(model.canLaunch)
        XCTAssertTrue(model.content.isEmpty)
        let mismatch = WorkspaceHandoffModel(source: .init(harness: "codex", sessionID: "source-one"), command: { command }, runner: runner)
        mismatch.load(); runner.release()
        XCTAssertFalse(mismatch.canLaunch)
        XCTAssertNotNil(mismatch.error)
    }

    func testKnownRejectionCanRetryAndUnconfirmedOutcomesCannotDuplicateLaunch() {
        for outcome in ["rejection", "timeout", "partial", "missingID", "malformed"] {
            let runner = HandoffRunner()
            let command = command { args in
                guard args.contains("--content-file") else { return self.preview(args) }
                switch outcome {
                case "rejection": return self.reply(#"{"schema_version":1,"error":{"code":"not_found","message":"Profile removed"}}"#, status: 1)
                case "timeout": return .timedOut
                case "partial": return self.reply(#"{"schema_version":1,"data":{"message":"Target launch failed","data":{"launched":false,"path":"/saved/context.md"}}}"#, status: 1)
                case "missingID": return self.reply(#"{"schema_version":1,"data":{"data":{"launched":true}}}"#)
                default: return self.reply("unreadable")
                }
            }
            let model = WorkspaceHandoffModel(source: source, command: { command }, runner: runner)
            model.load(); runner.release(); model.launch(); runner.release()
            XCTAssertNotNil(model.error, outcome)
            XCTAssertEqual(model.canLaunch, outcome == "rejection", outcome)
            if outcome == "partial" { XCTAssertEqual(model.artifactPath, "/saved/context.md") }
        }
    }

    func testPanelLeavesSourceUsableAndProtectsAnInFlightLaunch() {
        let runner = HandoffRunner()
        let command = command { args in self.preview(args) }
        let model = WorkspaceHandoffModel(source: source, command: { command }, runner: runner)
        let panel = WorkspaceHandoffPanel(model: model)
        let sourceWindow = NSWindow(contentRect: NSRect(x: 100, y: 100, width: 800, height: 760), styleMask: [.titled], backing: .buffered, defer: false)
        sourceWindow.isReleasedWhenClosed = false
        defer { panel.close(); sourceWindow.close() }
        panel.show(on: sourceWindow)
        XCTAssertNil(sourceWindow.attachedSheet, "the source transcript remains readable while context is edited")
        XCTAssertTrue(panel.panel.isVisible)
        runner.release(); model.launch()
        XCTAssertFalse(panel.windowShouldClose(panel.panel))
        panel.close()
        XCTAssertTrue(panel.panel.isVisible)
        runner.release()
        XCTAssertTrue(panel.windowShouldClose(panel.panel))
        panel.close()
        XCTAssertFalse(panel.panel.isVisible)
    }

    func testSourceLinksPersistAcrossRestartAndConversationIdentityDiscovery() throws {
        let suite = "WorkspaceHandoffTests." + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let origins = WorkspaceHandoffOrigins(defaults: defaults)
        origins.record(runtimeID: F.runtimeID(2), source: source, artifactPath: "/saved/context.md")
        var receiving = F.tmux(2, project: "API")
        receiving.conversation = .init(harness: "codex", sessionID: "same-id")
        origins.observe([receiving])
        let restarted = WorkspaceHandoffOrigins(defaults: defaults)
        XCTAssertEqual(restarted.origin(runtimeID: F.runtimeID(2))?.identity, source)
        XCTAssertEqual(restarted.origin(conversation: receiving.conversation)?.identity, source)
        XCTAssertEqual(restarted.origin(conversation: receiving.conversation)?.artifactPath, "/saved/context.md")
        XCTAssertNil(restarted.origin(conversation: .init(harness: "claude", sessionID: "same-id")))
        XCTAssertNil(restarted.origin(runtimeID: F.runtimeID(1)))
    }
}

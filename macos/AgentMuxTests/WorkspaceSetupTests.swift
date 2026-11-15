import AppKit
import XCTest
@testable import AgentMux

struct SetupTestRunner: ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}

private final class HeldSetupRunner: ASLWorkRunner {
    var pending: (() -> Void)?
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending = { then(work()) } }
    func finish() { let task = pending; pending = nil; task?() }
}

enum WorkspaceSetupFixture {
    static func detection(_ detected: [String] = ["claude"], projects: [String] = ["/work/first", "/work/second"], cli: Bool = true) -> [String: Any] {
        ["harnesses": ["claude", "codex", "copilot"].map { name in
            ["harness": name, "cli_available": detected.contains(name) && cli,
             "home": "/test/\(name)", "home_exists": detected.contains(name)] as [String: Any]
        }, "recent_projects": projects.map { ["directory": $0, "harness": "claude", "last_used": "2026-11-14T12:00:00Z"] }, "warnings": []]
    }
    static func output(_ data: [String: Any], status: Int32 = 0) -> ProcessRun {
        .finished(.init(status: status, stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": data]), stderr: Data()))
    }
    static func command(_ response: @escaping ([String]) -> ProcessRun = { _ in output(detection()) }) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/bundled/amx"), launchEnvironment: LaunchEnvironment(home: "/test", path: "/bin"))
        command.inheritedEnvironment = [:]
        command.runner = { binary, args, _, _ in
            XCTAssertEqual(binary, "/bundled/amx")
            return response(args)
        }
        return command
    }
}

@MainActor
final class WorkspaceSetupTests: XCTestCase {
    func testDetectionNoneOneAndSeveralAndNoImplicitMutation() {
        for detected in [[], ["claude"], ["codex", "copilot"]] as [[String]] {
            var calls: [[String]] = []
            let command = WorkspaceSetupFixture.command { args in
                calls.append(args)
                return WorkspaceSetupFixture.output(WorkspaceSetupFixture.detection(detected))
            }
            let model = WorkspaceSetupModel(command: { command }, runner: SetupTestRunner())
            model.detect()
            XCTAssertEqual(calls, [["config", "setup", "--detect", "--json"]])
            XCTAssertEqual(model.choices.map(\.harness), detected)
            XCTAssertEqual(model.harness, detected.first ?? "")
            XCTAssertEqual(model.canSetUp, !detected.isEmpty)
            XCTAssertTrue(model.syncHooks)
            XCTAssertEqual(model.searchedCLIs, "claude, codex, copilot")
            XCTAssertFalse(model.saved)
        }
    }

    func testHomeOnlyDetectionAndNoRecentProjectsRequireFolderChoice() {
        let command = WorkspaceSetupFixture.command { _ in
            WorkspaceSetupFixture.output(WorkspaceSetupFixture.detection(["codex"], projects: [], cli: false))
        }
        let model = WorkspaceSetupModel(command: { command }, runner: SetupTestRunner())
        model.detect()
        XCTAssertEqual(model.selected?.harness, "codex")
        XCTAssertEqual(model.selected?.cliAvailable, false)
        XCTAssertFalse(model.canSetUp)
        model.directory = "/work/chosen"
        XCTAssertEqual(model.directories, ["/work/chosen"])
        XCTAssertTrue(model.canSetUp)
    }

    func testArgumentsForEveryHarnessFolderAndUncheckedHooks() {
        var calls: [[String]] = []
        let command = WorkspaceSetupFixture.command { args in
            calls.append(args)
            if args.contains("--detect") { return WorkspaceSetupFixture.output(WorkspaceSetupFixture.detection(["claude", "codex", "copilot"])) }
            return WorkspaceSetupFixture.output(["saved": true, "hooks_status": "not_synced", "summary": "Saved"])
        }
        let model = WorkspaceSetupModel(command: { command }, runner: SetupTestRunner())
        model.detect()
        for harness in ["claude", "codex", "copilot"] {
            model.harness = harness
            XCTAssertEqual(model.arguments, ["config", "setup", "--json", "--harness", harness, "--home", "/test/\(harness)",
                                            "--directory", "/work/first", "--backend", "keep", "--sync-hooks"])
        }
        model.directory = "/work/a folder's $(literal)"
        model.syncHooks = false
        var saved = 0, finished = 0
        model.onSaved = { saved += 1 }; model.onFinished = { finished += 1 }
        model.setUp(); model.setUp()
        XCTAssertEqual(calls.count, 2)
        XCTAssertEqual(calls.last, ["config", "setup", "--json", "--harness", "copilot", "--home", "/test/copilot",
                                    "--directory", "/work/a folder's $(literal)", "--backend", "keep"])
        XCTAssertEqual(saved, 1); XCTAssertEqual(finished, 1)
        XCTAssertFalse(model.canSetUp)
    }

    func testStructuredFailureStaysOpenAndCanRetry() {
        let command = WorkspaceSetupFixture.command { args in
            if args.contains("--detect") { return WorkspaceSetupFixture.output(WorkspaceSetupFixture.detection()) }
            return .finished(.init(status: 1, stdout: Data(#"{"schema_version":1,"error":{"code":"setup_failed","message":"Directory disappeared"}}"#.utf8), stderr: Data()))
        }
        let model = WorkspaceSetupModel(command: { command }, runner: SetupTestRunner())
        model.onFinished = { XCTFail("Failure must leave the sheet open") }
        model.onSaved = { XCTFail("No config saved") }
        model.detect(); model.setUp()
        XCTAssertEqual(model.error, "Directory disappeared")
        XCTAssertTrue(model.canSetUp)
    }

    func testSavedHookFailureRefreshesButNeverRepeatsSetup() {
        var mutations = 0
        let command = WorkspaceSetupFixture.command { args in
            if args.contains("--detect") { return WorkspaceSetupFixture.output(WorkspaceSetupFixture.detection()) }
            mutations += 1
            return WorkspaceSetupFixture.output(["saved": true, "hooks_status": "failed", "hooks_error": "Permission denied",
                                                 "summary": "Retry: amx config hooks-sync --profile personal"], status: 1)
        }
        let model = WorkspaceSetupModel(command: { command }, runner: SetupTestRunner())
        var refreshed = 0
        model.onSaved = { refreshed += 1 }
        model.onFinished = { XCTFail("Hook failure must leave the sheet open") }
        model.detect(); model.setUp(); model.setUp(); model.detect()
        XCTAssertEqual(mutations, 1)
        XCTAssertEqual(refreshed, 1)
        XCTAssertTrue(model.saved)
        XCTAssertTrue(model.error?.contains("Permission denied") == true)
        XCTAssertTrue(model.error?.contains("hooks-sync --profile personal") == true)
        XCTAssertFalse(model.canSetUp)
    }

    func testUnknownOutcomeCannotBeResubmitted() {
        let command = WorkspaceSetupFixture.command { args in
            args.contains("--detect") ? WorkspaceSetupFixture.output(WorkspaceSetupFixture.detection()) : .timedOut
        }
        let model = WorkspaceSetupModel(command: { command }, runner: SetupTestRunner())
        model.detect(); model.setUp()
        XCTAssertTrue(model.error?.contains("Check Settings") == true)
        XCTAssertFalse(model.canSetUp)
    }

    func testDetectionFailureAndMissingCommandRetainManualFallback() {
        for command in [nil, WorkspaceSetupFixture.command { _ in .launchFailed(reason: "Missing CLI") }] {
            let model = WorkspaceSetupModel(command: { command }, runner: SetupTestRunner())
            var manual = 0
            model.onManual = { manual += 1 }
            model.detect()
            XCTAssertNotNil(model.error)
            XCTAssertFalse(model.canSetUp)
            model.manual()
            XCTAssertEqual(manual, 1)
        }
    }

    func testClosingDetectionDiscardsLateResultAndSavingBlocksManualAndDuplicates() {
        let runner = HeldSetupRunner()
        var calls = 0
        let command = WorkspaceSetupFixture.command { args in
            calls += 1
            return WorkspaceSetupFixture.output(args.contains("--detect") ? WorkspaceSetupFixture.detection() :
                ["saved": true, "hooks_status": "synced", "summary": "Saved"])
        }
        let model = WorkspaceSetupModel(command: { command }, runner: runner)
        model.detect(); model.close(); runner.finish()
        XCTAssertNil(model.detection)
        model.detect(); runner.finish()
        model.onManual = { XCTFail("Cannot open wizard during save") }
        model.setUp(); model.setUp(); model.manual(); model.close()
        XCTAssertTrue(model.saving)
        runner.finish()
        XCTAssertEqual(calls, 3)
        XCTAssertTrue(model.saved)
    }

    func testNativeSheetSuccessRefreshesWorkspaceWithoutOpeningATerminal() throws {
        let app = FakeWorkspaceApp()
        app.workspaceOffersSetup = true
        app.recapCommand = WorkspaceSetupFixture.command { args in
            WorkspaceSetupFixture.output(args.contains("--detect") ? WorkspaceSetupFixture.detection() :
                ["saved": true, "hooks_status": "synced", "summary": "Saved"])
        }
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        workspace.setupRunner = SetupTestRunner()
        defer { workspace.setupPanel?.close(); workspace.window.close() }
        workspace.openForTesting()
        app.store.apply(health: .notRunning); workspace.refresh()
        workspace.placeholder.press()
        let panel = try XCTUnwrap(workspace.setupPanel)
        XCTAssertTrue(workspace.window.attachedSheet === panel.panel)
        XCTAssertGreaterThan(panel.panel.frame.width, 500)
        XCTAssertGreaterThan(panel.panel.frame.height, 400)
        XCTAssertTrue(panel.model.canSetUp)
        panel.model.setUp()
        XCTAssertEqual(app.setupFinishes, 1)
        XCTAssertNil(panel.panel.sheetParent)
        app.workspaceOffersSetup = false; workspace.refreshCommands()
        XCTAssertEqual(workspace.placeholder.button.title, WorkspaceWindowController.startDaemonTitle)
        XCTAssertTrue(app.madeViews.isEmpty)
        XCTAssertTrue(app.made.isEmpty)
    }
}

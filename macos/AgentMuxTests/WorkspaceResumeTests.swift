import AppKit
import XCTest
@testable import AgentMux

private struct ResumeRunner: ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}

@MainActor
final class WorkspaceResumeTests: XCTestCase {
    func testRecoveryReadsExactSourceAndRefusesChangedProfileOrDuplicateSubmission() throws {
        var profile = "work", id = "original", reads = 0
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in
            XCTAssertEqual(Array(args.prefix(2)), ["session", "show"])
            XCTAssertEqual(args.last, "codex:original")
            reads += 1
            let body = "{\"schema_version\":1,\"data\":{\"session\":{\"id\":\"\(id)\",\"harness\":\"codex\",\"profile\":\"\(profile)\",\"cwd\":\"/work/api\"}}}"
            return .finished(.init(status: 0, stdout: Data(body.utf8), stderr: Data()))
        }
        let model = WorkspaceResumeModel(identity: .init(harness: "codex", sessionID: "original"), command: { command }, runner: ResumeRunner())
        var submissions: [RecentConversation] = [], finish: ((String?) -> Void)?
        model.onResume = { conversation, done in submissions.append(conversation); finish = done }
        model.load()
        XCTAssertEqual(model.conversation?.profile, "work")
        XCTAssertTrue(submissions.isEmpty)
        profile = "personal"
        model.resume()
        XCTAssertTrue(submissions.isEmpty)
        XCTAssertFalse(model.canResume)
        XCTAssertEqual(model.conversation?.profile, "personal")
        model.load(); model.resume(); model.resume()
        XCTAssertEqual(submissions.count, 1)
        XCTAssertTrue(model.acting)
        finish?("Outcome unknown")
        XCTAssertFalse(model.acting)
        XCTAssertEqual(model.feedbackHistory.entries.count, 1)
        XCTAssertEqual(model.feedbackHistory.entries.first?.destination, .conversation("codex:original"))
        id = "wrong"
        model.load()
        XCTAssertFalse(model.canResume)
        XCTAssertTrue(model.error?.contains("did not match") == true)
        XCTAssertGreaterThanOrEqual(reads, 4)
    }

    func testFirstProjectSetupStaysInWorkspaceAndRefreshesOnReturn() throws {
        let app = FakeWorkspaceApp()
        app.workspaceOffersSetup = true
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        defer { workspace.window.close() }
        app.store.onChange = { workspace.refresh() }
        workspace.openForTesting()
        app.store.apply(snapshot: WorkspaceFixture.snapshot([]))
        XCTAssertEqual(workspace.placeholder.button.title, "Set Up First Project…")
        workspace.placeholder.press()
        XCTAssertEqual(app.madeViews, ["setup"])
        XCTAssertTrue(app.made.isEmpty)
        XCTAssertEqual(WorkspaceUtilityView.setup.arguments, ["workspace-view", "setup"])
        workspace.utilityEnded("setup")
        XCTAssertEqual(app.setupFinishes, 1)
        app.workspaceOffersSetup = false
        workspace.refreshCommands()
        XCTAssertEqual(workspace.placeholder.button.title, "Launch Project…")
        XCTAssertTrue(app.made.isEmpty)
        XCTAssertNil(WorkspaceUtilityView.from(.init(kind: "setup")), "Only the operator opens setup")
    }
    func testRecoverySheetIsReadableAndDoesNotLaunchWhenOpenedOrCancelled() async throws {
        let model = WorkspaceResumeModel(identity: .init(harness: "codex", sessionID: "original"), command: { nil }, runner: ResumeRunner())
        var launches = 0
        model.onResume = { _, done in launches += 1; done(nil) }
        let owner = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 800, height: 650), styleMask: [.titled], backing: .buffered, defer: false)
        owner.isReleasedWhenClosed = false
        owner.orderFront(nil)
        let panel = WorkspaceResumePanel(model: model)
        defer { panel.close(); owner.close() }
        panel.show(on: owner)
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertTrue(owner.attachedSheet === panel.panel)
        XCTAssertGreaterThan(panel.panel.frame.width, 450)
        XCTAssertGreaterThan(panel.panel.frame.height, 160)
        XCTAssertFalse(model.canResume)
        XCTAssertNotNil(model.error)
        XCTAssertEqual(launches, 0)
        panel.close()
        XCTAssertEqual(launches, 0)
    }

}

import AppKit
import XCTest
@testable import AgentMux

private final class QueueHeldRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) {
        pending.append { then(work()) }
    }
    func drain() {
        let work = pending
        pending = []
        work.forEach { $0() }
    }
}

private struct QueueImmediateRunner: ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}

private final class QueuePromptService: PromptAnswerService {
    var keys: [[String]] = []
    var screen = "blocked"
    var result: AMXCommandResult<PromptSent> = .success(PromptSent(delivery: "accepted"))
    func look(runtimeSessionID: String) -> AMXCommandResult<PromptLook> {
        .success(PromptLook(project: runtimeSessionID, screen: screen, lines: ["Allow this action?"]))
    }
    func press(arguments: [String]) -> AMXCommandResult<PromptSent> {
        keys.append(arguments)
        return result
    }
}

private final class QueueASLService: ASLService {
    var row = ASLFixtures.replyRow()
    var actions: [ASLRequestParams] = []
    init() { row.question?.questionIDs = ["q-api-1"]; row.question?.unansweredCount = 1 }
    func snapshot() -> Result<ASLResult, ASLRequestError> {
        .success(ASLResult(status: ASLFixtures.liveStatus, snapshot: ASLSnapshot(rows: [row]), meta: ASLSnapshotMeta()))
    }
    func question(planDir: String, questionID: String) -> Result<ASLQuestionBody, ASLRequestError> {
        .success(ASLFixtures.body(row, questionID, "Continue?"))
    }
    func act(_ params: ASLRequestParams) -> Result<ASLOperation, ASLRequestError> {
        actions.append(params)
        return .success(ASLOperation(operationID: params.operationID, status: "accepted"))
    }
}

@MainActor
final class WorkspaceAnswerQueueTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private var app: AppDelegate!
    private var defaultsSuite: String!
    private var terminals: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!
    private var reads: QueueHeldRunner!
    private var presses: QueueHeldRunner!
    private var service: QueuePromptService!
    private var asl: QueueASLService!
    private var prompt: PromptAnswerFlow { app.promptPanel!.flow }

    override func setUp() async throws {
        defaultsSuite = "WorkspaceAnswerQueueTests-\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: defaultsSuite))
        app = AppDelegate(preferencesModel: PreferencesModel(defaults: defaults))
        app.preferencesModel.preferences = AppPreferences()
        terminals = FakeWorkspaceApp()
        workspace = WorkspaceWindowController(delegate: terminals, glyphs: .fallback)
        app.workspace = workspace
        workspace.openForTesting()
        workspace.window.makeKeyAndOrderFront(nil)
        workspace.welcome.schedule = { _, _ in }
        reads = QueueHeldRunner(); presses = QueueHeldRunner(); service = QueuePromptService()
        app.promptPanel = PromptAnswerPanelController(flow: PromptAnswerFlow(
            service: service, reads: reads, presses: presses, pause: { _ in }))
        asl = QueueASLService()
        app.aslPanel = ASLQuestionPanelController(flow: ASLQuestionFlow(service: asl, runner: QueueImmediateRunner()))
    }

    override func tearDown() async throws {
        app.promptPanel?.dismissForQueue(); app.aslPanel?.dismissForQueue()
        reads.drain(); presses.drain()
        workspace.window.close()
        app.workspace = nil
        app.preferencesModel.defaults.removePersistentDomain(forName: defaultsSuite)
        workspace = nil; terminals = nil; app = nil
    }

    private func load(_ count: Int, aslIndex: Int? = nil) {
        var sessions = (1...count).map { F.tmux($0, project: "Project \($0)", icon: "key", attention: true) }
        var snapshot = F.snapshot(sessions)
        if let aslIndex {
            sessions[aslIndex - 1].aslRunID = asl.row.runID
            snapshot.sessions = sessions
            var waiting = CompanionASLWaiting()
            waiting.runID = asl.row.runID; waiting.planDir = asl.row.planDir
            waiting.runtimeSessionID = F.runtimeID(aslIndex)
            waiting.latestQuestionID = "q-api-1"; waiting.unanswered = 1
            snapshot.asl.waiting = [waiting]
        }
        apply(snapshot)
    }

    private func apply(_ snapshot: CompanionSnapshot) {
        app.store.apply(snapshot: snapshot)
        terminals.store.apply(snapshot: snapshot)
        workspace.refresh()
    }

    private func open(_ n: Int) throws {
        workspace.select(.embedded(F.runtimeID(n)), focus: true)
        let row = try XCTUnwrap(workspace.allWaitingRows.first { $0.key == .embedded(F.runtimeID(n)) })
        app.workspaceAnswer(try XCTUnwrap(row.answer))
    }

    private func sendPrompt() {
        reads.drain()
        prompt.press(PromptKey.digits[1])
        presses.drain()
    }

    func testPromptToPromptRequiresNewLookAndEndsWithReceipt() throws {
        load(2)
        XCTAssertNil(prompt.state, "Waiting snapshots never start the queue")
        try open(1)
        sendPrompt()
        XCTAssertEqual(prompt.state?.target.runtimeSessionID, F.runtimeID(2))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(2)))
        XCTAssertFalse(workspace.window.firstResponder === terminal)
        XCTAssertFalse(NSApp.keyWindow === workspace.window)
        XCTAssertFalse(prompt.state?.canPress == true)
        prompt.press(PromptKey.digits[1])
        presses.drain()
        XCTAssertEqual(service.keys.count, 1, "The previous prompt's look must not authorize the next key")
        reads.drain()
        XCTAssertTrue(prompt.state?.canPress == true)
        prompt.press(PromptKey.digits[1]); presses.drain()
        XCTAssertEqual(service.keys.map { $0[1] }, [F.runtimeID(1), F.runtimeID(2)])
        XCTAssertNil(prompt.state)
        XCTAssertFalse(app.promptPanel!.isVisible)
        XCTAssertEqual(workspace.welcome.receipt?.title, "All caught up")
        XCTAssertEqual(workspace.inspector.sentAnswers.count, 2)
        workspace.refresh()
        XCTAssertEqual(workspace.welcome.receipt?.title, "All caught up", "Pending daemon acknowledgment must not erase the receipt")
        load(3)
        XCTAssertNil(workspace.welcome.receipt, "A new unanswered question clears the caught-up receipt")
    }

    func testPromptToASLToPromptDoesNotCloseNewPanelOrKeepSentDraft() throws {
        load(3, aslIndex: 2)
        try open(1); sendPrompt()
        XCTAssertNil(prompt.state)
        XCTAssertFalse(app.promptPanel!.isVisible)
        let flow = app.aslPanel!.flow
        XCTAssertEqual(flow.answering?.runID, asl.row.runID)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        XCTAssertFalse(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(2)))
        flow.setDraft("Proceed")
        flow.submit(try XCTUnwrap(flow.state.buttons.first))
        XCTAssertEqual(asl.actions.count, 1)
        XCTAssertEqual(flow.phase, .idle)
        XCTAssertEqual(flow.unsent.drafts, 0)
        XCTAssertFalse(app.aslPanel!.isVisible)
        XCTAssertTrue(app.promptPanel!.isVisible)
        XCTAssertEqual(prompt.state?.target.runtimeSessionID, F.runtimeID(3))
        XCTAssertFalse(prompt.state?.canPress == true)
        XCTAssertFalse(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(3)))
        sendPrompt()
        XCTAssertEqual(workspace.welcome.receipt?.title, "All caught up")
    }

    func testSkipsAnsweredAndUnanswerableRowsAndWrapsInSidebarOrder() throws {
        load(4)
        var snapshot = try XCTUnwrap(terminals.store.snapshot)
        snapshot.sessions[2].iconState = "stop"
        apply(snapshot)
        let answered = try XCTUnwrap(workspace.allWaitingRows.first { $0.key == .embedded(F.runtimeID(4)) })
        workspace.inspector.markAnswerSent(answered)
        try open(2); sendPrompt()
        XCTAssertEqual(prompt.state?.target.runtimeSessionID, F.runtimeID(1))
        sendPrompt()
        XCTAssertNil(prompt.state)
        XCTAssertEqual(workspace.welcome.receipt?.title, "All caught up")
    }

    func testPreferenceOffNeverAdvancesPromptOrASL() throws {
        app.preferencesModel.preferences.continueToNextWaiting = false
        load(3, aslIndex: 2)
        try open(1); sendPrompt()
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertEqual(prompt.state?.target.runtimeSessionID, F.runtimeID(1))
        XCTAssertNil(app.aslPanel!.flow.answering)
        XCTAssertEqual(workspace.welcome.receipt?.title, "Answer sent")
        app.promptPanel!.dismissForQueue()
        try open(2)
        let flow = app.aslPanel!.flow
        flow.setDraft("Proceed"); flow.submit(try XCTUnwrap(flow.state.buttons.first))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        XCTAssertNil(prompt.state)
        XCTAssertEqual(flow.phase, .idle)
    }

    func testCollapsedSidebarAndOtherSplitPaneNeverReceiveQueueKeyboard() throws {
        load(2)
        workspace.select(.embedded(F.runtimeID(2)), focus: true)
        workspace.split(with: .embedded(F.runtimeID(1)))
        workspace.setSidebarCollapsed(true)
        try open(1); sendPrompt()
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(2)))
        XCTAssertFalse(workspace.window.firstResponder === terminal)
        workspace.refresh()
        XCTAssertFalse(workspace.window.firstResponder === terminal, "A later snapshot must not focus the queued terminal")
        app.promptPanel!.dismissForQueue()
        XCTAssertFalse(workspace.window.firstResponder === terminal)
    }

    func testFailedOrNavigationKeysDoNotAdvanceAndNonPromptLookRefusesKeys() throws {
        load(2); try open(1); reads.drain()
        prompt.press(PromptKey.all.first { $0.name == "down" }!); presses.drain()
        XCTAssertEqual(prompt.state?.target.runtimeSessionID, F.runtimeID(1))
        reads.drain()
        service.result = .failure(.undecodable(status: 1, detail: "Refused"))
        prompt.press(PromptKey.digits[1]); presses.drain()
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertTrue(workspace.inspector.sentAnswers.isEmpty)
        service.result = .success(PromptSent(delivery: "accepted"))
        prompt.refresh(); reads.drain()
        prompt.press(PromptKey.digits[1]); presses.drain()
        service.screen = "working"; reads.drain()
        let count = service.keys.count
        prompt.press(PromptKey.digits[1]); presses.drain()
        XCTAssertEqual(service.keys.count, count)
    }

    func testChangedQuestionAndDisconnectedFeedDoNotAdvance() throws {
        load(2); try open(1); reads.drain()
        prompt.press(PromptKey.digits[1])
        var snapshot = try XCTUnwrap(terminals.store.snapshot)
        snapshot.sessions[0].lastEventAt += 1
        apply(snapshot)
        presses.drain()
        XCTAssertTrue(workspace.inspector.sentAnswers.isEmpty)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        app.promptPanel!.dismissForQueue()
        try open(1); reads.drain(); prompt.press(PromptKey.digits[1])
        terminals.store.apply(.disconnected(.heartbeatLost, retryIn: 0.5)); workspace.refresh()
        presses.drain()
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertEqual(prompt.state?.target.runtimeSessionID, F.runtimeID(1))
    }

    func testLateSubmissionUsesOriginalQuestionAndDoesNotMoveNewPanel() throws {
        load(3); try open(1); reads.drain()
        prompt.press(PromptKey.digits[1])
        try open(3)
        presses.drain()
        XCTAssertEqual(prompt.state?.target.runtimeSessionID, F.runtimeID(3))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(3)))
        let first = try XCTUnwrap(workspace.allWaitingRows.first { $0.key == .embedded(F.runtimeID(1)) })
        XCTAssertTrue(workspace.inspector.answerWasSent(first), "A late result retains the submitted question identity")
        XCTAssertFalse(prompt.state?.canPress == true)
        reads.drain()
        XCTAssertEqual(prompt.state?.look.project, F.runtimeID(3))
    }

    func testQueueUsesConfiguredRecentVisitOrder() throws {
        load(3)
        workspace.sidebarGrouping = .compact
        workspace.sidebarSort = .recentlyVisited
        try open(1)
        workspace.order.visit(.embedded(F.runtimeID(2)))
        workspace.order.visit(.embedded(F.runtimeID(3)))
        workspace.order.visit(.embedded(F.runtimeID(1)))
        XCTAssertEqual(workspace.allWaitingRows.map(\.key), [1, 3, 2].map { .embedded(F.runtimeID($0)) })
        sendPrompt()
        XCTAssertEqual(prompt.state?.target.runtimeSessionID, F.runtimeID(3))
    }
}

import AppKit
import XCTest
@testable import AgentMux

private final class ContextASL: ASLService {
    var stale = false
    var wrongQuestion = false
    var reads = 0
    func snapshot() -> Result<ASLResult, ASLRequestError> {
        .success(ASLResult(status: ASLStatus(available: true, snapshotStale: stale), snapshot: ASLSnapshot(rows: [ASLFixtures.replyRow()])))
    }
    func question(planDir: String, questionID: String) -> Result<ASLQuestionBody, ASLRequestError> {
        reads += 1
        return .success(ASLFixtures.body(ASLFixtures.replyRow(), wrongQuestion ? "another" : questionID, "Question body"))
    }
    func act(_ params: ASLRequestParams) -> Result<ASLOperation, ASLRequestError> {
        XCTFail("Reading context must never act")
        return .failure(.unsupported)
    }
}

final class WorkspaceWaitingContextTests: XCTestCase {
    func testRefreshFailureRetainsContentAndAgeButUnavailableClearsIt() {
        var reading = WorkspaceWaitingContext.Reading()
        let at = Date(timeIntervalSince1970: 100)
        reading.begin()
        XCTAssertTrue(reading.loading)
        reading.finish(.loaded(SecretText("Evidence")), at: at)
        reading.begin()
        XCTAssertEqual(reading.content, SecretText("Evidence"))
        reading.finish(.failed(SecretText("Disconnected")), at: at.addingTimeInterval(20))
        XCTAssertTrue(reading.failed)
        XCTAssertEqual(reading.content, SecretText("Evidence"))
        XCTAssertEqual(reading.refreshedAt, at)
        reading.begin()
        reading.finish(.unavailable(SecretText("Question replaced")))
        XCTAssertNil(reading.content)
        XCTAssertNil(reading.refreshedAt)
        XCTAssertTrue(reading.unavailable)
    }

    @MainActor
    func testWaitingRetryKeepsReaderAndRejectsReplacedQuestion() throws {
        let inspector = WorkspaceInspectorView()
        let row = WorkspaceWaitingRow(key: .embedded("one"), title: "API", detail: "", glyph: "?", toolTip: "",
            answer: .asl(ASLAnswerTarget(runID: "run-reply")), contextVersion: "first")
        var replies: [(WorkspaceWaitingContext.Result) -> Void] = []
        inspector.onContext = { _, receive in replies.append(receive) }
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 300, height: 650), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = inspector
        defer { window.close() }
        inspector.show(.init(waiting: [row]))
        inspector.toggleContext(row)
        replies[0](.loaded(SecretText("# Deployment\n\n- Review staging\n\n```sh\nmake check\n```")))
        window.layoutIfNeeded()
        let card = try XCTUnwrap(inspector.lines.compactMap { $0 as? WorkspaceWaitingRowView }.first)
        XCTAssertFalse(card.contextScroll.documentView === card.contextLabel, "ASL uses the shared formatted prose reader")
        let document = card.contextScroll.documentView
        card.refreshContextButton.performClick(nil)
        XCTAssertEqual(replies.count, 2)
        XCTAssertTrue(card.contextScroll.documentView === document)
        XCTAssertTrue(card.contextStatus.stringValue.contains("Previous context"))
        replies[1](.failed(SecretText("Connection lost")))
        XCTAssertEqual(card.refreshContextButton.title, "Retry")
        XCTAssertTrue(card.contextStatus.stringValue.contains("Context read failed"))
        XCTAssertTrue(card.contextScroll.documentView === document)
        card.refreshContextButton.performClick(nil)
        var replacement = row; replacement.contextVersion = "second"
        inspector.show(.init(waiting: [replacement]))
        replies[2](.loaded(SecretText("Obsolete question")))
        XCTAssertFalse(card.contextLabel.stringValue.contains("Obsolete"))
        XCTAssertTrue(card.contextScroll.isHidden)
        XCTAssertFalse(card.refreshContextButton.isEnabled)
    }

    func testSupervisorContextNeverReadsPaneOrUsesStaleSnapshot() {
        let service = ContextASL()
        let row = WorkspaceWaitingRow(key: .embedded("rs-one"), title: "Run", detail: "", glyph: "", toolTip: "",
                                      answer: .asl(ASLAnswerTarget(runID: "run-reply")))
        func read() -> WorkspaceWaitingContext.Result {
            WorkspaceWaitingContext.read(row, asl: service) { _ in
                XCTFail("A supervisor question is read from its mailbox")
                return .success(PromptLook())
            }
        }
        XCTAssertEqual(read(), .loaded(SecretText("Question body")))
        service.stale = true
        XCTAssertNotEqual(read(), .loaded(SecretText("Question body")))
        XCTAssertEqual(service.reads, 1)
        service.stale = false
        service.wrongQuestion = true
        XCTAssertNotEqual(read(), .loaded(SecretText("Question body")))
    }

    func testCompletedTurnContextReadsOutputWithoutSendingKeys() {
        let row = WorkspaceWaitingRow(key: .embedded("rs-one"), title: "Completed", detail: "", glyph: "", toolTip: "")
        let text = WorkspaceWaitingContext.read(row, asl: ContextASL()) { id in
            XCTAssertEqual(id, "rs-one")
            return .success(PromptLook(screen: "idle", lines: ["Tests passed", "Ready for review"]))
        }
        XCTAssertEqual(text, .loaded(SecretText("Tests passed\nReady for review")))
        XCTAssertFalse(String(describing: text).contains("Tests passed"))
    }
}

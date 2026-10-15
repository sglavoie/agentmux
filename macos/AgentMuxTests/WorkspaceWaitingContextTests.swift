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
    func testSupervisorContextNeverReadsPaneOrUsesStaleSnapshot() {
        let service = ContextASL()
        let row = WorkspaceWaitingRow(key: .embedded("rs-one"), title: "Run", detail: "", glyph: "", toolTip: "",
                                      answer: .asl(ASLAnswerTarget(runID: "run-reply")))
        func read() -> String {
            WorkspaceWaitingContext.read(row, asl: service) { _ in
                XCTFail("A supervisor question is read from its mailbox")
                return .success(PromptLook())
            }.reveal()
        }
        XCTAssertEqual(read(), "Question body")
        service.stale = true
        XCTAssertNotEqual(read(), "Question body")
        XCTAssertEqual(service.reads, 1)
        service.stale = false
        service.wrongQuestion = true
        XCTAssertNotEqual(read(), "Question body")
    }

    func testCompletedTurnContextReadsOutputWithoutSendingKeys() {
        let row = WorkspaceWaitingRow(key: .embedded("rs-one"), title: "Completed", detail: "", glyph: "", toolTip: "")
        let text = WorkspaceWaitingContext.read(row, asl: ContextASL()) { id in
            XCTAssertEqual(id, "rs-one")
            return .success(PromptLook(screen: "idle", lines: ["Tests passed", "Ready for review"]))
        }
        XCTAssertEqual(text.reveal(), "Tests passed\nReady for review")
        XCTAssertEqual(String(describing: text), "[redacted]")
    }
}

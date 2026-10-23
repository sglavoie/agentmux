import XCTest
@testable import AgentMux

@MainActor
final class WorkspaceFeedbackPersistenceTests: XCTestCase {
    func testFailuresSurviveRestartAndRecoveryRevalidatesDestination() throws {
        let suite = "amx.feedback.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let history = WorkspaceFeedbackHistory(defaults: defaults)
        let failure = WorkspaceFeedback(target: "codex:original", message: "Resume outcome unknown")
        var oldAction = 0
        history.record(failure, destination: .conversation("codex:original"), recover: { oldAction += 1 })
        history.record(failure, destination: .conversation("codex:original"))
        for i in 0..<40 { history.record(.init(target: "success-\(i)", message: "Done", kind: .success, recovery: nil)) }
        XCTAssertEqual(history.entries.count, 30)
        let reopened = WorkspaceFeedbackHistory(defaults: defaults)
        let entry = try XCTUnwrap(reopened.entries.first)
        XCTAssertEqual(reopened.entries.count, 1)
        XCTAssertEqual(entry.occurrences, 2)
        XCTAssertEqual(entry.feedback, failure)
        XCTAssertFalse(entry.available())
        entry.recover()
        var destinations: [WorkspaceFeedbackDestination] = []
        reopened.open = { destinations.append($0) }
        reopened.canOpen = { _ in true }
        XCTAssertTrue(entry.available())
        entry.recover()
        XCTAssertEqual(destinations, [.conversation("codex:original")])
        XCTAssertEqual(oldAction, 0)
        reopened.canOpen = { _ in false }
        entry.recover()
        XCTAssertEqual(destinations.count, 1)
        reopened.dismiss(entry.id)
        XCTAssertTrue(WorkspaceFeedbackHistory(defaults: defaults).entries.isEmpty)
    }

    func testRestartedReceiptOnlyOpensOriginalRunAndPlanOptionsRoundTrip() throws {
        let suite = "amx.feedback.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let history = WorkspaceFeedbackHistory(defaults: defaults)
        let plan = WorkspaceFeedbackDestination.plan(.init(plan: "/plans/one", project: "api", profile: "work", harness: "codex", reviewHarness: "claude", commitMode: "at-end"))
        history.record(.init(target: "/plans/one", message: "Readiness failed", recovery: .readiness), destination: plan)
        history.record(.init(target: "run", message: "Unknown outcome", recovery: .receipt), destination: .runs(rowID: "row", runID: "original"))
        let reopened = WorkspaceFeedbackHistory(defaults: defaults)
        XCTAssertEqual(reopened.entries.first?.feedback.recovery, .runs)
        XCTAssertEqual(reopened.entries.last?.destination, plan)
        XCTAssertEqual(reopened.entries.first?.destination, .runs(rowID: "row", runID: "original"))
        defaults.set(Data("corrupt".utf8), forKey: WorkspaceFeedbackHistory.storageKey)
        XCTAssertTrue(WorkspaceFeedbackHistory(defaults: defaults).entries.isEmpty)
    }
}

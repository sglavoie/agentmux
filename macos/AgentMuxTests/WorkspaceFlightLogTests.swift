import AppKit
import XCTest
@testable import AgentMux

private final class FlightRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func drain() { while !pending.isEmpty { pending.removeFirst()() } }
}

enum FlightLogFixture {
    static func data() throws -> Data {
        try Data(contentsOf: URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("Fixtures/flight-log.json"))
    }
    static func command(_ data: @escaping ([String]) throws -> Data = { _ in try FlightLogFixture.data() }) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/fixture", path: "/bin"))
        command.runner = { _, args, _, _ in
            let payload = try! JSONSerialization.jsonObject(with: data(args))
            return .finished(.init(status: 0, stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": payload]), stderr: Data()))
        }
        return command
    }
}

@MainActor
final class WorkspaceFlightLogTests: XCTestCase {
    func testGoReportDecodesAndScorecardsNavigateToExactConversation() throws {
        let runner = FlightRunner()
        let command = FlightLogFixture.command { _ in
            var payload = try XCTUnwrap(JSONSerialization.jsonObject(with: FlightLogFixture.data()) as? [String: Any])
            var sessions = try XCTUnwrap(payload["sessions"] as? [[String: Any]])
            let index = try XCTUnwrap(sessions.firstIndex { $0["key"] as? String == "rs-flight-1" })
            sessions[index]["conversation_id"] = "codex:a-newer-conversation"
            payload["sessions"] = sessions
            return try JSONSerialization.data(withJSONObject: payload)
        }
        let model = WorkspaceFlightLogModel(command: { command }, runner: runner)
        model.refresh(); runner.drain()
        XCTAssertNil(model.error)
        let report = try XCTUnwrap(model.report)
        XCTAssertEqual(report.totals.launches, 4)
        XCTAssertEqual(report.totals.stateChanges, 12)
        XCTAssertEqual(report.totals.workingSeconds, 80)
        XCTAssertEqual(report.sessions.count, 4)
        XCTAssertEqual(report.sessions.first?.timeline.count, 3)
        model.selection = "rs-flight-1"; model.inspect("question")
        XCTAssertEqual(model.visibleEvents.count, 1)
        var opened: ConversationIdentity?, eventTime: String?
        model.onConversation = { identity, at in opened = identity; eventTime = at }
        model.openConversation(at: model.visibleEvents[0].at)
        XCTAssertEqual(opened, ConversationIdentity(encoded: "codex:conversation-1"))
        XCTAssertEqual(eventTime, model.visibleEvents[0].at)
        let achievement = try XCTUnwrap(report.achievements.first { $0.id == "first-flight" })
        model.project = "Other"; model.revealAchievement(achievement); runner.drain()
        XCTAssertEqual(model.selection, "rs-flight-0")
        XCTAssertEqual(model.project, ""); XCTAssertEqual(model.period, "all")
    }

    func testCalendarOutsideWeekLoadsAllTimeAndClosingRejectsStaleResponse() throws {
        let runner = FlightRunner(), command = FlightLogFixture.command()
        let model = WorkspaceFlightLogModel(command: { command }, runner: runner)
        model.refresh(); runner.drain()
        model.selectDay("2026-10-30")
        XCTAssertEqual(model.period, "all"); runner.drain()
        XCTAssertEqual(model.visibleSessions.count, 4)
        model.selectDay("2026-10-29")
        XCTAssertTrue(model.visibleSessions.isEmpty)
        model.close(); model.refresh(); model.close(); runner.drain()
        XCTAssertFalse(model.loading)
    }

    func testCelebrationsAreOptionalAndSettingsUseTheCLIContract() throws {
        let runner = FlightRunner()
        var payload = try XCTUnwrap(JSONSerialization.jsonObject(with: FlightLogFixture.data()) as? [String: Any])
        var calls: [[String]] = []
        let command = FlightLogFixture.command { args in
            calls.append(args)
            if args.contains("settings") { return Data(#"{"enabled":true,"celebrations":false}"#.utf8) }
            return try JSONSerialization.data(withJSONObject: payload)
        }
        let model = WorkspaceFlightLogModel(command: { command }, runner: runner)
        var achievements = try XCTUnwrap(payload["achievements"] as? [[String: Any]])
        achievements[1]["unlocked_at"] = "2026-10-30T15:30:00Z" // unlock on the next refresh
        model.refresh(); runner.drain(); XCTAssertNil(model.celebration, "Opening history does not celebrate historical unlocks")
        payload["achievements"] = achievements
        model.refresh(); runner.drain(); XCTAssertTrue(model.celebration?.contains("Frequent Flyer") == true)
        model.dismissCelebration(); model.settings(celebrations: false); runner.drain()
        XCTAssertTrue(calls.contains { $0.contains("--celebrations=false") })
        XCTAssertFalse(model.acting)
    }

    func testCalendarFindsAnInteriorDayOfACompactedObservationSpan() throws {
        let runner = FlightRunner()
        let command = FlightLogFixture.command { _ in
            var payload = try XCTUnwrap(JSONSerialization.jsonObject(with: FlightLogFixture.data()) as? [String: Any])
            var sessions = try XCTUnwrap(payload["sessions"] as? [[String: Any]])
            sessions[0]["timeline"] = [["from": "2026-10-27T00:00:00Z", "to": "2026-10-30T00:00:00Z", "icon": "bolt", "looping": false, "observed": true]]
            payload["sessions"] = sessions
            return try JSONSerialization.data(withJSONObject: payload)
        }
        let model = WorkspaceFlightLogModel(command: { command }, runner: runner)
        model.refresh(); runner.drain(); model.selectDay("2026-10-28"); runner.drain()
        XCTAssertEqual(model.visibleSessions.count, 1)
        XCTAssertEqual(model.visibleSessions.first?.key, "rs-flight-3")
        model.inspect("question")
        XCTAssertTrue(model.visibleSessions.isEmpty, "Questions from another date must not match the selected day")
    }

    func testReadFailureRetainsScorecardsAndReportsTheError() throws {
        let runner = FlightRunner(), command = FlightLogFixture.command()
        var available = true
        let model = WorkspaceFlightLogModel(command: { available ? command : nil }, runner: runner)
        model.refresh(); runner.drain()
        available = false; model.refresh()
        XCTAssertNotNil(model.error); XCTAssertEqual(model.report?.sessions.count, 4)
        XCTAssertFalse(model.loading)
    }

    func testRetainedActivityDrilldownUsesRuntimeIdentityAndKind() throws {
        let report = try JSONDecoder().decode(FlightReport.self, from: FlightLogFixture.data())
        let session = try XCTUnwrap(report.sessions.first)
        let runner = FlightRunner()
        let activity = WorkspaceActivityModel(command: { nil }, runner: runner)
        activity.filterFlightSession(session, kind: "question")
        XCTAssertEqual(activity.flightSession?.runtimeSessionID, session.runtimeSessionID)
        XCTAssertEqual(activity.flightKind, "question")
        XCTAssertEqual(activity.days, 90); XCTAssertFalse(activity.unreadOnly)
        activity.clearFlightFilter(); XCTAssertNil(activity.flightSession)
    }
}

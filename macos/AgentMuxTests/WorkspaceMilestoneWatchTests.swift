import AppKit
import XCTest
import SwiftUI
@testable import AgentMux

private final class MilestoneRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release() { pending.removeFirst()() }
}

@MainActor
final class WorkspaceMilestoneWatchTests: XCTestCase {
    private var defaults: UserDefaults!
    private var suite: String!
    override func setUp() {
        super.setUp()
        suite = "WorkspaceMilestoneWatchTests.\(UUID().uuidString)"
        defaults = UserDefaults(suiteName: suite)!
    }
    override func tearDown() {
        defaults.removePersistentDomain(forName: suite)
        defaults = nil
        super.tearDown()
    }

    private func data(_ achievements: [(String, Bool?, Bool)], enabled: Bool = true,
                      celebrations: Bool = true, since: String = "2026-10-26T10:00:00Z") throws -> Data {
        var payload = try XCTUnwrap(JSONSerialization.jsonObject(with: FlightLogFixture.data()) as? [String: Any])
        payload["tracking_since"] = since
        payload["settings"] = ["enabled": enabled, "celebrations": celebrations]
        payload["achievements"] = achievements.map { id, eligible, unlocked in
            var value: [String: Any] = ["id": id, "title": id, "description": "", "session_key": "", "progress": 1, "target": 1]
            if unlocked { value["unlocked_at"] = "2026-10-26T10:00:15Z" }
            if let eligible { value["celebration_eligible"] = eligible }
            return value
        }
        return try JSONSerialization.data(withJSONObject: payload)
    }
    private func report(_ achievements: [(String, Bool?, Bool)], enabled: Bool = true,
                        celebrations: Bool = true, since: String = "2026-10-26T10:00:00Z") throws -> FlightReport {
        try JSONDecoder().decode(FlightReport.self, from: data(achievements, enabled: enabled, celebrations: celebrations, since: since))
    }

    func testBaselineThenNewUnlockOnceAcrossAppRestartInReportOrder() throws {
        var watch = WorkspaceMilestoneWatch(defaults: defaults)
        XCTAssertEqual(try watch.newlyUnlocked(in: report([("old", true, true), ("locked", true, false)])), [])
        let next = try report([("old", true, true), ("second", true, true), ("first", true, true), ("locked", true, false)])
        XCTAssertEqual(watch.newlyUnlocked(in: next).map(\.id), ["second", "first"])
        watch = WorkspaceMilestoneWatch(defaults: try XCTUnwrap(UserDefaults(suiteName: suite)))
        XCTAssertEqual(watch.newlyUnlocked(in: next), [])
        XCTAssertFalse(defaults.stringArray(forKey: "amxSeenMilestones")!.contains("locked"))
    }

    func testDisabledSettingsRecordUnlocksWithoutDeferringThem() throws {
        for tracking in [false, true] {
            defaults.removePersistentDomain(forName: suite)
            var watch = WorkspaceMilestoneWatch(defaults: defaults)
            XCTAssertEqual(try watch.newlyUnlocked(in: report([])), [])
            XCTAssertEqual(try watch.newlyUnlocked(in: report([("off", true, true)], enabled: !tracking, celebrations: tracking)), [])
            XCTAssertEqual(try watch.newlyUnlocked(in: report([("off", true, true)])), [])
        }
    }

    func testNewTrackingPeriodAndMissingSeenSetAreBaselines() throws {
        var watch = WorkspaceMilestoneWatch(defaults: defaults)
        XCTAssertEqual(try watch.newlyUnlocked(in: report([])), [])
        XCTAssertEqual(try watch.newlyUnlocked(in: report([("new-period", true, true)], since: "2026-11-01T00:00:00Z")), [])
        XCTAssertEqual(defaults.stringArray(forKey: "amxSeenMilestones"), ["new-period"])
        defaults.removeObject(forKey: "amxSeenMilestones")
        XCTAssertEqual(try watch.newlyUnlocked(in: report([("missing-set", true, true)], since: "2026-11-01T00:00:00Z")), [])
    }

    func testUnobservedOffPeriodAndUnknownEligibilityNeverAnnounceAfterRestart() throws {
        var watch = WorkspaceMilestoneWatch(defaults: defaults)
        XCTAssertEqual(try watch.newlyUnlocked(in: report([])), [])
        watch = WorkspaceMilestoneWatch(defaults: try XCTUnwrap(UserDefaults(suiteName: suite)))
        // Both settings are now true, but durable evidence records the off-period unlock.
        let after = try report([("off-period", false, true), ("legacy", nil, true), ("after-enable", true, true)])
        XCTAssertEqual(after.achievements[0].celebrationEligible, false)
        XCTAssertNil(after.achievements[1].celebrationEligible)
        XCTAssertEqual(watch.newlyUnlocked(in: after).map(\.id), ["after-enable"])
        XCTAssertEqual(watch.newlyUnlocked(in: after), [])
        // Even changed eligibility cannot resurrect an already observed unlock.
        XCTAssertEqual(try watch.newlyUnlocked(in: report([("off-period", true, true), ("legacy", true, true)])), [])
        XCTAssertEqual(Set(defaults.stringArray(forKey: "amxSeenMilestones")!), ["off-period", "legacy", "after-enable"])
    }

    func testGoEligibilityFixtureSuppressesOffPeriodAndEmitsLaterUnlockOnce() throws {
        let url = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
            .appendingPathComponent("Fixtures/flight-celebration-eligibility.json")
        let achievements = try JSONSerialization.jsonObject(with: Data(contentsOf: url))
        var payload = try XCTUnwrap(JSONSerialization.jsonObject(with: data([])) as? [String: Any])
        payload["achievements"] = achievements
        let report = try JSONDecoder().decode(FlightReport.self, from: JSONSerialization.data(withJSONObject: payload))
        var watch = WorkspaceMilestoneWatch(defaults: defaults)
        XCTAssertEqual(try watch.newlyUnlocked(in: self.report([])), [])
        XCTAssertEqual(report.achievements.first?.celebrationEligible, false)
        XCTAssertEqual(watch.newlyUnlocked(in: report).map(\.id), ["branching-out"])
        watch = WorkspaceMilestoneWatch(defaults: try XCTUnwrap(UserDefaults(suiteName: suite)))
        XCTAssertEqual(watch.newlyUnlocked(in: report), [])
    }

    func testMixedAndMissionOnlyReportsNeverAnnounceMissionPatches() throws {
        var watch = WorkspaceMilestoneWatch(defaults: defaults)
        XCTAssertEqual(try watch.newlyUnlocked(in: report([])), [])
        XCTAssertEqual(try watch.newlyUnlocked(in: report([("mission:one", true, true), ("ordinary", true, true)])).map(\.id), ["ordinary"])
        XCTAssertEqual(try watch.newlyUnlocked(in: report([("mission:two", true, true)])), [])
        XCTAssertTrue(defaults.stringArray(forKey: "amxSeenMilestones")!.contains("mission:two"))
    }

    func testCaughtUpWiringRunsExactCommandAndRoutesFlightLog() throws {
        let runner = MilestoneRunner(), app = FakeWorkspaceApp()
        var payload = try data([])
        var calls: [[String]] = []
        app.recapCommand = FlightLogFixture.command { args in calls.append(args); return payload }
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        workspace.milestoneRunner = runner
        workspace.milestoneWatch = WorkspaceMilestoneWatch(defaults: defaults)
        workspace.welcome.schedule = { _, _ in }
        XCTAssertTrue(runner.pending.isEmpty)
        workspace.welcome.answerSent()
        XCTAssertTrue(runner.pending.isEmpty)
        workspace.welcome.allCaughtUp(); runner.release()
        XCTAssertNil(workspace.welcome.receipt?.milestone)
        payload = try data([("milestone", true, true), ("mission:patch", true, true)])
        workspace.welcome.allCaughtUp(); runner.release()
        XCTAssertEqual(workspace.welcome.receipt?.milestone?.id, "milestone")
        XCTAssertEqual(calls, Array(repeating: ["session", "flight-log", "--period", "all", "--json"], count: 2))
        workspace.welcome.onAction(.flightLog)
        XCTAssertEqual(app.performedCommands, [.flightLog])
    }

    func testReceiptLinkPerformsFlightLogAction() async throws {
        let model = WorkspaceWelcomeModel(command: { nil })
        model.schedule = { _, _ in }
        model.allCaughtUp()
        model.milestoneUnlocked(.init(id: "first-flight", title: "First Flight"))
        var opened: [WorkspaceCommand] = []
        model.onAction = { opened.append($0) }
        let host = NSHostingView(rootView: WorkspaceWelcomeView(model: model, width: 260))
        let window = NSWindow(contentRect: NSRect(x: 100, y: 100, width: 260, height: 220),
                              styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.contentView = host; window.makeKeyAndOrderFront(nil)
        defer { window.close() }
        try await Task.sleep(for: .milliseconds(200))
        func buttons(_ view: NSView) -> [NSButton] {
            (view as? NSButton).map { [$0] } ?? view.subviews.flatMap(buttons)
        }
        // SwiftUI's hosted link labels are not reliably exposed to in-process
        // AX lookup; exercise the native controls' actual actions.
        let controls = buttons(host)
        XCTAssertEqual(controls.count, 2, "Open Flight Log and Open Briefing are native link buttons")
        for button in controls { button.performClick(nil) }
        XCTAssertTrue(opened.contains(.flightLog))
        XCTAssertTrue(opened.contains(.briefing))
    }

    func testFailedReadLeavesSeenStateAndReceiptAlone() {
        let runner = MilestoneRunner(), app = FakeWorkspaceApp()
        var command = AMXCommand(binary: URL(fileURLWithPath: "/fixture/amx"), launchEnvironment: LaunchEnvironment(home: "/fixture", path: "/bin"))
        command.runner = { _, _, _, _ in .finished(.init(status: 1, stdout: Data(), stderr: Data())) }
        app.recapCommand = command
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        workspace.milestoneRunner = runner
        workspace.milestoneWatch = WorkspaceMilestoneWatch(defaults: defaults)
        workspace.welcome.schedule = { _, _ in }
        workspace.welcome.allCaughtUp(); runner.release()
        XCTAssertEqual(workspace.welcome.receipt?.title, "All caught up")
        XCTAssertNil(workspace.welcome.receipt?.milestone)
        XCTAssertNil(defaults.object(forKey: "amxSeenMilestones"))
    }

    func testLateReadCannotResurrectExpiredReceipt() throws {
        let runner = MilestoneRunner(), app = FakeWorkspaceApp()
        var payload = try data([])
        app.recapCommand = FlightLogFixture.command { _ in payload }
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        workspace.milestoneRunner = runner
        workspace.milestoneWatch = WorkspaceMilestoneWatch(defaults: defaults)
        var expiry: DispatchWorkItem?
        workspace.welcome.schedule = { _, work in expiry = work }
        workspace.welcome.allCaughtUp(); runner.release()
        payload = try data([("late", true, true)])
        workspace.welcome.allCaughtUp()
        expiry?.perform(); runner.release()
        XCTAssertNil(workspace.welcome.receipt)
        XCTAssertEqual(defaults.stringArray(forKey: "amxSeenMilestones"), ["late"])
    }
}

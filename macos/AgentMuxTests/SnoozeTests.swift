import XCTest
@testable import AgentMux

final class SnoozeTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    private var utc: Calendar {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        return calendar
    }

    // MARK: Choices

    func testChoicesBuildTheirRequests() throws {
        XCTAssertEqual(SnoozeChoice.oneHour.request(now: now), SnoozeRequest(mode: "until", until: 1_800_003_600))
        XCTAssertEqual(SnoozeChoice.untilResumed.request(now: now), SnoozeRequest(mode: "indefinite"))
        XCTAssertEqual(SnoozeChoice.resume.request(now: now), SnoozeRequest(mode: "clear"))

        // 1_800_000_000 is 2027-01-15 08:00:00 UTC; tomorrow 08:00 is a day on.
        let morning = SnoozeChoice.untilTomorrowMorning.request(now: now, calendar: utc)
        XCTAssertEqual(morning, SnoozeRequest(mode: "until", until: 1_800_000_000 + 86400))
        let lateNight = now.addingTimeInterval(15 * 3600 + 59 * 60) // 23:59
        XCTAssertEqual(SnoozeChoice.tomorrowMorning(after: lateNight, calendar: utc), Date(timeIntervalSince1970: 1_800_000_000 + 86400))
    }

    func testRequestsEncodeAsTheDaemonReadsThem() throws {
        let set = try JSONEncoder().encode(DaemonRequest.snoozing(.ending(at: Date(timeIntervalSince1970: 1_800_003_600))))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: set) as? [String: Any])
        XCTAssertEqual(object["type"] as? String, "notification_snooze")
        let snooze = try XCTUnwrap(object["snooze"] as? [String: Any])
        XCTAssertEqual(snooze["mode"] as? String, "until")
        XCTAssertEqual(snooze["until"] as? Int, 1_800_003_600)

        let clear = try JSONEncoder().encode(DaemonRequest.snoozing(.resume))
        let cleared = try XCTUnwrap(JSONSerialization.jsonObject(with: clear) as? [String: Any])
        XCTAssertEqual((cleared["snooze"] as? [String: Any])?.keys.sorted(), ["mode"], "until is left off when unset")
    }

    // MARK: Daemon replies

    func testSnoozeOutcomes() {
        XCTAssertEqual(DaemonClient.snoozeOutcome(fromReply: Data(#"{"ok":true,"snooze":{"snoozed_until":1800003600}}"#.utf8)),
                       .state(SnoozeState(snoozedUntil: 1_800_003_600)))
        XCTAssertEqual(DaemonClient.snoozeOutcome(fromReply: Data(#"{"ok":true,"snooze":{"indefinite":true}}"#.utf8)),
                       .state(SnoozeState(indefinite: true)))
        XCTAssertEqual(DaemonClient.snoozeOutcome(fromReply: Data(#"{"ok":true}"#.utf8)), .state(SnoozeState()))
        XCTAssertEqual(DaemonClient.snoozeOutcome(fromReply: Data(#"{"ok":false,"error":"unknown request type: notification_snooze"}"#.utf8)),
                       .unsupported)
        XCTAssertEqual(DaemonClient.snoozeOutcome(fromReply: Data(#"{"ok":false,"error":"notification_snooze: until is not in the future"}"#.utf8)),
                       .failed("notification_snooze: until is not in the future"))
        XCTAssertEqual(DaemonClient.snoozeOutcome(fromReply: Data("nope".utf8)), .failed("unreadable notification_snooze reply"))
    }

    func testClientSendsOneSnoozeRequest() throws {
        var sent: Data?
        var client = DaemonClient(socketPath: "/tmp/d.sock")
        client.roundTrip = { path, request, timeout in
            XCTAssertEqual(path, "/tmp/d.sock")
            XCTAssertEqual(timeout, DaemonClient.snoozeTimeout)
            sent = request
            return Data(#"{"ok":true,"snooze":{"indefinite":true}}"#.utf8)
        }
        XCTAssertEqual(client.notificationSnooze(.untilResumed), .state(SnoozeState(indefinite: true)))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: try XCTUnwrap(sent)) as? [String: Any])
        XCTAssertEqual((object["snooze"] as? [String: Any])?["mode"] as? String, "indefinite")

        client.roundTrip = { _, _, _ in throw LineSocketError.noSocket }
        XCTAssertEqual(client.notificationSnooze(.resume), .notRunning)
    }

    // MARK: State

    func testStateMirrorsIntoAmxSnoozeUntil() {
        XCTAssertNil(SnoozeState().mirrored(now: now))
        XCTAssertEqual(SnoozeState(indefinite: true).mirrored(now: now), .distantFuture)
        XCTAssertEqual(SnoozeState(snoozedUntil: 1_800_000_060).mirrored(now: now), now.addingTimeInterval(60))
        XCTAssertNil(SnoozeState(snoozedUntil: 1_800_000_000).mirrored(now: now), "a snooze ending now has run out")
        XCTAssertEqual(SnoozeState(snoozedUntil: 1_800_000_060).description(now: now) { _ in "T" }, "Snoozed until T")
        XCTAssertEqual(SnoozeState(indefinite: true).description(now: now), "Snoozed until resumed")
        XCTAssertEqual(SnoozeState(snoozedUntil: 5).description(now: now), "Not snoozed")
    }

    // MARK: Mirror

    func testReappliesASnoozeANewDaemonLost() {
        let until = now.addingTimeInterval(1800)
        XCTAssertEqual(
            SnoozeMirror.reconcile(daemon: "2:20", live: SnoozeState(), stored: until, storedDaemon: "1:10", now: now),
            .reapply(.ending(at: until))
        )
        XCTAssertEqual(
            SnoozeMirror.reconcile(daemon: "2:20", live: SnoozeState(), stored: .distantFuture, storedDaemon: "1:10", now: now),
            .reapply(.untilResumed)
        )
        XCTAssertEqual(
            SnoozeMirror.reconcile(daemon: "2:20", live: SnoozeState(), stored: until, storedDaemon: nil, now: now),
            .reapply(.ending(at: until)),
            "a snooze stored before any daemon was recorded is re-applied too"
        )
    }

    func testAdoptsWhatTheSameDaemonReports() {
        let until = now.addingTimeInterval(1800)
        XCTAssertEqual(
            SnoozeMirror.reconcile(daemon: "1:10", live: SnoozeState(), stored: until, storedDaemon: "1:10", now: now),
            .adopt(nil),
            "a snooze cleared with `amx notifications snooze off` stays cleared"
        )
        XCTAssertEqual(
            SnoozeMirror.reconcile(daemon: "1:10", live: SnoozeState(snoozedUntil: 1_800_003_600), stored: nil, storedDaemon: "1:10", now: now),
            .adopt(now.addingTimeInterval(3600))
        )
        XCTAssertEqual(
            SnoozeMirror.reconcile(daemon: "1:10", live: SnoozeState(snoozedUntil: 1_800_001_800), stored: until, storedDaemon: "1:10", now: now),
            .keep
        )
        XCTAssertEqual(
            SnoozeMirror.reconcile(daemon: "2:20", live: SnoozeState(indefinite: true), stored: until, storedDaemon: "1:10", now: now),
            .adopt(.distantFuture),
            "a new daemon that is already snoozed wins"
        )
    }

    func testExpiredSnoozeIsNotReapplied() {
        XCTAssertEqual(
            SnoozeMirror.reconcile(daemon: "2:20", live: SnoozeState(), stored: now.addingTimeInterval(-1), storedDaemon: "1:10", now: now),
            .adopt(nil)
        )
        XCTAssertEqual(
            SnoozeMirror.reconcile(daemon: "2:20", live: SnoozeState(), stored: nil, storedDaemon: "1:10", now: now),
            .keep
        )
    }

    func testDaemonIdentity() {
        var daemon = CompanionDaemon()
        daemon.pid = 42
        daemon.startedAt = 1_700_000_000
        XCTAssertEqual(SnoozeMirror.identity(of: daemon), "42:1700000000")
    }

    // MARK: Menu

    func testNotificationEntries() {
        var input = MenuBuilder.ActionsInput()
        input.now = now
        XCTAssertEqual(MenuBuilder.notificationEntries(input), [.info("The daemon is not connected")])

        var notification = CompanionNotification()
        notification.quietHours = "22:00-07:00"
        input.notification = notification
        input.configPath = "~/.config/agent-mux/config.toml"
        XCTAssertEqual(MenuBuilder.notificationEntries(input), [
            .info("Not snoozed"),
            .action(.snooze(.oneHour), "Snooze for 1 Hour"),
            .action(.snooze(.untilTomorrowMorning), "Snooze Until Tomorrow 08:00"),
            .action(.snooze(.untilResumed), "Snooze Until Resumed"),
            .unavailable("Resume Now", reason: "Notifications are not snoozed"),
            .separator,
            .info("Quiet hours: 22:00-07:00"),
            .info("Set in ~/.config/agent-mux/config.toml"),
        ])

        notification.indefinite = true
        notification.quietHours = ""
        input.notification = notification
        let entries = MenuBuilder.notificationEntries(input)
        XCTAssertEqual(entries[0], .info("Snoozed until resumed"))
        XCTAssertEqual(entries[3], .unavailable("Snooze Until Resumed", reason: "Already snoozed until resumed"))
        XCTAssertEqual(entries[4], .action(.snooze(.resume), "Resume Now"))
        XCTAssertEqual(entries[6], .info("Quiet hours: off"))
    }
}

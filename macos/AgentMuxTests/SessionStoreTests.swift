import XCTest
@testable import AgentMux

@MainActor
final class SessionStoreTests: XCTestCase {
    private func snapshot(_ ids: [Int], key: Int = 0) -> CompanionSnapshot {
        var snapshot = CompanionSnapshot()
        snapshot.daemon.ready = true
        snapshot.daemon.build = DaemonBuild(version: "1.0.0")
        snapshot.attention = CompanionAttention(total: key, key: key)
        snapshot.sessions = ids.map { CompanionSession(windowID: $0) }
        return snapshot
    }

    func testSnapshotsDriveTheFeedAndTitle() {
        let store = SessionStore()
        var changes = 0
        store.onChange = { changes += 1 }
        XCTAssertEqual(store.feed, .connecting)
        XCTAssertEqual(store.title(glyphs: .fallback), "")

        store.apply(.subscribed(SubscriptionAck()))
        store.apply(.frame(.snapshot(seq: 1, snapshot([1, 2], key: 1))))
        XCTAssertEqual(store.feed, .live)
        XCTAssertEqual(store.title(glyphs: .fallback), "🔑1")
        XCTAssertEqual(store.daemonStatus, .ready(DaemonBuild(version: "1.0.0")))
        XCTAssertEqual(store.sessions.map(\.windowID), [1, 2])
        store.apply(.frame(.heartbeat(seq: 2)))
        store.apply(.connecting(attempt: 0))
        XCTAssertEqual(changes, 2, "heartbeats and dial attempts do not redraw")
    }

    func testDisconnects() {
        var clock = Date(timeIntervalSince1970: 1_000)
        let store = SessionStore(now: { clock })
        store.apply(.frame(.snapshot(seq: 1, snapshot([1]))))
        store.apply(.disconnected(.heartbeatLost, retryIn: 1))
        XCTAssertEqual(store.feed, .reconnecting("the daemon is not answering"))
        XCTAssertEqual(store.title(glyphs: .fallback), "", "no counts from a feed that is not live")
        XCTAssertNotNil(store.snapshot, "a reconnect keeps the last snapshot")

        store.apply(.disconnected(.bye("shutdown"), retryIn: 0.5))
        XCTAssertEqual(store.feed, .reconnecting("the daemon is restarting"))

        clock += SessionStore.byeHold
        store.apply(.disconnected(.notRunning, retryIn: 1))
        XCTAssertEqual(store.feed, .notRunning)
        XCTAssertNil(store.snapshot)
        XCTAssertEqual(store.daemonStatus, .notRunning)

        store.apply(.disconnected(.unsupported, retryIn: 10))
        XCTAssertEqual(store.feed, .daemonTooOld)
        store.apply(health: .ready(nil))
        XCTAssertEqual(store.daemonStatus, .ready(nil), "an old daemon's line comes from health")
        store.apply(health: .notRunning)
        XCTAssertEqual(store.feed, .daemonTooOld, "health does not override what subscribe learned")
    }

    func testDisconnectsReadAsOperatorPhrases() {
        let store = SessionStore()
        store.apply(.disconnected(.refused("subscriber_limit"), retryIn: 1))
        XCTAssertEqual(store.feed, .reconnecting("too many app connections — quit other copies"))
        store.apply(.disconnected(.refused("shutting_down"), retryIn: 1))
        XCTAssertEqual(store.feed, .reconnecting("the daemon is shutting down"))
        store.apply(.disconnected(.refused("something new"), retryIn: 1))
        XCTAssertEqual(store.feed, .reconnecting("the daemon refused the connection"))
        store.apply(.disconnected(.lost(LineSocketError.timedOut.operatorMessage), retryIn: 1))
        XCTAssertEqual(store.feed, .reconnecting("the daemon is not answering"))
        XCTAssertEqual(LineSocketError.system("read", ECONNRESET).operatorMessage, "could not reach the daemon")
        XCTAssertEqual(LineSocketError.closed.operatorMessage, "the daemon closed the connection")
    }

    /// A daemon restart says `bye`, then nothing listens for a moment: the
    /// last snapshot stays, marked reconnecting, until `byeHold` passes.
    func testARestartKeepsTheLastSnapshotForAWhile() {
        var clock = Date(timeIntervalSince1970: 1_000)
        let store = SessionStore(now: { clock })
        store.apply(.frame(.snapshot(seq: 1, snapshot([1, 2]))))
        store.apply(.disconnected(.bye("shutdown"), retryIn: 0.5))
        XCTAssertTrue(store.holdingAfterBye)

        clock += 1
        store.apply(.disconnected(.notRunning, retryIn: 1))
        store.apply(health: .notRunning)
        XCTAssertEqual(store.feed, .reconnecting("the daemon is restarting"))
        XCTAssertEqual(store.sessions.map(\.windowID), [1, 2], "the menu does not blank during a restart")

        // The daemon comes back: the hold ends with the new snapshot.
        store.apply(.subscribed(SubscriptionAck()))
        store.apply(.frame(.snapshot(seq: 1, snapshot([1]))))
        XCTAssertFalse(store.holdingAfterBye)
        XCTAssertEqual(store.feed, .live)

        // A daemon that does not come back is declared not running.
        store.apply(.disconnected(.bye(""), retryIn: 0.5))
        clock += SessionStore.byeHold
        store.apply(.disconnected(.notRunning, retryIn: 1))
        XCTAssertEqual(store.feed, .notRunning)
        XCTAssertNil(store.snapshot)

        // Without a bye, not running is at once.
        store.apply(.frame(.snapshot(seq: 1, snapshot([1]))))
        store.apply(.disconnected(.notRunning, retryIn: 1))
        XCTAssertEqual(store.feed, .notRunning)
    }

    /// A health check that finds the daemon answering redials a feed that is
    /// down, without waiting out the backoff.
    func testHealthThatFindsTheDaemonRedialsADownFeed() {
        XCTAssertTrue(AppDelegate.shouldReconnect(after: .ready(nil), feed: .notRunning))
        XCTAssertTrue(AppDelegate.shouldReconnect(after: .degraded(reason: "x", build: nil), feed: .reconnecting("x")))
        XCTAssertFalse(AppDelegate.shouldReconnect(after: .ready(nil), feed: .live))
        XCTAssertFalse(AppDelegate.shouldReconnect(after: .ready(nil), feed: .daemonTooOld), "an old daemon cannot subscribe")
        XCTAssertFalse(AppDelegate.shouldReconnect(after: .ready(nil), feed: .connecting), "already dialing")
        XCTAssertFalse(AppDelegate.shouldReconnect(after: .notRunning, feed: .notRunning))
        XCTAssertFalse(AppDelegate.shouldReconnect(after: .unresponsive("x"), feed: .reconnecting("x")))
    }

    func testAProgressNoticeGivesWayOnlyToItsOwnOutcome() {
        let store = SessionStore()
        store.show(notice: DaemonStart.startingNotice)
        XCTAssertTrue(store.replace(notice: DaemonStart.startingNotice, with: DaemonStart.failedNotice))
        XCTAssertEqual(store.notice, "The daemon did not start — run amx doctor")

        store.show(notice: "Launching API…")
        store.show(notice: "Could not toggle mute")
        XCTAssertFalse(store.replace(notice: "Launching API…", with: nil), "a newer notice stays")
        XCTAssertEqual(store.notice, "Could not toggle mute")

        store.show(notice: "Launching API…")
        XCTAssertTrue(store.replace(notice: "Launching API…", with: nil))
        XCTAssertNil(store.notice)
    }

    func testLaunchProgressAndFailureLines() {
        XCTAssertEqual(AppDelegate.launchingNotice("API"), "Launching API…")
        XCTAssertEqual(AppDelegate.launchingNotice(""), "Launching…")
        XCTAssertEqual(AppDelegate.failureLine(title: "Could not launch work/API", message: "no such profile"),
                       "Could not launch work/API: no such profile")
        XCTAssertEqual(AppDelegate.failureLine(title: "Could not launch", message: "Could not reach Kitty: gone"),
                       "Could not reach Kitty: gone", "a message that already says it is not prefixed again")
        XCTAssertEqual(AppDelegate.failureLine(title: "Could not restart the daemon", message: ""), "Could not restart the daemon")
    }

    func testDaemonStartVerdict() {
        XCTAssertEqual(DaemonStart.verdict(feed: .notRunning, exitCode: nil, deadlinePassed: false), .waiting)
        XCTAssertEqual(DaemonStart.verdict(feed: .connecting, exitCode: 0, deadlinePassed: false), .waiting,
                       "an exit that reports success leaves it to the feed and the deadline")
        XCTAssertEqual(DaemonStart.verdict(feed: .notRunning, exitCode: 1, deadlinePassed: false), .failed,
                       "an early failing exit is reported at once")
        XCTAssertEqual(DaemonStart.verdict(feed: .reconnecting("x"), exitCode: nil, deadlinePassed: true), .failed)
        XCTAssertEqual(DaemonStart.verdict(feed: .live, exitCode: nil, deadlinePassed: false), .started)
        XCTAssertEqual(DaemonStart.verdict(feed: .live, exitCode: 1, deadlinePassed: true), .started,
                       "a live feed means some daemon answers")
        XCTAssertEqual(DaemonStart.verdict(feed: .daemonTooOld, exitCode: nil, deadlinePassed: true), .started)
    }

    func testDaemonStartTailIsBounded() {
        let text = (1...20).map { "line \($0)" }.joined(separator: "\n") + "\n\n"
        XCTAssertEqual(DaemonStart.tail(Data(text.utf8)), "line 16\nline 17\nline 18\nline 19\nline 20")
        XCTAssertEqual(DaemonStart.tail(Data(String(repeating: "x", count: 5000).utf8)).count, 1024)
        XCTAssertEqual(DaemonStart.tail(Data()), "")
    }

    func testANoticeStaysUntilSeenAndHeldLongEnough() {
        var clock = Date(timeIntervalSince1970: 1_000)
        let store = SessionStore(now: { clock })
        store.show(notice: "Could not toggle mute: the daemon is not answering")
        clock += 60
        store.apply(snapshot: snapshot([1]))
        XCTAssertNotNil(store.notice, "unseen, a notice outlasts any number of snapshots")

        store.noticeWasSeen()
        store.apply(snapshot: snapshot([1]))
        XCTAssertNil(store.notice, "seen and older than the hold: the next snapshot clears it")

        store.show(notice: "Starting the daemon…")
        store.noticeWasSeen()
        clock += SessionStore.noticeHold - 1
        store.apply(snapshot: snapshot([1]))
        XCTAssertEqual(store.notice, "Starting the daemon…", "seen at once, it still stays the hold")
        clock += 1
        store.apply(snapshot: snapshot([1]))
        XCTAssertNil(store.notice)

        store.show(notice: "first")
        store.noticeWasSeen()
        store.show(notice: "second")
        clock += SessionStore.noticeHold
        store.apply(snapshot: snapshot([1]))
        XCTAssertEqual(store.notice, "second", "a new notice is unseen again")
    }

    func testHealthNotRunningClearsALiveFeed() {
        let store = SessionStore()
        store.apply(.frame(.snapshot(seq: 1, snapshot([1]))))
        store.apply(health: .notRunning)
        XCTAssertEqual(store.feed, .notRunning)
        XCTAssertTrue(store.sessions.isEmpty)
    }

    func testStaleWindowsHideUntilASnapshotDropsThem() {
        let store = SessionStore()
        store.apply(.frame(.snapshot(seq: 1, snapshot([1, 2]))))
        store.markStale(socket: "", windowID: 2)
        XCTAssertEqual(store.sessions.map(\.windowID), [1])
        XCTAssertEqual(store.notice, "That session has closed.")

        store.apply(.frame(.snapshot(seq: 2, snapshot([1, 2]))))
        XCTAssertEqual(store.sessions.map(\.windowID), [1], "still hidden while the daemon lags")
        XCTAssertEqual(store.notice, "That session has closed.", "a notice outlasts snapshots until it is seen")

        store.apply(.frame(.snapshot(seq: 3, snapshot([1]))))
        XCTAssertTrue(store.staleWindows.isEmpty)
        store.apply(.frame(.snapshot(seq: 4, snapshot([1, 2]))))
        XCTAssertEqual(store.sessions.map(\.windowID), [1, 2], "a reused window ID shows again")
    }

    private func sessions(_ pairs: [(String, Int)]) -> CompanionSnapshot {
        var snapshot = snapshot([])
        snapshot.sessions = pairs.map { socket, id in
            var session = CompanionSession(windowID: id)
            session.kittySocket = socket
            return session
        }
        return snapshot
    }

    private func listed(_ store: SessionStore) -> [String] {
        store.sessions.map { "\($0.kittySocket)#\($0.windowID)" }
    }

    func testStaleTargetIsScopedToKittySocket() {
        let store = SessionStore()
        store.apply(snapshot: sessions([("unix:/tmp/kitty-101", 7), ("unix:/tmp/kitty-202", 7)]))
        store.markStale(socket: "unix:/tmp/kitty-101", windowID: 7)
        XCTAssertEqual(listed(store), ["unix:/tmp/kitty-202#7"], "only the stale Kitty's window hides")

        store.apply(snapshot: sessions([("unix:/tmp/kitty-202", 7)]))
        XCTAssertEqual(listed(store), ["unix:/tmp/kitty-202#7"], "the other Kitty's window 7 stays listed")
        XCTAssertTrue(store.staleWindows.isEmpty, "dropping the stale pair clears its suppression")
    }

    func testReusedIDInOtherKittyRemainsVisible() {
        let store = SessionStore()
        store.apply(snapshot: sessions([("unix:/tmp/kitty-101", 7), ("unix:/tmp/kitty-202", 3)]))
        store.markStale(socket: "unix:/tmp/kitty-101", windowID: 7)
        XCTAssertEqual(listed(store), ["unix:/tmp/kitty-202#3"])

        // Another Kitty reuses 7 while the daemon still lists the stale one.
        store.apply(snapshot: sessions([("unix:/tmp/kitty-101", 7), ("unix:/tmp/kitty-202", 7)]))
        XCTAssertEqual(listed(store), ["unix:/tmp/kitty-202#7"], "a reused ID in another Kitty shows")

        store.apply(snapshot: sessions([("unix:/tmp/kitty-202", 7)]))
        XCTAssertTrue(store.staleWindows.isEmpty)
        store.apply(snapshot: sessions([("unix:/tmp/kitty-101", 7), ("unix:/tmp/kitty-202", 7)]))
        XCTAssertEqual(listed(store), ["unix:/tmp/kitty-101#7", "unix:/tmp/kitty-202#7"],
                       "the stale Kitty's reappearing window 7 shows again")
    }

    func testEmptyRowSocketNamesTheSnapshotSocket() {
        let store = SessionStore()
        var snapshot = sessions([("", 4), ("unix:/tmp/kitty-202", 4)])
        snapshot.kitty.socket = "unix:/tmp/kitty-101"
        store.apply(snapshot: snapshot)
        store.markStale(socket: "", windowID: 4)
        XCTAssertEqual(listed(store), ["unix:/tmp/kitty-202#4"])
        store.markStale(socket: "unix:/tmp/kitty-202", windowID: 4)
        XCTAssertTrue(store.sessions.isEmpty)
    }
    private func mutedSnapshot(_ muted: Bool, socket: String = "unix:/tmp/kitty-101") -> CompanionSnapshot {
        var snapshot = sessions([(socket, 7)])
        snapshot.sessions[0].muted = muted
        return snapshot
    }

    func testExpectedMuteShowsAtOnceAndYieldsToAnAgreeingSnapshot() {
        var clock = Date(timeIntervalSince1970: 1_000)
        let store = SessionStore(now: { clock })
        store.apply(snapshot: mutedSnapshot(false))
        store.expectMute(socket: "unix:/tmp/kitty-101", windowID: 7, muted: true)
        XCTAssertEqual(store.sessions.map(\.muted), [true], "the click shows before the daemon publishes it")

        clock += 1
        store.apply(snapshot: mutedSnapshot(false))
        XCTAssertEqual(store.sessions.map(\.muted), [true], "a snapshot taken before the toggle landed does not revert it")

        store.apply(snapshot: mutedSnapshot(true))
        XCTAssertTrue(store.expectedMutes.isEmpty, "an agreeing snapshot settles the expectation")
        store.apply(snapshot: mutedSnapshot(false))
        XCTAssertEqual(store.sessions.map(\.muted), [false], "once settled, snapshots are authoritative again")
    }

    func testExpectedMuteExpiresAndCanBeDropped() {
        var clock = Date(timeIntervalSince1970: 1_000)
        let store = SessionStore(now: { clock })
        store.apply(snapshot: mutedSnapshot(false))
        store.expectMute(socket: "unix:/tmp/kitty-101", windowID: 7, muted: true)
        clock += SessionStore.expectedMuteHold + 1
        XCTAssertEqual(store.sessions.map(\.muted), [false], "an unconfirmed expectation lapses")
        store.apply(snapshot: mutedSnapshot(false))
        XCTAssertTrue(store.expectedMutes.isEmpty)

        store.expectMute(socket: "unix:/tmp/kitty-101", windowID: 7, muted: true)
        store.dropExpectedMute(socket: "unix:/tmp/kitty-101", windowID: 7)
        XCTAssertEqual(store.sessions.map(\.muted), [false], "a failed toggle withdraws it")
    }

    func testExpectedMuteOfAnEmbeddedSessionIsKeyedByItsRuntimeSession() {
        var clock = Date(timeIntervalSince1970: 1_000)
        let store = SessionStore(now: { clock })
        var snapshot = sessions([("unix:/tmp/kitty-101", 7)])
        var tmux = CompanionSession(windowID: 0)
        tmux.runtime = CompanionRuntime(sessionID: "rs-1", backend: "tmux")
        snapshot.sessions.append(tmux)
        store.apply(snapshot: snapshot)

        store.expectMute(.embedded("rs-1"), muted: true)
        XCTAssertEqual(store.sessions.map(\.muted), [false, true], "only the embedded session shows the mute")
        clock += 1
        store.apply(snapshot: snapshot)
        XCTAssertEqual(store.sessions.map(\.muted), [false, true], "a stale snapshot does not undo it, so a banner's Mute sees it muted")

        snapshot.sessions[1].muted = true
        store.apply(snapshot: snapshot)
        XCTAssertTrue(store.expectedMutes.isEmpty, "an agreeing snapshot settles it")

        store.expectMute(.embedded("rs-1"), muted: false)
        store.dropExpectedMute(.embedded("rs-1"))
        XCTAssertEqual(store.sessions.map(\.muted), [false, true], "a failed toggle withdraws it")
    }

    func testNotificationsSnoozedFollowsTheLiveSnapshot() {
        let clock = Date(timeIntervalSince1970: 1_000)
        let store = SessionStore(now: { clock })
        XCTAssertFalse(store.notificationsSnoozed)
        var snapshot = sessions([])
        snapshot.notification.snoozedUntil = 2_000
        store.apply(snapshot: snapshot)
        XCTAssertTrue(store.notificationsSnoozed)
        snapshot.notification.snoozedUntil = 500
        store.apply(snapshot: snapshot)
        XCTAssertFalse(store.notificationsSnoozed, "a snooze that ran out is no snooze")
        snapshot.notification.indefinite = true
        store.apply(snapshot: snapshot)
        XCTAssertTrue(store.notificationsSnoozed)
        store.apply(.disconnected(.notRunning, retryIn: 1))
        XCTAssertFalse(store.notificationsSnoozed)
    }

    func testExpectedMuteIsScopedToKittySocket() {
        let store = SessionStore()
        var snapshot = sessions([("", 7), ("unix:/tmp/kitty-202", 7)])
        snapshot.kitty.socket = "unix:/tmp/kitty-101"
        store.apply(snapshot: snapshot)
        store.expectMute(socket: "", windowID: 7, muted: true)
        XCTAssertEqual(store.sessions.map(\.muted), [true, false], "only the row's own Kitty shows the mute")
    }
}

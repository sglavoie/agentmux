import XCTest
@testable import AgentMux

@MainActor
final class SessionStoreTests: XCTestCase {
    private func idsRuntime(_ ids: [Int]) -> [String] { ids.map(WorkspaceFixture.runtimeID) }

    private func snapshot(_ ids: [Int], key: Int = 0) -> CompanionSnapshot {
        var snapshot = CompanionSnapshot()
        snapshot.daemon.ready = true
        snapshot.daemon.build = DaemonBuild(version: "1.0.0")
        snapshot.attention = CompanionAttention(total: key, key: key)
        snapshot.sessions = ids.map { WorkspaceFixture.tmux($0, project: "p\($0)") }
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
        XCTAssertEqual(store.sessions.compactMap { $0.runtime?.sessionID }, idsRuntime([1, 2]))
        store.apply(.frame(.heartbeat(seq: 2)))
        store.apply(.connecting(attempt: 0))
        XCTAssertEqual(changes, 2, "heartbeats and dial attempts do not redraw")
    }

    func testManagedPresentationExcludesLegacyRowsAndTheirAttention() {
        let store = SessionStore()
        var snapshot = CompanionSnapshot()
        var legacy = CompanionSession()
        legacy.iconState = "key"
        legacy.attention = true
        var managed = CompanionSession()
        managed.runtime = CompanionRuntime(sessionID: "rs-managed", backend: "tmux", pane: "%1")
        managed.iconState = "stop"
        managed.attention = true
        snapshot.sessions = [legacy, managed]
        snapshot.attention = CompanionAttention(total: 2, stop: 1, key: 1)
        store.apply(snapshot: snapshot)

        XCTAssertEqual(store.managedSessions.count, 1)
        XCTAssertEqual(store.managedAttention.total, 1)
        XCTAssertEqual(store.managedAttention.stop, 1)
        XCTAssertEqual(store.managedAttention.key, 0)
        XCTAssertEqual(store.managedTitle(glyphs: .fallback), "✓1")
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

    func testAReconnectTheAppAskedForKeepsTheFeedAndTitle() {
        let store = SessionStore()
        var changes = 0
        store.apply(.subscribed(SubscriptionAck()))
        store.apply(.frame(.snapshot(seq: 1, snapshot([1], key: 1))))
        store.onChange = { changes += 1 }
        store.apply(.disconnected(.reconnectRequested, retryIn: 0))
        XCTAssertEqual(store.feed, .live)
        XCTAssertEqual(store.title(glyphs: .fallback), "🔑1", "the counts do not blink")
        XCTAssertEqual(changes, 0)
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
        XCTAssertEqual(store.sessions.compactMap { $0.runtime?.sessionID }, idsRuntime([1, 2]), "the menu does not blank during a restart")

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
        XCTAssertEqual(AppDelegate.failureLine(title: "Could not launch", message: "Could not reach terminal: gone"),
                       "Could not reach terminal: gone", "a message that already says it is not prefixed again")
        XCTAssertEqual(AppDelegate.failureLine(title: "Could not restart the daemon", message: ""), "Could not restart the daemon")
    }

    func testAnUnreadableSnapshotIsReconnectingAndSaysWhy() {
        let store = SessionStore()
        store.apply(.disconnected(.unreadableSnapshot, retryIn: 10))
        XCTAssertEqual(store.feed, .reconnecting(DaemonPhrase.unreadableSnapshot))
    }

    func testQuitWhileAmxChangesSomethingNamesWhat() {
        XCTAssertEqual(AppDelegate.launchingChange("API"), "launching API")
        XCTAssertEqual(AppDelegate.launchingChange(""), "launching a session")
        XCTAssertEqual(AppDelegate.quitWhileChangingQuestion(["launching API"]), "Quit while amx is still launching API?")
        XCTAssertEqual(AppDelegate.quitWhileChangingQuestion(["launching API", "ending a session", "restarting the daemon"]),
                       "Quit while amx is still launching API, ending a session and restarting the daemon?")
    }

    func testSidebarAndInspectorItemsSayWhatTheyWillDo() {
        XCTAssertEqual(AppDelegate.sidebarItemTitle(shown: true), "Hide Sidebar")
        XCTAssertEqual(AppDelegate.sidebarItemTitle(shown: false), "Show Sidebar")
        XCTAssertEqual(AppDelegate.inspectorItemTitle(shown: true), "Hide Inspector")
        XCTAssertEqual(AppDelegate.inspectorItemTitle(shown: false), "Show Inspector")
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

    func testExpectedMuteOfAnEmbeddedSessionIsKeyedByItsRuntimeSession() {
        var clock = Date(timeIntervalSince1970: 1_000)
        let store = SessionStore(now: { clock })
        var snapshot = snapshot([])
        var tmux = CompanionSession()
        tmux.runtime = CompanionRuntime(sessionID: "rs-1", backend: "tmux")
        snapshot.sessions.append(tmux)
        store.apply(snapshot: snapshot)

        store.expectMute(.embedded("rs-1"), muted: true)
        XCTAssertEqual(store.sessions.map(\.muted), [true], "the session shows the expected mute")
        clock += 1
        store.apply(snapshot: snapshot)
        XCTAssertEqual(store.sessions.map(\.muted), [true], "a stale snapshot does not undo it, so a banner's Mute sees it muted")

        snapshot.sessions[0].muted = true
        store.apply(snapshot: snapshot)
        XCTAssertTrue(store.expectedMutes.isEmpty, "an agreeing snapshot settles it")

        store.expectMute(.embedded("rs-1"), muted: false)
        store.dropExpectedMute(.embedded("rs-1"))
        XCTAssertEqual(store.sessions.map(\.muted), [true], "a failed toggle withdraws it")
    }

    func testNotificationsSnoozedFollowsTheLiveSnapshot() {
        let clock = Date(timeIntervalSince1970: 1_000)
        let store = SessionStore(now: { clock })
        XCTAssertFalse(store.notificationsSnoozed)
        var snapshot = snapshot([])
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

}

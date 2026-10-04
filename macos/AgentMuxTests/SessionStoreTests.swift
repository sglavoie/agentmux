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
        let store = SessionStore()
        store.apply(.frame(.snapshot(seq: 1, snapshot([1]))))
        store.apply(.disconnected(.heartbeatLost, retryIn: 1))
        XCTAssertEqual(store.feed, .reconnecting("no heartbeat"))
        XCTAssertEqual(store.title(glyphs: .fallback), "", "no counts from a feed that is not live")
        XCTAssertNotNil(store.snapshot, "a reconnect keeps the last snapshot")

        store.apply(.disconnected(.bye("shutdown"), retryIn: 0.5))
        XCTAssertEqual(store.feed, .reconnecting("daemon shutdown"))

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
        XCTAssertNil(store.notice)

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

    func testExpectedMuteIsScopedToKittySocket() {
        let store = SessionStore()
        var snapshot = sessions([("", 7), ("unix:/tmp/kitty-202", 7)])
        snapshot.kitty.socket = "unix:/tmp/kitty-101"
        store.apply(snapshot: snapshot)
        store.expectMute(socket: "", windowID: 7, muted: true)
        XCTAssertEqual(store.sessions.map(\.muted), [true, false], "only the row's own Kitty shows the mute")
    }
}

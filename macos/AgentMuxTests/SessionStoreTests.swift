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
        store.markStale(windowID: 2)
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
}

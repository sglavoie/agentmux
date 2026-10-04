import XCTest
@testable import AgentMux

final class AttentionCyclerTests: XCTestCase {
    private func session(_ windowID: Int, changedMS: Int64, socket: String = "", attention: Bool = true) -> CompanionSession {
        var session = CompanionSession(windowID: windowID)
        session.kittySocket = socket
        session.attention = attention
        session.presentationChangedAtMS = changedMS
        return session
    }

    private func ids(_ sessions: [CompanionSession]) -> [Int] { sessions.map(\.windowID) }

    func testOrdersOldestChangeFirstAndSkipsRowsNotWaiting() {
        let sessions = [
            session(1, changedMS: 3_000),
            session(2, changedMS: 1_000),
            session(3, changedMS: 500, attention: false),
            session(4, changedMS: 0),
            session(5, changedMS: 2_000),
        ]
        XCTAssertEqual(ids(AttentionCycler.ordered(sessions)), [2, 5, 1, 4], "no timestamp sorts last")
    }

    func testCyclesAndWrapsAround() {
        let sessions = [session(1, changedMS: 3_000), session(2, changedMS: 1_000), session(3, changedMS: 2_000)]
        var cycler = AttentionCycler()
        let presses = (0..<5).compactMap { _ in cycler.advance(in: sessions)?.windowID }
        XCTAssertEqual(presses, [2, 3, 1, 2, 3])
    }

    func testContinuesFromWhereASettledSessionStood() {
        var sessions = [session(1, changedMS: 1_000), session(2, changedMS: 2_000), session(3, changedMS: 3_000)]
        var cycler = AttentionCycler()
        XCTAssertEqual(cycler.advance(in: sessions)?.windowID, 1)
        XCTAssertEqual(cycler.advance(in: sessions)?.windowID, 2)
        // Focusing 2 settled it; the next press goes on to 3, not back to 1.
        sessions[1].attention = false
        XCTAssertEqual(cycler.advance(in: sessions)?.windowID, 3)
        sessions[2].attention = false
        XCTAssertEqual(cycler.advance(in: sessions)?.windowID, 1, "past the last it wraps")
        XCTAssertNil(AttentionCycler().next(in: sessions.map { var s = $0; s.attention = false; return s }))
    }

    func testSpansKittyInstances() {
        // The same window ID in two Kitty instances is two sessions.
        let sessions = [
            session(7, changedMS: 1_000, socket: "unix:/tmp/kitty-1"),
            session(7, changedMS: 2_000, socket: "unix:/tmp/kitty-2"),
            session(8, changedMS: 3_000),
        ]
        var cycler = AttentionCycler()
        let presses = (0..<4).compactMap { _ in cycler.advance(in: sessions, snapshotSocket: "unix:/tmp/kitty-1") }
        XCTAssertEqual(presses.map(\.kittySocket), ["unix:/tmp/kitty-1", "unix:/tmp/kitty-2", "", "unix:/tmp/kitty-1"])
        XCTAssertEqual(
            AttentionCycler.key(sessions[2], snapshotSocket: "unix:/tmp/kitty-1"),
            AttentionCycler.Key(socket: "unix:/tmp/kitty-1", windowID: 8),
            "a row with no socket of its own belongs to the snapshot's Kitty"
        )
    }

    func testTiesBreakBySocketThenWindow() {
        let sessions = [
            session(2, changedMS: 1_000, socket: "b"),
            session(9, changedMS: 1_000, socket: "a"),
            session(1, changedMS: 1_000, socket: "b"),
        ]
        XCTAssertEqual(ids(AttentionCycler.ordered(sessions)), [9, 1, 2])
    }
}

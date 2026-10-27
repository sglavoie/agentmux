import XCTest
@testable import AgentMux

final class AttentionCyclerTests: XCTestCase {
    /// A Kitty session of `project`; with none given, each window is a
    /// project of its own, named so that the windows sort by number.
    private func session(_ windowID: Int, project: String? = nil, changedMS: Int64 = 0, socket: String = "",
                         attention: Bool = true) -> CompanionSession {
        var session = CompanionSession(windowID: windowID)
        session.project = project ?? "p\(windowID)"
        session.kittySocket = socket
        session.attention = attention
        session.presentationChangedAtMS = changedMS
        return session
    }

    private func ids(_ sessions: [CompanionSession]) -> [Int] { sessions.map(\.windowID) }

    func testOrdersByProjectNotByAgeAndSkipsRowsNotWaiting() {
        let sessions = [
            session(1, project: "web", changedMS: 3_000),
            session(2, project: "api", changedMS: 1_000),
            session(3, project: "api", changedMS: 500, attention: false),
            session(4, project: "Docs"),
            session(5, project: "api", changedMS: 2_000),
            session(6, project: "🚀 cli", changedMS: 100),
        ]
        XCTAssertEqual(ids(AttentionCycler.ordered(sessions)), [2, 5, 6, 4, 1],
                       "projects by name past their icon, and a project's sessions as the sidebar lists them")
        XCTAssertEqual(ids(AttentionCycler.ordered(sessions)),
                       ids(WorkspaceSidebar.ordered(sessions).filter(\.attention)))
    }

    func testFollowsTheOrderAProjectWasDraggedInto() {
        let sessions = [session(1, project: "api"), session(2, project: "api"), session(3, project: "web")]
        var order = WorkspaceOrder()
        order.set([2, 1].map { AttentionCycler.Key(socket: "", windowID: $0) }, for: "api")
        XCTAssertEqual(ids(AttentionCycler.ordered(sessions, order: order)), [2, 1, 3])
        var cycler = AttentionCycler()
        let presses = (0..<4).compactMap { _ in cycler.advance(in: sessions, order: order)?.windowID }
        XCTAssertEqual(presses, [2, 1, 3, 2])
    }

    func testCyclesAndWrapsAround() {
        let sessions = [session(1, changedMS: 3_000), session(2, changedMS: 1_000), session(3, changedMS: 2_000)]
        var cycler = AttentionCycler()
        let presses = (0..<5).compactMap { _ in cycler.advance(in: sessions)?.windowID }
        XCTAssertEqual(presses, [1, 2, 3, 1, 2])
    }

    func testContinuesFromWhereASettledSessionStood() {
        var sessions = [session(1), session(2), session(3)]
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

    func testContinuesPastASettledSessionThatLeftNeedsAttention() {
        // A finished unseen and waits under Needs attention, above B, which
        // waits for a key; C finished too, but was seen, so it stays in web.
        var a = session(1, project: "web")
        a.iconState = "stop"
        a.unseen = true
        var b = session(2, project: "zed")
        b.iconState = "key"
        var c = session(3, project: "web")
        c.iconState = "stop"
        var sessions = [a, b, c]
        XCTAssertEqual(ids(WorkspaceSidebar.ordered(sessions)), [1, 2, 3])
        var cycler = AttentionCycler()
        XCTAssertEqual(cycler.advance(in: sessions)?.windowID, 1)
        // Focusing A marked it seen: it goes back to web, just above C.
        sessions[0].unseen = false
        XCTAssertEqual(ids(WorkspaceSidebar.ordered(sessions)), [2, 1, 3])
        XCTAssertEqual(cycler.advance(in: sessions)?.windowID, 2, "B stood right after A")
        XCTAssertEqual(cycler.advance(in: sessions)?.windowID, 1)
        XCTAssertEqual(cycler.advance(in: sessions)?.windowID, 3)
    }

    func testASessionListedSinceComesAfterThoseThatWere() {
        let sessions = [session(1), session(3)]
        var cycler = AttentionCycler()
        XCTAssertEqual(cycler.advance(in: sessions)?.windowID, 1)
        // 2 is listed between them now, but 3 was already waiting after 1.
        let more = [session(1), session(2), session(3)]
        XCTAssertEqual(cycler.advance(in: more)?.windowID, 3)
        XCTAssertEqual(cycler.advance(in: more)?.windowID, 1)
        XCTAssertEqual(cycler.advance(in: more)?.windowID, 2)
    }

    func testContinuesFromWhereAClosedSessionStood() {
        let sessions = [session(1), session(2), session(3), session(4)]
        var cycler = AttentionCycler()
        _ = cycler.advance(in: sessions)
        XCTAssertEqual(cycler.advance(in: sessions)?.windowID, 2)
        // 2 was closed, and 3 with it: the press goes to what stood after them.
        let left = [sessions[0], sessions[3]]
        XCTAssertEqual(cycler.advance(in: left)?.windowID, 4)
        XCTAssertEqual(cycler.advance(in: left)?.windowID, 1)
    }

    func testTheOnlyWaitingSessionIsFocusedAgain() {
        let sessions = [session(1), session(2, attention: false)]
        var cycler = AttentionCycler()
        XCTAssertEqual((0..<2).compactMap { _ in cycler.advance(in: sessions)?.windowID }, [1, 1])
    }

    func testSpansKittyInstances() {
        // The same window ID in two Kitty instances is two sessions.
        let sessions = [
            session(7, project: "a", socket: "unix:/tmp/kitty-1"),
            session(7, project: "b", socket: "unix:/tmp/kitty-2"),
            session(8, project: "c"),
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

    func testSessionsOfAProjectAndProfileGoBySocketThenWindow() {
        let sessions = [
            session(2, project: "api", socket: "b"),
            session(9, project: "api", socket: "a"),
            session(1, project: "api", socket: "b"),
        ]
        XCTAssertEqual(ids(AttentionCycler.ordered(sessions)), [9, 1, 2])
    }
}

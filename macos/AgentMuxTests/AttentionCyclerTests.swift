import XCTest
@testable import AgentMux

final class AttentionCyclerTests: XCTestCase {
    /// Each session has a project name that sorts by number by default.
    private func session(_ id: Int, project: String? = nil, changedMS: Int64 = 0,
                         attention: Bool = true) -> CompanionSession {
        var session = WorkspaceFixture.tmux(id, project: project ?? "p\(id)")
        session.attention = attention
        session.presentationChangedAtMS = changedMS
        return session
    }

    private func ids(_ sessions: [CompanionSession]) -> [Int] { sessions.map(\.testID) }

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
        order.set([2, 1].map { .embedded(WorkspaceFixture.runtimeID($0)) }, for: "api")
        XCTAssertEqual(ids(AttentionCycler.ordered(sessions, order: order)), [2, 1, 3])
        var cycler = AttentionCycler()
        let presses = (0..<4).compactMap { _ in cycler.advance(in: sessions, order: order)?.testID }
        XCTAssertEqual(presses, [2, 1, 3, 2])
    }

    func testCyclesAndWrapsAround() {
        let sessions = [session(1, changedMS: 3_000), session(2, changedMS: 1_000), session(3, changedMS: 2_000)]
        var cycler = AttentionCycler()
        let presses = (0..<5).compactMap { _ in cycler.advance(in: sessions)?.testID }
        XCTAssertEqual(presses, [1, 2, 3, 1, 2])
    }

    func testContinuesFromWhereASettledSessionStood() {
        var sessions = [session(1), session(2), session(3)]
        var cycler = AttentionCycler()
        XCTAssertEqual(cycler.advance(in: sessions)?.testID, 1)
        XCTAssertEqual(cycler.advance(in: sessions)?.testID, 2)
        // Focusing 2 settled it; the next press goes on to 3, not back to 1.
        sessions[1].attention = false
        XCTAssertEqual(cycler.advance(in: sessions)?.testID, 3)
        sessions[2].attention = false
        XCTAssertEqual(cycler.advance(in: sessions)?.testID, 1, "past the last it wraps")
        XCTAssertNil(AttentionCycler().next(in: sessions.map { var s = $0; s.attention = false; return s }))
    }

    func testContinuesPastASettledSessionThatLeftNeedsAttention() {
        // A and B finished unseen and wait under Needs attention, A's web
        // above B's zed; C finished too, but was seen, so it stays in web.
        var a = session(1, project: "web")
        a.iconState = "stop"
        a.unseen = true
        var b = session(2, project: "zed")
        b.iconState = "stop"
        b.unseen = true
        var c = session(3, project: "web")
        c.iconState = "stop"
        var sessions = [a, b, c]
        XCTAssertEqual(ids(WorkspaceSidebar.ordered(sessions)), [1, 2, 3])
        var cycler = AttentionCycler()
        XCTAssertEqual(cycler.advance(in: sessions)?.testID, 1)
        // Focusing A marked it seen: it goes back to web, just above C.
        sessions[0].unseen = false
        XCTAssertEqual(ids(WorkspaceSidebar.ordered(sessions)), [2, 1, 3])
        XCTAssertEqual(cycler.advance(in: sessions)?.testID, 2, "B stood right after A")
        XCTAssertEqual(cycler.advance(in: sessions)?.testID, 1)
        XCTAssertEqual(cycler.advance(in: sessions)?.testID, 3)
    }

    func testASessionListedSinceComesAfterThoseThatWere() {
        let sessions = [session(1), session(3)]
        var cycler = AttentionCycler()
        XCTAssertEqual(cycler.advance(in: sessions)?.testID, 1)
        // 2 is listed between them now, but 3 was already waiting after 1.
        let more = [session(1), session(2), session(3)]
        XCTAssertEqual(cycler.advance(in: more)?.testID, 3)
        XCTAssertEqual(cycler.advance(in: more)?.testID, 1)
        XCTAssertEqual(cycler.advance(in: more)?.testID, 2)
    }

    func testContinuesFromWhereAClosedSessionStood() {
        let sessions = [session(1), session(2), session(3), session(4)]
        var cycler = AttentionCycler()
        _ = cycler.advance(in: sessions)
        XCTAssertEqual(cycler.advance(in: sessions)?.testID, 2)
        // 2 was closed, and 3 with it: the press goes to what stood after them.
        let left = [sessions[0], sessions[3]]
        XCTAssertEqual(cycler.advance(in: left)?.testID, 4)
        XCTAssertEqual(cycler.advance(in: left)?.testID, 1)
    }

    func testTheOnlyWaitingSessionIsFocusedAgain() {
        let sessions = [session(1), session(2, attention: false)]
        var cycler = AttentionCycler()
        XCTAssertEqual((0..<2).compactMap { _ in cycler.advance(in: sessions)?.testID }, [1, 1])
    }

}

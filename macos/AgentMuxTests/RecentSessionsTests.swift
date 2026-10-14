import XCTest
@testable import AgentMux

final class RecentSessionsTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    private let listJSON = """
    {"schema_version":1,"data":{"sessions":[
    {"id":"a1","harness":"claude","profile":"work","profiles":["work"],"project":"API","projects":["API"],"cwd":"/p/api",\
    "started_at":"2027-01-15T07:00:00Z","last_at":"2027-01-15T08:00:00.123456789Z","path":"/t/a1.jsonl","file_size":10,\
    "file_mod_time":"2027-01-15T08:00:00Z","message_count":4,"snippet":"fix the flaky retry test\\nmore"},
    {"id":"b2","harness":"codex","profile":"home","project":"","cwd":"/p/blog","last_at":"2027-01-15T09:30:00Z","snippet":""},
    {"id":"c3","harness":"copilot","last_at":"0001-01-01T00:00:00Z"}
    ]}}
    """

    func testListDecodesGoTimes() throws {
        let result = AMXEnvelope.decode(SessionListData.self, stdout: Data(listJSON.utf8), status: 0)
        let sessions = try XCTUnwrap(result.value?.sessions)
        XCTAssertEqual(sessions.map(\.selector), ["claude:a1", "codex:b2", "copilot:c3"])
        let eight = ISO8601DateFormatter().date(from: "2027-01-15T08:00:00Z")!.timeIntervalSince1970
        XCTAssertEqual(try XCTUnwrap(sessions[0].lastAt).timeIntervalSince1970, eight + 0.123, accuracy: 0.001, "fractional seconds parse")
        XCTAssertEqual(sessions[1].lastAt, ISO8601DateFormatter().date(from: "2027-01-15T09:30:00Z"))
        XCTAssertNil(sessions[2].lastAt, "Go's zero time means never")
        XCTAssertEqual(sessions[0].project, "API")
    }

    func testTopIsNewestFirstAndBounded() throws {
        let sessions = try XCTUnwrap(AMXEnvelope.decode(SessionListData.self, stdout: Data(listJSON.utf8), status: 0).value?.sessions)
        XCTAssertEqual(RecentSessions.top(sessions, limit: 10).map(\.id), ["b2", "a1", "c3"])
        XCTAssertEqual(RecentSessions.top(sessions, limit: 1).map(\.id), ["b2"])
        XCTAssertEqual(RecentSessions.top(sessions, limit: 0), [])
    }

    func testArguments() {
        XCTAssertEqual(RecentSessions.listArguments(days: 7), ["session", "list", "--since", "7d"])
        XCTAssertEqual(RecentSessions.listArguments(days: 0), ["session", "list", "--since", "1d"])
        XCTAssertEqual(RecentSessions.continueArguments(for: RecentConversation(id: "x", harness: "codex")), ["session", "continue", "codex:x"])
        XCTAssertEqual(RecentSessions.workspaceContinueArguments(for: RecentConversation(id: "x", harness: "codex")),
                       ["session", "continue", "codex:x", "--backend", "tmux"])
    }

    func testFetchRunsWithTheRecentTimeout() {
        var timeouts: [TimeInterval] = []
        var amx = AMXCommand(binary: URL(fileURLWithPath: "/opt/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        amx.runner = { _, _, _, timeout in
            timeouts.append(timeout)
            return .timedOut
        }
        guard case let .failure(.timedOut(_, timeout, mutates)) = RecentSessions.fetch(amx, days: 3), !mutates else {
            return XCTFail("a run that never finishes is a timeout")
        }
        XCTAssertEqual(timeout, 15)
        XCTAssertEqual(timeouts, [RecentSessions.timeout])
    }

    func testCacheKeepsTheLastResultWhileRefreshing() {
        var recent = RecentSessions()
        XCTAssertTrue(recent.needsRefresh(now: now))
        _ = recent.state.begin()
        recent.state.finish(.success([RecentConversation(id: "a", harness: "claude")]), at: now)
        XCTAssertFalse(recent.needsRefresh(now: now.addingTimeInterval(4)), "a menu reopened at once reuses it")
        XCTAssertTrue(recent.needsRefresh(now: now.addingTimeInterval(5)))
        _ = recent.state.begin()
        XCTAssertEqual(recent.state.value?.count, 1, "the cached rows stay while a refresh runs")
        var input = MenuBuilder.ActionsInput()
        input.recent = recent.state
        input.now = now
        XCTAssertEqual(MenuBuilder.recentEntries(input).last, .info("Refreshing…"))
    }

    func testContinueResultDecodesBothOutcomes() {
        let focused = AMXEnvelope.decode(SessionContinueResult.self, stdout: Data(#"{"schema_version":1,"data":{"action":"session.continue","changed":true,"message":"focused live conversation","data":{"continuation":"focused","tab_id":3,"window_id":8}}}"#.utf8), status: 0)
        XCTAssertEqual(focused, .success(SessionContinueResult(changed: true, message: "focused live conversation", continuation: "focused", tabID: 3, windowID: 8)))
        let resumed = AMXEnvelope.decode(SessionContinueResult.self, stdout: Data(#"{"schema_version":1,"data":{"action":"session.continue","changed":true,"data":{"continuation":"resumed","harness":"codex","session_id":"x"}}}"#.utf8), status: 0)
        XCTAssertEqual(resumed.value?.continuation, "resumed")
        XCTAssertEqual(resumed.value?.windowID, 0)
        XCTAssertEqual(resumed.value?.runtimeSessionID, "", "a Kitty resume names no runtime session")
        let onTmux = AMXEnvelope.decode(SessionContinueResult.self, stdout: Data(#"{"schema_version":1,"data":{"action":"session.continue","changed":true,"data":{"continuation":"resumed","backend":"tmux","runtime_session_id":"rs-1"}}}"#.utf8), status: 0)
        XCTAssertEqual(onTmux.value?.runtimeSessionID, "rs-1")
        let odd = AMXEnvelope.decode(SessionContinueResult.self, stdout: Data(#"{"schema_version":1,"data":{"changed":true,"data":{"continuation":"resumed","runtime_session_id":7}}}"#.utf8), status: 0)
        XCTAssertEqual(odd.value?.runtimeSessionID, "", "an unexpected type falls back rather than failing the continue")
        let missing = AMXEnvelope.decode(SessionContinueResult.self, stdout: Data(#"{"schema_version":1,"error":{"code":"not_found","message":"indexed conversation codex:x was not found"}}"#.utf8), status: 1)
        guard case let .failure(.command(error, _)) = missing else { return XCTFail("\(missing)") }
        XCTAssertEqual(error.code, "not_found")
    }

    func testRecentEntries() {
        var input = MenuBuilder.ActionsInput()
        input.now = now
        XCTAssertEqual(MenuBuilder.recentEntries(input), [.info("Loading…")])

        var state = FetchState<[RecentConversation]>()
        _ = state.begin()
        state.finish(.failure(.undecodable(status: 1, detail: "index locked")), at: now)
        input.recent = state
        XCTAssertEqual(MenuBuilder.recentEntries(input), [.info("Could not list conversations: index locked")])

        let api = RecentConversation(id: "a1", harness: "claude", project: "API", lastAt: now.addingTimeInterval(-7200),
                                     snippet: String(repeating: "x", count: 60))
        let blog = RecentConversation(id: "b2", harness: "codex", cwd: "/p/blog", lastAt: now.addingTimeInterval(-60))
        state = FetchState()
        _ = state.begin()
        state.finish(.success([api, blog]), at: now)
        input.recent = state
        input.recentLimit = 5
        input.liveConversations = [ConversationIdentity(harness: "codex", sessionID: "b2")]
        let entries = MenuBuilder.recentEntries(input)
        XCTAssertEqual(entries.count, 2)
        XCTAssertEqual(entries[0], .action(.continueConversation(blog), "blog · codex · live"))
        guard case let .action(.continueConversation(first), title) = entries[1] else { return XCTFail("\(entries)") }
        XCTAssertEqual(first, api)
        XCTAssertTrue(title.hasPrefix("API · claude · 2h · xxx"))
        XCTAssertTrue(title.hasSuffix("…"))

        state = FetchState()
        _ = state.begin()
        state.finish(.success([]), at: now)
        input.recent = state
        input.recentDays = 3
        XCTAssertEqual(MenuBuilder.recentEntries(input), [.info("No conversations in the last 3 days")])
    }
}

import XCTest
@testable import AgentMux

final class CompanionFrameTests: XCTestCase {
    private func frame(_ line: String) -> StreamFrame? {
        SubscriptionClient.decodeFrame(Data(line.utf8))
    }

    func testSnapshotFrameDecodes() throws {
        let decoded = try XCTUnwrap(frame(WireFixture.snapshot(seq: 4)))
        guard case let .snapshot(seq, snapshot) = decoded else { return XCTFail("\(decoded)") }
        XCTAssertEqual(seq, 4)
        XCTAssertEqual(snapshot.daemon.pid, 7)
        XCTAssertTrue(snapshot.daemon.ready)
        XCTAssertEqual(snapshot.kitty.socket, "unix:/tmp/kitty-1")
        XCTAssertEqual(snapshot.attention, CompanionAttention(total: 1, key: 1))
        let session = try XCTUnwrap(snapshot.sessions.first)
        XCTAssertEqual(session.windowID, 3)
        XCTAssertEqual(session.iconState, "key")
        XCTAssertTrue(session.attention)
        XCTAssertEqual(session.project, "api")
        XCTAssertEqual(session.reason, "permission")
    }

    func testFullSessionDecodes() throws {
        let line = #"""
        {"v":1,"seq":1,"type":"snapshot","data":{"v":1,"daemon":{"pid":1,"started_at":10,"build":{"version":"0.8.0","revision":"abc","dirty":true},"ready":false,"degraded_reason":"restore failed"},
        "kitty":{"ready":true,"stale":true,"socket":"unix:/tmp/kitty-1","sockets":["unix:/tmp/kitty-1","unix:/tmp/kitty-2"]},
        "notification":{"backend":"alerter","app_connected":true,"snoozed_until":99,"quiet_hours":"22:00-07:00","quiet_now":true},
        "dashboard":{"open":true,"kitty_socket":"unix:/tmp/kitty-1","os_window_id":1,"tab_id":2,"window_id":3},
        "attention":{"total":2,"stop":1,"question":1,"working":3,"unseen":1},
        "asl":{"waiting":[{"run_id":"r1","plan_dir":"/p","window_id":5,"unanswered":2,"latest_question_id":"q9","latest_question_created_at":77}]},
        "sessions":[{"window_id":5,"tab_id":6,"os_window_id":1,"kitty_socket":"unix:/tmp/kitty-2","harness":"codex","profile":"work","project":"api","base_title":"W:api",
        "conversation":{"harness":"codex","session_id":"s-1"},"asl_run_id":"r1","icon_state":"question","reason":"asl","unseen":true,"attention":true,"muted":true,"locked":true,"looping":true,
        "busy_since_at":1,"last_event_at":2,"presentation_changed_at_ms":3000,"branch":"main","dirty":true}],"truncated":true}}
        """#.replacingOccurrences(of: "\n", with: "")
        guard case let .snapshot(_, snapshot)? = frame(line) else { return XCTFail("did not decode") }
        XCTAssertEqual(snapshot.daemon.status, .degraded(reason: "restore failed", build: DaemonBuild(version: "0.8.0", revision: "abc", dirty: true)))
        XCTAssertEqual(snapshot.kitty.sockets.count, 2)
        XCTAssertTrue(snapshot.kitty.stale)
        XCTAssertEqual(snapshot.notification.quietHours, "22:00-07:00")
        XCTAssertTrue(snapshot.dashboard.open)
        XCTAssertEqual(snapshot.dashboard.tabID, 2)
        XCTAssertEqual(snapshot.attention.working, 3)
        XCTAssertEqual(snapshot.asl.waiting.first?.latestQuestionID, "q9")
        XCTAssertTrue(snapshot.truncated)
        let session = try XCTUnwrap(snapshot.sessions.first)
        XCTAssertEqual(session.conversation, ConversationIdentity(harness: "codex", sessionID: "s-1"))
        XCTAssertEqual(session.kittySocket, "unix:/tmp/kitty-2")
        XCTAssertTrue(session.muted && session.locked && session.looping && session.dirty)
        XCTAssertEqual(session.changedAt, Date(timeIntervalSince1970: 3))
    }

    func testDecodingToleratesUnknownMissingAndMistypedFields() throws {
        // A future daemon adds fields and blocks, omits most, and sends one
        // field with a type this app does not expect.
        let line = #"{"v":2,"seq":9,"type":"snapshot","extra":true,"data":{"future_block":{"a":1},"attention":{"total":"three","key":2},"sessions":[{"window_id":1,"new_field":[1,2]}]}}"#
        guard case let .snapshot(seq, snapshot)? = frame(line) else { return XCTFail("did not decode") }
        XCTAssertEqual(seq, 9)
        XCTAssertEqual(snapshot.attention.total, 0)
        XCTAssertEqual(snapshot.attention.key, 2)
        XCTAssertEqual(snapshot.sessions.map(\.windowID), [1])
        XCTAssertFalse(snapshot.daemon.ready)
        XCTAssertNil(snapshot.sessions.first?.changedAt)
    }

    func testOtherFrameTypes() {
        XCTAssertEqual(frame(WireFixture.heartbeat(seq: 2)), .heartbeat(seq: 2))
        XCTAssertEqual(frame(WireFixture.bye(seq: 3, reason: "slow_consumer")), .bye(seq: 3, reason: "slow_consumer"))
        XCTAssertEqual(frame(#"{"v":1,"seq":4,"type":"bye"}"#), .bye(seq: 4, reason: ""))
        XCTAssertEqual(frame(#"{"v":1,"seq":5,"type":"later"}"#), .unknown(seq: 5, type: "later"))
        guard case let .notificationRetract(_, retract)? = frame(#"{"v":1,"seq":6,"type":"notification_retract","data":{"notification_id":"n-1","reason":"superseded"}}"#) else {
            return XCTFail("retract did not decode")
        }
        XCTAssertEqual(retract.notificationID, "n-1")
        XCTAssertEqual(retract.reason, "superseded")
        guard case let .notification(seq, data)? = frame(#"{"v":1,"seq":7,"type":"notification","data":{"notification_id":"n-2","title":"t"}}"#) else {
            return XCTFail("notification did not decode")
        }
        XCTAssertEqual(seq, 7)
        let object = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        XCTAssertEqual(object?["notification_id"] as? String, "n-2")
    }

    func testBadLinesAreNotFrames() {
        XCTAssertNil(frame(""))
        XCTAssertNil(frame("garbage"))
        XCTAssertNil(frame(#"{"v":1,"seq":1}"#), "a frame needs a type")
        XCTAssertNil(frame(#"[1,2,3]"#))
        XCTAssertNil(frame(#"{"v":1,"seq":1,"type":"snapshot","data":"not an object"}"#))
    }

    // MARK: Ack

    func testAckClassification() throws {
        let ok = try JSONDecoder().decode(DaemonResponse.self, from: Data(WireFixture.ack.utf8))
        XCTAssertEqual(SubscriptionClient.classifyAck(ok),
                       .success(SubscriptionAck(protocolVersion: 1, serverProtocolMax: 1, subscriberID: "sub-1", heartbeatMS: 15000, features: ["snapshot", "heartbeat"])))
        XCTAssertEqual(SubscriptionClient.classifyAck(DaemonResponse(ok: false, error: "unknown request type: subscribe")),
                       .failure(.init(reason: .unsupported)))
        XCTAssertEqual(SubscriptionClient.classifyAck(DaemonResponse(ok: false, error: "subscriber_limit")),
                       .failure(.init(reason: .refused("subscriber_limit"))))
        XCTAssertEqual(SubscriptionClient.classifyAck(DaemonResponse(ok: true)),
                       .failure(.init(reason: .refused("subscribe refused"))), "an ok without an ack opens no stream")
    }

    // MARK: Requests

    func testRequestsEncodeOnlyTheirOwnBlock() throws {
        func object(_ request: DaemonRequest) throws -> [String: Any] {
            try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(request)) as? [String: Any])
        }
        XCTAssertEqual(try object(.health).keys.sorted(), ["type"])
        let focus = try object(.focusing(socket: "unix:/tmp/kitty-1", windowID: 4))
        XCTAssertEqual(focus.keys.sorted(), ["focus_window", "type"])
        XCTAssertEqual(focus["type"] as? String, "focus_window")
        let block = try XCTUnwrap(focus["focus_window"] as? [String: Any])
        XCTAssertEqual(block["kitty_socket"] as? String, "unix:/tmp/kitty-1")
        XCTAssertEqual(block["window_id"] as? Int, 4)
        let session = try object(.focusing(runtimeSessionID: "rs-0123456789abcdef0123456789abcdef"))
        XCTAssertEqual(session.keys.sorted(), ["focus_session", "type"])
        XCTAssertEqual(session["type"] as? String, "focus_session")
        XCTAssertEqual((session["focus_session"] as? [String: Any])?["runtime_session_id"] as? String, "rs-0123456789abcdef0123456789abcdef")
        let subscribe = try object(.subscribing(SubscribeParams()))
        let params = try XCTUnwrap(subscribe["subscribe"] as? [String: Any])
        XCTAssertEqual(params["protocol"] as? Int, 1)
        XCTAssertEqual(params["notification_backend"] as? Bool, false)
    }

    // MARK: focus_window replies

    func testFocusOutcomes() {
        func outcome(_ json: String) -> FocusOutcome { DaemonClient.focusOutcome(fromReply: Data(json.utf8)) }
        XCTAssertEqual(outcome(#"{"ok":true,"focused":{"window_id":4,"tab_id":2,"os_window_id":1}}"#),
                       .focused(FocusWindowResult(windowID: 4, tabID: 2, osWindowID: 1)))
        XCTAssertEqual(outcome(#"{"ok":false,"error":"stale_target"}"#), .staleTarget)
        XCTAssertEqual(outcome(#"{"ok":false,"error":"unknown_socket"}"#), .unknownSocket)
        XCTAssertEqual(outcome(#"{"ok":false,"error":"unknown request type: focus_window"}"#), .unsupported)
        XCTAssertEqual(outcome("nope"), .failed(DaemonPhrase.unreadable))
    }

    // MARK: focus_session replies

    func testFocusSessionOutcomes() {
        func outcome(_ json: String) -> FocusSessionOutcome { DaemonClient.focusSessionOutcome(fromReply: Data(json.utf8)) }
        XCTAssertEqual(outcome(#"{"ok":true,"focused_session":{"runtime_session_id":"rs-1","workspaces":1}}"#), .shown)
        XCTAssertEqual(outcome(#"{"ok":true,"focused_session":{"runtime_session_id":"rs-1","client":"/dev/ttys004"}}"#), .shown)
        XCTAssertEqual(outcome(#"{"ok":false,"error":"stale_target"}"#), .staleTarget)
        XCTAssertEqual(outcome(#"{"ok":false,"error":"no_viewer"}"#), .noViewer)
        XCTAssertEqual(outcome(#"{"ok":false,"error":"runtime_session_unknown"}"#), .failed("runtime_session_unknown"))
        XCTAssertEqual(outcome(#"{"ok":false,"error":"unknown request type: focus_session"}"#), .unsupported)
        XCTAssertEqual(outcome("nope"), .failed(DaemonPhrase.unreadable))
    }

    func testFocusSessionRoundTrip() {
        var sent: Data?
        let client = DaemonClient(socketPath: "/x", roundTrip: { _, request, timeout in
            sent = request
            XCTAssertEqual(timeout, DaemonClient.focusTimeout)
            return Data(#"{"ok":true,"focused_session":{"runtime_session_id":"rs-1","workspaces":1}}"#.utf8)
        })
        XCTAssertEqual(client.focusSession(runtimeSessionID: "rs-1"), .shown)
        let text = String(decoding: sent ?? Data(), as: UTF8.self)
        XCTAssertTrue(text.contains(#""type":"focus_session""#))
        XCTAssertTrue(text.contains(#""runtime_session_id":"rs-1""#))
        let down = DaemonClient(socketPath: "/x", roundTrip: { _, _, _ in throw LineSocketError.noSocket })
        XCTAssertEqual(down.focusSession(runtimeSessionID: "rs-1"), .notRunning)
    }

    func testFocusWindowRoundTrip() {
        var sent: Data?
        let client = DaemonClient(socketPath: "/x", roundTrip: { _, request, timeout in
            sent = request
            XCTAssertEqual(timeout, DaemonClient.focusTimeout)
            return Data(#"{"ok":false,"error":"stale_target"}"#.utf8)
        })
        XCTAssertEqual(client.focusWindow(socket: "unix:/tmp/kitty-9", windowID: 2), .staleTarget)
        XCTAssertTrue(String(decoding: sent ?? Data(), as: UTF8.self).contains(#""type":"focus_window""#))
        let down = DaemonClient(socketPath: "/x", roundTrip: { _, _, _ in throw LineSocketError.noSocket })
        XCTAssertEqual(down.focusWindow(socket: "s", windowID: 1), .notRunning)
    }
}

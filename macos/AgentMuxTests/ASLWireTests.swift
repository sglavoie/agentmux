import XCTest
@testable import AgentMux

/// The ASL requests on the daemon wire, and the menu's Answer… items.
final class ASLWireTests: XCTestCase {
    private func json(_ value: some Encodable) throws -> [String: Any] {
        let data = try JSONEncoder().encode(value)
        return try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    func testRequestsEncodeAsTheDaemonReadsThem() throws {
        XCTAssertEqual(try json(DaemonRequest.aslSnapshot) as NSDictionary, ["type": "asl_snapshot"])
        XCTAssertEqual(try json(DaemonRequest.aslQuestion(planDir: "/p", questionID: "q1")) as NSDictionary,
                       ["type": "asl_question", "asl": ["plan_dir": "/p", "question_id": "q1"]])
        let action = ASLRequestParams(method: ASLMethod.questionReply, planDir: "/p", rowID: "r", runID: "run",
                                      questionID: "q1", operationID: "amx-1", actionToken: "tok", text: "the answer")
        XCTAssertEqual(try json(DaemonRequest.aslAction(action)) as NSDictionary, [
            "type": "asl_action",
            "asl": [
                "method": "question.reply", "plan_dir": "/p", "row_id": "r", "run_id": "run", "question_id": "q1",
                "operation_id": "amx-1", "action_token": "tok", "text": "the answer",
            ],
        ])
        let resolve = ASLRequestParams(method: ASLMethod.questionResolve, resolution: "pause")
        XCTAssertEqual(try json(resolve) as NSDictionary, ["method": "question.resolve", "resolution": "pause"],
                       "empty fields stay off the wire")
    }

    func testDecodesAGoShapedSnapshotReply() throws {
        let reply = """
        {"ok":true,"asl_result":{"status":{"mode":"auto","available":true,"mutations_enabled":true,"restart_count":0},
        "snapshot":{"protocol":2,"rows":[{"row_id":"r1","plan_dir":"/p","label":"api","run_id":"run-1","session_id":"3",
        "primary_state":"waiting","urgency_rank":1,"window":{"kitty_window_id":7},"question":{"context_kind":"question",
        "question_id":"q1","unanswered_count":2,"question_ids":["q1","q2"],"asked_at":"2026-10-03T10:00:00Z"},
        "actions":["question.reply","question.resolve"],"disabled_reasons":{"run.resume":"not paused"},
        "action_token":"tok","warnings":null}],"digest":"d"},
        "meta":{"generation":4,"received_at":"2026-10-03T10:00:01Z","age_seconds":1,"stale":false},"receipts":null}}
        """
        let result = try DaemonClient.aslResult(fromReply: Data(reply.utf8)).get()
        XCTAssertTrue(result.status.available)
        XCTAssertTrue(result.status.mutationsEnabled)
        XCTAssertEqual(result.meta, ASLSnapshotMeta(generation: 4, ageSeconds: 1, stale: false))
        let row = try XCTUnwrap(result.snapshot?.rows.first)
        XCTAssertEqual(row.runID, "run-1")
        XCTAssertEqual(row.question?.questionIDs, ["q1", "q2"])
        XCTAssertEqual(row.actions, ["question.reply", "question.resolve"])
        XCTAssertEqual(row.disabledReasons, ["run.resume": "not paused"])
        XCTAssertEqual(row.actionToken, "tok")
    }

    func testDecodesQuestionAndOperationReplies() throws {
        let question = #"{"ok":true,"asl_result":{"status":{},"question":{"plan_dir":"/p","question_id":"q1","context_kind":"pause","body":"Proceed?","body_bytes":8,"action_token":"tok2"}}}"#
        let body = try XCTUnwrap(DaemonClient.aslResult(fromReply: Data(question.utf8)).get().question)
        XCTAssertEqual(body.body.reveal(), "Proceed?")
        XCTAssertEqual(body.actionToken, "tok2")
        XCTAssertEqual(body.contextKind, "pause")

        let operation = #"{"ok":true,"asl_result":{"status":{},"operation":{"operation_id":"amx-1","method":"question.reply","status":"accepted","question_id":"q1"}}}"#
        XCTAssertEqual(try DaemonClient.aslResult(fromReply: Data(operation.utf8)).get().operation,
                       ASLOperation(operationID: "amx-1", method: "question.reply", status: "accepted", questionID: "q1"))
    }

    func testRefusalsAndFailures() {
        XCTAssertEqual(DaemonClient.aslResult(fromReply: Data(#"{"ok":false,"error":"stale-action-token: expired"}"#.utf8)),
                       .failure(.refused("stale-action-token: expired")))
        XCTAssertEqual(DaemonClient.aslResult(fromReply: Data(#"{"ok":false,"error":"unknown request type \"asl_action\""}"#.utf8)),
                       .failure(.unsupported))
        XCTAssertEqual(DaemonClient.aslResult(fromReply: Data(#"{"ok":true}"#.utf8)),
                       .failure(.transport("daemon returned no ASL result")))
        XCTAssertEqual(DaemonClient.aslResult(fromReply: Data("garbage".utf8)), .failure(.transport(DaemonPhrase.unreadable)))
    }

    func testClientSendsOneRequestPerCall() throws {
        var client = DaemonClient()
        var sent: [(String, TimeInterval)] = []
        client.roundTrip = { _, request, timeout in
            let object = try JSONSerialization.jsonObject(with: request) as? [String: Any]
            sent.append((object?["type"] as? String ?? "", timeout))
            return Data(#"{"ok":true,"asl_result":{"status":{},"operation":{"operation_id":"amx-1","status":"accepted"}}}"#.utf8)
        }
        XCTAssertEqual(try client.aslAction(ASLRequestParams(method: ASLMethod.questionReply)).get().status, "accepted")
        XCTAssertEqual(client.aslQuestion(planDir: "/p", questionID: "q1"), .failure(.transport("daemon returned no question body")))
        XCTAssertEqual(client.aslSnapshot(), .failure(.transport("daemon returned no ASL snapshot")))
        XCTAssertEqual(sent.map(\.0), ["asl_action", "asl_question", "asl_snapshot"])
        XCTAssertEqual(sent.map(\.1), [DaemonClient.aslActionTimeout, DaemonClient.aslReadTimeout, DaemonClient.aslReadTimeout])

        client.roundTrip = { _, _, _ in throw LineSocketError.noSocket }
        XCTAssertEqual(client.aslSnapshot(), .failure(.notRunning))
    }

    // MARK: Menu

    private func session(_ id: Int, run: String = "", icon: String = "question") -> CompanionSession {
        var s = CompanionSession(windowID: id)
        s.iconState = icon
        s.attention = icon == "question"
        s.project = "p\(id)"
        s.aslRunID = run
        return s
    }

    private func waiting(_ run: String, window: Int, unanswered: Int = 1, plan: String = "") -> CompanionASLWaiting {
        var w = try! JSONDecoder().decode(CompanionASLWaiting.self, from: Data("{}".utf8))
        w.runID = run
        w.windowID = window
        w.unanswered = unanswered
        w.planDir = plan
        return w
    }

    private func entries(_ sessions: [CompanionSession], _ waiting: [CompanionASLWaiting]) -> [MenuEntry] {
        var snapshot = CompanionSnapshot()
        snapshot.daemon.ready = true
        snapshot.sessions = sessions
        snapshot.asl.waiting = waiting
        return MenuBuilder.entries(MenuBuilder.Input(feed: .live, daemonStatus: .ready(nil), sessions: sessions,
                                                     snapshot: snapshot, notice: nil, glyphs: .fallback))
    }

    private func answers(_ entries: [MenuEntry]) -> [(ASLAnswerTarget, String, Bool)] {
        entries.compactMap { entry in
            if case let .answer(target, title, indented) = entry { return (target, title, indented) }
            return nil
        }
    }

    func testAnswerFollowsTheSessionItsRunBelongsTo() {
        let list = entries([session(1, run: "run-a"), session(2, icon: "bolt")],
                           [waiting("run-a", window: 1, unanswered: 3, plan: "/plans/api")])
        let found = answers(list)
        XCTAssertEqual(found.count, 1)
        XCTAssertEqual(found.first?.0, ASLAnswerTarget(runID: "run-a", planDir: "/plans/api", windowID: 1))
        XCTAssertEqual(found.first?.1, "Answer… (3 questions)")
        XCTAssertEqual(found.first?.2, true)
        let index = list.firstIndex { if case .answer = $0 { return true }; return false }!
        guard case let .session(row) = list[index - 1] else { return XCTFail("Answer… does not follow its row") }
        XCTAssertEqual(row.windowID, 1)
        XCTAssertTrue(row.title.hasPrefix("❓"))
    }

    func testAnswerBindsByWindowOnlyWhenUnambiguous() {
        // One session with the window ID and no run of its own: bound.
        XCTAssertEqual(answers(entries([session(4)], [waiting("run-b", window: 4)])).first?.2, true)
        // Two Kitty instances both have a window 4: not bound to either.
        var other = session(4)
        other.kittySocket = "unix:/tmp/other"
        let ambiguous = entries([session(4), other], [waiting("run-b", window: 4, plan: "/plans/web")])
        XCTAssertEqual(answers(ambiguous).map(\.1), ["❓ web · Answer…"])
        XCTAssertEqual(answers(ambiguous).first?.2, false)
        XCTAssertTrue(ambiguous.contains(.header("Supervisor questions")))
    }

    func testUnlistedRunsGetTheirOwnSection() {
        let list = entries([], [waiting("run-z", window: 0)])
        XCTAssertTrue(list.contains(.info("No live sessions")))
        XCTAssertEqual(answers(list).map(\.1), ["❓ run-z · Answer…"])
        XCTAssertTrue(answers(entries([session(1, icon: "bolt")], [])).isEmpty)
    }

    func testAnswerItemSendsItsTarget() {
        final class Target: NSObject {}
        let target = Target()
        let items = MenuBuilder.items(
            for: [.answer(ASLAnswerTarget(runID: "run-a"), "Answer…", indented: true)],
            target: target, sessionAction: #selector(NSObject.mutableCopy), actionSelector: #selector(NSObject.copy)
        )
        XCTAssertEqual(items.count, 1)
        XCTAssertEqual(items[0].indentationLevel, 1)
        XCTAssertEqual((items[0].representedObject as? MenuBuilder.ActionBox)?.action, .answerASL(ASLAnswerTarget(runID: "run-a")))
    }
}

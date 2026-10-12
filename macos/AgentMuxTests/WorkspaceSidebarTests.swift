import XCTest
@testable import AgentMux

/// The workspace sidebar as data, and the wire that feeds it.
final class WorkspaceSidebarTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private let now = Date(timeIntervalSince1970: 1_000_000)
    private func ms(secondsAgo: Int) -> Int64 { Int64((now.timeIntervalSince1970 - Double(secondsAgo)) * 1000) }

    private func groups(_ sessions: [CompanionSession]) -> [WorkspaceGroup] {
        WorkspaceSidebar.groups(sessions, snapshotSocket: "unix:/tmp/kitty-1", now: now, glyphs: .fallback)
    }

    // MARK: Wire

    func testSubscribeAsksForRuntimeSessions() throws {
        let data = try JSONEncoder().encode(DaemonRequest.subscribing(SubscribeParams()))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let params = try XCTUnwrap(object["subscribe"] as? [String: Any])
        XCTAssertEqual(params["features"] as? [String], ["runtime_sessions", "workspace_views"])
        XCTAssertEqual(params["protocol"] as? Int, 1, "the feature rides protocol 1")
    }

    func testV2SnapshotDecodesRuntimeIdentity() throws {
        let json = #"""
        {"v":2,"kitty":{"socket":"unix:/tmp/kitty-100"},
         "asl":{"waiting":[{"run_id":"run-2","runtime_session_id":"rs-fedcba9876543210fedcba9876543210","unanswered":1}]},
         "sessions":[
          {"window_id":101,"kitty_socket":"unix:/tmp/kitty-100","project":"API","profile":"work","harness":"claude",
           "runtime":{"session_id":"rs-0123456789abcdef0123456789abcdef","backend":"kitty","instance":"unix:/tmp/kitty-100","generation":"kitty-gen-1","pane":"101"}},
          {"harness":"codex","profile":"work","project":"API","asl_run_id":"run-2","icon_state":"stop","attention":true,
           "runtime":{"session_id":"rs-fedcba9876543210fedcba9876543210","backend":"tmux","instance":"/tmp/amx-tmux.sock","generation":"tmux-gen-1","pane":"%7"}},
          {"window_id":9,"project":"legacy"}]}
        """#
        let snapshot = try JSONDecoder().decode(CompanionSnapshot.self, from: Data(json.utf8))
        XCTAssertEqual(snapshot.v, 2)
        let (kitty, tmux, legacy) = (snapshot.sessions[0], snapshot.sessions[1], snapshot.sessions[2])
        XCTAssertEqual(kitty.runtime?.backend, "kitty")
        XCTAssertNil(kitty.embeddedRuntimeSessionID, "a Kitty session is focused in Kitty even when a runtime session claims it")
        XCTAssertEqual(tmux.windowID, 0)
        XCTAssertEqual(tmux.runtime?.pane, "%7")
        XCTAssertEqual(tmux.embeddedRuntimeSessionID, "rs-fedcba9876543210fedcba9876543210")
        XCTAssertNil(legacy.runtime)
        XCTAssertEqual(snapshot.asl.waiting[0].runtimeSessionID, "rs-fedcba9876543210fedcba9876543210")

        XCTAssertEqual(AttentionCycler.key(kitty, snapshotSocket: ""), AttentionCycler.Key(socket: "unix:/tmp/kitty-100", windowID: 101))
        XCTAssertEqual(AttentionCycler.key(tmux, snapshotSocket: "unix:/tmp/kitty-100"), .embedded("rs-fedcba9876543210fedcba9876543210"))
    }

    func testASessionIsListedOnTheColorTheDaemonPublishes() throws {
        let json = #"""
        {"v":2,"sessions":[
          {"project":"api","profile":"work","tab_color":"#1a3655",
           "runtime":{"session_id":"rs-00000000000000000000000000000001","backend":"tmux","pane":"%1"}},
          {"project":"api","profile":"plain",
           "runtime":{"session_id":"rs-00000000000000000000000000000002","backend":"tmux","pane":"%2"}},
          {"project":"api","profile":"odd","tab_color":"blue",
           "runtime":{"session_id":"rs-00000000000000000000000000000003","backend":"tmux","pane":"%3"}},
          {"window_id":4,"project":"api","profile":"kitty","tab_color":"#551a1a"}]}
        """#
        let snapshot = try JSONDecoder().decode(CompanionSnapshot.self, from: Data(json.utf8))
        XCTAssertEqual(snapshot.sessions.map(\.tabColor), ["#1a3655", "", "blue", "#551a1a"])

        let rows = Dictionary(uniqueKeysWithValues: groups(snapshot.sessions)[0].rows.map { ($0.label, $0) })
        let color = try XCTUnwrap(rows["work"]?.tabColor)
        XCTAssertEqual([color.red, color.green, color.blue], [0x1a / 255.0, 0x36 / 255.0, 0x55 / 255.0])
        XCTAssertNil(rows["plain"]?.tabColor, "a session with no color is drawn on the list's own")
        XCTAssertNil(rows["odd"]?.tabColor, "anything but #rrggbb is no color")
        XCTAssertNil(rows["kitty"]?.tabColor, "a Kitty session's color is on its tab, in Kitty")
        XCTAssertNil(WorkspaceTabColor(hex: "#1a365"))
        XCTAssertNil(WorkspaceTabColor(hex: "#1a36zz"))
    }

    func testASessionCarriesTheNameTheDaemonPublishes() throws {
        let json = #"""
        {"v":2,"sessions":[
          {"project":"api","profile":"work","name":"billing fix",
           "runtime":{"session_id":"rs-00000000000000000000000000000001","backend":"tmux","pane":"%1"}},
          {"project":"api","profile":"plain",
           "runtime":{"session_id":"rs-00000000000000000000000000000002","backend":"tmux","pane":"%2"}},
          {"window_id":4,"project":"api","profile":"kitty","name":"stray"}]}
        """#
        let snapshot = try JSONDecoder().decode(CompanionSnapshot.self, from: Data(json.utf8))
        XCTAssertEqual(snapshot.sessions.map(\.name), ["billing fix", "", "stray"])

        let rows = Dictionary(uniqueKeysWithValues: groups(snapshot.sessions)[0].rows.map { ($0.label, $0) })
        XCTAssertEqual(rows["work"]?.name, "billing fix")
        XCTAssertEqual(rows["plain"]?.name, "", "a session nobody named has none")
        XCTAssertEqual(rows["kitty"]?.name, "", "a Kitty session is named by its tab, in Kitty")
        XCTAssertEqual(rows["work"]?.label, "work", "the name does not replace the profile, or move the row")
    }

    func testASessionCarriesTheHarnessIconTheDaemonPublishes() throws {
        let json = #"""
        {"v":2,"sessions":[
          {"project":"api","profile":"work","harness_icon":"🦊",
           "runtime":{"session_id":"rs-00000000000000000000000000000001","backend":"tmux","pane":"%1"}},
          {"project":"api","profile":"plain",
           "runtime":{"session_id":"rs-00000000000000000000000000000002","backend":"tmux","pane":"%2"}},
          {"window_id":4,"project":"api","profile":"kitty","harness_icon":"📟"}]}
        """#
        let snapshot = try JSONDecoder().decode(CompanionSnapshot.self, from: Data(json.utf8))
        XCTAssertEqual(snapshot.sessions.map(\.harnessIcon), ["🦊", "", "📟"])

        let rows = Dictionary(uniqueKeysWithValues: groups(snapshot.sessions)[0].rows.map { ($0.label, $0) })
        XCTAssertEqual(rows["work"]?.harnessIcon, "🦊")
        XCTAssertEqual(rows["plain"]?.harnessIcon, "", "a session whose icon was cleared has none")
        XCTAssertEqual(rows["kitty"]?.harnessIcon, "", "a Kitty session's icon is on its tab, in Kitty")
        XCTAssertEqual(rows["work"]?.label, "work", "the icon does not replace the profile, or move the row")
    }

    func testNamingPutsEveryFlagBeforeTheName() {
        // `AMXCommand.json` appends `--json` only when it is missing, and
        // anything after `--` is a name, not a flag.
        let arguments = NameSessionResult.arguments(runtimeSessionID: "rs-1", name: "--odd name")
        XCTAssertEqual(arguments, ["name-session", "--runtime-session", "rs-1", "--json", "--", "--odd name"])
        XCTAssertEqual(arguments.last, "--odd name")
        XCTAssertEqual(arguments.firstIndex(of: "--"), arguments.count - 2)
    }

    // MARK: Sidebar

    func testGroupsByProjectAndLabelsByProfile() {
        let sessions = [
            F.tmux(1, project: "web", profile: "work", harness: "codex", icon: "bolt"),
            F.kitty(4, project: "api", profile: "work", icon: "key", attention: true, changedAtMS: ms(secondsAgo: 240)),
            F.tmux(2, project: "api", profile: "personal", icon: "stop", attention: true, changedAtMS: ms(secondsAgo: 30)),
            F.tmux(3, project: "API tools", profile: "", harness: "copilot"),
        ]
        var reasoned = sessions
        reasoned[1].reason = "permission"
        reasoned[1].muted = true
        reasoned[0].branch = "main"
        reasoned[0].dirty = true
        let built = groups(reasoned)

        XCTAssertEqual(built.map(\.project), ["api", "API tools", "web"], "projects sort by name, not by attention")
        XCTAssertEqual(built.map(\.attention), [2, 0, 0])
        XCTAssertEqual(built[0].rows.map(\.label), ["personal", "work"], "rows sort by profile inside a project")
        XCTAssertEqual(built[1].rows.map(\.label), ["copilot"], "a session with no profile is labeled by its harness")

        let personal = built[0].rows[0]
        XCTAssertEqual(personal.target, .embedded(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(personal.key, .embedded(F.runtimeID(2)))
        XCTAssertEqual(personal.glyph, "🛑")
        XCTAssertTrue(personal.attention)
        XCTAssertEqual(personal.detail, "claude · <1m")
        XCTAssertEqual(personal.toolTip, "tmux %2")

        let work = built[0].rows[1]
        XCTAssertEqual(work.target, .kitty(socket: "", windowID: 4))
        XCTAssertEqual(work.key, AttentionCycler.Key(socket: "unix:/tmp/kitty-1", windowID: 4))
        XCTAssertEqual(work.detail, "claude · 4m · permission · Kitty", "a Kitty row says where it opens")
        XCTAssertTrue(work.muted)
        XCTAssertEqual(work.toolTip, "window 4")

        let web = built[2].rows[0]
        XCTAssertEqual(web.detail, "codex · main*")
        XCTAssertTrue(web.working)
        XCTAssertFalse(web.attention)
    }

    func testTwoTmuxSessionsOfOneProjectAndProfileStayApart() {
        let built = groups([F.tmux(2, project: "api"), F.tmux(1, project: "api")])
        XCTAssertEqual(built.count, 1)
        XCTAssertEqual(built[0].rows.map(\.key), [.embedded(F.runtimeID(1)), .embedded(F.runtimeID(2))])
    }

    func testADraggedOrderHoldsAndNewSessionsFollowIt() throws {
        let sessions = [F.tmux(1, project: "api"), F.tmux(2, project: "api"), F.kitty(4, project: "api"), F.tmux(3, project: "web")]
        let kitty = AttentionCycler.Key(socket: "unix:/tmp/kitty-1", windowID: 4)
        var order = WorkspaceOrder()
        order.set([kitty, .embedded(F.runtimeID(2)), .embedded(F.runtimeID(1))], for: "api")
        func keys(_ sessions: [CompanionSession], _ project: String = "api") -> [AttentionCycler.Key] {
            WorkspaceSidebar.groups(sessions, snapshotSocket: "unix:/tmp/kitty-1", now: now, glyphs: .fallback, order: order)
                .first { $0.project == project }?.rows.map(\.key) ?? []
        }
        XCTAssertEqual(keys(sessions), [kitty, .embedded(F.runtimeID(2)), .embedded(F.runtimeID(1))])
        XCTAssertEqual(keys(sessions, "web"), [.embedded(F.runtimeID(3))], "a project nobody dragged in sorts as it did")
        XCTAssertEqual(keys(sessions + [F.tmux(5, project: "api", profile: "alpha")]).last, .embedded(F.runtimeID(5)),
                       "a session launched since follows the dragged ones")
        XCTAssertEqual(keys([sessions[0], sessions[2]]), [kitty, .embedded(F.runtimeID(1))], "one that ended leaves no gap")

        var state = WorkspaceState()
        state.apply(groups(sessions))
        state.arrange(order)
        XCTAssertEqual(state.groups[0].rows.map(\.key), keys(sessions), "held rows are arranged without a snapshot")

        let suite = "amx.tests.order.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        XCTAssertEqual(WorkspaceOrder.load(from: defaults), WorkspaceOrder())
        order.save(to: defaults)
        XCTAssertEqual(WorkspaceOrder.load(from: defaults), order)
    }

    func testFeedNotice() {
        XCTAssertNil(WorkspaceSidebar.feedNotice(.live))
        XCTAssertEqual(WorkspaceSidebar.feedNotice(.notRunning), "Daemon: not running")
        XCTAssertEqual(WorkspaceSidebar.feedNotice(.reconnecting("no heartbeat")), "Daemon: reconnecting · no heartbeat")
        XCTAssertEqual(WorkspaceSidebar.feedNotice(.connecting), "Daemon: connecting…")
    }

    // MARK: State

    func testStateHoldsRowsAndSelectionWhileTheFeedIsDown() {
        var state = WorkspaceState()
        let listed = groups([F.tmux(1, project: "api"), F.kitty(4, project: "web")])
        XCTAssertFalse(state.apply(listed))
        XCTAssertTrue(state.current)
        XCTAssertTrue(state.select(.embedded(F.runtimeID(1))))
        XCTAssertFalse(state.select(.embedded(F.runtimeID(9))), "a session that is not listed cannot be selected")
        XCTAssertEqual(state.selection, .embedded(F.runtimeID(1)))

        XCTAssertFalse(state.apply(nil), "a feed that is down decides nothing")
        XCTAssertFalse(state.current)
        XCTAssertEqual(state.groups, listed)
        XCTAssertEqual(state.selection, .embedded(F.runtimeID(1)))
        XCTAssertEqual(state.embeddedRuntimeSessionIDs, [F.runtimeID(1)])

        XCTAssertFalse(state.apply(listed), "the same sessions after the reconnect keep the selection")
        XCTAssertTrue(state.current)
        XCTAssertEqual(state.selection, .embedded(F.runtimeID(1)))

        XCTAssertTrue(state.apply(groups([F.kitty(4, project: "web")])), "a live snapshot without it drops the selection")
        XCTAssertNil(state.selection)
    }

    func testNeighborWrapsInSidebarOrder() {
        var state = WorkspaceState()
        state.apply(groups([F.tmux(1, project: "b"), F.kitty(4, project: "a"), F.tmux(2, project: "c")]))
        XCTAssertEqual(state.neighbor(1)?.project, "a")
        XCTAssertEqual(state.neighbor(-1)?.project, "c")
        state.select(state.rows[2].key)
        XCTAssertEqual(state.neighbor(1)?.project, "a")
        XCTAssertEqual(state.neighbor(-1)?.project, "b")
        XCTAssertNil(WorkspaceState().neighbor(1))
    }

    // MARK: Mixed backends in the menu bar and the cycle

    func testNextAttentionCyclesAcrossBackendsOldestFirst() {
        let sessions = [
            F.tmux(1, project: "a", icon: "stop", attention: true, changedAtMS: ms(secondsAgo: 100)),
            F.kitty(4, project: "b", icon: "key", attention: true, changedAtMS: ms(secondsAgo: 300)),
            F.tmux(2, project: "c", icon: "question", attention: true, changedAtMS: ms(secondsAgo: 200)),
            F.tmux(3, project: "d", icon: "bolt"),
            F.kitty(4, project: "e", socket: "unix:/tmp/kitty-2", icon: "key", attention: true, changedAtMS: ms(secondsAgo: 50)),
        ]
        var cycler = AttentionCycler()
        var visited: [String] = []
        for _ in 0..<5 {
            let session = cycler.advance(in: sessions, snapshotSocket: "unix:/tmp/kitty-1")
            visited.append(session?.project ?? "")
        }
        XCTAssertEqual(visited, ["b", "c", "a", "e", "b"])

        // The row each press focuses names where the session is.
        let rows = AttentionCycler.ordered(sessions, snapshotSocket: "unix:/tmp/kitty-1").map { MenuSessionRow.focusing($0) }
        XCTAssertEqual(rows.map(\.runtimeSessionID), ["", F.runtimeID(2), F.runtimeID(1), ""])
        XCTAssertEqual(rows.map(\.windowID), [4, 0, 0, 4])

        // The session focused last settles and leaves; the cycle goes on from
        // where it stood instead of starting over.
        var after = sessions
        cycler = AttentionCycler()
        _ = cycler.advance(in: after, snapshotSocket: "unix:/tmp/kitty-1")
        XCTAssertEqual(cycler.advance(in: after, snapshotSocket: "unix:/tmp/kitty-1")?.project, "c")
        after[2].attention = false
        XCTAssertEqual(cycler.advance(in: after, snapshotSocket: "unix:/tmp/kitty-1")?.project, "a")
    }

    func testMenuListsTmuxSessionsWithTheirAnswerItems() throws {
        var tmuxA = F.tmux(1, project: "api", icon: "question", attention: true, changedAtMS: ms(secondsAgo: 60))
        tmuxA.aslRunID = "run-a"
        let tmuxB = F.tmux(2, project: "web", icon: "question", attention: true, changedAtMS: ms(secondsAgo: 30))
        let kitty = F.kitty(4, project: "ops", icon: "bolt")
        var snapshot = F.snapshot([tmuxA, tmuxB, kitty])
        var runA = CompanionASLWaiting()
        runA.runID = "run-a"
        runA.planDir = "/plans/a"
        var runB = CompanionASLWaiting()
        runB.runID = "run-b"
        runB.runtimeSessionID = F.runtimeID(2)
        runB.unanswered = 2
        snapshot.asl.waiting = [runA, runB]

        let input = MenuBuilder.Input(feed: .live, daemonStatus: .ready(nil), sessions: snapshot.sessions, snapshot: snapshot,
                                      notice: nil, now: now, glyphs: .fallback, gate: ActionGate())
        let entries = MenuBuilder.entries(input)
        let rows = entries.compactMap { entry -> MenuSessionRow? in
            if case let .session(row) = entry { return row }
            return nil
        }
        XCTAssertEqual(rows.map(\.runtimeSessionID), [F.runtimeID(1), F.runtimeID(2), ""])
        XCTAssertEqual(rows[0].title, "❓ api · claude · 1m")
        XCTAssertEqual(rows[0].toolTip, "tmux %1 · profile work")
        XCTAssertEqual(rows[0].muteUnavailable, MenuBuilder.muteNeedsKitty)
        XCTAssertEqual(rows[2].muteUnavailable, ActionGate().unavailableReason(.muteToggle), "a Kitty row follows the gate")

        // Both tmux sessions have window ID 0; each still gets its own run.
        let answers = entries.compactMap { entry -> String? in
            if case let .answer(target, title, _) = entry { return "\(target.runID):\(title)" }
            return nil
        }
        XCTAssertEqual(answers, ["run-a:Answer…", "run-b:Answer… (2 questions)"])
        let index = try XCTUnwrap(entries.firstIndex(of: .session(rows[1])))
        XCTAssertEqual(entries[index + 1], .answer(ASLAnswerTarget(runID: "run-b", planDir: "", windowID: 0), "Answer… (2 questions)", indented: true))

        XCTAssertEqual(MenuBuilder.actionEntries(MenuBuilder.ActionsInput())[1], .action(.openWorkspace, "Open Workspace"))
    }

    func testOnlyKittySessionsNeedAKittySocket() {
        XCTAssertFalse(StatusItemController.needsKittySocket([F.tmux(1, project: "api")], snapshotSocket: ""))
        XCTAssertTrue(StatusItemController.needsKittySocket([F.tmux(1, project: "api"), F.kitty(4, project: "web")], snapshotSocket: ""))
        XCTAssertFalse(StatusItemController.needsKittySocket([F.kitty(4, project: "web", socket: "unix:/tmp/k")], snapshotSocket: ""))
        XCTAssertFalse(StatusItemController.needsKittySocket([F.kitty(4, project: "web")], snapshotSocket: "unix:/tmp/k"))
    }

    func testANoticeForATmuxSessionOpensItInTheWorkspace() throws {
        let json = #"{"notification_id":"n-1","navigable":false,"title":"api","runtime_session_id":"rs-00000000000000000000000000000001","backend":"tmux"}"#
        let notice = try JSONDecoder().decode(AppNotice.self, from: Data(json.utf8))
        XCTAssertEqual(notice.embeddedRuntimeSessionID, F.runtimeID(1))
        let info = NotificationPresenter.request(for: notice).content.userInfo
        XCTAssertEqual(info[NotificationPresenter.UserInfoKey.embeddedRuntimeSessionID] as? String, F.runtimeID(1))

        let kitty = #"{"notification_id":"n-2","window_id":4,"navigable":true,"runtime_session_id":"rs-00000000000000000000000000000002","backend":"kitty"}"#
        XCTAssertEqual(try JSONDecoder().decode(AppNotice.self, from: Data(kitty.utf8)).embeddedRuntimeSessionID, "")
    }
}

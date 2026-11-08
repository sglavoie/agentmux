import XCTest
@testable import AgentMux

extension WorkspaceSidebarTests {
    func testFeedNotice() {
        XCTAssertNil(WorkspaceSidebar.feedNotice(.live))
        XCTAssertEqual(WorkspaceSidebar.feedNotice(.notRunning), "Daemon not running")
        XCTAssertEqual(WorkspaceSidebar.feedNotice(.reconnecting("no heartbeat")), "Reconnecting… (no heartbeat)")
        XCTAssertEqual(WorkspaceSidebar.feedNotice(.connecting), "Connecting to the daemon…")
        XCTAssertEqual(WorkspaceSidebar.feedNotice(.daemonTooOld), "Daemon too old — choose Restart Daemon")
        XCTAssertTrue(WorkspaceSidebar.feedNotice(.daemonTooOld)?.contains(MenuBuilder.restartHint) ?? false,
                      "it names the Restart Daemon offered with it, as the menu bar does")
        for feed in [FeedState.connecting, .notRunning, .daemonTooOld, .reconnecting("x")] {
            let notice = WorkspaceSidebar.feedNotice(feed)
            XCTAssertFalse(notice?.contains("`") ?? true, "no Markdown in a label")
            XCTAssertEqual(notice?.first?.isUppercase, true, "sentence case")
        }
        XCTAssertNil(WorkspaceSidebar.feedExplanation(.live))
        XCTAssertEqual(WorkspaceSidebar.feedExplanation(.notRunning)?.action, .start)
        XCTAssertEqual(WorkspaceSidebar.feedExplanation(.daemonTooOld)?.action, .restart)
        XCTAssertNil(WorkspaceSidebar.feedExplanation(.connecting)?.action)
        XCTAssertEqual(WorkspaceSidebar.feedExplanation(.reconnecting("x"))?.title, "Reconnecting…")
        XCTAssertNil(WorkspaceSidebar.feedExplanation(.reconnecting("x"))?.action)
        XCTAssertEqual(WorkspaceSidebar.daemonAction(.reconnecting(DaemonPhrase.notAnswering)), .restart,
                       "a daemon that stopped answering is not waited on for good")
        XCTAssertEqual(WorkspaceSidebar.daemonAction(.reconnecting(DaemonPhrase.unreadableSnapshot)), .restart)
        XCTAssertNil(WorkspaceSidebar.daemonAction(.reconnecting(DaemonPhrase.restarting)), "a restart under way is waited on")
        XCTAssertNil(WorkspaceSidebar.daemonAction(.live))
        XCTAssertEqual(WorkspaceSidebar.DaemonAction.start.title, "Start Daemon")
        XCTAssertEqual(WorkspaceSidebar.DaemonAction.restart.title, "Restart Daemon")
    }

    func testDisplayNamePrefersTheGivenName() {
        var row = WorkspaceRow(key: .embedded("rs-1"), target: .embedded(runtimeSessionID: "rs-1"), project: "api", label: "work",
                               detail: "", glyph: "", attention: false, working: false, muted: false, toolTip: "")
        XCTAssertEqual(row.displayName, "api — work")
        row.name = "release notes"
        XCTAssertEqual(row.displayName, "release notes")
    }

    func testARowIsSpokenAsItIsShown() {
        var row = WorkspaceRow(key: .embedded("rs-1"), target: .embedded(runtimeSessionID: "rs-1"), project: "api", label: "work",
                               detail: "claude · 4m", glyph: "", attention: true, working: false, muted: false, toolTip: "")
        XCTAssertEqual(WorkspaceRowCellView.accessibilityLabel(row, sessionKey: "2"),
                       "api, work, needs attention, claude · 4m, Command 2")
        XCTAssertEqual(WorkspaceRowCellView.accessibilityLabel(row, closing: true),
                       "api, work, needs attention, ending session", "a closing session is said to be closing, not its detail")
        row = WorkspaceRow(key: row.key, target: row.target, project: "api", label: "work",
                           detail: "", glyph: "", attention: false, working: true, muted: true, toolTip: "")
        row.pinned = true
        row.name = "release notes"
        XCTAssertEqual(WorkspaceRowCellView.accessibilityLabel(row, bell: true),
                       "release notes, api, work, pinned, muted, working, bell")
    }

    // MARK: State

    func testStateHoldsRowsAndSelectionWhileTheFeedIsDown() {
        var state = WorkspaceState()
        let listed = groups([F.tmux(1, project: "api"), F.tmux(4, project: "web")])
        XCTAssertFalse(state.apply(listed))
        XCTAssertTrue(state.current)
        XCTAssertTrue(state.select(.embedded(F.runtimeID(1))))
        XCTAssertFalse(state.select(.embedded(F.runtimeID(9))), "a session that is not listed cannot be selected")
        XCTAssertEqual(state.selection, .embedded(F.runtimeID(1)))

        XCTAssertFalse(state.apply(nil), "a feed that is down decides nothing")
        XCTAssertFalse(state.current)
        XCTAssertEqual(state.groups, listed)
        XCTAssertEqual(state.selection, .embedded(F.runtimeID(1)))
        XCTAssertEqual(state.embeddedRuntimeSessionIDs, [F.runtimeID(1), F.runtimeID(4)])

        XCTAssertFalse(state.apply(listed), "the same sessions after the reconnect keep the selection")
        XCTAssertTrue(state.current)
        XCTAssertEqual(state.selection, .embedded(F.runtimeID(1)))

        XCTAssertTrue(state.apply(groups([F.tmux(4, project: "web")])), "a live snapshot without it drops the selection")
        XCTAssertNil(state.selection)
    }

    func testNeighborWrapsInSidebarOrder() {
        var state = WorkspaceState()
        state.apply(groups([F.tmux(1, project: "b"), F.tmux(4, project: "a"), F.tmux(2, project: "c")]))
        XCTAssertEqual(state.neighbor(1)?.project, "a")
        XCTAssertEqual(state.neighbor(-1)?.project, "c")
        state.select(state.rows[2].key)
        XCTAssertEqual(state.neighbor(1)?.project, "a")
        XCTAssertEqual(state.neighbor(-1)?.project, "b")
        XCTAssertNil(WorkspaceState().neighbor(1))
    }

    // MARK: Mixed backends in the menu bar and the cycle

    func testNextAttentionCyclesAcrossBackendsInSidebarOrder() {
        var sessions = [
            F.tmux(1, project: "a", icon: "question", attention: true, changedAtMS: ms(secondsAgo: 100)),
            F.tmux(4, project: "b", icon: "key", attention: true, changedAtMS: ms(secondsAgo: 300)),
            F.tmux(2, project: "c", icon: "question", attention: true, changedAtMS: ms(secondsAgo: 200)),
            F.tmux(3, project: "d", icon: "bolt"),
            F.tmux(5, project: "e", icon: "key", attention: true, changedAtMS: ms(secondsAgo: 50)),
        ]
        sessions[0].unseen = true
        var cycler = AttentionCycler()
        var visited: [String] = []
        for _ in 0..<5 {
            let session = cycler.advance(in: sessions)
            visited.append(session?.project ?? "")
        }
        XCTAssertEqual(visited, ["a", "b", "c", "e", "a"], "however long each has waited")

        // The row each press focuses names where the session is.
        let rows = AttentionCycler.ordered(sessions).map { MenuSessionRow.focusing($0) }
        XCTAssertEqual(rows.map(\.runtimeSessionID), [F.runtimeID(1), F.runtimeID(4), F.runtimeID(2), F.runtimeID(5)])
        XCTAssertEqual(rows.map(\.testID), [1, 4, 2, 5])

        // The session focused last settles and leaves; the cycle goes on from
        // where it stood instead of starting over.
        var after = sessions
        cycler = AttentionCycler()
        _ = cycler.advance(in: after)
        XCTAssertEqual(cycler.advance(in: after)?.project, "b")
        after[1].attention = false
        XCTAssertEqual(cycler.advance(in: after)?.project, "c")
    }

    func testMenuListsTmuxSessionsWithTheirAnswerItems() throws {
        var tmuxA = F.tmux(1, project: "api", icon: "question", attention: true, changedAtMS: ms(secondsAgo: 60))
        tmuxA.aslRunID = "run-a"
        let tmuxB = F.tmux(2, project: "web", icon: "question", attention: true, changedAtMS: ms(secondsAgo: 30))
        let fourth = F.tmux(4, project: "ops", icon: "bolt")
        var snapshot = F.snapshot([tmuxA, tmuxB, fourth])
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
        XCTAssertEqual(rows.map(\.runtimeSessionID), [F.runtimeID(1), F.runtimeID(2), F.runtimeID(4)])
        XCTAssertEqual(rows[0].title, "❓ api · claude · 1m")
        XCTAssertEqual(rows[0].toolTip, "tmux %1 · profile work")
        XCTAssertEqual(rows[0].muteUnavailable, ActionGate().unavailableReason(.muteToggle), "an embedded row follows the gate too")
        XCTAssertEqual(rows[2].muteUnavailable, ActionGate().unavailableReason(.muteToggle))
        let full = ActionGate(capabilities: AMXCapabilities(project: true, openDashboard: true, notificationsSnooze: true, muteToggleJSON: true))
        let muteable = MenuBuilder.row(snapshot.sessions[0], showReason: false, input: MenuBuilder.Input(
            feed: .live, daemonStatus: .ready(nil), sessions: snapshot.sessions, snapshot: snapshot,
            notice: nil, now: now, glyphs: .fallback, gate: full))
        XCTAssertNil(muteable.muteUnavailable, "an embedded session mutes by runtime session")

        // Both tmux sessions have window ID 0; each still gets its own run.
        let answers = entries.compactMap { entry -> String? in
            if case let .answer(target, title, _) = entry { return "\(target.runID):\(title)" }
            return nil
        }
        XCTAssertEqual(answers, ["run-a:Answer…", "run-b:Answer… (2 questions)"])
        let index = try XCTUnwrap(entries.firstIndex(of: .session(rows[1])))
		XCTAssertEqual(entries[index + 1], .answer(ASLAnswerTarget(runID: "run-b", planDir: ""), "Answer… (2 questions)", indented: true))

        XCTAssertEqual(MenuBuilder.actionEntries(MenuBuilder.ActionsInput())[1], .action(.openWorkspace, "Open Workspace"))
    }

    func testANoticeForATmuxSessionOpensItInTheWorkspace() throws {
        let json = #"{"notification_id":"n-1","navigable":false,"title":"api","runtime_session_id":"rs-00000000000000000000000000000001","backend":"tmux"}"#
        let notice = try JSONDecoder().decode(AppNotice.self, from: Data(json.utf8))
        XCTAssertEqual(notice.embeddedRuntimeSessionID, F.runtimeID(1))
        let info = NotificationPresenter.request(for: notice).content.userInfo
        XCTAssertEqual(info[NotificationPresenter.UserInfoKey.embeddedRuntimeSessionID] as? String, F.runtimeID(1))

        let unsupported = #"{"notification_id":"n-2","window_id":4,"navigable":true,"runtime_session_id":"rs-00000000000000000000000000000002","backend":"unsupported"}"#
        XCTAssertEqual(try JSONDecoder().decode(AppNotice.self, from: Data(unsupported.utf8)).embeddedRuntimeSessionID, "")
    }
}

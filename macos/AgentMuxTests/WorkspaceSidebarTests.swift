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

    func testAttentionMatchesOverviewAndUnreadCompletionAcknowledgment() {
        for (icon, unseen, expected) in [
            ("stop", true, true), ("stop", false, false),
            ("key", false, true), ("error", false, true), ("question", false, true),
            ("bolt", true, false), ("warning", true, false), ("compact", true, false),
            ("subagent", true, false), ("", true, false), ("unknown", true, false),
        ] {
            var session = F.tmux(1, project: "api", icon: icon, attention: true)
            session.unseen = unseen
            XCTAssertEqual(session.needsAttention, expected, "\(icon), unseen=\(unseen)")
            XCTAssertEqual(groups([session])[0].needsAttention, expected)
        }
    }

    func testAttentionFloatsAboveAllProjectsAndPinsUntilAcknowledged() {
        var sessions = [
            F.tmux(1, project: "api", profile: "a", icon: "bolt"),
            F.tmux(2, project: "web", profile: "b", icon: "stop", attention: true),
            F.tmux(3, project: "web", profile: "c", icon: "bolt"),
            F.kitty(4, project: "Needs attention", icon: "question", attention: true),
        ]
        sessions[1].unseen = true
        var order = WorkspaceOrder()
        order.setPinned(.embedded(F.runtimeID(1)), true)
        order.set([.embedded(F.runtimeID(3)), .embedded(F.runtimeID(2))], for: "web")
        order.setCollapsed(project: "web", pinned: false, true)
        func built() -> [WorkspaceGroup] {
            WorkspaceSidebar.groups(sessions, snapshotSocket: "unix:/tmp/kitty-1", order: order)
        }
        var state = WorkspaceState()
        state.apply(built())
        let key = AttentionCycler.Key(socket: "unix:/tmp/kitty-1", windowID: 4)
        XCTAssertEqual(state.groups.map(\.project), ["Needs attention", "Pinned", "web"])
        XCTAssertEqual(state.groups[0].rows.map(\.key), [key, .embedded(F.runtimeID(2))])
        XCTAssertFalse(state.groups[0].collapsed, "waiting sessions escape collapsed projects")
        XCTAssertEqual(state.rows.count, sessions.count, "sessions appear exactly once")
        state.select(.embedded(F.runtimeID(2)))
        let saved = order
        state.apply(nil)
        state.arrange(order)
        XCTAssertEqual(state.groups, built(), "held rows retain their attention positions")

        sessions[1].unseen = false // The icon and the older attention bit stay set.
        sessions[3].iconState = "bolt"
        sessions[3].attention = false
        state.apply(built())
        XCTAssertFalse(state.groups.contains(where: \.needsAttention))
        XCTAssertEqual(state.groups.map(\.project), ["Pinned", "Needs attention", "web"])
        XCTAssertEqual(state.groups.last?.rows.map(\.key), [.embedded(F.runtimeID(3)), .embedded(F.runtimeID(2))])
        XCTAssertTrue(state.groups.last?.collapsed ?? false)
        XCTAssertEqual(state.selection, .embedded(F.runtimeID(2)))
        XCTAssertEqual(order, saved, "promotion never rewrites the saved order")

        sessions[0].iconState = "error"
        XCTAssertTrue(built()[0].rows[0].pinned, "a promoted pin remains pinned")
        sessions[0].iconState = "bolt"
        XCTAssertTrue(built()[0].pinned, "it returns to Pinned when resolved")
    }

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

        XCTAssertEqual(built.map(\.project), ["Needs attention", "api", "API tools", "web"])
        XCTAssertEqual(built.map(\.attention), [1, 1, 0, 0])
        XCTAssertEqual(built[0].rows.map(\.label), ["work"], "the permission request floats above the seen completion")
        XCTAssertEqual(built[2].rows.map(\.label), ["copilot"], "a session with no profile is labeled by its harness")

        let personal = built[1].rows[0]
        XCTAssertEqual(personal.target, .embedded(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(personal.key, .embedded(F.runtimeID(2)))
        XCTAssertEqual(personal.glyph, "🛑")
        XCTAssertTrue(personal.attention)
        XCTAssertEqual(personal.detail, "claude · <1m")
        XCTAssertEqual(personal.toolTip, "tmux %2")

        let work = built[0].rows[0]
        XCTAssertEqual(work.target, .kitty(socket: "", windowID: 4))
        XCTAssertEqual(work.key, AttentionCycler.Key(socket: "unix:/tmp/kitty-1", windowID: 4))
        XCTAssertEqual(work.detail, "claude · 4m · permission · Kitty", "a Kitty row says where it opens")
        XCTAssertTrue(work.muted)
        XCTAssertEqual(work.toolTip, "window 4")

        let web = built[3].rows[0]
        XCTAssertEqual(web.detail, "codex · main*")
        XCTAssertTrue(web.working)
        XCTAssertFalse(web.attention)
    }

    func testProjectsSortByNameNotByTheIconTheyLeadWith() {
        let names = ["🎵 muse", "🐱 amx", "👶 Lyzma", "🔍 LogScanner", "🧠 BrainNotes", "🧰 dev-helpers", "api", "★"]
        let built = groups(names.enumerated().map { F.tmux($0 + 1, project: $1) })
        XCTAssertEqual(
            built.map(\.project),
            ["★", "🐱 amx", "api", "🧠 BrainNotes", "🧰 dev-helpers", "🔍 LogScanner", "👶 Lyzma", "🎵 muse"],
            "a name that is all symbols sorts as written"
        )
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

    func testPinnedSessionsLeaveTheirProjectsForAGroupListedFirst() throws {
        let sessions = [F.tmux(1, project: "api"), F.tmux(2, project: "api", profile: "personal"), F.kitty(4, project: "web"),
                        F.tmux(3, project: "Pinned")]
        let kitty = AttentionCycler.Key(socket: "unix:/tmp/kitty-1", windowID: 4)
        var order = WorkspaceOrder()
        func built(_ sessions: [CompanionSession]) -> [WorkspaceGroup] {
            WorkspaceSidebar.groups(sessions, snapshotSocket: "unix:/tmp/kitty-1", now: now, glyphs: .fallback, order: order)
        }
        XCTAssertEqual(built(sessions).map(\.pinned), [false, false, false], "nothing is pinned until asked")

        order.setPinned(kitty, true)
        order.setPinned(.embedded(F.runtimeID(1)), true)
        var groups = built(sessions)
        XCTAssertEqual(groups.map(\.project), ["Pinned", "api", "Pinned"], "a project of that name is still a project")
        XCTAssertEqual(groups.map(\.pinned), [true, false, false])
        XCTAssertEqual(groups[0].rows.map(\.key), [kitty, .embedded(F.runtimeID(1))], "in the order they were pinned")
        XCTAssertEqual(groups[0].rows.map(\.project), ["web", "api"], "a pinned row keeps its project")
        XCTAssertEqual(groups[0].rows.map(\.pinned), [true, true])
        XCTAssertEqual(groups[1].rows.map(\.key), [.embedded(F.runtimeID(2))])
        XCTAssertEqual(WorkspaceSidebar.ordered(sessions, snapshotSocket: "unix:/tmp/kitty-1", order: order).first?.windowID, 4,
                       "what follows sidebar order starts with the pinned")

        order.setPinned([.embedded(F.runtimeID(1)), kitty])
        XCTAssertEqual(built(sessions)[0].rows.map(\.key), [.embedded(F.runtimeID(1)), kitty])
        XCTAssertEqual(built([sessions[1], sessions[2]])[0].rows.map(\.key), [kitty], "one that ended leaves no gap")
        XCTAssertFalse(built([sessions[1]])[0].pinned, "and no heading when none is listed")

        // Held rows are grouped again without a snapshot.
        var state = WorkspaceState()
        state.apply(built(sessions))
        order.setPinned(kitty, false)
        state.arrange(order)
        XCTAssertEqual(state.groups, built(sessions))
        groups = state.groups
        XCTAssertEqual(groups.map(\.project), ["Pinned", "api", "Pinned", "web"])

        order.setPinned(kitty, true)
        order.forgetPins(notAmong: [kitty])
        XCTAssertEqual(order.pinned, [WorkspaceOrder.token(kitty)], "the pin of a session that ended is forgotten")

        let suite = "amx.tests.pins.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        order.save(to: defaults)
        XCTAssertEqual(WorkspaceOrder.load(from: defaults), order)
    }

    func testCollapsedGroupsAreKeptAndStillCountWhatWaits() throws {
        let sessions = [F.tmux(1, project: "api"), F.tmux(2, project: "api", profile: "personal", attention: true),
                        F.tmux(3, project: "Pinned")]
        var order = WorkspaceOrder()
        func built() -> [WorkspaceGroup] {
            WorkspaceSidebar.groups(sessions, now: now, glyphs: .fallback, order: order)
        }
        XCTAssertEqual(built().map(\.collapsed), [false, false], "nothing is collapsed until asked")

        order.setCollapsed(project: "api", pinned: false, true)
        order.setPinned(.embedded(F.runtimeID(1)), true)
        var groups = built()
        XCTAssertEqual(groups.map(\.project), ["Pinned", "api", "Pinned"])
        XCTAssertEqual(groups.map(\.collapsed), [false, true, false], "the pinned group is not the project called Pinned")
        XCTAssertEqual(groups[1].rows.count, 1, "a collapsed group keeps its rows")
        XCTAssertEqual(groups[1].attention, 1)

        order.setCollapsed(project: "Pinned", pinned: true, true)
        XCTAssertEqual(built().map(\.collapsed), [true, true, false])
        order.forget(notAmong: [])
        XCTAssertTrue(order.isCollapsed(project: "api", pinned: false), "a project with nothing listed stays collapsed")

        let suite = "amx.tests.collapsed.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        order.save(to: defaults)
        XCTAssertEqual(WorkspaceOrder.load(from: defaults), order)
        XCTAssertEqual(defaults.stringArray(forKey: WorkspaceOrder.collapsedDefaultsKey), ["pinned", "project:api"])

        order.setCollapsed(project: "api", pinned: false, false)
        order.setPinned(.embedded(F.runtimeID(1)), true)
        groups = built()
        XCTAssertEqual(groups.map(\.collapsed), [true, false, false])
    }

    func testForgettingSessionsThatEndedPrunesTheDraggedOrders() {
        let sessions = [F.tmux(1, project: "api"), F.tmux(2, project: "api", profile: "personal"), F.kitty(4, project: "web"),
                        F.tmux(3, project: "docs")]
        let kitty = AttentionCycler.Key(socket: "unix:/tmp/kitty-1", windowID: 4)
        var order = WorkspaceOrder()
        order.set([.embedded(F.runtimeID(2)), .embedded(F.runtimeID(1))], for: "api")
        order.set([kitty], for: "web")
        order.set([.embedded(F.runtimeID(3))], for: "docs")
        order.set([.embedded(F.runtimeID(9))], for: "gone")
        order.setPinned(.embedded(F.runtimeID(3)), true)
        func rows(_ sessions: [CompanionSession]) -> [WorkspaceRow] {
            var state = WorkspaceState()
            state.apply(WorkspaceSidebar.groups(sessions, snapshotSocket: "unix:/tmp/kitty-1", now: now, glyphs: .fallback, order: order))
            return state.rows
        }

        var pruned = order
        pruned.forget(notAmong: rows(sessions))
        XCTAssertEqual(pruned.projects.keys.sorted(), ["api", "docs", "web"], "a project nobody lists is forgotten")
        XCTAssertEqual(pruned.projects["docs"], [WorkspaceOrder.token(.embedded(F.runtimeID(3)))], "a pinned row keeps its project")
        XCTAssertEqual(pruned.pinned, order.pinned)

        pruned.forget(notAmong: rows([sessions[1], sessions[2]]))
        XCTAssertEqual(pruned.projects, ["api": [WorkspaceOrder.token(.embedded(F.runtimeID(2)))], "web": [WorkspaceOrder.token(kitty)]],
                       "an ended session leaves its project's order, and a project with none left is forgotten")
        XCTAssertEqual(pruned.pinned, [])

        var unchanged = pruned
        unchanged.forget(notAmong: rows(sessions))
        XCTAssertEqual(unchanged, pruned, "what is listed is kept")
    }

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
                       "api, work, needs attention, closing", "a closing session is said to be closing, not its detail")
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

    func testNextAttentionCyclesAcrossBackendsInSidebarOrder() {
        var sessions = [
            F.tmux(1, project: "a", icon: "stop", attention: true, changedAtMS: ms(secondsAgo: 100)),
            F.kitty(4, project: "b", icon: "key", attention: true, changedAtMS: ms(secondsAgo: 300)),
            F.tmux(2, project: "c", icon: "question", attention: true, changedAtMS: ms(secondsAgo: 200)),
            F.tmux(3, project: "d", icon: "bolt"),
            F.kitty(4, project: "e", socket: "unix:/tmp/kitty-2", icon: "key", attention: true, changedAtMS: ms(secondsAgo: 50)),
        ]
        sessions[0].unseen = true
        var cycler = AttentionCycler()
        var visited: [String] = []
        for _ in 0..<5 {
            let session = cycler.advance(in: sessions, snapshotSocket: "unix:/tmp/kitty-1")
            visited.append(session?.project ?? "")
        }
        XCTAssertEqual(visited, ["a", "b", "c", "e", "a"], "however long each has waited")

        // The row each press focuses names where the session is.
        let rows = AttentionCycler.ordered(sessions, snapshotSocket: "unix:/tmp/kitty-1").map { MenuSessionRow.focusing($0) }
        XCTAssertEqual(rows.map(\.runtimeSessionID), [F.runtimeID(1), "", F.runtimeID(2), ""])
        XCTAssertEqual(rows.map(\.windowID), [0, 4, 0, 4])

        // The session focused last settles and leaves; the cycle goes on from
        // where it stood instead of starting over.
        var after = sessions
        cycler = AttentionCycler()
        _ = cycler.advance(in: after, snapshotSocket: "unix:/tmp/kitty-1")
        XCTAssertEqual(cycler.advance(in: after, snapshotSocket: "unix:/tmp/kitty-1")?.project, "b")
        after[1].attention = false
        XCTAssertEqual(cycler.advance(in: after, snapshotSocket: "unix:/tmp/kitty-1")?.project, "c")
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
        XCTAssertEqual(rows[0].muteUnavailable, ActionGate().unavailableReason(.muteToggle), "an embedded row follows the gate too")
        XCTAssertEqual(rows[2].muteUnavailable, ActionGate().unavailableReason(.muteToggle), "a Kitty row follows the gate")
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

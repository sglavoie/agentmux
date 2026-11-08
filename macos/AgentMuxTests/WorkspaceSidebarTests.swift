import XCTest
@testable import AgentMux

/// The workspace sidebar as data, and the wire that feeds it.
final class WorkspaceSidebarTests: XCTestCase {
    typealias F = WorkspaceFixture
    let now = Date(timeIntervalSince1970: 1_000_000)
    func ms(secondsAgo: Int) -> Int64 { Int64((now.timeIntervalSince1970 - Double(secondsAgo)) * 1000) }

    /// The groups of the pinned, the waiting, and the projects, without the
    /// state sections' headings, which `testStateSectionsListTheProjectsOfTheirSessionsByName` covers.
    func groups(_ sessions: [CompanionSession]) -> [WorkspaceGroup] {
        projects(WorkspaceSidebar.groups(sessions, now: now, glyphs: .fallback))
    }

    func projects(_ groups: [WorkspaceGroup]) -> [WorkspaceGroup] {
        groups.filter { !$0.isSectionHeading }
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
        {"v":2,
         "asl":{"waiting":[{"run_id":"run-2","runtime_session_id":"rs-fedcba9876543210fedcba9876543210","unanswered":1}]},
         "sessions":[
          {"project":"API","profile":"work","harness":"claude",
           "runtime":{"session_id":"rs-0123456789abcdef0123456789abcdef","backend":"tmux","instance":"/tmp/amx-tmux.sock","generation":"tmux-gen-1","pane":"%6"}},
          {"harness":"codex","profile":"work","project":"API","asl_run_id":"run-2","icon_state":"stop","attention":true,
           "runtime":{"session_id":"rs-fedcba9876543210fedcba9876543210","backend":"tmux","instance":"/tmp/amx-tmux.sock","generation":"tmux-gen-1","pane":"%7"}},
          {"project":"legacy"}]}
        """#
        let snapshot = try JSONDecoder().decode(CompanionSnapshot.self, from: Data(json.utf8))
        XCTAssertEqual(snapshot.v, 2)
        let (first, tmux, legacy) = (snapshot.sessions[0], snapshot.sessions[1], snapshot.sessions[2])
        XCTAssertEqual(first.runtime?.backend, "tmux")
        XCTAssertEqual(first.embeddedRuntimeSessionID, "rs-0123456789abcdef0123456789abcdef")
        XCTAssertEqual(tmux.runtime?.pane, "%7")
        XCTAssertEqual(tmux.embeddedRuntimeSessionID, "rs-fedcba9876543210fedcba9876543210")
        XCTAssertNil(legacy.runtime)
        XCTAssertEqual(snapshot.asl.waiting[0].runtimeSessionID, "rs-fedcba9876543210fedcba9876543210")

        XCTAssertEqual(AttentionCycler.key(first), .embedded("rs-0123456789abcdef0123456789abcdef"))
        XCTAssertEqual(AttentionCycler.key(tmux), .embedded("rs-fedcba9876543210fedcba9876543210"))
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
          {"window_id":4,"project":"api","profile":"unsupported","tab_color":"#551a1a"}]}
        """#
        let snapshot = try JSONDecoder().decode(CompanionSnapshot.self, from: Data(json.utf8))
        XCTAssertEqual(snapshot.sessions.map(\.tabColor), ["#1a3655", "", "blue", "#551a1a"])

        let rows = Dictionary(uniqueKeysWithValues: groups(snapshot.sessions).flatMap(\.rows).map { ($0.label, $0) })
        let color = try XCTUnwrap(rows["work"]?.tabColor)
        XCTAssertEqual([color.red, color.green, color.blue], [0x1a / 255.0, 0x36 / 255.0, 0x55 / 255.0])
        XCTAssertNil(rows["plain"]?.tabColor, "a session with no color is drawn on the list's own")
        XCTAssertNil(rows["odd"]?.tabColor, "anything but #rrggbb is no color")
        XCTAssertNil(rows["unsupported"]?.tabColor, "a session without a runtime ID has no tab color")
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
          {"window_id":4,"project":"api","profile":"unsupported","name":"stray"}]}
        """#
        let snapshot = try JSONDecoder().decode(CompanionSnapshot.self, from: Data(json.utf8))
        XCTAssertEqual(snapshot.sessions.map(\.name), ["billing fix", "", "stray"])

        let rows = Dictionary(uniqueKeysWithValues: groups(snapshot.sessions).flatMap(\.rows).map { ($0.label, $0) })
        XCTAssertEqual(rows["work"]?.name, "billing fix")
        XCTAssertEqual(rows["plain"]?.name, "", "a session nobody named has none")
        XCTAssertNil(rows["unsupported"], "a session without a runtime ID is not listed")
        XCTAssertEqual(rows["work"]?.label, "work", "the name does not replace the profile, or move the row")
    }

    func testASessionCarriesTheHarnessIconTheDaemonPublishes() throws {
        let json = #"""
        {"v":2,"sessions":[
          {"project":"api","profile":"work","harness_icon":"🦊",
           "runtime":{"session_id":"rs-00000000000000000000000000000001","backend":"tmux","pane":"%1"}},
          {"project":"api","profile":"plain",
           "runtime":{"session_id":"rs-00000000000000000000000000000002","backend":"tmux","pane":"%2"}},
          {"window_id":4,"project":"api","profile":"unsupported","harness_icon":"📟"}]}
        """#
        let snapshot = try JSONDecoder().decode(CompanionSnapshot.self, from: Data(json.utf8))
        XCTAssertEqual(snapshot.sessions.map(\.harnessIcon), ["🦊", "", "📟"])

        let rows = Dictionary(uniqueKeysWithValues: groups(snapshot.sessions).flatMap(\.rows).map { ($0.label, $0) })
        XCTAssertEqual(rows["work"]?.harnessIcon, "🦊")
        XCTAssertEqual(rows["plain"]?.harnessIcon, "", "a session whose icon was cleared has none")
        XCTAssertNil(rows["unsupported"], "a session without a runtime ID is not listed")
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

    func testAttentionFloatsBelowPinsAboveAllProjectsUntilAcknowledged() {
        var sessions = [
            F.tmux(1, project: "api", profile: "a", icon: "bolt"),
            F.tmux(2, project: "web", profile: "b", icon: "stop", attention: true),
            F.tmux(3, project: "web", profile: "c", icon: "bolt"),
            F.tmux(4, project: "Needs attention", icon: "question", attention: true),
        ]
        sessions[1].unseen = true
        sessions[0].pinned = true
        var order = WorkspaceOrder()
        order.set([.embedded(F.runtimeID(3)), .embedded(F.runtimeID(2))], for: "web")
        order.setCollapsed(project: "web", pinned: false, true)
        func built() -> [WorkspaceGroup] {
            WorkspaceSidebar.groups(sessions, order: order)
        }
        var state = WorkspaceState()
        state.apply(built())
        let key: AttentionCycler.Key = .embedded(F.runtimeID(4))
        XCTAssertEqual(state.groups.map(\.project), ["Pinned", "Needs attention", "Working", "web"])
        XCTAssertTrue(state.groups[1].needsAttention, "pinned sessions stay above Needs attention")
        XCTAssertEqual(state.groups[1].rows.map(\.key), [key, .embedded(F.runtimeID(2))])
        XCTAssertFalse(state.groups[1].collapsed, "waiting sessions escape collapsed projects")
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
        XCTAssertEqual(state.groups.map(\.project), ["Pinned", "Done", "web", "Working", "Needs attention", "web"],
                       "back in their sections; the project called Needs attention is still a project")
        XCTAssertEqual(state.groups[2].rows.map(\.key), [.embedded(F.runtimeID(2))])
        XCTAssertEqual(state.groups.last?.rows.map(\.key), [.embedded(F.runtimeID(3))])
        XCTAssertTrue(state.groups.last?.collapsed ?? false)
        XCTAssertTrue(state.groups[2].collapsed, "a collapsed project is collapsed in every section")
        XCTAssertEqual(state.selection, .embedded(F.runtimeID(2)))
        XCTAssertEqual(order, saved, "promotion never rewrites the saved order")

        sessions[0].iconState = "error"
        XCTAssertEqual(projects(built()).map(\.needsAttention), [false, false, false, false], "a waiting pin stays in an open Pinned group")
        XCTAssertTrue(built()[0].pinned && built()[0].rows[0].needsAttention)
        order.setCollapsed(project: WorkspaceSidebar.pinnedSection, pinned: true, true)
        XCTAssertEqual(projects(built()).map(\.needsAttention), [true, false, false, false], "a collapsed Pinned group cannot hide it")
        XCTAssertTrue(built()[0].rows[0].pinned, "a promoted pin remains pinned")
        sessions[0].iconState = "bolt"
        XCTAssertTrue(built()[0].pinned, "it returns to Pinned when resolved")
    }

    func testGroupsByProjectAndLabelsByProfile() {
        let sessions = [
            F.tmux(1, project: "web", profile: "work", harness: "codex", icon: "bolt"),
            F.tmux(4, project: "api", profile: "work", icon: "key", attention: true, changedAtMS: ms(secondsAgo: 240)),
            F.tmux(2, project: "api", profile: "personal", icon: "stop", attention: true, changedAtMS: ms(secondsAgo: 30)),
            F.tmux(3, project: "API tools", profile: "", harness: "copilot"),
        ]
        var reasoned = sessions
        reasoned[1].reason = "permission"
        reasoned[1].muted = true
        reasoned[0].branch = "main"
        reasoned[0].dirty = true
        let built = groups(reasoned)

        XCTAssertEqual(built.map(\.project), ["Needs attention", "api", "web", "API tools"], "by state, then by name")
        XCTAssertEqual(built.map(\.attention), [1, 1, 0, 0])
        XCTAssertEqual(built[0].rows.map(\.label), ["work"], "the permission request floats above the seen completion")
        XCTAssertEqual(built[3].rows.map(\.label), ["copilot"], "a session with no profile is labeled by its harness")

        let personal = built[1].rows[0]
        XCTAssertEqual(personal.target, .embedded(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(personal.key, .embedded(F.runtimeID(2)))
        XCTAssertEqual(personal.glyph, "✓")
        XCTAssertTrue(personal.attention)
        XCTAssertEqual(personal.detail, "Turn complete · claude · now")
        XCTAssertEqual(personal.toolTip, "tmux %2")

        let work = built[0].rows[0]
        XCTAssertEqual(work.target, .embedded(runtimeSessionID: F.runtimeID(4)))
        XCTAssertEqual(work.key, .embedded(F.runtimeID(4)))
        XCTAssertEqual(work.detail, "Needs permission · claude · 4m")
        XCTAssertTrue(work.muted)
        XCTAssertEqual(work.toolTip, "tmux %4")

        let web = built[2].rows[0]
        XCTAssertEqual(web.detail, "working · codex · main*")
        XCTAssertTrue(web.working)
        XCTAssertFalse(web.attention)
    }

    func testWaitingCycleUsesProjectFirstOrder() {
        let sessions = [
            F.tmux(1, project: "web", icon: "key", attention: true),
            F.tmux(2, project: "api", icon: "stop", attention: true),
            F.tmux(3, project: "api", icon: "warning", attention: true),
        ]
        var cycle = AttentionCycler()
        let keys = (0..<4).compactMap { _ in
            cycle.advance(in: sessions, grouping: .projectThenState).map(AttentionCycler.key)
        }
        XCTAssertEqual(keys, [3, 2, 1, 3].map { .embedded(F.runtimeID($0)) })
        XCTAssertEqual(WorkspaceInspector.waiting(sessions, grouping: .projectThenState).map(\.key), Array(keys.prefix(3)))
    }
}

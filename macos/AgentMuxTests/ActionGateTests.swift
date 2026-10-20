import AppKit
import XCTest
@testable import AgentMux

final class ActionGateTests: XCTestCase {
    private let full = AMXCapabilities(project: true, openDashboard: true, notificationsSnooze: true, muteToggleJSON: true)

    // MARK: Gate

    func testEveryFeatureIsAvailableOnACurrentAmx() {
        let gate = ActionGate(capabilities: full, binaryPath: "/bin/amx")
        XCTAssertNil(gate.unavailableReason(.project))
        XCTAssertNil(gate.unavailableReason(.openDashboard))
        XCTAssertNil(gate.unavailableReason(.muteToggle))
        XCTAssertNil(gate.unavailableReason(.sessions))
    }

    func testAnOlderAmxGreysOutWhatItLacks() {
        // amx 0.8.0: no project, no open-dashboard, mute-toggle without --json.
        let gate = ActionGate(capabilities: AMXCapabilities(notificationsSnooze: false), binaryPath: "/bin/amx")
        XCTAssertEqual(gate.unavailableReason(.project), "/bin/amx has no project command; run just install")
        XCTAssertEqual(gate.unavailableReason(.openDashboard), "/bin/amx has no open-dashboard command; run just install")
        XCTAssertEqual(gate.unavailableReason(.muteToggle), "/bin/amx has no mute-toggle --json command; run just install")
        XCTAssertNil(gate.unavailableReason(.sessions))
    }

    func testUnresolvedAmxGreysOutEverything() {
        XCTAssertEqual(ActionGate().unavailableReason(.sessions), AMXWording.resolving)
        let missing = ActionGate(capabilities: full, problem: "amx was not found")
        XCTAssertEqual(missing.unavailableReason(.project), "amx was not found")
        XCTAssertEqual(missing.unavailableReason(.sessions), "amx was not found")
    }

    /// Found but not yet asked (or not yet answering): what every amx has
    /// is offered, and the rest waits without being called missing.
    func testAnAmxStillBeingCheckedIsNotCalledMissingAnything() {
        let gate = ActionGate(binaryPath: "/bin/amx", resolved: true)
        XCTAssertNil(gate.unavailableReason(.sessions))
        XCTAssertEqual(gate.unavailableReason(.project), ActionGate.checking)
        XCTAssertEqual(gate.unavailableReason(.openDashboard), ActionGate.checking)
        XCTAssertEqual(gate.unavailableReason(.muteToggle), ActionGate.checking)
        let silent = ActionGate(problem: "/bin/amx did not say which commands it has; see the log", binaryPath: "/bin/amx", resolved: true)
        XCTAssertEqual(silent.unavailableReason(.project), "/bin/amx did not say which commands it has; see the log")
    }

    func testCapabilitiesFromHelpGateTheMenu() {
        let oldRoot = AMXHelp(subcommands: ["session", "mute-toggle", "notifications"], flags: [])
        let capabilities = AMXCapabilities(root: oldRoot, notifications: AMXHelp(subcommands: ["test"], flags: []),
                                           muteToggle: AMXHelp(subcommands: [], flags: ["--window-id"]))
        var input = MenuBuilder.ActionsInput()
        input.gate = ActionGate(capabilities: capabilities, binaryPath: "amx")
        input.feedLive = true
        input.dashboardPlace = .kitty
        let entries = MenuBuilder.actionEntries(input)
        XCTAssertEqual(entries, [
            .separator,
            .action(.openWorkspace, "Open Workspace"),
            .unavailable("Open Dashboard", reason: "amx has no open-dashboard command; run just install"),
            .unavailableAction(.nextAttention, "Next Waiting Session", reason: "No session is waiting"),
            .submenuRef(.projects, "Launch Project", reason: "amx has no project command; run just install"),
            .submenuRef(.recent, "Recent", reason: nil),
            .submenuRef(.notifications, "Notifications", reason: nil),
        ])
    }

    func testOpenDashboardInTheWorkspaceNeedsNoOpenDashboardCommand() {
        let old = AMXCapabilities(root: AMXHelp(subcommands: ["session"], flags: []), notifications: nil, muteToggle: nil)
        var input = MenuBuilder.ActionsInput()
        input.gate = ActionGate(capabilities: old, binaryPath: "amx")
        input.dashboardPlace = .workspace
        XCTAssertEqual(MenuBuilder.actionEntries(input)[2], .action(.openDashboard, "Open Dashboard"))
        input.dashboardPlace = .kitty
        XCTAssertEqual(MenuBuilder.actionEntries(input)[2], .unavailable("Open Dashboard", reason: "amx has no open-dashboard command; run just install"))
    }

    func testActionEntriesOnACurrentAmx() {
        var input = MenuBuilder.ActionsInput()
        input.gate = ActionGate(capabilities: full)
        XCTAssertEqual(MenuBuilder.actionEntries(input), [
            .separator,
            .action(.openWorkspace, "Open Workspace"),
            .action(.openDashboard, "Open Dashboard"),
            .unavailableAction(.nextAttention, "Next Waiting Session", reason: "The daemon is not connected"),
            .submenuRef(.projects, "Launch Project", reason: nil),
            .submenuRef(.recent, "Recent", reason: nil),
            .submenuRef(.notifications, "Notifications", reason: "The daemon is not connected"),
        ])
        input.feedLive = true
        input.snoozeSupported = false
        XCTAssertEqual(MenuBuilder.actionEntries(input).last, .submenuRef(.notifications, "Notifications", reason: nil),
                       "a daemon too old to snooze still opens the submenu, which offers Restart Daemon")
    }

    func testNextWaitingSessionNeedsALiveFeedAndAWaitingSession() {
        var input = MenuBuilder.ActionsInput()
        input.gate = ActionGate(capabilities: full)
        input.waiting = 2
        XCTAssertEqual(MenuBuilder.actionEntries(input)[3], .unavailableAction(.nextAttention, "Next Waiting Session", reason: "The daemon is not connected"))
        input.feedLive = true
        XCTAssertEqual(MenuBuilder.actionEntries(input)[3], .action(.nextAttention, "Next Waiting Session"))
        input.waiting = 0
        XCTAssertEqual(MenuBuilder.actionEntries(input)[3], .unavailableAction(.nextAttention, "Next Waiting Session", reason: "No session is waiting"))
    }

    // MARK: Project submenu

    func testProjectEntriesAreGroupedByProfile() {
        var state = FetchState<[ProjectEntry]>()
        XCTAssertEqual(MenuBuilder.projectEntries(state), [.info("Loading…")])
        _ = state.begin()
        state.finish(.failure(.command(AMXJSONError(code: "config_invalid", message: "bad config"), status: 1)), at: Date())
        XCTAssertEqual(MenuBuilder.projectEntries(state), [.info("Could not list projects: bad config")])

        let api = ProjectEntry(profile: "work", title: "API")
        let blog = ProjectEntry(profile: "home", title: "Blog")
        _ = state.begin()
        state.finish(.success([api, blog]), at: Date())
        XCTAssertEqual(MenuBuilder.projectEntries(state), [
            .header("work"), .action(.launchProject(api), "API"),
            .header("home"), .action(.launchProject(blog), "Blog"),
        ])
        _ = state.begin()
        state.finish(.success([]), at: Date())
        XCTAssertEqual(MenuBuilder.projectEntries(state), [.info("No configured projects")])
    }

    func testProjectsWithAMissingDirectoryAreGreyedOutWithTheReason() {
        var state = FetchState<[ProjectEntry]>()
        let api = ProjectEntry(profile: "work", title: "API", directory: "/src/api")
        let gone = ProjectEntry(profile: "work", title: "Gone", directory: "/src/gone")
        _ = state.begin()
        state.finish(.success([api, gone]), at: Date())
        XCTAssertEqual(MenuBuilder.projectEntries(state, missing: ["/src/gone"]), [
            .header("work"),
            .action(.launchProject(api), "API"),
            .unavailableProject(gone, reason: "Directory not found: /src/gone"),
        ])

        var input = MenuBuilder.ActionsInput()
        input.projects = state
        input.missingProjectDirectories = ["/src/gone"]
        XCTAssertEqual(MenuBuilder.entries(for: .projects, input).last, .unavailableProject(gone, reason: "Directory not found: /src/gone"),
                       "the submenu reads the survey from its input")
    }

    func testAMissingDirectoryKeepsTheInFolderAlternate() {
        let gone = ProjectEntry(profile: "work", title: "Gone", directory: "/src/gone")
        let items = MenuBuilder.items(
            for: [.unavailableProject(gone, reason: "Directory not found: /src/gone")],
            target: NSObject(), sessionAction: #selector(NSObject.description), actionSelector: #selector(NSObject.hash)
        )
        XCTAssertEqual(items.count, 2)
        XCTAssertFalse(items[0].isEnabled)
        XCTAssertEqual(items[0].toolTip, "Directory not found: /src/gone")
        XCTAssertEqual(items[0].keyEquivalentModifierMask, [])
        XCTAssertTrue(items[1].isAlternate)
        XCTAssertTrue(items[1].isEnabled, "another folder can still be chosen")
        XCTAssertEqual(items[1].keyEquivalentModifierMask, .option)
        XCTAssertEqual(items[1].title, "Gone in Folder…")
        XCTAssertEqual(items[1].action, #selector(NSObject.hash))
        XCTAssertEqual((items[1].representedObject as? MenuBuilder.ActionBox)?.action, .launchProjectInFolder(gone))
    }

    // MARK: Mute

    func testRowsCarryTheirMuteState() {
        var session = CompanionSession(windowID: 4)
        session.project = "api"
        session.muted = true
        session.kittySocket = "unix:/tmp/kitty-9"
        var input = MenuBuilder.Input(feed: .live, daemonStatus: nil, sessions: [session], snapshot: CompanionSnapshot())
        input.gate = ActionGate(capabilities: full)
        let row = MenuBuilder.row(session, showReason: false, input: input)
        XCTAssertTrue(row.muted)
        XCTAssertNil(row.muteUnavailable)
        input.gate = ActionGate(capabilities: AMXCapabilities())
        XCTAssertNotNil(MenuBuilder.row(session, showReason: false, input: input).muteUnavailable)
    }

    func testEachSessionItemHasAnOptionAlternate() {
        let muted = MenuSessionRow(title: "api", windowID: 4, kittySocket: "s", toolTip: "t", muted: true)
        let blocked = MenuSessionRow(title: "web", windowID: 5, kittySocket: "s", toolTip: "t", muteUnavailable: "too old")
        let target = NSObject()
        let items = MenuBuilder.items(
            for: [.session(muted), .submenu("Idle (1)", [blocked])], target: target,
            sessionAction: #selector(NSObject.description), actionSelector: #selector(NSObject.hash),
            muteAction: #selector(NSObject.copy)
        )
        XCTAssertEqual(items.count, 3)
        XCTAssertFalse(items[0].isAlternate)
        XCTAssertEqual(items[0].keyEquivalentModifierMask, [])
        XCTAssertTrue(items[1].isAlternate)
        XCTAssertEqual(items[1].keyEquivalentModifierMask, .option)
        XCTAssertEqual(items[1].title, "Unmute · api")
        XCTAssertEqual(items[1].action, #selector(NSObject.copy))
        XCTAssertEqual((items[1].representedObject as? MenuBuilder.RowBox)?.row, muted)
        XCTAssertTrue(items[1].isEnabled)
        let idle = items[2].submenu?.items ?? []
        XCTAssertEqual(idle.count, 2, "idle rows get their alternate too")
        XCTAssertEqual(idle[1].title, "Mute · web")
        XCTAssertFalse(idle[1].isEnabled)
        XCTAssertEqual(idle[1].toolTip, "too old")
    }

    func testSubmenuReferencesAndUnavailableItems() {
        let live = NSMenu()
        let items = MenuBuilder.items(
            for: [.submenuRef(.projects, "Launch Project", reason: nil), .submenuRef(.recent, "Recent", reason: "nope"),
                  .unavailable("Open Dashboard", reason: "old"), .action(.launchProject(ProjectEntry(profile: "w", title: "A")), "A")],
            target: NSObject(), sessionAction: #selector(NSObject.description), actionSelector: #selector(NSObject.hash),
            submenus: [.projects: live, .recent: NSMenu()]
        )
        XCTAssertTrue(items[0].submenu === live)
        XCTAssertNil(items[1].submenu, "a greyed-out submenu does not open")
        XCTAssertFalse(items[1].isEnabled)
        XCTAssertEqual(items[1].toolTip, "nope")
        XCTAssertFalse(items[2].isEnabled)
        XCTAssertEqual(items[2].toolTip, "old")
        XCTAssertEqual((items[3].representedObject as? MenuBuilder.ActionBox)?.action, .launchProject(ProjectEntry(profile: "w", title: "A")))
        XCTAssertEqual(items.count, 5, "a project is followed by its ⌥ alternate")
        XCTAssertTrue(items[4].isAlternate)
        XCTAssertEqual(items[4].keyEquivalentModifierMask, .option)
        XCTAssertEqual(items[4].title, "A in Folder…")
        XCTAssertEqual((items[4].representedObject as? MenuBuilder.ActionBox)?.action, .launchProjectInFolder(ProjectEntry(profile: "w", title: "A")))
    }

    func testMuteToggleRunsAgainstTheRowSocket() {
        var calls: [([String], [String: String])] = []
        var amx = AMXCommand(binary: URL(fileURLWithPath: "/opt/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        amx.inheritedEnvironment = ["KITTY_LISTEN_ON": "unix:/tmp/kitty-1"]
        amx.runner = { _, args, env, _ in
            calls.append((args, env))
            return .finished(ProcessOutput(status: 1, stdout: Data(#"{"schema_version":1,"data":{"window_id":4,"muted":true,"changed":true}}"#.utf8), stderr: Data()))
        }
        let result = amx.json(MuteToggleResult.self, MuteToggleResult.arguments(windowID: 4), kittyListenOn: "unix:/tmp/kitty-2")
        XCTAssertEqual(result, .partial(MuteToggleResult(windowID: 4, muted: true, changed: true), status: 1),
                       "a mute whose title write failed is partial success")
        XCTAssertEqual(calls.first?.0, ["mute-toggle", "--window-id", "4", "--json"])
        XCTAssertEqual(calls.first?.1["KITTY_LISTEN_ON"], "unix:/tmp/kitty-2")

        let failed = AMXEnvelope.decode(MuteToggleResult.self, stdout: Data(#"{"schema_version":1,"error":{"code":"mute_toggle_failed","message":"toggle mute: no such window"}}"#.utf8), status: 1)
        guard case let .failure(.command(error, _)) = failed else { return XCTFail("\(failed)") }
        XCTAssertEqual(error.code, "mute_toggle_failed")
    }

    // MARK: Config path

    func testConfigPathFollowsAmxConfig() {
        XCTAssertEqual(AMXConfigPath.resolve(LaunchEnvironment(home: "/Users/me", path: "/bin")), "~/.config/agent-mux/config.toml")
        XCTAssertEqual(AMXConfigPath.resolve(LaunchEnvironment(home: "/Users/me", path: "/bin", forwarded: ["AMX_CONFIG": "~/dot/amx.toml"])),
                       "~/dot/amx.toml")
        XCTAssertEqual(AMXConfigPath.resolve(LaunchEnvironment(home: "/Users/me", path: "/bin", forwarded: ["AMX_CONFIG": "/etc/amx.toml"])),
                       "/etc/amx.toml")
        XCTAssertEqual(AMXConfigPath.resolve(nil, home: "/Users/me"), "~/.config/agent-mux/config.toml")
    }
}

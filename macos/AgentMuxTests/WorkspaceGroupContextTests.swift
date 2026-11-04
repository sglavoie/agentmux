import AppKit
import XCTest
@testable import AgentMux

@MainActor
final class WorkspaceGroupContextTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private let projects = [
        ProjectEntry(profile: "work", title: "API", directory: "/api", groups: ["work", "shared", "shared"]),
        ProjectEntry(profile: "personal", title: "Garden", directory: "/garden", groups: ["personal", "shared"]),
        ProjectEntry(profile: "work", title: "Offline", directory: "/offline", groups: ["empty"]),
        ProjectEntry(profile: "work", title: "Literal All", groups: ["All"]),
    ]

    private func launches(_ entries: [MenuEntry]) -> [ProjectEntry] {
        entries.compactMap { if case let .action(.launchProject(project), _) = $0 { return project }; return nil }
    }

    func testLauncherScopesConfiguredProjectsAndKeepsAllProjectsAndFolderAlternates() throws {
        var input = MenuBuilder.ActionsInput()
        input.projects = FetchState(value: projects)
        input.projectGroup = "work"
        input.missingProjectDirectories = ["/garden"]
        let entries = MenuBuilder.entries(for: .projects, input)
        XCTAssertEqual(launches(entries), [projects[0]])
        XCTAssertTrue(entries.contains(.header("Group: work")))
        let items = MenuBuilder.items(for: entries, target: self, sessionAction: #selector(NSObject.description),
                                      actionSelector: #selector(NSObject.description))
        let all = try XCTUnwrap(items.first { $0.title == "All projects" }?.submenu)
        XCTAssertEqual(Set(all.items.filter { !$0.isAlternate && $0.representedObject != nil }.map(\.title)),
                       ["API", "Literal All", "Offline"])
        let missing = try XCTUnwrap(all.items.first { $0.title == "Garden" })
        XCTAssertFalse(missing.isEnabled)
        let alternate = try XCTUnwrap(all.items.first { $0.title == "Garden in Folder…" })
        XCTAssertTrue(alternate.isEnabled)
        XCTAssertTrue(alternate.isAlternate)
        XCTAssertTrue(alternate.target === self)
        XCTAssertEqual((alternate.representedObject as? MenuBuilder.ActionBox)?.action, .launchProjectInFolder(projects[1]))

        input.projectGroup = "shared"
        input.missingProjectDirectories = []
        XCTAssertEqual(Set(launches(MenuBuilder.entries(for: .projects, input))), Set(projects.prefix(2)),
                       "membership in multiple groups never duplicates a launch")
        input.projectGroup = "empty"
        XCTAssertEqual(launches(MenuBuilder.entries(for: .projects, input)), [projects[2]],
                       "launching depends on configuration, not running sessions")
        input.projectGroup = "All"
        XCTAssertEqual(launches(MenuBuilder.entries(for: .projects, input)), [projects[3]])
        input.projectGroup = nil
        XCTAssertEqual(Set(launches(MenuBuilder.entries(for: .projects, input))), Set(projects))
        XCTAssertFalse(MenuBuilder.entries(for: .projects, input).contains { if case .actionSubmenu = $0 { return true }; return false })
    }

    func testScopedLauncherKeepsLoadingAndRefreshErrorsVisible() {
        var input = MenuBuilder.ActionsInput()
        input.projectGroup = "work"
        XCTAssertTrue(MenuBuilder.entries(for: .projects, input).contains(.info("Loading…")))
        input.projects = FetchState(error: "unavailable")
        XCTAssertTrue(MenuBuilder.entries(for: .projects, input).contains(.info("Could not list projects: unavailable")))
        input.projects = FetchState(value: projects, error: "unavailable")
        let entries = MenuBuilder.entries(for: .projects, input)
        XCTAssertEqual(launches(entries), [projects[0]])
        XCTAssertTrue(entries.contains(.info("Last refresh failed: unavailable")))
        input.projectGroup = "removed"
        XCTAssertTrue(MenuBuilder.entries(for: .projects, input).contains(.info("No projects in removed")))
    }

    func testSavedGroupSurvivesStartupAndFailedReadsThenFallsBackWhenRemoved() throws {
        let suite = "amx.tests.groups.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let app = FakeWorkspaceApp()
        app.workspaceProjects = projects
        var workspace = WorkspaceWindowController(delegate: app)
        app.onGroupChanged = { WorkspaceRestoration.saveGroup($0, to: defaults) }
        workspace.selectGroup("work")
        XCTAssertEqual(WorkspaceRestoration.load(from: defaults).selectedGroup, "work")

        app.workspaceProjects = []
        app.workspaceProjectsLoaded = false
        workspace = WorkspaceWindowController(delegate: app)
        workspace.selectedGroup = WorkspaceRestoration.load(from: defaults).selectedGroup
        workspace.reloadSidebar()
        XCTAssertEqual(workspace.selectedGroup, "work", "an unread or failed catalog cannot invalidate restoration")
        XCTAssertEqual(workspace.groupFilter.pills.filter { $0.state == .on }.map(\.group), ["work"])
        app.workspaceProjects = projects
        app.workspaceProjectsLoaded = true
        workspace.reloadSidebar()
        XCTAssertEqual(workspace.selectedGroup, "work")

        app.workspaceProjects = [projects[1]]
        workspace.reloadSidebar()
        XCTAssertNil(workspace.selectedGroup)
        XCTAssertNil(WorkspaceRestoration.load(from: defaults).selectedGroup)
        workspace.selectGroup("shared")
        workspace.selectGroup("shared")
        XCTAssertNil(WorkspaceRestoration.load(from: defaults).selectedGroup, "clicking the selected group saves All")
        WorkspaceRestoration.saveGroup("All", to: defaults)
        XCTAssertEqual(WorkspaceRestoration.load(from: defaults).selectedGroup, "All", "a literal All group is not the unfiltered choice")
        defaults.set(42, forKey: WorkspaceRestoration.groupKey)
        XCTAssertNil(WorkspaceRestoration.load(from: defaults).selectedGroup)
    }

    func testWaitingCountsIncludeHiddenAndOverlappingGroupsAndClearOnDisconnect() throws {
        let app = FakeWorkspaceApp()
        app.workspaceProjects = projects
        let workspace = WorkspaceWindowController(delegate: app)
        app.store.onChange = { workspace.refresh() }
        workspace.openForTesting()
        defer { workspace.window.close(); app.store.onChange = nil }
        var sessions = [
            F.tmux(1, project: "API", profile: "work", icon: "key", attention: true),
            F.tmux(2, project: "Garden", profile: "personal", icon: "question", attention: true),
            F.tmux(3, project: "Unconfigured", icon: "key", attention: true),
            F.tmux(4, project: "API", profile: "another-harness", icon: "bolt"),
        ]
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.selectGroup("work")
        func pill(_ group: String?) throws -> WorkspaceGroupPill {
            try XCTUnwrap(workspace.groupFilter.pills.first { $0.group == group })
        }
        XCTAssertEqual(try pill(nil).waitingCount, 3, "All counts sessions once, including unconfigured projects")
        XCTAssertEqual(try pill("shared").waitingCount, 2)
        XCTAssertEqual(try pill("personal").title, "personal · 1")
        XCTAssertEqual(try pill("empty").waitingCount, 0)
        XCTAssertEqual(try pill("work").accessibilityLabel(), "Group: work, 1 waiting")
        let personal = try pill("personal")
        sessions[1].attention = false
        sessions[3].attention = true
        sessions[3].iconState = "question"
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertTrue(try pill("personal") === personal, "count updates retain the button and its focus")
        XCTAssertEqual(personal.waitingCount, 0)
        XCTAssertEqual(try pill("work").waitingCount, 2, "alternate launch profiles inherit project tags")
        app.store.apply(.disconnected(.notRunning, retryIn: 1))
        XCTAssertTrue(workspace.groupFilter.pills.allSatisfy { $0.waitingCount == nil })
        XCTAssertEqual(try pill("work").title, "work")
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(try pill("work").waitingCount, 2)
        XCTAssertEqual(workspace.selectedGroup, "work")
    }

    func testWorkspaceGroupChangesRefreshOpenLauncherAndRemovedGroupClearsScope() throws {
        let app = AppDelegate()
        app.preferences.capabilities = AMXCapabilities(project: true)
        app.command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        app.projects.state = FetchState(value: projects)
        let workspace = WorkspaceWindowController(delegate: app)
        app.workspace = workspace
        app.workspaceProjectMenu = NSMenu(title: "Launch Project")
        defer { app.workspaceProjectMenu = nil }
        workspace.selectGroup("work")
        XCTAssertEqual(app.actionsInput.projectGroup, "work", "the status menu uses the workspace scope too")
        XCTAssertTrue(app.workspaceProjectMenu!.items.contains { $0.title == "Group: work" })
        XCTAssertFalse(app.workspaceProjectMenu!.items.contains { $0.title == "Garden" })
        workspace.selectGroup("personal")
        XCTAssertTrue(app.workspaceProjectMenu!.items.contains { $0.title == "Garden" })
        app.projects.state = FetchState(value: [projects[0]])
        workspace.reloadSidebar()
        XCTAssertNil(app.actionsInput.projectGroup)
        XCTAssertFalse(app.workspaceProjectMenu!.items.contains { $0.title == "All projects" })
    }

    func testLaunchRevealsOutsideGroupForImmediateAndDelayedSnapshotsAndSavesAll() throws {
        let suite = "amx.tests.group-launch.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        for delayed in [false, true] {
            let app = FakeWorkspaceApp()
            app.workspaceProjects = projects
            let workspace = WorkspaceWindowController(delegate: app)
            app.onGroupChanged = { WorkspaceRestoration.saveGroup($0, to: defaults) }
            app.store.onChange = { workspace.refresh() }
            workspace.openForTesting()
            defer { workspace.window.close(); app.store.onChange = nil }
            let inside = F.tmux(1, project: "API", profile: "work")
            let outside = F.tmux(2, project: "Garden", profile: "personal")
            app.store.apply(snapshot: F.snapshot(delayed ? [inside] : [inside, outside]))
            workspace.selectGroup("work")
            workspace.open(WorkspaceViewRequest(kind: WorkspaceViewRequest.sessionKind,
                                               runtimeSessionID: F.runtimeID(1), awaited: true))
            XCTAssertEqual(workspace.selectedGroup, "work", "launching inside the group keeps its scope")
            workspace.open(WorkspaceViewRequest(kind: WorkspaceViewRequest.sessionKind,
                                               runtimeSessionID: F.runtimeID(2), awaited: true))
            if delayed {
                XCTAssertEqual(workspace.selectedGroup, "work", "scope changes only after the new session is known")
                app.store.apply(snapshot: F.snapshot([inside, outside]))
            }
            XCTAssertNil(workspace.selectedGroup)
            XCTAssertNil(WorkspaceRestoration.load(from: defaults).selectedGroup)
            XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
            XCTAssertTrue(workspace.sessionShortcutRows.contains { $0.key == .embedded(F.runtimeID(2)) })
            XCTAssertNotEqual(workspace.sidebar.selectedRow, -1)

            workspace.selectGroup("work")
            workspace.restore(.session(runtimeSessionID: F.runtimeID(2)))
            XCTAssertEqual(workspace.selectedGroup, "work", "restoring the prior pane must not erase the restored filter")
        }
    }

    func testCountChangesReflowButtonsWithinNarrowSidebar() {
        let filter = WorkspaceGroupFilter()
        filter.setFrameSize(NSSize(width: 160, height: 300))
        filter.show(groups: ["personal", "work"], selected: "work", waiting: ["personal": 1, "work": 1], totalWaiting: 2)
        filter.layoutSubtreeIfNeeded()
        let buttons = filter.pills
        filter.show(groups: ["personal", "work"], selected: "work", waiting: ["personal": 100, "work": 100], totalWaiting: 200)
        filter.layoutSubtreeIfNeeded()
        XCTAssertTrue(zip(buttons, filter.pills).allSatisfy { $0 === $1 })
        XCTAssertEqual(filter.pills.last?.state, .on)
        for pill in filter.pills {
            XCTAssertLessThanOrEqual(pill.frame.maxX, 160)
            XCTAssertLessThanOrEqual(pill.frame.maxY, filter.intrinsicContentSize.height)
        }
        for (previous, next) in zip(filter.pills, filter.pills.dropFirst()) {
            XCTAssertFalse(previous.frame.intersects(next.frame))
        }
    }
}

import AppKit
import XCTest
@testable import AgentMux

@MainActor
final class WorkspaceEmptyStateTests: XCTestCase {
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!

    override func setUp() async throws {
        app = FakeWorkspaceApp()
        app.store.apply(snapshot: WorkspaceFixture.snapshot([]))
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        workspace.frameAutosaveName = nil
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
    }

    override func tearDown() async throws {
        workspace.window.close()
        workspace = nil
        app = nil
    }

    private var buttons: [NSButton] {
        workspace.placeholder.secondaryActions.arrangedSubviews.compactMap { $0 as? NSButton }
    }

    private var headings: [String] {
        workspace.placeholder.secondaryActions.arrangedSubviews.compactMap { ($0 as? NSTextField)?.stringValue }
    }

    func testGroupsRenderInOrderAndButtonsRunTheirClosures() {
        var chosen: [String] = []
        app.workspaceQuickStarts = (
            [("API", { chosen.append("api") }), ("Web", { chosen.append("web") })],
            [("Fix retries · API · 2h", { chosen.append("recent") })])
        workspace.openForTesting()
        XCTAssertEqual(headings, ["Projects", "Recent conversations"])
        XCTAssertEqual(buttons.map(\.title), ["API", "Web", "Fix retries · API · 2h", "Conversation History", "Open Dashboard"])
        XCTAssertTrue(chosen.isEmpty, "appearing never launches an agent")
        for button in buttons.prefix(3) { button.performClick(nil) }
        XCTAssertEqual(chosen, ["api", "web", "recent"])
        XCTAssertTrue(buttons.allSatisfy { $0.keyEquivalent.isEmpty })
        buttons[3].performClick(nil)
        XCTAssertEqual(app.performedCommands, [.history])
        buttons[4].performClick(nil)
        XCTAssertEqual(app.madeViews, ["dashboard"])
    }

    func testBoundsAndEmptyHeadings() {
        app.workspaceQuickStarts = ((1...8).map { ("Project \($0)", {}) }, (1...6).map { ("Recent \($0)", {}) })
        workspace.openForTesting()
        XCTAssertEqual(buttons.map(\.title), (1...5).map { "Project \($0)" } + (1...3).map { "Recent \($0)" } + ["Conversation History", "Open Dashboard"])
        app.workspaceQuickStarts.projects = []
        workspace.refreshCommands()
        XCTAssertEqual(headings, ["Recent conversations"])
        app.workspaceQuickStarts.recent = []
        workspace.refreshCommands()
        XCTAssertTrue(headings.isEmpty)
        XCTAssertEqual(buttons.map(\.title), ["Conversation History", "Open Dashboard"])
        app.workspaceQuickStarts.projects = [("API", {})]
        workspace.refreshCommands()
        XCTAssertEqual(headings, ["Projects"])
    }

    func testRefreshOnAppearanceAndCatalogUpdatesRetainButtonsWithNewHandlers() {
        var selected = ""
        app.onQuickStartRefresh = { [unowned self] in
            app.workspaceQuickStarts.projects = [("API", { selected = "original" })]
            workspace.refreshCommands()
        }
        workspace.openForTesting()
        XCTAssertEqual(app.quickStartRefreshes, 1)
        let button = buttons[0]
        app.workspaceQuickStarts.projects = [("API", { selected = "updated" })]
        workspace.refreshCommands()
        workspace.refresh()
        XCTAssertTrue(button === buttons[0], "unchanged titles retain keyboard focus")
        button.performClick(nil)
        XCTAssertEqual(selected, "updated")
        XCTAssertEqual(app.quickStartRefreshes, 1, "rendering is not another appearance")
        workspace.window.close()
        workspace.openForTesting()
        XCTAssertEqual(app.quickStartRefreshes, 2)
    }

    func testReturnAndKeypadEnterStillOpenProjectMenu() {
        app.workspaceQuickStarts = ([("API", { XCTFail("Return must open the menu") })], [])
        workspace.openForTesting()
        for collapsed in [false, true] {
            workspace.setSidebarCollapsed(collapsed)
            for code: UInt16 in [36, 76] {
                workspace.window.makeFirstResponder(workspace.window)
                let event = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0,
                    windowNumber: workspace.window.windowNumber, context: nil, characters: "\r",
                    charactersIgnoringModifiers: "\r", isARepeat: false, keyCode: code)!
                workspace.window.sendEvent(event)
            }
        }
        XCTAssertEqual(app.launchMenuRequests, 4)
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "No live sessions")
        XCTAssertEqual(workspace.placeholder.button.title, "Launch project…")
    }

    func testSetupAndDaemonDownKeepTheirExistingActions() {
        app.workspaceQuickStarts = ([("API", {})], [("Fix retries", {})])
        app.workspaceOffersSetup = true
        workspace.openForTesting()
        XCTAssertTrue(headings.isEmpty)
        XCTAssertEqual(workspace.placeholder.button.title, "Set up first project…")
        XCTAssertEqual(buttons.map(\.title), ["Conversation History", "Open Dashboard"])
        XCTAssertEqual(app.quickStartRefreshes, 0)
        app.workspaceOffersSetup = false
        app.store.apply(.disconnected(.notRunning, retryIn: 1))
        XCTAssertTrue(headings.isEmpty)
        XCTAssertTrue(buttons.isEmpty)
        XCTAssertEqual(workspace.placeholder.button.title, "Start Daemon")
        XCTAssertEqual(app.quickStartRefreshes, 0)
        app.store.apply(snapshot: WorkspaceFixture.snapshot([]))
        XCTAssertEqual(headings, ["Projects", "Recent conversations"])
        XCTAssertEqual(app.quickStartRefreshes, 1)
    }

    func testAppChoicesFollowMenuOrderSkipUnavailableProjectsAndSortRecent() {
        let delegate = AppDelegate()
        delegate.command = AMXCommand(binary: URL(fileURLWithPath: "/unused/amx"), launchEnvironment: LaunchEnvironment(home: "/unused", path: "/bin"))
        delegate.preferences.capabilities = AMXCapabilities(project: true)
        let projects = (1...8).reversed().map { ProjectEntry(profile: "work", title: "Project \($0)", directory: "/project/\($0)") }
        delegate.projects.state = FetchState(value: projects)
        delegate.projects.missingDirectories = ["/project/1"]
        let now = Date()
        delegate.recent.state = FetchState(value: (1...5).reversed().map {
            RecentConversation(id: "\($0)", harness: "claude", project: "API", lastAt: now.addingTimeInterval(Double(-$0 * 3600)), title: "Task \($0)")
        })
        let menuTitles = MenuBuilder.entries(for: .projects, delegate.actionsInput).compactMap { entry -> String? in
            if case let .action(.launchProject, title) = entry { return title }
            return nil
        }
        XCTAssertEqual(delegate.workspaceQuickStarts.projects.map(\.title), Array(menuTitles.prefix(5)))
        XCTAssertEqual(delegate.workspaceQuickStarts.recent.map(\.title), ["Task 1 · API · 1h", "Task 2 · API · 2h", "Task 3 · API · 3h"])
        delegate.preferences.capabilities = .none
        XCTAssertTrue(delegate.workspaceQuickStarts.projects.isEmpty)
        delegate.command = nil
        XCTAssertTrue(delegate.workspaceQuickStarts.recent.isEmpty)
    }

    func testGroupedMenuIncludesAllProjectsWithoutDuplicateQuickStarts() {
        let delegate = AppDelegate()
        delegate.command = AMXCommand(binary: URL(fileURLWithPath: "/unused/amx"), launchEnvironment: LaunchEnvironment(home: "/unused", path: "/bin"))
        delegate.preferences.capabilities = AMXCapabilities(project: true)
        delegate.projects.state = FetchState(value: [
            ProjectEntry(profile: "work", title: "API", groups: ["work"]),
            ProjectEntry(profile: "personal", title: "Garden", groups: ["personal"]),
        ])
        let window = WorkspaceWindowController(delegate: delegate)
        delegate.workspace = window
        window.selectGroup("work")
        XCTAssertEqual(delegate.workspaceQuickStarts.projects.map(\.title), ["API", "Garden"])
        delegate.workspace = nil
    }

    func testRealCatalogCompletionsUpdateTheVisibleGroupsIndependently() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let delegate = AppDelegate()
        delegate.preferences.capabilities = AMXCapabilities(project: true)
        delegate.store.apply(snapshot: WorkspaceFixture.snapshot([]))
        let projectStarted = expectation(description: "project read started")
        let recentStarted = expectation(description: "recent read started")
        let releaseProjects = DispatchSemaphore(value: 0)
        let releaseRecent = DispatchSemaphore(value: 0)
        defer { releaseProjects.signal(); releaseRecent.signal() }
        var command = AMXCommand(binary: directory.appendingPathComponent("amx"),
                                 launchEnvironment: LaunchEnvironment(home: directory.path, path: "/bin"))
        command.runner = { _, args, _, _ in
            let data: Any
            switch Array(args.prefix(2)) {
            case ["project", "list"]:
                projectStarted.fulfill()
                XCTAssertEqual(releaseProjects.wait(timeout: .now() + 10), .success)
                data = [["profile": "work", "title": "API", "directory": directory.path]]
            case ["session", "list"]:
                recentStarted.fulfill()
                XCTAssertEqual(releaseRecent.wait(timeout: .now() + 10), .success)
                data = ["sessions": [["id": "c1", "harness": "claude", "project": "API", "title": "Fix retries"]]]
            default:
                XCTFail("unexpected command: \(args)")
                return .timedOut
            }
            return .finished(ProcessOutput(status: 0,
                stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": data]), stderr: Data()))
        }
        delegate.command = command
        let window = WorkspaceWindowController(delegate: delegate)
        window.frameAutosaveName = nil
        delegate.workspace = window
        defer { window.window.close(); delegate.workspace = nil }
        window.openForTesting()
        await fulfillment(of: [projectStarted, recentStarted], timeout: 5)
        func labels() -> [String] {
            window.placeholder.secondaryActions.arrangedSubviews.compactMap { ($0 as? NSTextField)?.stringValue }
        }
        XCTAssertTrue(labels().isEmpty)
        releaseRecent.signal()
        for _ in 0..<200 where delegate.recent.state.loading {
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertFalse(delegate.recent.state.loading)
        XCTAssertEqual(labels(), ["Recent conversations"])
        releaseProjects.signal()
        for _ in 0..<200 where delegate.projects.state.loading {
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertFalse(delegate.projects.state.loading)
        XCTAssertEqual(labels(), ["Projects", "Recent conversations"])
        let titles = window.placeholder.secondaryActions.arrangedSubviews.compactMap { ($0 as? NSButton)?.title }
        XCTAssertEqual(titles, ["API", "Fix retries · API", "Conversation History", "Open Dashboard"])
    }

    func testRecentTitleUsesFolderFallbackAndSharedAgeFormatting() {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        var conversation = RecentConversation(id: "id", harness: "codex", cwd: "/work/API", lastAt: now.addingTimeInterval(-120), snippet: "Fix retries\nDetails")
        XCTAssertEqual(MenuBuilder.quickStartTitle(conversation, now: now), "Fix retries … · API · 2m")
        XCTAssertTrue(MenuBuilder.recentTitle(conversation, live: false, now: now).contains("API · codex · 2m"))
        conversation.title = "Named conversation"
        XCTAssertEqual(MenuBuilder.quickStartTitle(conversation, now: now), "Named conversation · API · 2m")
        conversation.lastAt = nil
        XCTAssertEqual(MenuBuilder.quickStartTitle(conversation, now: now), "Named conversation · API")
    }
}

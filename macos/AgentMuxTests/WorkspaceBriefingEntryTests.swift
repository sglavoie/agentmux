import AppKit
import XCTest
@testable import AgentMux

@MainActor
final class WorkspaceBriefingEntryTests: XCTestCase {
    func testLegacyShortcutMigrationPreservesDisabledAndExplicitBriefingBindings() throws {
        let suite = "briefing-migration-" + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        for legacy in ["", "ctrl+cmd+u"] {
            defaults.set(["activity": legacy], forKey: AppPreferences.Key.workspaceShortcuts)
            defaults.set("retired frame", forKey: "NSWindow Frame WorkspaceActivity")
            defaults.set("keep frame", forKey: "NSWindow Frame WorkspaceBriefing")
            let preferences = AppPreferences.load(from: defaults)
            XCTAssertEqual(preferences.workspaceShortcuts, ["briefing": legacy])
            XCTAssertEqual(AppPreferences.load(from: defaults).workspaceShortcuts, preferences.workspaceShortcuts)
            XCTAssertNil(defaults.object(forKey: "NSWindow Frame WorkspaceActivity"))
            XCTAssertEqual(defaults.string(forKey: "NSWindow Frame WorkspaceBriefing"), "keep frame")
        }
        for destination in ["", "shift+cmd+b"] {
            defaults.set(["activity": "ctrl+cmd+u", "briefing": destination], forKey: AppPreferences.Key.workspaceShortcuts)
            let preferences = AppPreferences.load(from: defaults)
            XCTAssertEqual(preferences.workspaceShortcuts, ["briefing": destination])
        }
    }

    func testMigratedConflictsRemainVisibleButInactive() throws {
        let suite = "briefing-conflict-" + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        defaults.set(["activity": "shift+cmd+h"], forKey: AppPreferences.Key.workspaceShortcuts)
        let preferences = AppPreferences.load(from: defaults)
        let oldWindowsMenu = NSApp.windowsMenu
        defer { NSApp.windowsMenu = oldWindowsMenu }
        let menu = WorkspaceMenu.make(target: AppDelegate())
        let combo = WorkspaceShortcut.briefing.combo(in: preferences)
        XCTAssertNotNil(combo)
        XCTAssertNotNil(WorkspaceShortcut.briefing.problem(for: combo, preferences: preferences, menu: menu))
        WorkspaceShortcut.apply(preferences, to: menu)
        let view = try XCTUnwrap(menu.item(withTitle: "View")?.submenu)
        XCTAssertEqual(view.item(withTitle: "Briefing…")?.keyEquivalent, "")
        XCTAssertEqual(preferences.workspaceShortcuts["briefing"], "shift+cmd+h")
    }

    func testMenuPaletteAndLegacySearchNamesLeadToBriefing() throws {
        let app = AppDelegate()
        let oldWindowsMenu = NSApp.windowsMenu
        defer { NSApp.windowsMenu = oldWindowsMenu }
        let menu = WorkspaceMenu.make(target: app)
        let view = try XCTUnwrap(menu.item(withTitle: "View")?.submenu)
        XCTAssertEqual(Array(view.items.filter { !$0.isSeparatorItem && !$0.isHidden }.prefix(7)).map(\.title),
            ["Dashboard", "Briefing…", "Conversation History…", "Checkout Changes…", "Overview", "ASL Runs…", "Flight Log…"])
        XCTAssertTrue(view.items[4].isSeparatorItem)
        XCTAssertTrue(view.items[8].isSeparatorItem)
        let entries = app.workspaceCommands
        XCTAssertEqual(Array(WorkspaceSwitcher.matches(">", in: entries).prefix(7)).map(\.place),
            [.command(.dashboard), .command(.briefing), .command(.history), .command(.changes), .command(.overview), .command(.runs), .command(.flightLog)])
        for alias in ["briefing", "activity", "since last looked", "return summary"] {
            XCTAssertEqual(WorkspaceSwitcher.matches(alias, in: entries).first?.place, .command(.briefing))
            XCTAssertEqual(WorkspaceSwitcher.matches(">" + alias, in: entries).first?.place, .command(.briefing))
        }
        XCTAssertFalse(WorkspaceShortcut.allCases.contains { $0.rawValue == "activity" })
    }

    func testWelcomeUsesAbsenceBoundaryWithoutGenerationOrAcknowledgement() throws {
        let runner = ExperienceRunner()
        let left = try XCTUnwrap(WorkspaceActivityEntry.date("2026-11-01T12:00:00Z"))
        var calls: [[String]] = []
        let command = ExperienceFixture.command { args in
            calls.append(args)
            return ExperienceFixture.report([
                ExperienceFixture.event("before", runtime: "old", at: "2026-11-01T11:59:00Z"),
                ExperienceFixture.event("after", runtime: "new", at: "2026-11-01T12:04:00Z")])
        }
        let welcome = WorkspaceWelcomeModel(command: { command }, runner: runner,
            readStore: .init(load: { .init() }, save: { _ in XCTFail("Welcome must not acknowledge") }))
        welcome.isEnabled = true
        welcome.arrive(at: left); welcome.leave(at: left)
        welcome.arrive(at: left.addingTimeInterval(119)); runner.drain()
        XCTAssertTrue(calls.isEmpty)
        welcome.leave(at: left)
        welcome.arrive(at: left.addingTimeInterval(120)); runner.drain()
        XCTAssertEqual(calls.count, 1)
        XCTAssertTrue(welcome.detail.hasPrefix("1 unread update since you stepped away"))
        welcome.arrive(at: left.addingTimeInterval(121)); runner.drain()
        XCTAssertEqual(calls.count, 1)
        welcome.isEnabled = false
        XCTAssertFalse(welcome.visible)
        welcome.leave(at: left.addingTimeInterval(130)); welcome.arrive(at: left.addingTimeInterval(300)); runner.drain()
        XCTAssertEqual(calls.count, 1)
        welcome.isEnabled = true
        welcome.leave(at: left.addingTimeInterval(310)); welcome.arrive(at: left.addingTimeInterval(500))
        welcome.close(); runner.drain()
        XCTAssertFalse(welcome.visible, "Closing discards an in-flight welcome read")
        XCTAssertTrue(calls.allSatisfy { $0.starts(with: ["session", "activity"]) })
    }

    func testReturningKeepsTerminalFocusSelectionAndBounds() throws {
        let app = FakeWorkspaceApp()
        var time = Date()
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback, now: { time })
        workspace.openForTesting(); workspace.window.makeKeyAndOrderFront(nil)
        defer { workspace.window.close() }
        app.store.apply(snapshot: WorkspaceFixture.snapshot([WorkspaceFixture.tmux(1, project: "API")]))
        workspace.refresh(); workspace.select(.embedded(WorkspaceFixture.runtimeID(1)), focus: true)
        workspace.window.layoutIfNeeded()
        let responder = workspace.window.firstResponder
        let selection = workspace.state.selection
        let bounds = workspace.shownTerminalView?.bounds
        let windows = Set(NSApp.windows.filter(\.isVisible).map(ObjectIdentifier.init))
        workspace.welcome.isEnabled = true
        workspace.workspaceResignedActive(); time = time.addingTimeInterval(120); workspace.workspaceBecameActive()
        workspace.window.layoutIfNeeded()
        XCTAssertTrue(workspace.window.firstResponder === responder)
        XCTAssertEqual(workspace.state.selection, selection)
        XCTAssertEqual(workspace.shownTerminalView?.bounds, bounds)
        XCTAssertEqual(Set(NSApp.windows.filter(\.isVisible).map(ObjectIdentifier.init)), windows)
    }
}

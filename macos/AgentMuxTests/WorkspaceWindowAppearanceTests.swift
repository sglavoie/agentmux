import AppKit
import GhosttyKit
import XCTest
@testable import AgentMux

@MainActor
final class WorkspaceWindowAppearanceTests: XCTestCase {
    private var app: AppDelegate!
    private var runtime: Ghostty.App!
    private var workspace: WorkspaceWindowController!
    private var suite: String!
    private var directory: URL!
    private var systemAppearance: NSAppearance?
    private var mainMenu: NSMenu?
    private var themedAppearance: NSAppearance?

    override func setUp() async throws {
        themedAppearance = WorkspaceWindowChrome.themedAppearance
        WorkspaceWindowChrome.setThemedAppearance(nil)
        systemAppearance = NSApp.appearance
        mainMenu = NSApp.mainMenu
        NSApp.appearance = NSAppearance(named: .aqua)
        suite = "WorkspaceWindowAppearanceTests-\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let model = PreferencesModel(defaults: defaults)
        model.preferences.terminalTheme = TerminalTheme("Catppuccin Mocha")
        app = AppDelegate(preferencesModel: model)
        directory = FileManager.default.temporaryDirectory.appendingPathComponent(suite)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let configFile = directory.appendingPathComponent("ghostty.conf")
        runtime = Ghostty.App {
            Ghostty.Config(userConfig: configFile, overrides: AppDelegate.terminalOverrides(model.preferences))
        }
        XCTAssertEqual(runtime.readiness, .ready)
        runtime.delegate = app
        app.terminals = runtime
        model.onChange = { [weak app] old, new in app?.preferencesChanged(from: old, to: new) }
        workspace = app.workspaceController()
        workspace.frameAutosaveName = nil
    }

    override func tearDown() async throws {
        workspace.constructedWindow?.close()
        runtime.shutdown()
        app.preferencesModel.defaults.removePersistentDomain(forName: suite)
        app.workspace = nil
        workspace = nil; runtime = nil; app = nil
        NSApp.appearance = systemAppearance
        NSApp.mainMenu = mainMenu
        WorkspaceWindowChrome.setThemedAppearance(themedAppearance)
        try FileManager.default.removeItem(at: directory)
    }

    private func assertChrome(_ rgb: [Int], appearance: NSAppearance.Name,
                              file: StaticString = #filePath, line: UInt = #line) throws {
        let window = workspace.window
        XCTAssertTrue(window.titlebarAppearsTransparent, file: file, line: line)
        XCTAssertEqual(window.appearance?.name, appearance, file: file, line: line)
        let color = try XCTUnwrap(window.backgroundColor.usingColorSpace(.sRGB), file: file, line: line)
        XCTAssertEqual([color.redComponent, color.greenComponent, color.blueComponent].map { Int(($0 * 255).rounded()) },
                       rgb, file: file, line: line)
        XCTAssertEqual(workspace.sidebarZoomView?.fillColor, window.backgroundColor, file: file, line: line)
        XCTAssertEqual(workspace.inspectorZoomView?.fillColor, window.backgroundColor, file: file, line: line)
        for host in [workspace.paneLayout.active, workspace.paneLayout.other] {
            XCTAssertEqual(host.layer?.backgroundColor, window.backgroundColor.cgColor, file: file, line: line)
        }
    }

    func testAdoptedWindowsFollowThemeChangesAndResetWithoutChangingBackgrounds() throws {
        let early = NSWindow(contentRect: .zero, styleMask: [.titled], backing: .buffered, defer: false)
        early.isReleasedWhenClosed = false
        early.backgroundColor = .red
        defer { early.close() }
        WorkspaceWindowChrome.adopt(early)
        XCTAssertNil(workspace.constructedWindow)
        XCTAssertEqual(early.appearance?.name, .darkAqua)

        try assertChrome([0x1E, 0x1E, 0x2E], appearance: .darkAqua)
        XCTAssertEqual(early.appearance?.name, .darkAqua)
        let late = NSPanel(contentRect: .zero, styleMask: [.titled], backing: .buffered, defer: false)
        late.isReleasedWhenClosed = false
        let panelBackground = late.backgroundColor
        defer { late.close() }
        WorkspaceWindowChrome.adopt(late)
        XCTAssertEqual(late.appearance?.name, .darkAqua)

        app.preferencesModel.preferences.terminalTheme = TerminalTheme("Catppuccin Latte")
        for window in [early, late] { XCTAssertEqual(window.appearance?.name, .aqua) }
        app.preferencesModel.preferences.matchWindowToTerminal = false
        XCTAssertNil(WorkspaceWindowChrome.themedAppearance)
        for window in [early, late] { XCTAssertNil(window.appearance) }
        app.preferencesModel.preferences.terminalTheme = TerminalTheme("Catppuccin Mocha")
        for window in [early, late] { XCTAssertNil(window.appearance) }
        app.preferencesModel.preferences.matchWindowToTerminal = true
        for window in [early, late] { XCTAssertEqual(window.appearance?.name, .darkAqua) }
        XCTAssertEqual(early.backgroundColor, .red)
        XCTAssertEqual(late.backgroundColor, panelBackground)
        XCTAssertEqual(NSApp.appearance?.name, .aqua)
    }

    func testReviewAndSetupConstructorsAdoptThemeWhileSettingsFollowsSystem() throws {
        XCTAssertNil(workspace.constructedWindow)
        let review = AppearanceReviewWindow(title: "Appearance test", autosaveName: suite,
                                            size: NSSize(width: 800, height: 600))
        review.present()
        let reviewWindow = try XCTUnwrap(review.window)
        let setup = WorkspaceSetupPanel(model: WorkspaceSetupModel(command: { nil }))
        let settings = PreferencesWindowController(model: app.preferencesModel)
        defer {
            reviewWindow.close()
            setup.panel.close()
            settings.window.close()
            NSWindow.removeFrame(usingName: suite)
        }
        for window in [reviewWindow, setup.panel] {
            XCTAssertEqual(window.appearance?.name, .darkAqua)
            XCTAssertEqual(window.backgroundColor, .windowBackgroundColor)
        }
        XCTAssertNil(settings.window.appearance)
        XCTAssertEqual(settings.window.effectiveAppearance.bestMatch(from: [.aqua, .darkAqua]), .aqua)
        app.preferencesModel.preferences.terminalTheme = TerminalTheme("Catppuccin Latte")
        for window in [reviewWindow, setup.panel] { XCTAssertEqual(window.appearance?.name, .aqua) }
        app.preferencesModel.preferences.matchWindowToTerminal = false
        for window in [reviewWindow, setup.panel, settings.window] { XCTAssertNil(window.appearance) }
        app.preferencesModel.preferences.terminalTheme = TerminalTheme("Catppuccin Mocha")
        for window in [reviewWindow, setup.panel] { XCTAssertNil(window.appearance) }
        app.preferencesModel.preferences.matchWindowToTerminal = true
        for window in [reviewWindow, setup.panel] { XCTAssertEqual(window.appearance?.name, .darkAqua) }
        XCTAssertNil(workspace.constructedWindow, "adoption and theme changes must not build the workspace window")
    }

    func testInitialDarkThemeAndPreferenceChangeToLightKeepGeometryAndResponder() throws {
        try assertChrome([0x1E, 0x1E, 0x2E], appearance: .darkAqua)
        let otherWindow = NSWindow(contentRect: .zero, styleMask: [.titled], backing: .buffered, defer: false)
        defer { otherWindow.close() }
        otherWindow.isReleasedWhenClosed = false
        let size = workspace.pane.bounds.size
        let responder = workspace.window.firstResponder
        app.preferencesModel.preferences.terminalTheme = TerminalTheme("Catppuccin Latte")
        try assertChrome([0xEF, 0xF1, 0xF5], appearance: .aqua)
        XCTAssertEqual(workspace.pane.bounds.size, size)
        XCTAssertTrue(workspace.window.firstResponder === responder)
        XCTAssertNil(otherWindow.appearance)
        XCTAssertEqual(NSApp.appearance?.name, .aqua)
    }

    func testToggleRestoresStockChromeAndReenablesLatestTheme() throws {
        try assertChrome([0x1E, 0x1E, 0x2E], appearance: .darkAqua)
        app.preferencesModel.preferences.matchWindowToTerminal = false
        XCTAssertFalse(workspace.window.titlebarAppearsTransparent)
        XCTAssertNil(workspace.window.appearance)
        XCTAssertEqual(workspace.window.backgroundColor, .windowBackgroundColor)
        XCTAssertNil(workspace.sidebarZoomView?.fillColor)
        XCTAssertNil(workspace.inspectorZoomView?.fillColor)
        XCTAssertNil(workspace.paneLayout.active.layer?.backgroundColor)
        XCTAssertNil(workspace.paneLayout.other.layer?.backgroundColor)
        app.preferencesModel.preferences.terminalTheme = TerminalTheme("Catppuccin Latte")
        XCTAssertNil(workspace.window.appearance)
        app.preferencesModel.preferences.matchWindowToTerminal = true
        try assertChrome([0xEF, 0xF1, 0xF5], appearance: .aqua)
    }

    func testConfigReloadAndConditionalThemesFollowSystemRatherThanWindowAppearance() throws {
        _ = workspace.window
        // Intentionally invert the theme brightness: a window override must
        // never feed back into the system's choice of conditional theme.
        app.preferencesModel.preferences.terminalTheme = TerminalTheme("Catppuccin Mocha", dark: "Catppuccin Latte")
        try assertChrome([0x1E, 0x1E, 0x2E], appearance: .darkAqua)
        NSApp.appearance = NSAppearance(named: .darkAqua)
        try assertChrome([0xEF, 0xF1, 0xF5], appearance: .aqua)
        XCTAssertTrue(runtime.reloadConfig(), "reload must retain the applied dark conditional branch")
        try assertChrome([0xEF, 0xF1, 0xF5], appearance: .aqua)
        NSApp.appearance = NSAppearance(named: .aqua)
        try assertChrome([0x1E, 0x1E, 0x2E], appearance: .darkAqua)

        try "background = #abcdef\n".write(to: directory.appendingPathComponent("ghostty.conf"), atomically: true, encoding: .utf8)
        XCTAssertTrue(runtime.reloadConfig())
        try assertChrome([0xAB, 0xCD, 0xEF], appearance: .aqua)
    }

    func testSurfaceConfigurationAndOSCBackgroundDoNotChangeWindow() throws {
        try assertChrome([0x1E, 0x1E, 0x2E], appearance: .darkAqua)
        let config = Ghostty.Config(userConfig: nil, overrides: .init(settings: ["background = #ffffff"]))
        let surface = try XCTUnwrap(runtime.makeSurface(.init(
            program: URL(fileURLWithPath: "/bin/cat"), environment: ["HOME": directory.path], workingDirectory: directory)))
        defer { surface.close() }
        var target = ghostty_target_s()
        target.tag = GHOSTTY_TARGET_SURFACE
        target.target.surface = surface.surface
        var action = ghostty_action_s()
        action.tag = GHOSTTY_ACTION_CONFIG_CHANGE
        action.action.config_change.config = config.config
        XCTAssertTrue(Ghostty.App.action(try XCTUnwrap(runtime.app), target: target, action: action))
        action.tag = GHOSTTY_ACTION_COLOR_CHANGE
        action.action.color_change.kind = GHOSTTY_ACTION_COLOR_KIND_BACKGROUND
        action.action.color_change.r = 255
        action.action.color_change.g = 255
        action.action.color_change.b = 255
        XCTAssertTrue(Ghostty.App.action(try XCTUnwrap(runtime.app), target: target, action: action))
        ghosttyWait("the program's background event") { surface.backgroundColor != nil }
        let background = try XCTUnwrap(surface.backgroundColor?.usingColorSpace(.sRGB))
        XCTAssertEqual(background.redComponent, 1)
        XCTAssertEqual(background.greenComponent, 1)
        XCTAssertEqual(background.blueComponent, 1)
        try assertChrome([0x1E, 0x1E, 0x2E], appearance: .darkAqua)
    }

    func testMissingConfigUsesStockAndPreferenceDoesNotCreateWindow() {
        workspace.terminalBackground = { nil }
        app.preferencesModel.preferences.matchWindowToTerminal = false
        app.preferencesModel.preferences.matchWindowToTerminal = true
        XCTAssertNil(workspace.constructedWindow)
        XCTAssertFalse(workspace.window.titlebarAppearsTransparent)
        XCTAssertNil(workspace.window.appearance)
        XCTAssertEqual(workspace.window.backgroundColor, .windowBackgroundColor)
    }
}

@MainActor
private final class AppearanceReviewWindow: ReviewWindowController {
    override func makeContentView() -> NSView { NSView() }
}

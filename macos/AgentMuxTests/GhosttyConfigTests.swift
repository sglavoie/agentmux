import GhosttyKit
import XCTest
@testable import AgentMux

/// The hosting app has already run `ghostty_init`, so configs load here.
final class GhosttyConfigTests: XCTestCase {
    private var dir: URL!

    override func setUpWithError() throws {
        dir = FileManager.default.temporaryDirectory.appendingPathComponent("amx-ghostty-config-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: dir)
    }

    private func writeUserConfig(_ text: String) throws -> URL {
        let url = dir.appendingPathComponent("ghostty.conf")
        try text.write(to: url, atomically: true, encoding: .utf8)
        return url
    }

    func testGhosttyInitializedInTheHostingApp() {
        XCTAssertTrue(Ghostty.initialized)
        XCTAssertTrue(Ghostty.versionDescription.hasPrefix("1.3.1"), Ghostty.versionDescription)
    }

    func testUserConfigLivesBesideAmxConfig() {
        let home = URL(fileURLWithPath: "/Users/someone")
        XCTAssertEqual(
            Ghostty.Config.userConfigURL(home: home).path, "/Users/someone/.config/agent-mux/ghostty.conf")
    }

    /// The app configuration names no program: what runs is each surface's own.
    func testOverridesForceOnlyWhatTheHostOwns() {
        XCTAssertEqual(Ghostty.ConfigOverrides().lines, ["confirm-close-surface = false"])

        var sized = Ghostty.ConfigOverrides()
        sized.fontSize = 15
        sized.settings = ["cursor-style = bar", "split\ncommand = /bin/sh"]
        XCTAssertEqual(sized.lines, ["confirm-close-surface = false", "font-size = 15", "cursor-style = bar"])
        sized.fontSize = 12.5
        XCTAssertTrue(sized.lines.contains("font-size = 12.5"))
        XCTAssertFalse(sized.lines.contains { $0.hasPrefix("command") })
    }

    func testDefaultsApplyWithoutAUserFile() {
        let config = Ghostty.Config(userConfig: dir.appendingPathComponent("missing.conf"))
        XCTAssertTrue(config.loaded)
        XCTAssertEqual(config.diagnostics, [])
        XCTAssertEqual(config.string("confirm-close-surface"), "false")
    }

    func testUserFileLoadsAfterDefaultsAndBeforeOverrides() throws {
        let user = try writeUserConfig("""
            confirm-close-surface = always
            font-size = 17
            """)
        let config = Ghostty.Config(userConfig: user)
        XCTAssertTrue(config.loaded)
        XCTAssertEqual(config.diagnostics, [])
        // The user file beats the defaults...
        XCTAssertEqual(config.get("font-size", as: Float.self, initial: 0), 17)
        // ...and the forced overrides beat the user file.
        XCTAssertEqual(config.string("confirm-close-surface"), "false")

        var sized = Ghostty.ConfigOverrides()
        sized.fontSize = 12.5
        XCTAssertEqual(
            Ghostty.Config(userConfig: user, overrides: sized).get("font-size", as: Float.self, initial: 0), 12.5)
    }

    func testUserConfigDiagnosticsAreReported() throws {
        let user = try writeUserConfig("not-a-real-ghostty-key = 1\n")
        let config = Ghostty.Config(userConfig: user)
        XCTAssertTrue(config.loaded)
        XCTAssertFalse(config.diagnostics.isEmpty)
    }

    /// A standard theme resolves from the resources Agent Mux.app bundles,
    /// with no Ghostty install or user theme behind it.
    func testBundledThemeSuppliesItsColors() throws {
        if ProcessInfo.processInfo.environment["GHOSTTY_RESOURCES_DIR"] != nil {
            throw XCTSkip("GHOSTTY_RESOURCES_DIR points libghostty away from the bundle")
        }
        let resources = try XCTUnwrap(Bundle.main.resourceURL)
        XCTAssertTrue(FileManager.default.fileExists(
            atPath: resources.appendingPathComponent("terminfo/78/xterm-ghostty").path))
        XCTAssertTrue(FileManager.default.fileExists(
            atPath: resources.appendingPathComponent("ghostty/themes/Catppuccin Mocha").path))
        XCTAssertTrue(FileManager.default.fileExists(
            atPath: resources.appendingPathComponent("ghostty/shell-integration").path))

        let user = try writeUserConfig("theme = Catppuccin Mocha\n")
        let config = Ghostty.Config(userConfig: user)
        XCTAssertEqual(config.diagnostics, [])
        let background = try XCTUnwrap(config.get("background", as: ghostty_config_color_s.self, initial: .init()))
        XCTAssertEqual([background.r, background.g, background.b], [0x1E, 0x1E, 0x2E])
        let foreground = try XCTUnwrap(config.get("foreground", as: ghostty_config_color_s.self, initial: .init()))
        XCTAssertEqual([foreground.r, foreground.g, foreground.b], [0xCD, 0xD6, 0xF4])
    }

    func testGhosttyLicenseNoticeShipsInTheBundle() throws {
        let url = try XCTUnwrap(Bundle.main.url(forResource: "NOTICE-ghostty", withExtension: nil))
        let notice = try String(contentsOf: url, encoding: .utf8)
        XCTAssertTrue(notice.contains("Mitchell Hashimoto"))
        XCTAssertTrue(notice.contains("macos/AgentMux/Ghostty/"))
    }

    func testOpeningTheConfigNeverRewritesAnExistingFile() throws {
        let url = dir.appendingPathComponent("nested/ghostty.conf")
        XCTAssertTrue(Ghostty.App.ensureConfigFile(at: url))
        let created = try String(contentsOf: url, encoding: .utf8)
        XCTAssertTrue(created.hasPrefix("# Ghostty settings for Agent Mux.app"))

        try "font-size = 19\n".write(to: url, atomically: true, encoding: .utf8)
        XCTAssertTrue(Ghostty.App.ensureConfigFile(at: url))
        XCTAssertEqual(try String(contentsOf: url, encoding: .utf8), "font-size = 19\n")
    }

    func testAppCreatesGhosttyAppObject() {
        let app = Ghostty.App { Ghostty.Config(userConfig: nil) }
        XCTAssertEqual(app.readiness, .ready)
        XCTAssertNotNil(app.app)
        XCTAssertEqual(app.surfaces.count, 0)
        app.shutdown()
        XCTAssertNil(app.app)
        XCTAssertEqual(app.readiness, .closed)
        XCTAssertNil(app.makeSurface())
    }

    func testTabSplitQuickTerminalAndNewWindowActionsAreIgnored() {
        let ignored = [
            GHOSTTY_ACTION_NEW_WINDOW,
            GHOSTTY_ACTION_NEW_TAB,
            GHOSTTY_ACTION_CLOSE_TAB,
            GHOSTTY_ACTION_GOTO_TAB,
            GHOSTTY_ACTION_MOVE_TAB,
            GHOSTTY_ACTION_NEW_SPLIT,
            GHOSTTY_ACTION_GOTO_SPLIT,
            GHOSTTY_ACTION_RESIZE_SPLIT,
            GHOSTTY_ACTION_EQUALIZE_SPLITS,
            GHOSTTY_ACTION_TOGGLE_SPLIT_ZOOM,
            GHOSTTY_ACTION_TOGGLE_QUICK_TERMINAL,
            GHOSTTY_ACTION_TOGGLE_WINDOW_DECORATIONS,
            GHOSTTY_ACTION_TOGGLE_FULLSCREEN,
            GHOSTTY_ACTION_INITIAL_SIZE,
        ]
        for tag in ignored {
            XCTAssertEqual(Ghostty.disposition(of: tag), .ignored, "action \(tag.rawValue)")
        }
        for tag in [GHOSTTY_ACTION_QUIT, GHOSTTY_ACTION_RELOAD_CONFIG, GHOSTTY_ACTION_OPEN_CONFIG] {
            XCTAssertEqual(Ghostty.disposition(of: tag), .app, "action \(tag.rawValue)")
        }
        for tag in [
            GHOSTTY_ACTION_SET_TITLE, GHOSTTY_ACTION_PWD, GHOSTTY_ACTION_SHOW_CHILD_EXITED,
            GHOSTTY_ACTION_DESKTOP_NOTIFICATION, GHOSTTY_ACTION_CLOSE_WINDOW,
            // A bell, a hovered link, secure input, and key sequences belong
            // to the terminal they happen in.
            GHOSTTY_ACTION_RING_BELL, GHOSTTY_ACTION_MOUSE_OVER_LINK, GHOSTTY_ACTION_SECURE_INPUT,
            GHOSTTY_ACTION_KEY_SEQUENCE, GHOSTTY_ACTION_KEY_TABLE,
        ] {
            XCTAssertEqual(Ghostty.disposition(of: tag), .surface, "action \(tag.rawValue)")
        }
    }
}

import AppKit
import GhosttyKit
import XCTest
@testable import AgentMux

/// A key as a keyboard sends it: the hardware key code, the characters with
/// and without modifiers, and the modifier flags.
struct TestKey {
    var keyCode: UInt16
    var characters: String
    var unmodified: String
    var flags: NSEvent.ModifierFlags = []

    func event(timestamp: TimeInterval = ProcessInfo.processInfo.systemUptime) -> NSEvent {
        NSEvent.keyEvent(
            with: .keyDown, location: .zero, modifierFlags: flags, timestamp: timestamp,
            windowNumber: 0, context: nil, characters: characters,
            charactersIgnoringModifiers: unmodified, isARepeat: false, keyCode: keyCode)!
    }

    static func function(_ key: Int, keyCode: UInt16, _ flags: NSEvent.ModifierFlags = []) -> TestKey {
        let text = String(Character(UnicodeScalar(key)!))
        return TestKey(keyCode: keyCode, characters: text, unmodified: text, flags: flags.union(.function))
    }

    static let pageUp = function(NSPageUpFunctionKey, keyCode: 116)
    static let pageDown = function(NSPageDownFunctionKey, keyCode: 121)
    static let home = function(NSHomeFunctionKey, keyCode: 115)
    static let end = function(NSEndFunctionKey, keyCode: 119)
    static let f12 = function(NSF12FunctionKey, keyCode: 111)
    static let left = function(NSLeftArrowFunctionKey, keyCode: 123)
    static let right = function(NSRightArrowFunctionKey, keyCode: 124)

    func with(_ extra: NSEvent.ModifierFlags) -> TestKey {
        var key = self
        key.flags.formUnion(extra)
        return key
    }

    /// ⌘ with a US-layout key; `shifted` is what Shift types on that key.
    static func command(_ plain: String, keyCode: UInt16, shifted: String? = nil) -> TestKey {
        if let shifted {
            return TestKey(keyCode: keyCode, characters: shifted, unmodified: shifted, flags: [.command, .shift])
        }
        return TestKey(keyCode: keyCode, characters: plain, unmodified: plain, flags: .command)
    }
}

/// Real surfaces whose program records every byte its terminal delivers, so
/// what a key, a composition, or a menu item sends is read back exactly.
final class GhosttyInteractionTests: XCTestCase {
    var ghostty: Ghostty.App!
    var window: GhosttyTestWindow!
    var view: Ghostty.SurfaceView!
    var dir: URL!
    var out: URL!

    override func setUpWithError() throws {
        ghostty = Ghostty.App { Ghostty.Config(userConfig: nil) }
        dir = FileManager.default.temporaryDirectory.appendingPathComponent("amx-ghostty-keys-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        window = GhosttyTestWindow.offscreen()
        try record(scrollback: .terminal)
    }

    /// Replaces the terminal under test with a fresh recorder whose history
    /// `scrollback` holds, which is fixed when a surface is made.
    func record(scrollback: Ghostty.Scrollback) throws {
        view?.close()
        out = dir.appendingPathComponent("bytes-\(UUID().uuidString)")
        var recorder = Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", #"stty raw -echo; : > "$OUT.ready"; exec cat > "$OUT""#],
            environment: ["OUT": out.path]
        )
        recorder.scrollback = scrollback
        view = try XCTUnwrap(ghostty.makeSurface(recorder))
        XCTAssertEqual(view.scrollback, scrollback)
        window.contentView = view
        XCTAssertTrue(window.makeFirstResponder(view))
        window.reportsKey = true
        XCTAssertTrue(view.focused)
        ghosttyWait("the recorder to start") { FileManager.default.fileExists(atPath: self.out.path + ".ready") }
    }

    @MainActor
    func testFindMenuNamesTerminalScopeAndOffersConversationSearch() throws {
        let item = NSMenuItem(title: "Find…", action: #selector(Ghostty.SurfaceView.find(_:)), keyEquivalent: "f")
        XCTAssertTrue(view.validateMenuItem(item))
        XCTAssertEqual(item.title, "Find in Terminal History…")
        try record(scrollback: .server)
        XCTAssertTrue(view.validateMenuItem(item))
        XCTAssertEqual(item.title, "Find in Terminal…")
        XCTAssertTrue(item.toolTip?.contains("visible screen only") == true)
        let delegate = AppDelegate()
        let menu = WorkspaceMenu.make(target: delegate)
        let edit = try XCTUnwrap(menu.items.first { $0.title == "Edit" }?.submenu)
        let search = try XCTUnwrap(edit.items.first { $0.action == #selector(AppDelegate.searchConversation(_:)) })
        XCTAssertEqual(search.title, "Search Conversation…")
        XCTAssertFalse(delegate.validateMenuItem(search), "conversation search requires the focused workspace's session")
        // The function keys about the shown session are greyed out with none
        // shown, rather than beeping when pressed.
        for action in [#selector(AppDelegate.pickSessionIcon(_:)), #selector(AppDelegate.pickSessionColor(_:)),
                       #selector(AppDelegate.clearSessionState(_:)), #selector(AppDelegate.markSessionActive(_:)),
                       #selector(AppDelegate.toggleSessionMute(_:))] {
            XCTAssertFalse(delegate.validateMenuItem(NSMenuItem(title: "", action: action, keyEquivalent: "")), "\(action)")
        }
    }

    override func tearDownWithError() throws {
        window.contentView = nil
        view = nil
        window.close()
        window = nil
        ghostty.shutdown()
        ghostty = nil
        try? FileManager.default.removeItem(at: dir)
    }

    var received: String {
        (try? Data(contentsOf: out)).map { String(decoding: $0, as: UTF8.self) } ?? ""
    }

    func expect(_ bytes: String, _ what: String, file: StaticString = #filePath, line: UInt = #line) {
        ghosttyWait("\(what): \(bytes.debugDescription), have \(received.debugDescription)", timeout: 5, file: file, line: line) {
            self.received == bytes
        }
    }

    /// Delivers a key the way AppKit does when no menu claims it: offered as
    /// a key equivalent first, then as a key press.
    func press(_ key: TestKey) {
        let event = key.event()
        if !view.performKeyEquivalent(with: event) {
            view.keyDown(with: event)
        }
    }

    @MainActor
    func testConfiguredWorkspaceShortcutsRunInsteadOfReachingTheTerminal() throws {
        let savedMenu = NSApp.mainMenu
        let savedWindowsMenu = NSApp.windowsMenu
        defer { NSApp.mainMenu = savedMenu; NSApp.windowsMenu = savedWindowsMenu }
        let menu = WorkspaceMenu.make(target: try XCTUnwrap(NSApp.delegate as? AppDelegate))
        NSApp.mainMenu = menu
        let receiver = WorkspaceShortcutReceiver()
        let item = try XCTUnwrap(menu.item(withTitle: "View")?.submenu?.items.first { $0.action == WorkspaceShortcut.splitShell.action })
        item.target = receiver
        var preferences = AppPreferences()
        let keys: [(String, TestKey)] = [
            ("shift+cmd+backslash", .command("\\", keyCode: 42, shifted: "|")),
            ("ctrl+return", TestKey(keyCode: 36, characters: "\r", unmodified: "\r", flags: .control)),
            ("opt+k", TestKey(keyCode: 40, characters: "˚", unmodified: "k", flags: .option)),
        ]
        for (binding, key) in keys {
            preferences.workspaceShortcuts[WorkspaceShortcut.splitShell.rawValue] = binding
            WorkspaceShortcut.apply(preferences, to: menu)
            press(key)
        }
        XCTAssertEqual(receiver.calls, keys.count)
        press(TestKey(keyCode: 0, characters: "a", unmodified: "a"))
        expect("a", "configured shortcuts send no bytes to the agent")
    }

    @MainActor
    func testNavigationArrowShortcutsAreClaimedBeforeTerminalInput() throws {
        let savedMenu = NSApp.mainMenu
        let savedWindowsMenu = NSApp.windowsMenu
        defer { NSApp.mainMenu = savedMenu; NSApp.windowsMenu = savedWindowsMenu }
        let menu = WorkspaceMenu.make(target: try XCTUnwrap(NSApp.delegate as? AppDelegate))
        NSApp.mainMenu = menu
        let receiver = NavigationShortcutReceiver()
        let keys: [TestKey] = [
            .right.with(.command), .left.with(.command),
            .right.with([.command, .shift]), .left.with([.command, .shift]),
        ]
        for key in keys {
            let item = try XCTUnwrap(Ghostty.MenuShortcut.item(in: menu, matching: key.event()))
            item.target = receiver
        }
        for scrollback in [Ghostty.Scrollback.terminal, .server] {
            try record(scrollback: scrollback)
            for key in keys {
                let event = key.event()
                if !view.performKeyEquivalent(with: event), !menu.performKeyEquivalent(with: event) {
                    view.keyDown(with: event)
                }
            }
            press(TestKey(keyCode: 0, characters: "a", unmodified: "a"))
            expect("a", "navigation sends no input to sessions or utility terminals")
        }
        XCTAssertEqual(receiver.steps, ["project next", "project previous", "session next", "session previous",
                                        "project next", "project previous", "session next", "session previous"])
    }

    @MainActor
    func testCommandReturnOpensATerminalBelowRatherThanReachingGhostty() throws {
        let savedMenu = NSApp.mainMenu
        let savedWindowsMenu = NSApp.windowsMenu
        defer { NSApp.mainMenu = savedMenu; NSApp.windowsMenu = savedWindowsMenu }
        let menu = WorkspaceMenu.make(target: try XCTUnwrap(NSApp.delegate as? AppDelegate))
        NSApp.mainMenu = menu
        let receiver = TerminalBelowReceiver()
        for (key, title) in [
            (TestKey.command("\r", keyCode: 36), "New Terminal Below"),
            (TestKey(keyCode: 36, characters: "\r", unmodified: "\r", flags: [.command, .shift]), "New Session Below…"),
        ] {
            let item = try XCTUnwrap(Ghostty.MenuShortcut.item(in: menu, matching: key.event()))
            XCTAssertEqual(item.title, title)
            item.target = receiver
            for scrollback in [Ghostty.Scrollback.terminal, .server] {
                try record(scrollback: scrollback)
                let event = key.event()
                if !view.performKeyEquivalent(with: event), !menu.performKeyEquivalent(with: event) {
                    view.keyDown(with: event)
                }
            }
        }
        XCTAssertEqual(receiver.calls, 4)
    }

    @MainActor
    func testZoomKeysResizeTheFocusedTerminalWithoutSendingInput() throws {
        let savedMenu = NSApp.mainMenu
        let savedWindowsMenu = NSApp.windowsMenu
        NSApp.mainMenu = WorkspaceMenu.make(target: try XCTUnwrap(NSApp.delegate as? AppDelegate))
        defer {
            NSApp.mainMenu = savedMenu
            NSApp.windowsMenu = savedWindowsMenu
        }
        let menu = try XCTUnwrap(NSApp.mainMenu)
        // An offscreen test window is not NSApp's key window, so direct the
        // menu's responder actions to the terminal it is exercising.
        let zoomActions = [#selector(Ghostty.SurfaceView.zoomIn(_:)),
                           #selector(Ghostty.SurfaceView.zoomOut(_:)),
                           #selector(Ghostty.SurfaceView.resetZoom(_:))]
        for item in menu.item(withTitle: "View")!.submenu!.items where item.action.map(zoomActions.contains) == true {
            item.target = view
        }
        func zoomKey(_ key: TestKey) {
            let event = key.event()
            if !view.performKeyEquivalent(with: event), !menu.performKeyEquivalent(with: event) {
                view.keyDown(with: event)
            }
        }
        let surface = try XCTUnwrap(view.surface)
        let initial = ghostty_surface_size(surface).cell_width_px
        for key in [TestKey.command("=", keyCode: 24), .command("=", keyCode: 24, shifted: "+")] {
            // Several point steps avoid pixel rounding hiding a size change.
            for _ in 0..<5 { zoomKey(key) }
            let bigger = ghostty_surface_size(surface).cell_width_px
            XCTAssertGreaterThan(bigger, initial, key.characters)
            for _ in 0..<3 { zoomKey(.command("-", keyCode: 27)) }
            XCTAssertLessThan(ghostty_surface_size(surface).cell_width_px, bigger)
            zoomKey(.command("-", keyCode: 27, shifted: "_"))
            XCTAssertEqual(ghostty_surface_size(surface).cell_width_px, initial)
        }
        press(TestKey(keyCode: 0, characters: "a", unmodified: "a"))
        expect("a", "zoom keys send nothing to the program")
    }

    @MainActor
    func testOptionSidebarZoomIsConsumedBeforeTerminalInput() throws {
        let zoomWindow = WorkspaceZoomWindow(contentRect: NSRect(x: -2000, y: -2000, width: 600, height: 400),
            styleMask: [.titled], backing: .buffered, defer: false)
        zoomWindow.isReleasedWhenClosed = false
        defer { zoomWindow.close() }
        zoomWindow.contentView = view
        zoomWindow.makeFirstResponder(view)
        var changes: [WorkspaceZoomWindow.Zoom] = []
        zoomWindow.onSidebarZoom = { changes.append($0) }
        let surface = try XCTUnwrap(view.surface)
        let originalSize = ghostty_surface_size(surface).cell_width_px
        for (code, text, flags) in [(UInt16(24), "≠", NSEvent.ModifierFlags.option),
                                   (24, "±", [.option, .shift]), (27, "–", .option), (29, "º", .option)] {
            zoomWindow.sendEvent(TestKey(keyCode: code, characters: text, unmodified: text, flags: flags).event())
        }
        XCTAssertEqual(changes, [.bigger, .bigger, .smaller, .reset])
        XCTAssertTrue(zoomWindow.firstResponder === view)
        XCTAssertEqual(ghostty_surface_size(surface).cell_width_px, originalSize)
        press(TestKey(keyCode: 0, characters: "a", unmodified: "a"))
        expect("a", "sidebar zoom sends no Option characters to the program")
    }

    /// ⌘0 is the tenth session's. With fewer sessions that item
    /// is disabled, and Ghostty must not reset the zoom behind it.
    @MainActor
    func testCommandZeroNeverResetsTheZoomBehindTheMenu() throws {
        let savedMenu = NSApp.mainMenu
        let savedWindowsMenu = NSApp.windowsMenu
        NSApp.mainMenu = WorkspaceMenu.make(target: try XCTUnwrap(NSApp.delegate as? AppDelegate))
        defer {
            NSApp.mainMenu = savedMenu
            NSApp.windowsMenu = savedWindowsMenu
        }
        let menu = try XCTUnwrap(NSApp.mainMenu)
        let surface = try XCTUnwrap(view.surface)
        let zeros = [TestKey.command("0", keyCode: 29)]
        for key in zeros {
            var flags = ghostty_binding_flags_e(0)
            let isBinding = key.characters.withCString { ptr in
                var event = key.event().ghosttyKeyEvent(GHOSTTY_ACTION_PRESS)
                event.text = ptr
                return ghostty_surface_key_is_binding(surface, event, &flags)
            }
            XCTAssertFalse(isBinding, "\(key.characters) is not Ghostty's")
        }
        view.performBindingAction("increase_font_size:3")
        let bigger = ghostty_surface_size(surface).cell_width_px
        for key in zeros {
            let event = key.event()
            let item = try XCTUnwrap(Ghostty.MenuShortcut.item(in: menu, matching: event), "the menu owns \(key.characters)")
            XCTAssertFalse(Ghostty.MenuShortcut.perform(for: event, in: menu), "\(item.title) is disabled with no sessions")
            if !view.performKeyEquivalent(with: event) { view.keyDown(with: event) }
        }
        XCTAssertEqual(ghostty_surface_size(surface).cell_width_px, bigger, "the zoom is where it was")
    }
}

@MainActor
private final class WorkspaceShortcutReceiver: NSObject {
    var calls = 0
    @objc func splitWorkspaceShell(_ sender: Any?) { calls += 1 }
}

@MainActor
private final class TerminalBelowReceiver: NSObject {
    var calls = 0
    @objc func newSessionTerminal(_ sender: Any?) { calls += 1 }
    @objc func newSessionBelow(_ sender: Any?) { calls += 1 }
}

private final class NavigationShortcutReceiver: NSObject {
    var steps: [String] = []
    @objc func selectNextProjectTab(_ sender: Any?) { steps.append("project next") }
    @objc func selectPreviousProjectTab(_ sender: Any?) { steps.append("project previous") }
    @objc func selectNextSession(_ sender: Any?) { steps.append("session next") }
    @objc func selectPreviousSession(_ sender: Any?) { steps.append("session previous") }
}

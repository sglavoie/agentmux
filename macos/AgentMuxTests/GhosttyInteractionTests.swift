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
    private var ghostty: Ghostty.App!
    private var window: GhosttyTestWindow!
    private var view: Ghostty.SurfaceView!
    private var dir: URL!
    private var out: URL!

    override func setUpWithError() throws {
        ghostty = Ghostty.App { Ghostty.Config(userConfig: nil) }
        dir = FileManager.default.temporaryDirectory.appendingPathComponent("amx-ghostty-keys-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        window = GhosttyTestWindow.offscreen()
        try record(scrollback: .terminal)
    }

    /// Replaces the terminal under test with a fresh recorder whose history
    /// `scrollback` holds, which is fixed when a surface is made.
    private func record(scrollback: Ghostty.Scrollback) throws {
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

    private var received: String {
        (try? Data(contentsOf: out)).map { String(decoding: $0, as: UTF8.self) } ?? ""
    }

    private func expect(_ bytes: String, _ what: String, file: StaticString = #filePath, line: UInt = #line) {
        ghosttyWait("\(what): \(bytes.debugDescription), have \(received.debugDescription)", timeout: 5, file: file, line: line) {
            self.received == bytes
        }
    }

    /// Delivers a key the way AppKit does when no menu claims it: offered as
    /// a key equivalent first, then as a key press.
    private func press(_ key: TestKey) {
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
        let key = TestKey.command("\r", keyCode: 36)
        let item = try XCTUnwrap(Ghostty.MenuShortcut.item(in: menu, matching: key.event()))
        XCTAssertEqual(item.title, "New Terminal Below")
        item.target = receiver
        for scrollback in [Ghostty.Scrollback.terminal, .server] {
            try record(scrollback: scrollback)
            let event = key.event()
            if !view.performKeyEquivalent(with: event), !menu.performKeyEquivalent(with: event) {
                view.keyDown(with: event)
            }
        }
        XCTAssertEqual(receiver.calls, 2)
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

    /// ⌘0 and ⌘⇧0 are the tenth session's. With fewer sessions that item
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
        let zeros = [TestKey.command("0", keyCode: 29), .command("0", keyCode: 29, shifted: ")")]
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

    // MARK: Keys that are the program's

    func testModifiedKeysReachTheProgramDistinctFromTheirPlainForms() {
        var sent = ""
        func check(_ key: TestKey, _ bytes: String, _ what: String, line: UInt = #line) {
            press(key)
            sent += bytes
            expect(sent, what, line: line)
        }
        check(TestKey(keyCode: 0, characters: "a", unmodified: "a"), "a", "a")
        check(TestKey(keyCode: 36, characters: "\r", unmodified: "\r"), "\r", "Return")
        check(TestKey(keyCode: 36, characters: "\r", unmodified: "\r", flags: .shift), "\u{1b}[27;2;13~", "Shift+Return")
        check(TestKey(keyCode: 36, characters: "\r", unmodified: "\r", flags: .control), "\u{1b}[27;5;13~", "Control+Return")
        check(TestKey(keyCode: 8, characters: "\u{3}", unmodified: "c", flags: .control), "\u{3}", "Control+C")
        check(TestKey(keyCode: 38, characters: "\n", unmodified: "j", flags: .control), "\n", "Control+J")
        check(TestKey(keyCode: 48, characters: "\u{19}", unmodified: "\u{19}", flags: .shift), "\u{1b}[Z", "Shift+Tab")
        check(TestKey(keyCode: 53, characters: "\u{1b}", unmodified: "\u{1b}"), "\u{1b}", "Escape")
        check(.left.with(.option), "\u{1b}b", "Option+Left")
        check(.pageUp, "\u{1b}[5~", "Page Up")
        check(.f12, "\u{1b}[24~", "F12")
    }

    // MARK: History

    func testASessionViewAsksTheServerForHistory() throws {
        try record(scrollback: .server)
        var sent = ""
        for (action, request) in [
            (#selector(NSResponder.scrollPageUp(_:)), Ghostty.HistoryRequest.pageUp),
            (#selector(NSResponder.scrollPageDown(_:)), .pageDown),
            (#selector(NSResponder.scrollToBeginningOfDocument(_:)), .top),
            (#selector(NSResponder.scrollToEndOfDocument(_:)), .bottom),
            (#selector(Ghostty.SurfaceView.find(_:)), .find),
            (#selector(Ghostty.SurfaceView.findNext(_:)), .findNext),
            (#selector(Ghostty.SurfaceView.findPrevious(_:)), .findPrevious),
            (#selector(Ghostty.SurfaceView.toggleMarkers(_:)), .markers),
        ] {
            let item = NSMenuItem(title: "", action: action, keyEquivalent: "")
            XCTAssertTrue(view.responds(to: action))
            XCTAssertTrue(view.validateMenuItem(item), "\(request) is offered on a session view")
            _ = view.perform(action, with: item)
            sent += "\u{1b}[\(request.serverKey)"
            expect(sent, "\(request)")
        }
        XCTAssertEqual(sent, "\u{1b}[5001~\u{1b}[5002~\u{1b}[5003~\u{1b}[5004~\u{1b}[5005~\u{1b}[5006~\u{1b}[5007~\u{1b}[5008~")
    }

    /// A function key the menu binds is not the surface's: it declines the
    /// key so AppKit offers it to the main menu, and the program hears
    /// nothing of it. F3 is the one that comes back to the terminal, as the
    /// server's marker key on a session view; in a utility view it is the
    /// program's own F3.
    @MainActor
    func testTheFunctionKeysGoToTheMenuNotTheProgram() throws {
        let savedMenu = NSApp.mainMenu
        let savedWindowsMenu = NSApp.windowsMenu
        NSApp.mainMenu = WorkspaceMenu.make(target: try XCTUnwrap(NSApp.delegate as? AppDelegate))
        defer {
            NSApp.mainMenu = savedMenu
            NSApp.windowsMenu = savedWindowsMenu
        }
        let menu = try XCTUnwrap(NSApp.mainMenu)
        let codes: [UInt16] = [122, 120, 99, 118, 96, 97, 98, 100, 101, 109, 103]
        for (index, code) in codes.enumerated() {
            let event = TestKey.function(NSF1FunctionKey + index, keyCode: code).event()
            XCTAssertFalse(view.performKeyEquivalent(with: event), "F\(index + 1) is left for the menu")
            XCTAssertNotNil(Ghostty.MenuShortcut.item(in: menu, matching: event), "the menu owns F\(index + 1)")
        }

        // An offscreen test window is not NSApp's key window, so direct the
        // marker item to the terminal it is exercising.
        let markers = try XCTUnwrap(Ghostty.MenuShortcut.item(in: menu, matching: TestKey.function(NSF3FunctionKey, keyCode: 99).event()))
        markers.target = view
        XCTAssertFalse(view.validateMenuItem(markers), "a utility view has no server to ask")
        press(TestKey.function(NSF3FunctionKey, keyCode: 99))
        expect("\u{1b}OR", "in a utility view F3 is the program's")

        try record(scrollback: .server)
        markers.target = view
        XCTAssertTrue(menu.performKeyEquivalent(with: TestKey.function(NSF3FunctionKey, keyCode: 99).event()))
        expect("\u{1b}[5008~", "on a session view F3 asks the server for its markers")
    }

    /// A right-click the program does not take shows Copy, Paste, and Select
    /// All on this terminal, validated like the Edit menu's.
    func testTheContextMenuEditsTheTerminal() throws {
        let event = try XCTUnwrap(NSEvent.mouseEvent(
            with: .rightMouseDown, location: NSPoint(x: 10, y: 10), modifierFlags: [], timestamp: ProcessInfo.processInfo.systemUptime,
            windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1))
        let menu = try XCTUnwrap(view.menu(for: event))
        XCTAssertEqual(menu.items.map(\.title), ["Copy", "Paste", "Select All"])
        XCTAssertTrue(menu.items.allSatisfy { $0.target === view })
        XCTAssertEqual(menu.items.map(\.action), [#selector(NSText.copy(_:)), #selector(NSText.paste(_:)), #selector(NSText.selectAll(_:))])
        XCTAssertFalse(view.validateMenuItem(menu.items[0]), "nothing selected, nothing to copy")
        XCTAssertTrue(view.validateMenuItem(menu.items[1]))
        XCTAssertTrue(view.validateMenuItem(menu.items[2]))
    }

    /// The Edit menu's Undo and Redo are the text fields'; in a terminal they
    /// are disabled, so ⌘Z and ⌘⇧Z are not taken from the program even when
    /// the window's undo manager has something to undo.
    @MainActor
    func testUndoAndRedoAreDisabledInATerminalSoTheProgramKeepsTheirKeys() throws {
        let savedMenu = NSApp.mainMenu
        let savedWindowsMenu = NSApp.windowsMenu
        NSApp.mainMenu = WorkspaceMenu.make(target: try XCTUnwrap(NSApp.delegate as? AppDelegate))
        defer {
            NSApp.mainMenu = savedMenu
            NSApp.windowsMenu = savedWindowsMenu
        }
        let menu = try XCTUnwrap(NSApp.mainMenu)
        window.undoManager?.registerUndo(withTarget: self) { _ in }
        XCTAssertEqual(window.undoManager?.canUndo, true)
        let undo = TestKey.command("z", keyCode: 6)
        let redo = TestKey.command("z", keyCode: 6, shifted: "Z")
        for (key, title) in [(undo, "Undo"), (redo, "Redo")] {
            let item = try XCTUnwrap(Ghostty.MenuShortcut.item(in: menu, matching: key.event()))
            XCTAssertEqual(item.title, title)
            XCTAssertNil(item.target, "\(title) goes to whatever has the keyboard")
            XCTAssertTrue(view.responds(to: try XCTUnwrap(item.action)), "\(title) stops at the terminal")
            XCTAssertFalse(view.validateMenuItem(item), "\(title) is disabled in a terminal")
            // An offscreen test window is not NSApp's key window, so direct
            // the item to the terminal it is exercising.
            item.target = view
            XCTAssertFalse(Ghostty.MenuShortcut.perform(for: key.event(), in: menu), "the menu leaves \(title)'s key to the terminal")
        }
        let cut = try XCTUnwrap(Ghostty.MenuShortcut.item(in: menu, matching: TestKey.command("x", keyCode: 7).event()))
        XCTAssertEqual(cut.title, "Cut")
        XCTAssertFalse(view.responds(to: try XCTUnwrap(cut.action)), "a terminal has nothing to cut")
        window.undoManager?.removeAllActions()
    }

    func testAUtilityViewScrollsItsOwnBufferAndSendsItsProgramNothing() {
        XCTAssertEqual(view.scrollback, .terminal)
        for request in [Ghostty.HistoryRequest.pageUp, .pageDown, .top, .bottom] {
            XCTAssertTrue(view.perform(request), "\(request) scrolls Ghostty's buffer")
        }
        for action in [#selector(Ghostty.SurfaceView.find(_:)), #selector(Ghostty.SurfaceView.findNext(_:)),
                       #selector(Ghostty.SurfaceView.findPrevious(_:))] {
            XCTAssertTrue(view.validateMenuItem(NSMenuItem(title: "", action: action, keyEquivalent: "")))
        }
        XCTAssertFalse(view.validateMenuItem(NSMenuItem(title: "", action: #selector(Ghostty.SurfaceView.toggleMarkers(_:)),
                                                        keyEquivalent: "")))
        XCTAssertTrue(view.perform(.find), "Ghostty's search opens the find bar")
        XCTAssertFalse(view.perform(.findNext), "nothing to go to before a needle is searched for")
        XCTAssertTrue(view.search(for: "needle"))
        XCTAssertTrue(view.perform(.findNext))
        XCTAssertTrue(view.performBindingAction("end_search"))
        XCTAssertFalse(view.perform(.markers))
        // A marker typed afterwards is the first thing the program hears.
        press(TestKey(keyCode: 0, characters: "a", unmodified: "a"))
        expect("a", "only the marker")
    }

    /// Ghostty's own paging and search keys are `performable` bindings, which
    /// act on a buffer a session view does not have. Read from the linked
    /// library: each must still be offered to the menu, whose items ask the
    /// server instead.
    func testTheMenuMayClaimGhosttysPagingAndSearchKeys() throws {
        let surface = try XCTUnwrap(view.surface)
        func flags(_ key: TestKey) -> ghostty_binding_flags_e? {
            let event = key.event()
            var keyEvent = event.ghosttyKeyEvent(GHOSTTY_ACTION_PRESS)
            var flags = ghostty_binding_flags_e(0)
            let bound = (event.characters ?? "").withCString { ptr -> Bool in
                keyEvent.text = ptr
                return ghostty_surface_key_is_binding(surface, keyEvent, &flags)
            }
            return bound ? flags : nil
        }
        let history: [(String, TestKey)] = [
            ("Shift+Page Up", .pageUp.with(.shift)), ("Shift+Page Down", .pageDown.with(.shift)),
            ("Command+Page Up", .pageUp.with(.command)), ("Command+Page Down", .pageDown.with(.command)),
            ("Command+Home", .home.with(.command)), ("Command+End", .end.with(.command)),
            ("Command+F", .command("f", keyCode: 3)), ("Command+G", .command("g", keyCode: 5)),
            ("Command+Shift+G", .command("g", keyCode: 5, shifted: "G")),
        ]
        var performable = 0
        for (name, key) in history {
            let found = try XCTUnwrap(flags(key), "\(name) is one of Ghostty's default bindings")
            XCTAssertTrue(Ghostty.menuMayClaim(found), "\(name) must be offered to the menu")
            if found.rawValue & GHOSTTY_BINDING_FLAGS_PERFORMABLE.rawValue != 0 { performable += 1 }
        }
        XCTAssertGreaterThan(performable, 0, "none of these is performable any more; the reason for the rule is gone")

        XCTAssertFalse(Ghostty.menuMayClaim(GHOSTTY_BINDING_FLAGS_ALL), "an unconsumed binding is not the menu's")
        XCTAssertFalse(Ghostty.menuMayClaim(ghostty_binding_flags_e(
            GHOSTTY_BINDING_FLAGS_CONSUMED.rawValue | GHOSTTY_BINDING_FLAGS_ALL.rawValue)))
        XCTAssertTrue(Ghostty.menuMayClaim(ghostty_binding_flags_e(
            GHOSTTY_BINDING_FLAGS_CONSUMED.rawValue | GHOSTTY_BINDING_FLAGS_PERFORMABLE.rawValue)))
        // A key Ghostty does not bind is not this rule's business.
        XCTAssertNil(flags(TestKey(keyCode: 0, characters: "a", unmodified: "a")))
    }

    func testHistoryRequestsNameTheServersUserKeys() {
        XCTAssertEqual(Ghostty.HistoryRequest.allCases.map(\.serverKey),
                       ["5001~", "5002~", "5003~", "5004~", "5005~", "5006~", "5007~", "5008~"])
        XCTAssertEqual(Ghostty.HistoryRequest.find.bindingAction(for: .server), "csi:5005~")
        XCTAssertEqual(Ghostty.HistoryRequest.markers.bindingAction(for: .server), "csi:5008~")
        XCTAssertNil(Ghostty.HistoryRequest.markers.bindingAction(for: .terminal),
                     "the markers are searched for in the server's history; a utility view has none")
        XCTAssertEqual(Ghostty.HistoryRequest.pageUp.bindingAction(for: .terminal), "scroll_page_up")
        // A utility view's search is Ghostty's, through the workspace's find
        // bar; next and previous navigate it, never search for a word.
        XCTAssertEqual(Ghostty.HistoryRequest.find.bindingAction(for: .terminal), "start_search")
        XCTAssertEqual(Ghostty.HistoryRequest.findNext.bindingAction(for: .terminal), "navigate_search:next")
        XCTAssertEqual(Ghostty.HistoryRequest.findPrevious.bindingAction(for: .terminal), "navigate_search:previous")
    }

    // MARK: Wheel

    /// A wheel turn over a program that tracks the mouse, as tmux does for
    /// every session view, is reported to it (SGR button 64 up, 65 down) at
    /// the pointer's cell rather than scrolling the terminal's own buffer.
    func testAWheelTurnIsReportedToAMouseTrackingProgram() throws {
        window.contentView = nil
        view.close()
        view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", #"printf '\033[?1000h\033[?1006h'; stty raw -echo; : > "$OUT.tracking"; exec cat > "$OUT""#],
            environment: ["OUT": out.path]
        )))
        window.contentView = view
        XCTAssertTrue(window.makeFirstResponder(view))
        let surface = try XCTUnwrap(view.surface)
        ghosttyWait("the program to take the mouse") { ghostty_surface_mouse_captured(surface) }

        let inside = NSPoint(x: 100, y: view.bounds.height - 60)
        view.mouseMoved(with: try XCTUnwrap(NSEvent.mouseEvent(
            with: .mouseMoved, location: inside, modifierFlags: [], timestamp: 0, windowNumber: window.windowNumber,
            context: nil, eventNumber: 0, clickCount: 0, pressure: 0)))
        func turn(_ lines: Int32) throws {
            let wheel = try XCTUnwrap(CGEvent(scrollWheelEvent2Source: nil, units: .line, wheelCount: 1, wheel1: lines, wheel2: 0, wheel3: 0))
            view.scrollWheel(with: try XCTUnwrap(NSEvent(cgEvent: wheel)))
        }
        try turn(3)
        ghosttyWait("a wheel-up report: have \(received.debugDescription)", timeout: 5) { self.received.hasPrefix("\u{1b}[<64;") }
        let afterUp = received
        try turn(-3)
        ghosttyWait("a wheel-down report: have \(received.debugDescription)", timeout: 5) {
            self.received.dropFirst(afterUp.count).contains("\u{1b}[<65;")
        }
        XCTAssertTrue(received.hasSuffix("M"), "each report is a press: \(received.debugDescription)")
    }

    // MARK: Input methods

    /// A composition is the input method's until it commits: the program
    /// hears nothing while text is marked, then exactly the committed text.
    func testACompositionReachesTheProgramOnlyWhenCommitted() throws {
        let none = NSRange(location: NSNotFound, length: 0)
        view.setMarkedText("k", selectedRange: NSRange(location: 1, length: 0), replacementRange: none)
        XCTAssertTrue(view.hasMarkedText())
        view.setMarkedText("か", selectedRange: NSRange(location: 1, length: 0), replacementRange: none)
        view.setMarkedText("かんじ", selectedRange: NSRange(location: 3, length: 0), replacementRange: none)
        XCTAssertEqual(view.markedRange(), NSRange(location: 0, length: 3))
        RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        XCTAssertEqual(received, "", "marked text is not input")

        // The candidate window is placed at the cursor, inside the view.
        let rect = view.firstRect(forCharacterRange: NSRange(location: 0, length: 3), actualRange: nil)
        let frame = window.convertToScreen(view.convert(view.bounds, to: nil))
        XCTAssertTrue(frame.insetBy(dx: -1, dy: -40).intersects(rect), "\(rect) is not at the terminal \(frame)")

        // A commit made inside a key press rides that key, so nothing is
        // written until the key is; one made outside (a candidate picked
        // with the pointer) is written at once.
        try withCurrentEvent {
            view.keyTextAccumulator = []
            view.insertText("漢字", replacementRange: none)
            XCTAssertFalse(view.hasMarkedText(), "a commit ends the composition")
            XCTAssertEqual(view.keyTextAccumulator, ["漢字"])
            view.keyTextAccumulator = nil
            RunLoop.current.run(until: Date().addingTimeInterval(0.1))
            XCTAssertEqual(received, "")

            view.setMarkedText("にほん", selectedRange: NSRange(location: 3, length: 0), replacementRange: none)
            view.insertText(NSAttributedString(string: "日本"), replacementRange: none)
            XCTAssertFalse(view.hasMarkedText())
        }
        expect("日本", "the committed text, once")
    }

    /// `insertText` is only honoured while AppKit is delivering an event, as
    /// an input method's commit always is.
    private func withCurrentEvent(_ body: () throws -> Void) throws {
        let marker = try XCTUnwrap(NSEvent.otherEvent(
            with: .applicationDefined, location: .zero, modifierFlags: [], timestamp: 0, windowNumber: 0,
            context: nil, subtype: 0x5a15, data1: 0, data2: 0))
        NSApp.postEvent(marker, atStart: true)
        let taken = NSApp.nextEvent(matching: .applicationDefined, until: Date().addingTimeInterval(2), inMode: .default, dequeue: true)
        XCTAssertNotNil(taken)
        XCTAssertNotNil(NSApp.currentEvent)
        try body()
    }

    func testACancelledCompositionSendsNothing() {
        let none = NSRange(location: NSNotFound, length: 0)
        view.setMarkedText("にほ", selectedRange: NSRange(location: 2, length: 0), replacementRange: none)
        XCTAssertTrue(view.hasMarkedText())
        view.unmarkText()
        XCTAssertFalse(view.hasMarkedText())
        XCTAssertEqual(view.markedRange(), NSRange())
        press(TestKey(keyCode: 0, characters: "a", unmodified: "a"))
        expect("a", "only what was typed after the composition was dropped")
    }
}

/// Who owns a shortcut: the workspace's main menu, or the program in the
/// focused terminal. Every menu shortcut holds ⌘, is Shift+Page Up/Down, or
/// is one of the function keys Kitty binds too (F1 to F11), so nothing else
/// an agent or the Dashboard binds is taken from it.
@MainActor
final class WorkspaceShortcutTests: XCTestCase {
    private var menu: NSMenu!

    override func setUp() async throws {
        let delegate = try XCTUnwrap(NSApp.delegate as? AppDelegate)
        let windows = NSApp.windowsMenu
        menu = WorkspaceMenu.make(target: delegate)
        NSApp.windowsMenu = windows
    }

    private func owner(_ key: TestKey) -> String? {
        Ghostty.MenuShortcut.item(in: menu, matching: key.event())?.title
    }

    /// With no workspace window in front — Settings or an answer panel has
    /// the keyboard, or the workspace is closed — the shortcuts that act on
    /// what it shows are disabled, as ⌘P, the digits, and the steps; what
    /// opens it is not.
    func testWorkspaceOnlyShortcutsNeedTheWorkspaceToBeKey() throws {
        let delegate = try XCTUnwrap(NSApp.delegate as? AppDelegate)
        XCTAssertFalse(delegate.workspaceIsKey, "an offscreen test window is never key")
        let workspaceOnly: [(TestKey, String)] = [
            (.command("p", keyCode: 35), "Go to…"),
            (.command("p", keyCode: 35, shifted: "P"), "Command Palette…"),
            (.command("1", keyCode: 18), "⌘1"),
            (.command("1", keyCode: 18, shifted: "!"), "⌘⇧1"),
            (.command("]", keyCode: 30), "⌘]"),
            (.command("[", keyCode: 33), "⌘["),
            (.left.with(.command), "Previous Tab in Project"),
            (.right.with(.command), "Next Tab in Project"),
            (.left.with([.command, .shift]), WorkspaceMenu.previousStopTitle),
            (.right.with([.command, .shift]), WorkspaceMenu.nextStopTitle),
            (.command("]", keyCode: 30, shifted: "}"), "⌘}"),
            (.command("[", keyCode: 33, shifted: "{"), "⌘{"),
            (.command("l", keyCode: 37), "Back to Last Shown"),
            (.command("b", keyCode: 11), "Toggle Sidebar"),
            (.command("b", keyCode: 11, shifted: "B"), "Toggle Inspector"),
            (.command("\r", keyCode: 36), "New Terminal Below"),
            (.command("r", keyCode: 15), "Name Session…"),
            (.command(".", keyCode: 47), "Pin Session"),
            (.command("w", keyCode: 13, shifted: "W"), "End Session…"),
            (.function(NSF7FunctionKey, keyCode: 98), "F7"),
            (.function(NSF9FunctionKey, keyCode: 101), "F9"),
            (.function(NSF5FunctionKey, keyCode: 96), "F5"),
            (.function(NSF10FunctionKey, keyCode: 109), "F10"),
        ]
        for (key, label) in workspaceOnly {
            let item = try XCTUnwrap(Ghostty.MenuShortcut.item(in: menu, matching: key.event()), label)
            XCTAssertTrue(AppDelegate.workspaceOnlyActions.contains(try XCTUnwrap(item.action)), label)
            XCTAssertFalse(delegate.validateMenuItem(item), "\(label) is disabled unless the workspace is key")
        }
        let opening: [(TestKey, String)] = [
            (.command("d", keyCode: 2), "Dashboard"),
            (.command("o", keyCode: 31), "Overview"),
            (.command("y", keyCode: 16), "Find or Resume a Conversation…"),
            (.command("t", keyCode: 17), "New Shell"),
            (.command("a", keyCode: 0, shifted: "A"), "Next Needing Attention"),
            (.command("l", keyCode: 37, shifted: "L"), "Session List"),
            (.command("i", keyCode: 34, shifted: "I"), "Waiting"),
            (.command("w", keyCode: 13), "Close"),
            (.function(NSF1FunctionKey, keyCode: 122), "F1"),
        ]
        for (key, label) in opening {
            let item = try XCTUnwrap(Ghostty.MenuShortcut.item(in: menu, matching: key.event()), label)
            XCTAssertTrue(delegate.validateMenuItem(item), "\(label) opens the workspace, or closes the key window")
        }
    }

    func testEveryMenuShortcutIsClaimedForItsKey() {
        let expected: [(TestKey, String)] = [
            (.command(",", keyCode: 43), "Settings…"),
            (.command("q", keyCode: 12), "Quit Agent Mux"),
            (.command("z", keyCode: 6), "Undo"),
            (.command("z", keyCode: 6, shifted: "Z"), "Redo"),
            (.command("x", keyCode: 7), "Cut"),
            (.command("c", keyCode: 8), "Copy"),
            (.command("v", keyCode: 9), "Paste"),
            (.command("a", keyCode: 0), "Select All"),
            (.command("f", keyCode: 3), "Find in Terminal…"),
            (.command("s", keyCode: 1, shifted: "S"), "Search Conversation…"),
            (.command("g", keyCode: 5), "Find Next"),
            (.command("g", keyCode: 5, shifted: "G"), "Find Previous"),
            (.command("=", keyCode: 24, shifted: "+"), "Bigger"),
            (.command("=", keyCode: 24), "Bigger"),
            (.command("-", keyCode: 27), "Smaller"),
            (.command("-", keyCode: 27, shifted: "_"), "Actual Size"),
            (TestKey(keyCode: 24, characters: "±", unmodified: "+", flags: [.option, .shift]), "Bigger Sidebars"),
            (TestKey(keyCode: 24, characters: "≠", unmodified: "=", flags: .option), "Bigger Sidebars"),
            (TestKey(keyCode: 27, characters: "–", unmodified: "-", flags: .option), "Smaller Sidebars"),
            (TestKey(keyCode: 29, characters: "º", unmodified: "0", flags: .option), "Actual Sidebar Size"),
            (.pageUp.with(.shift), "Page Up"),
            (.pageDown.with(.shift), "Page Down"),
            (.pageUp.with(.command), "Page Up"),
            (.pageDown.with(.command), "Page Down"),
            (.home.with(.command), "Scroll to Top"),
            (.end.with(.command), "Scroll to Bottom"),
            (.command("b", keyCode: 11), "Toggle Sidebar"),
            (.command("b", keyCode: 11, shifted: "B"), "Toggle Inspector"),
            (.command("d", keyCode: 2), "Dashboard"),
            (.command("o", keyCode: 31), "Overview"),
            (.command("y", keyCode: 16), "Find or Resume a Conversation…"),
            (.command("t", keyCode: 17), "New Shell"),
            (.command("w", keyCode: 13), "Close"),
            (.command("r", keyCode: 15, shifted: "R"), "ASL Runs…"),
            (.command("n", keyCode: 45, shifted: "N"), "Run a Plan…"),
            (.command("u", keyCode: 32, shifted: "U"), "Since Last Looked…"),
            (.command("h", keyCode: 4, shifted: "H"), "Conversation History…"),
            (.command("c", keyCode: 8, shifted: "C"), "Checkout Changes…"),
            (.command("p", keyCode: 35), "Go to…"),
            (.command("p", keyCode: 35, shifted: "P"), "Command Palette…"),
            (.command("a", keyCode: 0, shifted: "A"), "Next Needing Attention"),
            (.command("\r", keyCode: 36), "New Terminal Below"),
            (.command("r", keyCode: 15), "Name Session…"),
            (.command(".", keyCode: 47), "Pin Session"),
            (.command("]", keyCode: 30), WorkspaceMenu.nextStopTitle),
            (.command("[", keyCode: 33), WorkspaceMenu.previousStopTitle),
            (.left.with(.command), "Previous Tab in Project"),
            (.right.with(.command), "Next Tab in Project"),
            (.left.with([.command, .shift]), WorkspaceMenu.previousStopTitle),
            (.right.with([.command, .shift]), WorkspaceMenu.nextStopTitle),
            (.command("l", keyCode: 37), "Back to Last Shown"),
            (.function(NSF1FunctionKey, keyCode: 122), "Keybinding Reference"),
            (.function(NSF2FunctionKey, keyCode: 120), "Overview"),
            (.function(NSF3FunctionKey, keyCode: 99), "Toggle Error and Warning Markers"),
            (.function(NSF4FunctionKey, keyCode: 118), "Next Needing Attention"),
            (.function(NSF5FunctionKey, keyCode: 96), "Set Session Icon…"),
            (.function(NSF6FunctionKey, keyCode: 97), "Set Session Color…"),
            (.function(NSF7FunctionKey, keyCode: 98), "Previous Session Only"),
            (.function(NSF8FunctionKey, keyCode: 100), "Clear Session Markers"),
            (.function(NSF9FunctionKey, keyCode: 101), "Next Session Only"),
            (.function(NSF10FunctionKey, keyCode: 109), "Toggle Session Mute"),
            (.function(NSF11FunctionKey, keyCode: 103), "Mark Session Active"),
            (.command("]", keyCode: 30, shifted: "}"), WorkspaceMenu.nextSessionTitle),
            (.command("[", keyCode: 33, shifted: "{"), WorkspaceMenu.previousSessionTitle),
            (.command("1", keyCode: 18, shifted: "!"), "Session 1 in Kitty"),
            (.command("2", keyCode: 19, shifted: "@"), "Session 2 in Kitty"),
            (.command("3", keyCode: 20, shifted: "#"), "Session 3 in Kitty"),
            (.command("4", keyCode: 21, shifted: "$"), "Session 4 in Kitty"),
            (.command("5", keyCode: 23, shifted: "%"), "Session 5 in Kitty"),
            (.command("6", keyCode: 22, shifted: "^"), "Session 6 in Kitty"),
            (.command("7", keyCode: 26, shifted: "&"), "Session 7 in Kitty"),
            (.command("8", keyCode: 28, shifted: "*"), "Session 8 in Kitty"),
            (.command("9", keyCode: 25, shifted: "("), "Session 9 in Kitty"),
            (.command("0", keyCode: 29, shifted: ")"), "Session 10 in Kitty"),
            (.command("1", keyCode: 18), "Session 1"),
            (.command("2", keyCode: 19), "Session 2"),
            (.command("3", keyCode: 20), "Session 3"),
            (.command("4", keyCode: 21), "Session 4"),
            (.command("5", keyCode: 23), "Session 5"),
            (.command("6", keyCode: 22), "Session 6"),
            (.command("7", keyCode: 26), "Session 7"),
            (.command("8", keyCode: 28), "Session 8"),
            (.command("9", keyCode: 25), "Session 9"),
            (.command("0", keyCode: 29), "Session 10"),
            (.command("m", keyCode: 46), "Minimize"),
            (.command("w", keyCode: 13, shifted: "W"), "End Session…"),
            (.command("l", keyCode: 37, shifted: "L"), "Session List"),
            (.command("i", keyCode: 34, shifted: "I"), "Waiting"),
        ]
        for (key, title) in expected {
            XCTAssertEqual(owner(key), title, "\(key.flags.rawValue) \(key.unmodified)")
        }
        // Nothing in the menu is left out of the table above.
        var listed = 0
        func count(_ menu: NSMenu) {
            for item in menu.items {
                if let submenu = item.submenu { count(submenu) } else if !item.keyEquivalent.isEmpty { listed += 1 }
            }
        }
        count(menu)
        XCTAssertEqual(listed, expected.count + 1, "a menu shortcut is missing from this test (Hide is the one not listed)")
    }

    func testOnlyCommandSidebarZoomPagingAndKittysFunctionKeysAreTheMenus() {
        let functionKeys = Set(WorkspaceMenu.functionKeys.map(\.title))
        XCTAssertEqual(WorkspaceMenu.functionKeys.map(\.number).sorted(), Array(1...11), "F12 detaches and stays tmux's")
        func check(_ menu: NSMenu) {
            for item in menu.items {
                if let submenu = item.submenu { check(submenu); continue }
                guard !item.keyEquivalent.isEmpty else { continue }
                let mask = item.keyEquivalentModifierMask
                if mask.contains(.command) {
                    XCTAssertTrue(mask.isDisjoint(with: [.control, .option]), item.title)
                } else if mask.isEmpty {
                    XCTAssertTrue(functionKeys.contains(item.title), item.title)
                    XCTAssertEqual(item.menu?.title, "Function Keys", item.title)
                } else if mask == .option {
                    XCTAssertTrue(["Bigger Sidebars", "Smaller Sidebars", "Actual Sidebar Size"].contains(item.title), item.title)
                } else {
                    XCTAssertEqual(mask, .shift, item.title)
                    XCTAssertTrue(["Page Up", "Page Down"].contains(item.title), item.title)
                }
            }
        }
        check(menu)
    }

    func testKeysAProgramBindsAreNeverTheMenus() {
        let programs: [TestKey] = [
            TestKey(keyCode: 0, characters: "a", unmodified: "a"),
            TestKey(keyCode: 36, characters: "\r", unmodified: "\r"),
            TestKey(keyCode: 36, characters: "\r", unmodified: "\r", flags: .shift),
            TestKey(keyCode: 36, characters: "\r", unmodified: "\r", flags: .control),
            TestKey(keyCode: 36, characters: "\r", unmodified: "\r", flags: .option),
            TestKey(keyCode: 8, characters: "\u{3}", unmodified: "c", flags: .control),
            TestKey(keyCode: 31, characters: "\u{f}", unmodified: "o", flags: .control),
            TestKey(keyCode: 17, characters: "\u{14}", unmodified: "t", flags: .control),
            TestKey(keyCode: 3, characters: "\u{6}", unmodified: "f", flags: .control),
            TestKey(keyCode: 5, characters: "\u{7}", unmodified: "g", flags: .control),
            TestKey(keyCode: 2, characters: "\u{4}", unmodified: "d", flags: .control),
            TestKey(keyCode: 3, characters: "ƒ", unmodified: "f", flags: .option),
            TestKey(keyCode: 48, characters: "\t", unmodified: "\t"),
            TestKey(keyCode: 48, characters: "\u{19}", unmodified: "\u{19}", flags: .shift),
            TestKey(keyCode: 53, characters: "\u{1b}", unmodified: "\u{1b}"),
            .pageUp, .pageDown, .home, .end, .f12, .left, .left.with(.option), .left.with(.shift),
            .pageUp.with(.control), .home.with(.shift),
        ]
        for key in programs {
            XCTAssertNil(owner(key), "\(key.flags.rawValue) \(key.unmodified.debugDescription) is claimed by the menu")
        }
    }
}

/// Where a drag selection starts. Each case runs on a surface of its own: a
/// press with Shift, or on a surface that already has a selection, extends
/// what is there instead of starting where it lands.
final class GhosttySelectionTests: XCTestCase {
    private func mouse(_ type: NSEvent.EventType, _ p: NSPoint, in window: NSWindow) -> NSEvent {
        NSEvent.mouseEvent(with: type, location: p, modifierFlags: [], timestamp: ProcessInfo.processInfo.systemUptime,
                           windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1)!
    }

    private func selectedText(_ view: Ghostty.SurfaceView) -> String? {
        guard let surface = view.surface else { return nil }
        var text = ghostty_text_s()
        guard ghostty_surface_read_selection(surface, &text) else { return nil }
        defer { ghostty_surface_free_text(surface, &text) }
        return String(cString: text.text)
    }

    /// Drags along the first row of `LINE 2990 needle-2990` from `fraction`
    /// of the way across cell `column` to past the end of the line, and
    /// returns what ended up selected.
    private func dragSelection(fromColumn column: Int, at fraction: Double, moveFirst: Bool = true) throws -> String? {
        let ghostty = Ghostty.App { Ghostty.Config(userConfig: nil) }
        defer { ghostty.shutdown() }
        let window = GhosttyTestWindow.offscreen()
        defer { window.close() }
        let view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", "printf 'LINE 2990 needle-2990\\n'; exec cat"])))
        window.contentView = view
        defer { window.contentView = nil }
        XCTAssertTrue(window.makeFirstResponder(view))
        window.reportsKey = true
        ghosttyWait("the line to be drawn") {
            view.performBindingAction("select_all")
            return self.selectedText(view)?.contains("needle-2990") == true
        }
        // A plain click away from the text drops that selection and leaves
        // the pointer, as Ghostty knows it, far from where the drag starts.
        let away = NSPoint(x: view.bounds.width - 20, y: 20)
        view.mouseMoved(with: mouse(.mouseMoved, away, in: window))
        view.mouseDown(with: mouse(.leftMouseDown, away, in: window))
        view.mouseUp(with: mouse(.leftMouseUp, away, in: window))
        XCTAssertNil(selectedText(view), "a plain click clears the selection")

        // Ghostty's default padding is 2pt; the view's origin is bottom left.
        let cell = view.cellSize
        let y = view.bounds.height - 2 - cell.height / 2
        let start = NSPoint(x: 2 + (Double(column) + fraction) * cell.width, y: y)
        let end = NSPoint(x: 2 + 21.5 * cell.width, y: y)
        if moveFirst { view.mouseMoved(with: mouse(.mouseMoved, start, in: window)) }
        view.mouseDown(with: mouse(.leftMouseDown, start, in: window))
        for step in 1...40 {
            let x = start.x + (end.x - start.x) * Double(step) / 40
            view.mouseDragged(with: mouse(.leftMouseDragged, NSPoint(x: x, y: y), in: window))
        }
        view.mouseUp(with: mouse(.leftMouseUp, end, in: window))
        return selectedText(view)
    }

    func testASelectionStartsAtTheCellThePressIsIn() throws {
        XCTAssertEqual(try dragSelection(fromColumn: 0, at: 0.25), "LINE 2990 needle-2990")
        XCTAssertEqual(try dragSelection(fromColumn: 0, at: 0.5), "LINE 2990 needle-2990")
        XCTAssertEqual(try dragSelection(fromColumn: 5, at: 0.25), "2990 needle-2990")
        XCTAssertEqual(try dragSelection(fromColumn: 5, at: 0.5), "2990 needle-2990")
    }

    /// Ghostty's own rule, pinned so a new pin that moves it is noticed: a
    /// press well past the middle of a cell starts at the next one.
    func testAPressLateInACellStartsAtTheNextCell() throws {
        XCTAssertEqual(try dragSelection(fromColumn: 0, at: 0.75), "INE 2990 needle-2990")
        XCTAssertEqual(try dragSelection(fromColumn: 5, at: 0.75), "990 needle-2990")
    }

    /// The press carries its own position. Without that, a press no move
    /// preceded was anchored wherever the pointer was last reported, and the
    /// drag selected nothing.
    func testAPressWithNoMoveBeforeItStillAnchorsWhereItLands() throws {
        XCTAssertEqual(try dragSelection(fromColumn: 5, at: 0.5, moveFirst: false), "2990 needle-2990")
        XCTAssertEqual(try dragSelection(fromColumn: 0, at: 0.25, moveFirst: false), "LINE 2990 needle-2990")
    }
}

/// Which click opens a link. Nothing is opened on the machine running the
/// tests: the app's opener is replaced for each case.
final class GhosttyLinkTests: XCTestCase {
    private static let link = "http://127.0.0.1:47615/s15-link"
    private var savedOpener: ((URL) -> Void)!

    override func setUp() {
        savedOpener = Ghostty.App.openLink
    }

    override func tearDown() {
        Ghostty.App.openLink = savedOpener
    }

    private func mouse(_ type: NSEvent.EventType, _ p: NSPoint, _ flags: NSEvent.ModifierFlags, in window: NSWindow) -> NSEvent {
        NSEvent.mouseEvent(with: type, location: p, modifierFlags: flags, timestamp: ProcessInfo.processInfo.systemUptime,
                           windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1)!
    }

    /// Clicks the link with `flags` held, on a surface of its own whose
    /// program first turns on the given mouse-tracking modes, and returns
    /// what the app was asked to open and whether the program had the mouse.
    private func click(
        _ flags: NSEvent.ModifierFlags, mouseModes: [Int] = [],
        hovering: (Ghostty.SurfaceView) throws -> Void = { _ in }
    ) throws -> (opened: [String], captured: Bool, hovered: String?) {
        var opened: [String] = []
        Ghostty.App.openLink = { opened.append($0.absoluteString) }
        let ghostty = Ghostty.App { Ghostty.Config(userConfig: nil) }
        defer { ghostty.shutdown() }
        let window = GhosttyTestWindow.offscreen()
        defer { window.close() }
        let enable = mouseModes.isEmpty ? "" : "printf '" + mouseModes.map { "\\033[?\($0)h" }.joined() + "'; "
        let view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", enable + "printf 'link: \(Self.link)\\n'; exec cat"])))
        window.contentView = view
        defer { window.contentView = nil }
        XCTAssertTrue(window.makeFirstResponder(view))
        window.reportsKey = true
        let surface = try XCTUnwrap(view.surface)
        ghosttyWait("the link to be drawn") {
            view.performBindingAction("select_all")
            var text = ghostty_text_s()
            guard ghostty_surface_read_selection(surface, &text) else { return false }
            defer { ghostty_surface_free_text(surface, &text) }
            return String(cString: text.text).contains("s15-link")
        }
        if !mouseModes.isEmpty {
            ghosttyWait("the program to take the mouse") { ghostty_surface_mouse_captured(surface) }
        }
        let captured = ghostty_surface_mouse_captured(surface)
        // The link starts at column 6; this is well inside it, on row 0.
        let cell = view.cellSize
        let p = NSPoint(x: 2 + 18.5 * cell.width, y: view.bounds.height - 2 - cell.height / 2)
        view.mouseMoved(with: mouse(.mouseMoved, NSPoint(x: p.x - 3, y: p.y), flags, in: window))
        view.mouseMoved(with: mouse(.mouseMoved, p, flags, in: window))
        RunLoop.current.run(until: Date().addingTimeInterval(0.15))
        let hovered = view.hoveredLink
        try hovering(view)
        view.mouseDown(with: mouse(.leftMouseDown, p, flags, in: window))
        view.mouseUp(with: mouse(.leftMouseUp, p, flags, in: window))
        RunLoop.current.run(until: Date().addingTimeInterval(0.4))
        return (opened, captured, hovered)
    }

    func testCommandClickOpensALinkAndAPlainClickDoesNot() throws {
        let commanded = try click(.command)
        XCTAssertFalse(commanded.captured)
        XCTAssertEqual(commanded.opened, [Self.link])
        XCTAssertEqual(commanded.hovered, Self.link, "the link under the pointer is reported for the workspace to show")
        let plain = try click([])
        XCTAssertEqual(plain.opened, [])
        XCTAssertNil(plain.hovered)
    }

    /// While the pointer is on a link, the terminal's right-click menu
    /// opens it the way a click does, and copies it; off one it has neither.
    func testTheContextMenuOpensAndCopiesTheLinkUnderThePointer() throws {
        let savedCopier = Ghostty.SurfaceView.copyLink
        defer { Ghostty.SurfaceView.copyLink = savedCopier }
        var copied: [String] = []
        Ghostty.SurfaceView.copyLink = { copied.append($0) }
        var titles: [String] = []
        let hovered = try click(.command) { view in
            let menu = view.contextMenu()
            titles = menu.items.map(\.title)
            for title in [Ghostty.SurfaceView.openLinkTitle, Ghostty.SurfaceView.copyLinkTitle] {
                let item = try XCTUnwrap(menu.item(withTitle: title))
                XCTAssertTrue(item.target === view)
                _ = view.perform(try XCTUnwrap(item.action), with: item)
            }
        }
        XCTAssertEqual(titles, ["Copy", "Paste", "Select All", "", "Open Link", "Copy Link"])
        XCTAssertEqual(copied, [Self.link])
        // Opened once by the menu, once by the Command+click after it.
        XCTAssertEqual(hovered.opened, [Self.link, Self.link])

        _ = try click([]) { view in
            XCTAssertEqual(view.contextMenu().items.map(\.title), ["Copy", "Paste", "Select All"], "no link, no link items")
        }
    }

    /// A program that tracks the mouse is sent the click instead, Command
    /// held or not; Shift is what takes a click back from it. tmux tracks the
    /// mouse for every session view (1002 or 1003 with 1006), so there the
    /// link gesture is Command+Shift+click.
    func testUnderAMouseTrackingProgramShiftIsNeededToOpenALink() throws {
        for modes in [[1000, 1006], [1002, 1006], [1003, 1006]] {
            let plain = try click(.command, mouseModes: modes)
            XCTAssertTrue(plain.captured, "\(modes)")
            XCTAssertEqual(plain.opened, [], "Command+click under modes \(modes) goes to the program")
            XCTAssertEqual(try click([.command, .shift], mouseModes: modes).opened, [Self.link], "modes \(modes)")
        }
        XCTAssertEqual(try click(.shift, mouseModes: [1002, 1006]).opened, [], "Shift alone selects; it opens nothing")
    }

    /// One surface under mouse tracking: a first Command+Shift+click that
    /// opens the link, then `between`, then a second one. Returns how many
    /// links each click opened.
    private func clickTwice(
        between: (Ghostty.SurfaceView, NSWindow, (Double, Int) -> NSPoint) -> Void
    ) throws -> (first: Int, second: Int) {
        var opened = 0
        Ghostty.App.openLink = { _ in opened += 1 }
        let ghostty = Ghostty.App { Ghostty.Config(userConfig: nil) }
        defer { ghostty.shutdown() }
        let window = GhosttyTestWindow.offscreen()
        defer { window.close() }
        let view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", "printf '\\033[?1002h\\033[?1006h'; printf 'LINE 2990 needle-2990\\nlink: \(Self.link)\\n'; stty raw -echo; exec cat > /dev/null"])))
        window.contentView = view
        defer { window.contentView = nil }
        XCTAssertTrue(window.makeFirstResponder(view))
        window.reportsKey = true
        let surface = try XCTUnwrap(view.surface)
        ghosttyWait("the link to be drawn") {
            view.performBindingAction("select_all")
            var text = ghostty_text_s()
            guard ghostty_surface_read_selection(surface, &text) else { return false }
            defer { ghostty_surface_free_text(surface, &text) }
            return String(cString: text.text).contains("s15-link")
        }
        ghosttyWait("the program to take the mouse") { ghostty_surface_mouse_captured(surface) }
        let cell = view.cellSize
        let at: (Double, Int) -> NSPoint = { column, row in
            NSPoint(x: 2 + column * cell.width, y: view.bounds.height - 2 - (Double(row) + 0.5) * cell.height)
        }
        let link = at(18.5, 1)
        let held: NSEvent.ModifierFlags = [.command, .shift]
        func clickLink() -> Int {
            let before = opened
            view.mouseMoved(with: mouse(.mouseMoved, NSPoint(x: link.x - 3, y: link.y), held, in: window))
            view.mouseMoved(with: mouse(.mouseMoved, link, held, in: window))
            RunLoop.current.run(until: Date().addingTimeInterval(0.15))
            view.mouseDown(with: mouse(.leftMouseDown, link, held, in: window))
            view.mouseUp(with: mouse(.leftMouseUp, link, held, in: window))
            RunLoop.current.run(until: Date().addingTimeInterval(0.4))
            return opened - before
        }
        let first = clickLink()
        between(view, window, at)
        RunLoop.current.run(until: Date().addingTimeInterval(0.1))
        return (first, clickLink())
    }

    private func press(_ flags: NSEvent.ModifierFlags, keyCode: UInt16, on view: Ghostty.SurfaceView, in window: NSWindow) {
        view.flagsChanged(with: NSEvent.keyEvent(
            with: .flagsChanged, location: .zero, modifierFlags: flags, timestamp: ProcessInfo.processInfo.systemUptime,
            windowNumber: window.windowNumber, context: nil, characters: "", charactersIgnoringModifiers: "",
            isARepeat: false, keyCode: keyCode)!)
    }

    /// The ordinary way to click a link: the pointer wanders without any
    /// modifier, then Command and Shift are pressed, then the click. The
    /// view must pass the key presses on (`flagsChanged`), because under
    /// mouse tracking Ghostty takes a click back from the program only for
    /// modifiers it saw pressed as keys.
    func testALinkOpensAfterThePointerMovedWithoutModifiersOnceTheKeysArePressed() throws {
        let wandered = try clickTwice { view, window, at in
            view.mouseMoved(with: self.mouse(.mouseMoved, at(40, 1), [], in: window))
            self.press(.command, keyCode: 0x37, on: view, in: window)
            self.press([.command, .shift], keyCode: 0x38, on: view, in: window)
        }
        XCTAssertEqual(wandered.first, 1)
        XCTAssertEqual(wandered.second, 1)

        // Hover the link, press the keys, click without moving again.
        let hovered = try clickTwice { view, window, at in
            view.mouseMoved(with: self.mouse(.mouseMoved, at(18.5, 1), [], in: window))
            self.press(.command, keyCode: 0x37, on: view, in: window)
            self.press([.command, .shift], keyCode: 0x38, on: view, in: window)
        }
        XCTAssertEqual(hovered.second, 1)
    }

    /// Ghostty's rule, pinned because it is easy to mistake for a broken
    /// link: once the pointer has moved with no modifier held, modifiers
    /// that only ride on mouse events (as an automation tool sends them, with
    /// no key press) no longer open a link. Anything that drives the app with
    /// synthetic clicks has to press the keys too.
    func testModifiersOnMouseEventsAloneStopOpeningLinksAfterAPlainMove() throws {
        let untouched = try clickTwice { _, _, _ in }
        XCTAssertEqual(untouched.second, 1, "with no plain move in between, the second click still opens")
        let plainMove = try clickTwice { view, window, at in
            view.mouseMoved(with: self.mouse(.mouseMoved, at(40, 1), [], in: window))
        }
        XCTAssertEqual(plainMove.first, 1)
        XCTAssertEqual(plainMove.second, 0)
    }
}

/// What a Control-click and Look Up do, each on a surface of its own that
/// prints `hello world` and may first turn on mouse tracking.
final class GhosttyClickMenuTests: XCTestCase {
    private func surface(mouseModes: [Int] = [], _ body: (Ghostty.SurfaceView, NSWindow) throws -> Void) throws {
        let ghostty = Ghostty.App { Ghostty.Config(userConfig: nil) }
        defer { ghostty.shutdown() }
        let window = GhosttyTestWindow.offscreen()
        defer { window.close() }
        let enable = mouseModes.isEmpty ? "" : "printf '" + mouseModes.map { "\\033[?\($0)h" }.joined() + "'; "
        let view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", enable + "printf 'hello world\\n'; exec cat"])))
        window.contentView = view
        defer { window.contentView = nil }
        XCTAssertTrue(window.makeFirstResponder(view))
        let surface = try XCTUnwrap(view.surface)
        if !mouseModes.isEmpty {
            ghosttyWait("the program to take the mouse") { ghostty_surface_mouse_captured(surface) }
        }
        try body(view, window)
    }

    private func mouse(_ type: NSEvent.EventType, _ p: NSPoint, _ flags: NSEvent.ModifierFlags = [], in window: NSWindow) -> NSEvent {
        NSEvent.mouseEvent(with: type, location: p, modifierFlags: flags, timestamp: ProcessInfo.processInfo.systemUptime,
                           windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1)!
    }

    func testAControlClickShowsTheMenuUnlessTheProgramTracksTheMouse() throws {
        try surface { view, window in
            let p = NSPoint(x: 10, y: 10)
            XCTAssertNil(view.menu(for: mouse(.leftMouseDown, p, in: window)), "a plain click has no menu")
            XCTAssertEqual(view.menu(for: mouse(.leftMouseDown, p, .control, in: window))?.items.map(\.title),
                           ["Copy", "Paste", "Select All"])
            XCTAssertNotNil(view.menu(for: mouse(.rightMouseDown, p, in: window)))
            XCTAssertNil(view.menu(for: mouse(.otherMouseDown, p, in: window)))
        }
        try surface(mouseModes: [1002, 1006]) { view, window in
            XCTAssertNil(view.menu(for: mouse(.leftMouseDown, NSPoint(x: 10, y: 10), .control, in: window)),
                         "a program tracking the mouse is sent the Control-click")
        }
    }

    /// Look Up defines the word under the pointer, in the terminal's font.
    func testLookUpNamesTheWordUnderThePointer() throws {
        try surface { view, window in
            XCTAssertTrue(view.responds(to: #selector(NSResponder.quickLook(with:))))
            let cell = view.cellSize
            let p = NSPoint(x: 2 + 7.5 * cell.width, y: view.bounds.height - 2 - cell.height / 2)
            ghosttyWait("the line to be drawn") {
                view.mouseMoved(with: self.mouse(.mouseMoved, p, in: window))
                return view.lookUpWord()?.0.string == "world"
            }
            let (word, at) = try XCTUnwrap(view.lookUpWord())
            XCTAssertNotNil(word.attribute(.font, at: 0, effectiveRange: nil), "drawn in the terminal's font")
            XCTAssertEqual(at.x, 2 + 6 * cell.width, accuracy: 1, "where the word starts")

            view.mouseMoved(with: mouse(.mouseMoved, NSPoint(x: view.bounds.width - 20, y: 20), in: window))
            XCTAssertNil(view.lookUpWord(), "nothing to define over blank cells")
        }
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
}

private final class NavigationShortcutReceiver: NSObject {
    var steps: [String] = []
    @objc func selectNextProjectTab(_ sender: Any?) { steps.append("project next") }
    @objc func selectPreviousProjectTab(_ sender: Any?) { steps.append("project previous") }
    @objc func selectNextSession(_ sender: Any?) { steps.append("session next") }
    @objc func selectPreviousSession(_ sender: Any?) { steps.append("session previous") }
}

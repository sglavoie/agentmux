import AppKit
import GhosttyKit
import XCTest
@testable import AgentMux

extension GhosttyInteractionTests {
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

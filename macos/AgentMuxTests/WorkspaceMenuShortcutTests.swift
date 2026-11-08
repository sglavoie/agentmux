import AppKit
import GhosttyKit
import XCTest
@testable import AgentMux

/// Who owns a shortcut: the workspace's main menu, or the program in the
/// focused terminal. Every menu shortcut holds ⌘, is Shift+Page Up/Down, or
/// is one of the function keys the terminal reserves (F1 to F11), so nothing else
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
            (.command("]", keyCode: 30), "⌘]"),
            (.command("[", keyCode: 33), "⌘["),
            (.left.with(.command), "Previous Tab in Project"),
            (.right.with(.command), "Next Tab in Project"),
            (.left.with([.command, .shift]), WorkspaceMenu.previousStopTitle),
            (.right.with([.command, .shift]), WorkspaceMenu.nextStopTitle),
            (.command("l", keyCode: 37), "Back to Last Shown"),
            (.command("b", keyCode: 11), "Toggle Sidebar"),
            (.command("b", keyCode: 11, shifted: "B"), "Toggle Inspector"),
            (.command("j", keyCode: 38), "Reveal in Sidebar"),
            (.command("\r", keyCode: 36), "New Terminal Below"),
            (.command("\r", keyCode: 36, shifted: "\r"), "New Session Below…"),
            (.command("r", keyCode: 15), "Name Session…"),
            (.command("k", keyCode: 40, shifted: "K"), "Mark Accounted For"),
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
            (.command("f", keyCode: 3, shifted: "F"), "Flight Log…"),
            (.command("d", keyCode: 2), "Dashboard"),
            (.command("o", keyCode: 31), "Overview"),
            (.command("y", keyCode: 16), "Find or Resume a Conversation…"),
            (.command("t", keyCode: 17), "New Shell"),
            (.command("a", keyCode: 0, shifted: "A"), AttentionCycler.commandTitle),
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
            (.command("j", keyCode: 38), "Reveal in Sidebar"),
            (.command("d", keyCode: 2), "Dashboard"),
            (.command("o", keyCode: 31), "Overview"),
            (.command("y", keyCode: 16), "Find or Resume a Conversation…"),
            (.command("t", keyCode: 17), "New Shell"),
            (.command("w", keyCode: 13), "Close"),
            (.command("r", keyCode: 15, shifted: "R"), "ASL Runs…"),
            (.command("n", keyCode: 45, shifted: "N"), "Run a Plan…"),
            (.command("u", keyCode: 32, shifted: "U"), "Since Last Looked…"),
            (.command("f", keyCode: 3, shifted: "F"), "Flight Log…"),
            (.command("h", keyCode: 4, shifted: "H"), "Conversation History…"),
            (.command("c", keyCode: 8, shifted: "C"), "Checkout Changes…"),
            (.command("p", keyCode: 35), "Go to…"),
            (.command("p", keyCode: 35, shifted: "P"), "Command Palette…"),
            (.command("a", keyCode: 0, shifted: "A"), AttentionCycler.commandTitle),
            (.command("\r", keyCode: 36), "New Terminal Below"),
            (.command("\r", keyCode: 36, shifted: "\r"), "New Session Below…"),
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
            (.function(NSF4FunctionKey, keyCode: 118), AttentionCycler.commandTitle),
            (.function(NSF5FunctionKey, keyCode: 96), "Set Session Icon…"),
            (.function(NSF6FunctionKey, keyCode: 97), "Set Session Color…"),
            (.function(NSF7FunctionKey, keyCode: 98), "Previous Session Only"),
            (.function(NSF8FunctionKey, keyCode: 100), "Clear Session Markers"),
            (.function(NSF9FunctionKey, keyCode: 101), "Next Session Only"),
            (.function(NSF10FunctionKey, keyCode: 109), "Toggle Session Mute"),
            (.function(NSF11FunctionKey, keyCode: 103), "Mark Session Active"),
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
            (.command("k", keyCode: 40, shifted: "K"), "Mark Accounted For"),
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

    func testOnlyCommandSidebarZoomPagingAndFunctionKeysAreTheMenus() {
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

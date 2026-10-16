import AppKit
import Carbon
import XCTest
@testable import AgentMux

@MainActor
final class WorkspaceShortcutPreferencesTests: XCTestCase {
    private func combo(_ text: String) -> HotKeyCombo { HotKeyCombo(string: text, scope: .application)! }
    private func items(_ menu: NSMenu) -> [NSMenuItem] { menu.items.flatMap { [$0] + ($0.submenu.map(items) ?? []) } }

    func testApplicationScopeAllowsCommandWhileGlobalRulesRemainStrict() {
        XCTAssertNotNil(HotKeyCombo(string: "cmd+k", scope: .application))
        XCTAssertNil(HotKeyCombo(string: "cmd+k"))
        XCTAssertNil(HotKeyCombo(string: "k", scope: .application))
        XCTAssertNil(HotKeyCombo(string: "shift+k", scope: .application))
        XCTAssertNotNil(HotKeyCombo(string: "f12", scope: .application))
    }

    func testPreferencesPersistClearAndRestoreWithoutChangingGlobalBindings() {
        let suite = "WorkspaceShortcutTests-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        let model = PreferencesModel(defaults: defaults)
        model.preferences.workspaceShortcuts[WorkspaceShortcut.splitShell.rawValue] = "cmd+backslash"
        model.preferences.workspaceShortcuts[WorkspaceShortcut.commandPalette.rawValue] = ""
        let loaded = AppPreferences.load(from: defaults)
        XCTAssertEqual(WorkspaceShortcut.splitShell.combo(in: loaded), combo("cmd+backslash"))
        XCTAssertNil(WorkspaceShortcut.commandPalette.combo(in: loaded))
        XCTAssertEqual(loaded.hotKeyDashboard, HotKeyPreference.dashboard.default)
        model.preferences.workspaceShortcuts.removeValue(forKey: WorkspaceShortcut.commandPalette.rawValue)
        XCTAssertEqual(WorkspaceShortcut.commandPalette.combo(in: AppPreferences.load(from: defaults)), combo("shift+cmd+p"))
        model.preferences.workspaceShortcuts[WorkspaceShortcut.commandPalette.rawValue] = "invalid"
        XCTAssertEqual(WorkspaceShortcut.commandPalette.combo(in: model.preferences), WorkspaceShortcut.commandPalette.defaultCombo)
    }

    func testEveryNewCommandReceivesAndClearsItsMenuEquivalent() throws {
        let menu = WorkspaceMenu.make(target: AppDelegate())
        var preferences = AppPreferences()
        for shortcut in WorkspaceShortcut.allCases {
            preferences.workspaceShortcuts = [shortcut.rawValue: "cmd+backslash"]
            WorkspaceShortcut.apply(preferences, to: menu)
            let item = try XCTUnwrap(items(menu).first { $0.action == shortcut.action })
            XCTAssertEqual(item.keyEquivalent, "\\", shortcut.title)
            XCTAssertEqual(item.keyEquivalentModifierMask, .command)
            XCTAssertEqual(item.representedObject as? HotKeyCombo, combo("cmd+backslash"))
            preferences.workspaceShortcuts[shortcut.rawValue] = ""
            WorkspaceShortcut.apply(preferences, to: menu)
            XCTAssertEqual(item.keyEquivalent, "")
            XCTAssertNil(item.representedObject)
        }
    }

    func testConflictsIncludeMenusShiftedSymbolsOtherCommandsAndGlobalShortcuts() throws {
        let menu = WorkspaceMenu.make(target: AppDelegate())
        var preferences = AppPreferences()
        for key in ["cmd+c", "shift+cmd+p", "shift+cmd+1", "shift+cmd+minus", "f1", "ctrl+opt+cmd+a"] {
            XCTAssertNotNil(WorkspaceShortcut.splitShell.problem(for: combo(key), preferences: preferences, menu: menu), key)
        }
        XCTAssertNil(WorkspaceShortcut.commandPalette.problem(for: combo("shift+cmd+p"), preferences: preferences, menu: menu))
        preferences.workspaceShortcuts[WorkspaceShortcut.swapPanes.rawValue] = "cmd+backslash"
        preferences.workspaceShortcuts[WorkspaceShortcut.splitShell.rawValue] = "cmd+backslash"
        WorkspaceShortcut.apply(preferences, to: menu)
        for shortcut in [WorkspaceShortcut.swapPanes, .splitShell] {
            XCTAssertEqual(items(menu).first { $0.action == shortcut.action }?.keyEquivalent, "")
        }
        preferences.workspaceShortcuts[WorkspaceShortcut.swapPanes.rawValue] = ""
        WorkspaceShortcut.apply(preferences, to: menu)
        XCTAssertEqual(items(menu).first { $0.action == WorkspaceShortcut.splitShell.action }?.keyEquivalent, "\\")
    }

    func testRecorderKeepsOldBindingOnConflictThenAcceptsCommandKey() throws {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 300, height: 100), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        defer { window.close() }
        let recorder = HotKeyRecorder()
        recorder.window = window
        var saved: HotKeyCombo?
        var states: [Bool] = []
        recorder.start(scope: .application, validate: { $0.keyCode == UInt32(kVK_ANSI_C) ? "Copy already uses this key" : nil },
                       onStateChange: { states.append($0) }, onRecord: { saved = $0 })
        func event(_ key: Int, _ text: String) -> NSEvent {
            NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: .command, timestamp: 1,
                            windowNumber: window.windowNumber, context: nil, characters: text, charactersIgnoringModifiers: text,
                            isARepeat: false, keyCode: UInt16(key))!
        }
        XCTAssertNil(recorder.handle(event(kVK_ANSI_C, "c")))
        XCTAssertNil(saved)
        XCTAssertTrue(recorder.isRecording)
        XCTAssertEqual(recorder.hint, "Copy already uses this key")
        XCTAssertNil(recorder.handle(event(kVK_ANSI_K, "k")))
        XCTAssertEqual(saved, combo("cmd+k"))
        XCTAssertEqual(states, [true, false])
    }

    func testSettingsResigningKeyStopsWorkspaceRecorder() {
        let suite = "WorkspaceShortcutTests-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        let controller = PreferencesWindowController(model: PreferencesModel(defaults: defaults))
        let window = controller.window
        XCTAssertTrue(controller.workspaceRecorder.window === window)
        controller.workspaceRecorder.start(scope: .application, onStateChange: { _ in }, onRecord: { _ in })
        controller.windowDidResignKey(Notification(name: NSWindow.didResignKeyNotification, object: window))
        XCTAssertFalse(controller.workspaceRecorder.isRecording)
        window.close()
    }
}

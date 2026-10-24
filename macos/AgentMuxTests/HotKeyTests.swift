import Carbon
import XCTest
@testable import AgentMux

final class HotKeyTests: XCTestCase {
    func testDefaultsAndSpelling() {
        XCTAssertEqual(HotKeyPreference.dashboard.default.displayString, "⌃⌥⌘A")
        XCTAssertEqual(HotKeyPreference.nextAttention.default.displayString, "⌃⌥⌘N")
        XCTAssertEqual(HotKeyPreference.dashboard.default.string, "ctrl+opt+cmd+a")
        XCTAssertEqual(HotKeyCombo(string: "Command+Alt+Control+N"), HotKeyPreference.nextAttention.default)
        XCTAssertNil(HotKeyCombo(string: "shift+a"), "Shift alone would swallow typing")
        XCTAssertNotNil(HotKeyCombo(string: "f12"))
        XCTAssertNil(HotKeyCombo(string: "f5"), "F1–F11 are the workspace's")
        XCTAssertNil(HotKeyCombo(string: "ctrl+ctrl+a"))
    }

    func testPreferenceLoadAndSave() {
        let defaults = UserDefaults(suiteName: "HotKeyTests-\(UUID().uuidString)")!
        let preference = HotKeyPreference.nextAttention
        XCTAssertEqual(preference.load(from: defaults), preference.default, "absent is the default")
        preference.save(nil, to: defaults)
        XCTAssertEqual(defaults.string(forKey: "amxHotKeyNextAttention"), "")
        XCTAssertNil(preference.load(from: defaults), "empty is turned off")
        defaults.set("nonsense", forKey: "amxHotKeyNextAttention")
        XCTAssertEqual(preference.load(from: defaults), preference.default)
        preference.save(HotKeyCombo(string: "ctrl+shift+j"), to: defaults)
        XCTAssertEqual(defaults.string(forKey: "amxHotKeyNextAttention"), "ctrl+shift+j")
        XCTAssertEqual(preference.load(from: defaults)?.displayString, "⌃⇧J")
    }

    func testDuplicateOfTheAppsOwnShortcut() {
        let combo = HotKeyPreference.dashboard.default
        let earlier = [(label: "Show dashboard", combo: Optional(combo))]
        XCTAssertEqual(
            HotKeyConflicts.duplicate(combo, earlier: earlier),
            "⌃⌥⌘A is already used for “Show dashboard”. Choose another shortcut."
        )
        XCTAssertNil(HotKeyConflicts.duplicate(HotKeyPreference.nextAttention.default, earlier: earlier))
        XCTAssertNil(HotKeyConflicts.duplicate(nil, earlier: [(label: "x", combo: nil)]), "two turned-off shortcuts do not collide")
    }

    func testSystemShortcutConflict() {
        let combo = HotKeyPreference.nextAttention.default
        XCTAssertTrue(SystemHotKeys.conflicts(with: combo, enabled: [combo]))
        XCTAssertFalse(SystemHotKeys.conflicts(with: combo, enabled: []))
        XCTAssertEqual(
            HotKeyConflicts.problem(for: .registered(combo), systemCombos: [combo]),
            "macOS also uses ⌃⌥⌘N (System Settings › Keyboard › Keyboard Shortcuts); turn it off there, or choose another shortcut."
        )
        XCTAssertNil(HotKeyConflicts.problem(for: .registered(combo), systemCombos: []))
        XCTAssertNil(HotKeyConflicts.problem(for: .none, systemCombos: [combo]))
    }

    /// The status menu shows a shortcut only while the system holds it.
    func testOnlyARegisteredShortcutIsOneToShow() {
        let combo = HotKeyPreference.dashboard.default
        XCTAssertEqual(HotKeyCenter.Registration.registered(combo).registeredCombo, combo)
        XCTAssertNil(HotKeyCenter.Registration.failed(combo, OSStatus(eventHotKeyExistsErr)).registeredCombo)
        XCTAssertNil(HotKeyCenter.Registration.none.registeredCombo)
    }

    func testCarbonRefusals() {
        let combo = HotKeyPreference.dashboard.default
        XCTAssertEqual(
            HotKeyConflicts.problem(for: .failed(combo, OSStatus(eventHotKeyExistsErr)), systemCombos: []),
            "⌃⌥⌘A is already taken by another app. Choose another shortcut."
        )
        XCTAssertEqual(
            HotKeyConflicts.problem(for: .failed(combo, OSStatus(eventHotKeyInvalidErr)), systemCombos: []),
            "⌃⌥⌘A cannot be used as a global shortcut."
        )
    }

    @MainActor
    func testHijackingCombosAreRefusedWithAReason() {
        let cmd = UInt32(cmdKey), shift = UInt32(shiftKey), ctrl = UInt32(controlKey), opt = UInt32(optionKey)
        let t = UInt32(kVK_ANSI_T)
        XCTAssertNil(HotKeyCombo(keyCode: t, modifiers: cmd), "⌘T is every app's new tab")
        XCTAssertNil(HotKeyCombo(keyCode: t, modifiers: cmd | shift))
        XCTAssertEqual(HotKeyCombo.refusal(keyCode: t, modifiers: cmd), "⌘ alone is for apps' own menus; add ⌃ or ⌥…")
        XCTAssertNil(HotKeyCombo(string: "cmd+t"), "a stored ⌘-only combo reads as refused")
        XCTAssertNotNil(HotKeyCombo(keyCode: t, modifiers: cmd | opt))
        XCTAssertNotNil(HotKeyCombo(keyCode: t, modifiers: ctrl))

        for number in 1...11 {
            let code = UInt32([kVK_F1, kVK_F2, kVK_F3, kVK_F4, kVK_F5, kVK_F6, kVK_F7, kVK_F8, kVK_F9, kVK_F10, kVK_F11][number - 1])
            XCTAssertNil(HotKeyCombo(keyCode: code, modifiers: 0), "F\(number) is the workspace's")
            XCTAssertNil(HotKeyCombo(keyCode: code, modifiers: shift))
            XCTAssertEqual(HotKeyCombo.refusal(keyCode: code, modifiers: 0), "F1–F11 are the workspace's; add ⌃, ⌥, or ⌘…")
            XCTAssertNotNil(HotKeyCombo(keyCode: code, modifiers: ctrl | opt))
        }
        XCTAssertEqual(HotKeyCombo.workspaceFunctionKeys, WorkspaceMenu.functionKeys.count)
        XCTAssertNotNil(HotKeyCombo(keyCode: UInt32(kVK_F12), modifiers: 0))
        XCTAssertNotNil(HotKeyCombo(string: "f13"))
        XCTAssertEqual(HotKeyCombo.refusal(keyCode: UInt32(kVK_ANSI_A), modifiers: shift), "Add ⌃, ⌥, or ⌘…")
        XCTAssertEqual(HotKeyCombo.refusal(keyCode: UInt32(kVK_CapsLock), modifiers: ctrl), "That key cannot be a shortcut")
    }

    func testDisplayFollowsTheKeyboardLayout() {
        let combo = HotKeyPreference.dashboard.default
        // AZERTY types Q on the key US calls A.
        let azerty: (UInt32) -> String? = { $0 == UInt32(kVK_ANSI_A) ? "Q" : nil }
        XCTAssertEqual(combo.displayString(keyLabel: azerty), "⌃⌥⌘Q")
        XCTAssertEqual(combo.displayString(keyLabel: { _ in nil }), "⌃⌥⌘A", "the US table is the fallback")
        XCTAssertEqual(HotKeyCombo(string: "ctrl+opt+f5")?.displayString, "⌃⌥F5", "named keys keep their names")
        XCTAssertNil(HotKeyCombo.layoutLabel(UInt32(kVK_F5)))
        XCTAssertNil(HotKeyCombo.layoutLabel(UInt32(kVK_Return)))
        // Whatever this Mac's layout is, it names its letter keys.
        XCTAssertNotNil(HotKeyCombo.layoutLabel(UInt32(kVK_ANSI_A)))
        XCTAssertEqual(combo.displayString, combo.displayString(keyLabel: HotKeyCombo.layoutLabel))
    }

    func testMenuKeyEquivalent() throws {
        let f13 = try XCTUnwrap(HotKeyCombo(string: "ctrl+f13")?.menuKeyEquivalent)
        XCTAssertEqual(f13.key, String(Character(UnicodeScalar(NSF13FunctionKey)!)))
        XCTAssertEqual(f13.modifiers, [.control])
        let dashboard = try XCTUnwrap(HotKeyPreference.dashboard.default.menuKeyEquivalent)
        XCTAssertEqual(dashboard.key, HotKeyCombo.layoutLabel(UInt32(kVK_ANSI_A))?.lowercased())
        XCTAssertEqual(dashboard.modifiers, [.control, .option, .command])
        XCTAssertEqual(HotKeyCombo(string: "ctrl+opt+return")?.menuKeyEquivalent?.key, "\r")
    }

    @MainActor
    func testCollisionWithTheAppsOwnMenuKeys() throws {
        let combo = try XCTUnwrap(HotKeyCombo(string: "opt+cmd+i"))
        let key = try XCTUnwrap(combo.menuKeyEquivalent?.key)
        let menu = NSMenu()
        let top = NSMenuItem(title: "View", action: nil, keyEquivalent: "")
        top.submenu = NSMenu()
        let inspector = NSMenuItem(title: "Inspector", action: nil, keyEquivalent: key)
        inspector.keyEquivalentModifierMask = [.option, .command]
        top.submenu?.addItem(inspector)
        menu.addItem(top)
        let keys = HotKeyConflicts.menuKeys(menu)
        XCTAssertEqual(keys.map(\.title), ["Inspector"])
        XCTAssertEqual(
            HotKeyConflicts.menuCollision(combo, menuKeys: keys),
            "\(combo.displayString) is also the menu key for “Inspector”, which stops working while this shortcut is set."
        )
        XCTAssertNil(HotKeyConflicts.menuCollision(HotKeyCombo(string: "ctrl+opt+cmd+i"), menuKeys: keys))
        XCTAssertNil(HotKeyConflicts.menuCollision(nil, menuKeys: keys))
        // An uppercase key equivalent is the Shift combo.
        let shifted = [HotKeyConflicts.MenuKey(title: "Up", key: key.uppercased(), modifiers: [.option, .command])]
        XCTAssertNotNil(HotKeyConflicts.menuCollision(HotKeyCombo(string: "opt+shift+cmd+i"), menuKeys: shifted))
        XCTAssertNil(HotKeyConflicts.menuCollision(combo, menuKeys: shifted))
    }

    /// A menu item keyed `!` or `}` with ⌘ alone is ⌘⇧1 or ⌘⇧], as AppKit
    /// matches it, so a shortcut on those keys collides with it.
    @MainActor
    /// "Bigger Sidebars" is ⌥ and `+`, which is ⌥⇧= on the keyboard: a global
    /// ⌥⇧= takes it, though the menu spells the key as the shifted character.
    func testShiftedPunctuationMenuKeysCollideWithTheirShiftCombos() throws {
        for (name, base, shiftedKey) in [("equal", "=", "+"), ("minus", "-", "_")] {
            let combo = try XCTUnwrap(HotKeyCombo(string: "shift+opt+" + name))
            guard combo.menuKeyEquivalent?.key == base else {
                throw XCTSkip("the current keyboard layout does not put \(base) on that key")
            }
            let keys = [HotKeyConflicts.MenuKey(title: "Item \(shiftedKey)", key: shiftedKey, modifiers: [.option])]
            XCTAssertEqual(
                HotKeyConflicts.menuCollision(combo, menuKeys: keys),
                "\(combo.displayString) is also the menu key for “Item \(shiftedKey)”, which stops working while this shortcut is set."
            )
            XCTAssertNil(HotKeyConflicts.menuCollision(HotKeyCombo(string: "opt+" + name), menuKeys: keys))
            XCTAssertEqual(HotKeyConflicts.normalized(shiftedKey, [.option]),
                           HotKeyConflicts.normalized(base, [.option, .shift]))
        }
    }

    @MainActor
    func testOpenDashboardShowsTheGlobalShortcut() throws {
        let open = NSMenuItem(title: "Open Dashboard", action: nil, keyEquivalent: "")
        open.representedObject = MenuBuilder.ActionBox(.openDashboard)
        let other = NSMenuItem(title: "Open Workspace", action: nil, keyEquivalent: "")
        other.representedObject = MenuBuilder.ActionBox(.openWorkspace)
        var input = MenuBuilder.ActionsInput()
        StatusItemController.showHotKeys(on: [open, other], input)
        XCTAssertEqual(open.keyEquivalent, "", "no shortcut, no key")

        input.dashboardHotKey = HotKeyPreference.dashboard.default
        StatusItemController.showHotKeys(on: [open, other], input)
        let expected = try XCTUnwrap(HotKeyPreference.dashboard.default.menuKeyEquivalent)
        XCTAssertEqual(open.keyEquivalent, expected.key)
        XCTAssertEqual(open.keyEquivalentModifierMask, [.control, .option, .command])
        XCTAssertEqual(other.keyEquivalent, "")
    }

    @MainActor
    func testNextWaitingSessionShowsItsGlobalShortcut() throws {
        let open = NSMenuItem(title: "Open Dashboard", action: nil, keyEquivalent: "")
        open.representedObject = MenuBuilder.ActionBox(.openDashboard)
        let next = NSMenuItem(title: "Next Waiting Session", action: nil, keyEquivalent: "")
        next.representedObject = MenuBuilder.ActionBox(.nextAttention)
        var input = MenuBuilder.ActionsInput()
        input.nextAttentionHotKey = HotKeyPreference.nextAttention.default
        StatusItemController.showHotKeys(on: [open, next], input)
        let expected = try XCTUnwrap(HotKeyPreference.nextAttention.default.menuKeyEquivalent)
        XCTAssertEqual(next.keyEquivalent, expected.key)
        XCTAssertEqual(next.keyEquivalentModifierMask, [.control, .option, .command])
        XCTAssertEqual(open.keyEquivalent, "", "the dashboard's shortcut is turned off")
    }

    @MainActor
    func testAGreyedNextWaitingSessionKeepsItsShortcut() throws {
        let items = MenuBuilder.items(
            for: [.unavailableAction(.nextAttention, "Next Waiting Session", reason: "No session is waiting")],
            target: NSObject(), sessionAction: #selector(NSObject.description), actionSelector: #selector(NSObject.hash)
        )
        let next = try XCTUnwrap(items.first)
        XCTAssertFalse(next.isEnabled)
        XCTAssertEqual(next.toolTip, "No session is waiting")
        var input = MenuBuilder.ActionsInput()
        input.nextAttentionHotKey = HotKeyPreference.nextAttention.default
        StatusItemController.showHotKeys(on: items, input)
        XCTAssertEqual(next.keyEquivalent, try XCTUnwrap(HotKeyPreference.nextAttention.default.menuKeyEquivalent).key)
    }

    func testShortcutsAreNamedAsTheMenuNamesThem() {
        XCTAssertEqual(HotKeyPreference.dashboard.label, "Open Dashboard")
        XCTAssertEqual(HotKeyPreference.nextAttention.label, "Next Waiting Session")
        var input = MenuBuilder.ActionsInput()
        input.feedLive = true
        input.waiting = 1
        let titles = MenuBuilder.actionEntries(input).compactMap { entry -> String? in
            if case let .action(_, title) = entry { return title } else { return nil }
        }
        for preference in HotKeyPreference.all {
            XCTAssertTrue(titles.contains(preference.label), "\(preference.label) is a status menu item")
        }
    }

    /// The status menu, Settings, and the workspace's F4 call the attention
    /// cycle by one name (the Session menu's ⌘⇧A is checked with the
    /// workspace's keys).
    @MainActor
    func testTheNextWaitingCommandHasOneName() {
        XCTAssertEqual(HotKeyPreference.nextAttention.label, AttentionCycler.commandTitle)
        XCTAssertEqual(WorkspaceMenu.functionKeys.first { $0.number == 4 }?.title, AttentionCycler.commandTitle)
    }

    func testSystemCombosAreReadable() {
        // Whatever this Mac has enabled parses into valid combos.
        for combo in SystemHotKeys.enabledCombos() {
            XCTAssertNotNil(HotKeyCombo(keyCode: combo.keyCode, modifiers: combo.modifiers))
        }
    }

    // MARK: Recorder

    private func keyDown(_ keyCode: UInt16, _ characters: String, modifiers: NSEvent.ModifierFlags = [], in window: NSWindow?) -> NSEvent {
        NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: modifiers, timestamp: 0,
                         windowNumber: window?.windowNumber ?? 0, context: nil, characters: characters,
                         charactersIgnoringModifiers: characters, isARepeat: false, keyCode: keyCode)!
    }

    private func settingsWindow() -> NSWindow {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 200, height: 100), styleMask: [.titled],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.orderBack(nil)
        return window
    }

    @MainActor
    func testRecorderOnlyTakesKeysTypedInSettings() {
        let settings = settingsWindow()
        let other = settingsWindow()
        defer { settings.close(); other.close() }
        let recorder = HotKeyRecorder()
        recorder.window = settings
        var states: [Bool] = []
        var recorded: [HotKeyCombo] = []
        recorder.start(onStateChange: { states.append($0) }, onRecord: { recorded.append($0) })
        defer { recorder.stop() }

        let combo: NSEvent.ModifierFlags = [.control, .option, .command]
        let elsewhere = keyDown(UInt16(kVK_ANSI_K), "k", modifiers: combo, in: other)
        XCTAssertTrue(recorder.handle(elsewhere) === elsewhere, "another window's key goes on to it")
        let windowless = keyDown(UInt16(kVK_ANSI_K), "k", modifiers: combo, in: nil)
        XCTAssertTrue(recorder.handle(windowless) === windowless)
        XCTAssertTrue(recorder.isRecording)
        XCTAssertEqual(recorded, [])

        XCTAssertNil(recorder.handle(keyDown(UInt16(kVK_ANSI_K), "k", modifiers: combo, in: settings)))
        XCTAssertEqual(recorded, [HotKeyCombo(string: "ctrl+opt+cmd+k")!])
        XCTAssertFalse(recorder.isRecording)
        XCTAssertEqual(states, [true, false])

        let after = keyDown(UInt16(kVK_ANSI_J), "j", modifiers: combo, in: settings)
        XCTAssertTrue(recorder.handle(after) === after, "a stopped recorder takes nothing")
    }

    /// A recorder whose key monitor is captured rather than installed, so
    /// a test drives the callback the recorder registers.
    private final class CapturedMonitor {
        var callback: ((NSEvent) -> NSEvent?)?
        var removed = 0

        func attach(to recorder: HotKeyRecorder) {
            recorder.addMonitor = { [unowned self] handler in
                callback = handler
                return NSObject()
            }
            recorder.removeMonitor = { [unowned self] _ in removed += 1 }
        }
    }

    @MainActor
    func testRegisteredMonitorSwallowsSettingsKeysAndPassesOthers() throws {
        let settings = settingsWindow()
        let other = settingsWindow()
        defer { settings.close(); other.close() }
        let monitor = CapturedMonitor()
        let recorder = HotKeyRecorder()
        recorder.window = settings
        monitor.attach(to: recorder)
        var recorded: [HotKeyCombo] = []
        let combo: NSEvent.ModifierFlags = [.control, .option, .command]

        recorder.start(onStateChange: { _ in }, onRecord: { recorded.append($0) })
        var callback = try XCTUnwrap(monitor.callback)
        let elsewhere = keyDown(UInt16(kVK_ANSI_K), "k", modifiers: combo, in: other)
        XCTAssertTrue(callback(elsewhere) === elsewhere, "another window's key goes on to it")
        let windowless = keyDown(UInt16(kVK_ANSI_K), "k", modifiers: combo, in: nil)
        XCTAssertTrue(callback(windowless) === windowless, "a windowless key goes on")
        XCTAssertNil(callback(keyDown(UInt16(kVK_ANSI_A), "a", in: settings)), "a refused key is swallowed")
        XCTAssertNotNil(recorder.hint)
        XCTAssertTrue(recorder.isRecording)
        XCTAssertNil(callback(keyDown(UInt16(kVK_ANSI_K), "k", modifiers: combo, in: settings)),
                     "a recorded shortcut is swallowed")
        XCTAssertEqual(recorded, [HotKeyCombo(string: "ctrl+opt+cmd+k")!])
        XCTAssertFalse(recorder.isRecording)
        XCTAssertEqual(monitor.removed, 1)
        let after = keyDown(UInt16(kVK_ANSI_J), "j", modifiers: combo, in: settings)
        XCTAssertTrue(callback(after) === after, "a stopped recorder's monitor takes nothing")

        recorder.start(onStateChange: { _ in }, onRecord: { recorded.append($0) })
        callback = try XCTUnwrap(monitor.callback)
        XCTAssertNil(callback(keyDown(UInt16(kVK_Escape), "\u{1b}", in: settings)), "Esc is swallowed")
        XCTAssertFalse(recorder.isRecording)
        XCTAssertEqual(recorded.count, 1)
    }

    @MainActor
    func testRegisteredMonitorOfAGoneRecorderPassesKeysOn() throws {
        let settings = settingsWindow()
        defer { settings.close() }
        let monitor = CapturedMonitor()
        var recorder: HotKeyRecorder? = HotKeyRecorder()
        recorder?.window = settings
        monitor.attach(to: recorder!)
        recorder?.start(onStateChange: { _ in }, onRecord: { _ in })
        let callback = try XCTUnwrap(monitor.callback)
        recorder = nil
        let key = keyDown(UInt16(kVK_ANSI_K), "k", modifiers: [.control, .option, .command], in: settings)
        XCTAssertTrue(callback(key) === key)
    }

    @MainActor
    func testSettingsLosingKeyStopsRecording() {
        let model = PreferencesModel(defaults: UserDefaults(suiteName: "HotKeyTests-\(UUID().uuidString)")!)
        var recording: [Bool] = []
        model.onRecordingChange = { recording.append($0) }
        let controller = PreferencesWindowController(model: model)
        let window = controller.window
        XCTAssertTrue(controller.dashboardRecorder.window === window)
        XCTAssertTrue(controller.attentionRecorder.window === window)
        controller.dashboardRecorder.start(onStateChange: { model.onRecordingChange?($0) }, onRecord: { _ in })
        XCTAssertTrue(controller.dashboardRecorder.isRecording)
        controller.windowDidResignKey(Notification(name: NSWindow.didResignKeyNotification, object: controller.window))
        XCTAssertFalse(controller.dashboardRecorder.isRecording)
        XCTAssertEqual(recording, [true, false], "the global shortcuts are registered again")
        controller.window.close()
    }
}

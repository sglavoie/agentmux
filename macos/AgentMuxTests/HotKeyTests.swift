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
        XCTAssertNotNil(HotKeyCombo(string: "f5"))
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

    func testSystemCombosAreReadable() {
        // Whatever this Mac has enabled parses into valid combos.
        for combo in SystemHotKeys.enabledCombos() {
            XCTAssertNotNil(HotKeyCombo(keyCode: combo.keyCode, modifiers: combo.modifiers))
        }
    }
}

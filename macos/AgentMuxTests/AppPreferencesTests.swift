import XCTest
@testable import AgentMux

final class AppPreferencesTests: XCTestCase {
    private func freshDefaults() -> UserDefaults {
        UserDefaults(suiteName: "AppPreferencesTests-\(UUID().uuidString)")!
    }

    func testDefaults() {
        let preferences = AppPreferences.load(from: freshDefaults())
        XCTAssertEqual(preferences, AppPreferences())
        XCTAssertEqual(preferences.binaryPath, "")
        XCTAssertFalse(preferences.launchAtLogin)
        XCTAssertEqual(preferences.hotKeyDashboard?.displayString, "⌃⌥⌘A")
        XCTAssertEqual(preferences.hotKeyNextAttention?.displayString, "⌃⌥⌘N")
        XCTAssertEqual(preferences.statusStyle, .glyphs)
        XCTAssertTrue(preferences.showWorkingCount)
        XCTAssertEqual(preferences.recentDays, 7)
        XCTAssertEqual(preferences.recentLimit, 15)
        XCTAssertTrue(preferences.useAppNotifications)
        XCTAssertNil(preferences.snoozeUntil)
        XCTAssertEqual(preferences.kittySocketOverride, "")
        XCTAssertEqual(preferences.environment, [:])
    }

    func testKeysAreThePlannedNames() {
        XCTAssertEqual(AppPreferences.Key.all, [
            "amxBinaryPath", "amxLaunchAtLogin", "amxHotKeyDashboard", "amxHotKeyNextAttention", "amxStatusStyle",
            "amxShowWorkingCount", "amxRecentDays", "amxRecentLimit", "amxUseAppNotifications", "amxSnoozeUntil",
            "amxKittySocketOverride", "amxEnvironment",
        ])
    }

    func testRoundTrip() {
        let defaults = freshDefaults()
        var preferences = AppPreferences()
        preferences.binaryPath = " ~/bin/amx "
        preferences.launchAtLogin = true
        preferences.hotKeyDashboard = nil
        preferences.hotKeyNextAttention = HotKeyCombo(string: "ctrl+opt+j")
        preferences.statusStyle = .compact
        preferences.showWorkingCount = false
        preferences.recentDays = 30
        preferences.recentLimit = 5
        preferences.useAppNotifications = false
        preferences.snoozeUntil = Date(timeIntervalSince1970: 1_800_000_000)
        preferences.kittySocketOverride = "unix:/tmp/kitty-1"
        preferences.environment = ["AMX_CONFIG": "~/x.toml", "PATH": "/bin"]
        preferences.save(to: defaults)

        var expected = preferences
        expected.binaryPath = "~/bin/amx"
        XCTAssertEqual(AppPreferences.load(from: defaults), expected)
        XCTAssertEqual(defaults.string(forKey: "amxStatusStyle"), "compact")

        preferences.binaryPath = ""
        preferences.snoozeUntil = nil
        preferences.environment = [:]
        preferences.save(to: defaults)
        XCTAssertNil(defaults.object(forKey: "amxBinaryPath"))
        XCTAssertNil(defaults.object(forKey: "amxSnoozeUntil"))
        XCTAssertNil(defaults.object(forKey: "amxEnvironment"))
    }

    func testMistypedAndOutOfRangeValuesReadAsDefaults() {
        let defaults = freshDefaults()
        defaults.set("loud", forKey: "amxStatusStyle")
        defaults.set("yes", forKey: "amxShowWorkingCount")
        defaults.set(0, forKey: "amxRecentDays")
        defaults.set(5_000, forKey: "amxRecentLimit")
        defaults.set(true, forKey: "amxRecentLimit")
        defaults.set("tomorrow", forKey: "amxSnoozeUntil")
        defaults.set(["PATH": "/bin", "HOME": "/nope", "AMX_CONFIG": "a\nb", "XDG_DATA_HOME": "  "], forKey: "amxEnvironment")
        let preferences = AppPreferences.load(from: defaults)
        XCTAssertEqual(preferences.statusStyle, .glyphs)
        XCTAssertTrue(preferences.showWorkingCount)
        XCTAssertEqual(preferences.recentDays, 7)
        XCTAssertEqual(preferences.recentLimit, 15)
        XCTAssertNil(preferences.snoozeUntil)
        XCTAssertEqual(preferences.environment, ["PATH": "/bin"], "only overridable, single-line, non-blank values")
    }

    func testSaveClampsCounts() {
        let defaults = freshDefaults()
        var preferences = AppPreferences()
        preferences.recentDays = 500
        preferences.recentLimit = -3
        preferences.save(to: defaults)
        XCTAssertEqual(defaults.integer(forKey: "amxRecentDays"), AppPreferences.recentDaysRange.upperBound)
        XCTAssertEqual(defaults.integer(forKey: "amxRecentLimit"), AppPreferences.recentLimitRange.lowerBound)
    }

    func testEnvironmentOverridesApply() {
        let shell = LaunchEnvironment(home: "/Users/me", path: "/usr/bin", forwarded: ["AMX_CONFIG": "/a", "XDG_RUNTIME_DIR": "/r"])
        let applied = shell.applying(["PATH": "/opt/bin", "AMX_CONFIG": "/b", "HOME": "/ignored"])
        XCTAssertEqual(applied.path, "/opt/bin")
        XCTAssertEqual(applied.home, "/Users/me")
        XCTAssertEqual(applied.forwarded, ["AMX_CONFIG": "/b", "XDG_RUNTIME_DIR": "/r"])
        XCTAssertEqual(applied.value(of: "PATH"), "/opt/bin")
        XCTAssertEqual(EnvironmentOverrides.names(detected: shell),
                       EnvironmentOverrides.commonNames + ["XDG_RUNTIME_DIR"])
    }

    @MainActor
    func testModelSavesEachChangeAndReportsIt() {
        let defaults = freshDefaults()
        let model = PreferencesModel(defaults: defaults)
        var changes: [(AppPreferences, AppPreferences)] = []
        model.onChange = { changes.append(($0, $1)) }
        model.preferences.hotKeyNextAttention = HotKeyCombo(string: "ctrl+opt+cmd+m")
        XCTAssertEqual(defaults.string(forKey: "amxHotKeyNextAttention"), "ctrl+opt+cmd+m")
        XCTAssertEqual(changes.count, 1)
        XCTAssertEqual(changes.first?.0.hotKeyNextAttention, HotKeyPreference.nextAttention.default)
        model.preferences.hotKeyNextAttention = HotKeyCombo(string: "ctrl+opt+cmd+m")
        XCTAssertEqual(changes.count, 1, "an unchanged value is not reported")

        defaults.set("iconOnly", forKey: "amxStatusStyle")
        model.refresh()
        XCTAssertEqual(model.preferences.statusStyle, .iconOnly, "refresh reads a defaults write")
    }

    @MainActor
    func testModelDescribesTheSnooze() {
        let model = PreferencesModel(defaults: freshDefaults())
        XCTAssertEqual(model.snoozeDescription, "Not snoozed")
        var notification = CompanionNotification()
        notification.indefinite = true
        model.daemonNotification = notification
        XCTAssertEqual(model.snoozeDescription, "Snoozed until turned off")
        model.preferences.kittySocketOverride = "/tmp/kitty"
        XCTAssertNotNil(model.kittySocketProblem)
        model.preferences.kittySocketOverride = "tcp:localhost:1234"
        XCTAssertNil(model.kittySocketProblem)
    }
}

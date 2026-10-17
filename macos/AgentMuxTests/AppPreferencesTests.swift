import XCTest
@testable import AgentMux

final class AppPreferencesTests: XCTestCase {
    private func freshDefaults() -> UserDefaults {
        UserDefaults(suiteName: "AppPreferencesTests-\(UUID().uuidString)")!
    }

    func testLegacyLaunchDestinationMigratesAndFollowingCLIRoundTrips() {
        let defaults = freshDefaults()
        defaults.set("automatic", forKey: "amxLaunchBackend")
        var preferences = AppPreferences.load(from: defaults)
        XCTAssertEqual(preferences.launchBackend, .workspace)
        preferences.save(to: defaults)
        XCTAssertEqual(defaults.string(forKey: "amxLaunchBackend"), "workspace")
        preferences.launchBackend = .followCLI
        preferences.save(to: defaults)
        XCTAssertEqual(AppPreferences.load(from: defaults).launchBackend, .followCLI)
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
        XCTAssertEqual(preferences.launchBackend, .workspace)
        XCTAssertFalse(preferences.openWorkspaceAtLaunch)
        XCTAssertNil(preferences.defaultFontSize)
        XCTAssertEqual(preferences.dashboardPlace, .workspace)
    }

    func testWorkspacePreferencesRoundTripAndRejectUnknownValues() {
        let defaults = freshDefaults()
        var preferences = AppPreferences()
        preferences.launchBackend = .workspace
        preferences.openWorkspaceAtLaunch = true
        preferences.dashboardPlace = .kitty
        preferences.save(to: defaults)
        XCTAssertEqual(defaults.string(forKey: "amxLaunchBackend"), "workspace")
        XCTAssertEqual(defaults.string(forKey: "amxDashboardPlace"), "kitty")
        XCTAssertEqual(AppPreferences.load(from: defaults), preferences)

        // The hidden default earlier builds read is the same key, so an
        // operator who set it by hand keeps the window opening at launch.
        let earlier = freshDefaults()
        earlier.set(true, forKey: "amxOpenWorkspaceAtLaunch")
        XCTAssertTrue(AppPreferences.load(from: earlier).openWorkspaceAtLaunch)

        defaults.set("ghostty", forKey: "amxLaunchBackend")
        defaults.set("yes", forKey: "amxOpenWorkspaceAtLaunch")
        defaults.set("ghostty", forKey: "amxDashboardPlace")
        let stray = AppPreferences.load(from: defaults)
        XCTAssertEqual(stray.launchBackend, .workspace)
        XCTAssertFalse(stray.openWorkspaceAtLaunch)
        XCTAssertEqual(stray.dashboardPlace, .workspace)
    }

    func testKeysAreThePlannedNames() {
        XCTAssertEqual(AppPreferences.Key.all, [
            "amxBinaryPath", "amxLaunchAtLogin", "amxHotKeyDashboard", "amxHotKeyNextAttention", "amxStatusStyle",
            "amxShowWorkingCount", "amxRecentDays", "amxRecentLimit", "amxUseAppNotifications", "amxSnoozeUntil",
            "amxKittySocketOverride", "amxEnvironment", "amxLaunchBackend", "amxOpenWorkspaceAtLaunch", "amxDefaultFontSize",
            "amxDashboardPlace", "amxTerminalTheme", "amxWorkspaceShortcuts",
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

    func testDefaultZoomPersistsFractionalPointsAndResetRemovesOverride() {
        let defaults = freshDefaults()
        var preferences = AppPreferences()
        preferences.defaultFontSize = 17.5
        preferences.save(to: defaults)
        XCTAssertEqual(AppPreferences.load(from: defaults).defaultFontSize, 17.5)
        preferences.defaultFontSize = nil
        preferences.save(to: defaults)
        XCTAssertNil(defaults.object(forKey: AppPreferences.Key.defaultFontSize))
    }

    func testDefaultZoomRejectsInvalidStoredValuesAndClampsSavedValues() {
        let defaults = freshDefaults()
        for value: Any in ["17", true, 0, 73, Float.nan, Float.infinity] {
            defaults.set(value, forKey: AppPreferences.Key.defaultFontSize)
            XCTAssertNil(AppPreferences.load(from: defaults).defaultFontSize)
        }
        for (value, expected): (Float, Float?) in [(1, 6), (100, 72), (.nan, nil), (.infinity, nil)] {
            var preferences = AppPreferences()
            preferences.defaultFontSize = value
            preferences.save(to: defaults)
            XCTAssertEqual(AppPreferences.load(from: defaults).defaultFontSize, expected)
        }
    }

    @MainActor
    func testDefaultZoomChangesAreSavedAndReportedImmediately() {
        let defaults = freshDefaults()
        let model = PreferencesModel(defaults: defaults)
        var sizes: [Float?] = []
        model.onChange = { _, new in sizes.append(new.defaultFontSize) }
        model.preferences.defaultFontSize = 18
        XCTAssertEqual(AppPreferences.load(from: defaults).defaultFontSize, 18)
        model.preferences.defaultFontSize = nil
        XCTAssertNil(AppPreferences.load(from: defaults).defaultFontSize)
        XCTAssertEqual(sizes, [18, nil])
    }

    func testTerminalThemePersistsAsGhosttysValue() {
        let defaults = freshDefaults()
        XCTAssertNil(AppPreferences.load(from: defaults).terminalTheme)
        var preferences = AppPreferences()
        preferences.terminalTheme = TerminalTheme("Dracula")
        preferences.save(to: defaults)
        XCTAssertEqual(defaults.string(forKey: AppPreferences.Key.terminalTheme), "Dracula")
        preferences.terminalTheme = TerminalTheme(" Night Owl ", dark: "Dracula")
        preferences.save(to: defaults)
        XCTAssertEqual(defaults.string(forKey: AppPreferences.Key.terminalTheme), "light:Night Owl,dark:Dracula")
        XCTAssertEqual(AppPreferences.load(from: defaults).terminalTheme, preferences.terminalTheme)
        preferences.terminalTheme = nil
        preferences.save(to: defaults)
        XCTAssertNil(defaults.object(forKey: AppPreferences.Key.terminalTheme))
    }

    func testTerminalThemeReadsOnlyWhatGhosttyCanCarry() {
        XCTAssertEqual(TerminalTheme(configValue: "dark: B , light:A"), TerminalTheme("A", dark: "B"))
        for refused in ["", " ", "a,b", "light:A", "light:A,light:B", "light:A,dark:", "x:A,dark:B", "A\nB", "light:A,dark:B,c"] {
            XCTAssertNil(TerminalTheme(configValue: refused), refused)
        }
        let defaults = freshDefaults()
        defaults.set(true, forKey: AppPreferences.Key.terminalTheme)
        XCTAssertNil(AppPreferences.load(from: defaults).terminalTheme)
    }

    func testAvailableThemesMergeBundledAndUserThemes() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent("amx-themes-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: root) }
        let bundled = root.appendingPathComponent("bundled"), user = root.appendingPathComponent("user")
        for (dir, names) in [(bundled, ["Dracula", "3024 Day", ".DS_Store"]), (user, ["Dracula", "mine", "bad,name"])] {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            for name in names { FileManager.default.createFile(atPath: dir.appendingPathComponent(name).path, contents: Data()) }
        }
        XCTAssertEqual(TerminalTheme.available(bundled: bundled, user: user), ["3024 Day", "Dracula", "mine"])
        XCTAssertEqual(TerminalTheme.available(bundled: bundled, user: root.appendingPathComponent("missing")),
                       ["3024 Day", "Dracula"])
        XCTAssertTrue(TerminalTheme.available().contains("Catppuccin Mocha"), "the app bundles Ghostty's themes")
    }

    @MainActor
    func testThemeChoicesAreSavedAndReportedImmediately() {
        let defaults = freshDefaults()
        let model = PreferencesModel(defaults: defaults)
        var themes: [TerminalTheme?] = []
        model.onChange = { old, new in if old.terminalTheme != new.terminalTheme { themes.append(new.terminalTheme) } }

        model.separateDarkTheme = true
        XCTAssertNil(model.preferences.terminalTheme, "no Dark Mode theme without a theme")
        model.themeName = "Night Owl"
        model.separateDarkTheme = true
        XCTAssertEqual(model.darkThemeName, "Night Owl", "starts from the theme in use")
        model.darkThemeName = "Dracula"
        model.themeName = "3024 Day"
        XCTAssertEqual(AppPreferences.load(from: defaults).terminalTheme, TerminalTheme("3024 Day", dark: "Dracula"))
        model.separateDarkTheme = false
        model.themeName = nil
        XCTAssertNil(defaults.object(forKey: AppPreferences.Key.terminalTheme))
        XCTAssertEqual(themes, [
            TerminalTheme("Night Owl"), TerminalTheme("Night Owl", dark: "Night Owl"),
            TerminalTheme("Night Owl", dark: "Dracula"), TerminalTheme("3024 Day", dark: "Dracula"),
            TerminalTheme("3024 Day"), nil,
        ])

        model.availableThemes = ["Dracula"]
        model.themeName = "Gone"
        XCTAssertEqual(model.themeChoices, ["Dracula", "Gone"], "a chosen theme stays listed")
    }

    /// A Settings theme reaches the config every terminal is built from, and
    /// changing it reloads the open ones without resetting their zoom.
    func testATerminalThemeChangeReloadsOpenTerminals() {
        var old = AppPreferences()
        XCTAssertNil(AppDelegate.terminalOverrides(old).theme)
        var new = old
        new.terminalTheme = TerminalTheme("Night Owl", dark: "Dracula")
        XCTAssertEqual(AppDelegate.terminalOverrides(new).lines.last, "theme = light:Night Owl,dark:Dracula")
        XCTAssertEqual(AppDelegate.terminalReload(from: old, to: new), .keepZoom)
        XCTAssertEqual(AppDelegate.terminalReload(from: new, to: old), .keepZoom)
        new.defaultFontSize = 18
        XCTAssertEqual(AppDelegate.terminalReload(from: old, to: new), .resetFontSize, "a reset reloads the theme too")
        old = new
        new.recentDays = 3
        XCTAssertNil(AppDelegate.terminalReload(from: old, to: new))
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
    func testBinaryPathIsCommittedOnlyWhenEditingEnds() {
        let defaults = freshDefaults()
        let model = PreferencesModel(defaults: defaults)
        var changes: [(AppPreferences, AppPreferences)] = []
        model.onChange = { changes.append(($0, $1)) }

        for prefix in ["/", "/us", "/usr/loc"] {
            model.binaryPathDraft = prefix
        }
        XCTAssertTrue(changes.isEmpty, "typing changes nothing the app runs")
        XCTAssertEqual(model.preferences.binaryPath, "")
        XCTAssertNil(defaults.object(forKey: AMXBinaryResolver.overrideDefaultsKey))
        XCTAssertNil(model.binaryOverrideProblem, "a partial path is not judged until it is committed")

        model.binaryPathDraft = "/no/such/amx"
        model.commitBinaryPath()
        XCTAssertEqual(changes.count, 1)
        XCTAssertEqual(changes.first?.1.binaryPath, "/no/such/amx")
        XCTAssertEqual(defaults.string(forKey: AMXBinaryResolver.overrideDefaultsKey), "/no/such/amx")
        XCTAssertEqual(model.binaryOverrideProblem, "Not an executable file.")
        model.commitBinaryPath()
        XCTAssertEqual(changes.count, 1, "committing again changes nothing")

        // A path chosen in the panel, or written elsewhere, shows in the field.
        model.preferences.binaryPath = "/bin/ls"
        XCTAssertEqual(model.binaryPathDraft, "/bin/ls")
        defaults.set("/bin/cat", forKey: AMXBinaryResolver.overrideDefaultsKey)
        model.refresh()
        XCTAssertEqual(model.binaryPathDraft, "/bin/cat")
    }

    @MainActor
    func testEnvironmentIsCommittedOnlyWhenEditingEnds() {
        let defaults = freshDefaults()
        let model = PreferencesModel(defaults: defaults)
        var changes: [(AppPreferences, AppPreferences)] = []
        model.onChange = { changes.append(($0, $1)) }

        for prefix in ["/", "/us", "/usr/bi"] {
            model.environmentDrafts["PATH"] = prefix
        }
        XCTAssertTrue(changes.isEmpty, "typing a PATH changes nothing amx runs with")
        XCTAssertEqual(model.preferences.environment, [:])
        XCTAssertNil(defaults.object(forKey: EnvironmentOverrides.defaultsKey))

        model.environmentDrafts["PATH"] = "/usr/bin:/bin"
        model.commitEnvironment()
        XCTAssertEqual(changes.count, 1)
        XCTAssertEqual(changes.first?.1.environment, ["PATH": "/usr/bin:/bin"])
        XCTAssertEqual(EnvironmentOverrides.load(from: defaults), ["PATH": "/usr/bin:/bin"])
        model.commitEnvironment()
        XCTAssertEqual(changes.count, 1, "committing again changes nothing")

        model.environmentDrafts["PATH"] = ""
        model.commitEnvironment()
        XCTAssertEqual(model.preferences.environment, [:], "an emptied field removes the override")
        XCTAssertEqual(model.environmentDrafts, [:])

        // An override written elsewhere shows in the field.
        EnvironmentOverrides.save(["AMX_CONFIG": "~/x.toml"], to: defaults)
        model.refresh()
        XCTAssertEqual(model.environmentDrafts, ["AMX_CONFIG": "~/x.toml"])
    }

    @MainActor
    func testWordingOfTheMenuBarSection() {
        XCTAssertEqual(PreferencesModel.recentDaysLabel(1), "Recent: last day")
        XCTAssertEqual(PreferencesModel.recentDaysLabel(7), "Recent: last 7 days")
        XCTAssertEqual(PreferencesModel.recentLimitLabel(1), "Recent: at most 1 conversation")
        XCTAssertEqual(PreferencesModel.recentLimitLabel(10), "Recent: at most 10 conversations")
        XCTAssertNil(PreferencesModel.workingCountNote(.glyphs))
        XCTAssertEqual(PreferencesModel.workingCountNote(.compact), "Only the “Glyphs and counts” status shows a working count.")
        XCTAssertNotNil(PreferencesModel.workingCountNote(.iconOnly))
    }

    @MainActor
    func testSettingsSaysWhenNotificationsAreNotAllowed() {
        XCTAssertEqual(PreferencesModel.notificationPermissionNote(.denied),
                       "Not allowed — macOS blocks Agent Mux's notifications.")
        XCTAssertNil(PreferencesModel.notificationPermissionNote(.authorized))
        XCTAssertNil(PreferencesModel.notificationPermissionNote(.notDetermined), "never asked is not denied")
        XCTAssertEqual(PreferencesModel.notificationSettingsURL.absoluteString,
                       "x-apple.systempreferences:com.apple.Notifications-Settings.extension")

        let model = PreferencesModel(defaults: freshDefaults())
        var refreshes = 0
        model.onRefreshAuthorization = { refreshes += 1 }
        model.refresh()
        XCTAssertEqual(refreshes, 1, "opening Settings reads the permission again")
    }

    @MainActor
    func testModelDescribesTheSnooze() {
        let model = PreferencesModel(defaults: freshDefaults())
        XCTAssertEqual(model.snoozeDescription, "Not snoozed")
        var notification = CompanionNotification()
        notification.indefinite = true
        model.daemonNotification = notification
        XCTAssertEqual(model.snoozeDescription, "Snoozed until resumed")
        notification.indefinite = false
        notification.snoozedUntil = Int64(Date().timeIntervalSince1970) + 3600
        model.daemonNotification = notification
        XCTAssertTrue(model.snoozeDescription.hasPrefix("Snoozed until "), "snoozed_until is a Unix second")
        XCTAssertNotEqual(model.snoozeDescription, "Not snoozed")
        XCTAssertTrue(model.isSnoozed)
        model.preferences.kittySocketOverride = "/tmp/kitty"
        XCTAssertNotNil(model.kittySocketProblem)
        model.preferences.kittySocketOverride = "tcp:localhost:1234"
        XCTAssertNil(model.kittySocketProblem)
    }

    @MainActor
    func testResumeClearsTheSnoozePreferencesShows() {
        let defaults = freshDefaults()
        let model = PreferencesModel(defaults: defaults)
        var asked = 0
        model.onResumeSnooze = { asked += 1 }
        XCTAssertFalse(model.isSnoozed)

        // With the feed live the snooze is the daemon's, and the app clears it.
        var notification = CompanionNotification()
        notification.indefinite = true
        model.daemonNotification = notification
        model.preferences.snoozeUntil = .distantFuture
        XCTAssertTrue(model.isSnoozed)
        model.resumeSnooze()
        XCTAssertEqual(asked, 1)
        XCTAssertEqual(model.preferences.snoozeUntil, .distantFuture, "the daemon's answer is what gets mirrored")
        model.daemonNotification = CompanionNotification()
        XCTAssertFalse(model.isSnoozed, "the daemon's snooze outranks the stored one")

        // With no feed there is only the stored snooze to forget.
        model.daemonNotification = nil
        XCTAssertTrue(model.isSnoozed)
        model.resumeSnooze()
        XCTAssertEqual(asked, 1, "nothing is sent to a daemon that is not there")
        XCTAssertNil(model.preferences.snoozeUntil)
        XCTAssertNil(defaults.object(forKey: "amxSnoozeUntil"), "so a restarted daemon is not snoozed again")
        XCTAssertTrue(defaults.bool(forKey: SnoozeMirror.resumeQueuedKey),
                      "and the same daemon, still snoozed when the feed is back, is asked to resume")
        XCTAssertFalse(model.isSnoozed)
        XCTAssertEqual(model.snoozeDescription, "Not snoozed")

        model.preferences.snoozeUntil = Date().addingTimeInterval(-60)
        XCTAssertFalse(model.isSnoozed, "a snooze that ran out is not one to clear")
    }

    @MainActor
    func testResumeWithTheFeedLiveQueuesNothing() {
        let defaults = freshDefaults()
        let model = PreferencesModel(defaults: defaults)
        model.onResumeSnooze = {}
        var notification = CompanionNotification()
        notification.indefinite = true
        model.daemonNotification = notification
        model.resumeSnooze()
        XCTAssertFalse(defaults.bool(forKey: SnoozeMirror.resumeQueuedKey))
    }

    @MainActor
    func testOpeningSettingsReadsTheBackendStatusAgain() {
        let model = PreferencesModel(defaults: freshDefaults())
        var reads = 0
        model.onRefreshBackend = { reads += 1 }
        model.refresh()
        XCTAssertEqual(reads, 1)
        let controller = PreferencesWindowController(model: model)
        controller.show()
        defer { controller.window.close() }
        XCTAssertEqual(reads, 2, "showing Settings re-reads it rather than showing the launch-time status")
    }
}

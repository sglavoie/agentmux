import AppKit
import XCTest
@testable import AgentMux

final class WorkspaceSoundTests: XCTestCase {
    private var time: TimeInterval = 0
    private var played: [WorkspaceSound.Kind] = []
    private lazy var sound = WorkspaceSound(clock: { [unowned self] in self.time }, player: { [unowned self] in self.played.append($0) })

    private func rows(_ icons: [String], muted: Set<Int> = []) -> [WorkspaceRow] {
        let sessions = icons.enumerated().map { index, icon in
            var session = WorkspaceFixture.tmux(index, project: "api", icon: icon)
            session.muted = muted.contains(index)
            return session
        }
        return WorkspaceSidebar.groups(sessions, now: Date(), glyphs: .fallback, order: WorkspaceOrder()).flatMap(\.rows)
    }

    private func transition(_ old: [String], _ new: [String], muted: Set<Int> = [],
                            wasCurrent: Bool = true, current: Bool = true,
                            enabled: Bool = true, snoozed: Bool = false, visible: Set<Int> = []) {
        sound.transition(from: rows(old), to: rows(new, muted: muted),
                         wasCurrent: wasCurrent, current: current, enabled: enabled, snoozed: snoozed,
                         onScreen: Set(visible.map { .embedded(WorkspaceFixture.runtimeID($0)) }))
    }

    func testDistinctSystemSoundsForNeedsYouAndFinished() {
        for icon in ["key", "question", "error"] {
            transition(["bolt"], [icon])
            time += 2
        }
        transition(["bolt"], ["stop"])
        XCTAssertEqual(played, [.needsYou, .needsYou, .needsYou, .finished])
        XCTAssertNotEqual(WorkspaceSound.needsYouSound, WorkspaceSound.finishedSound)
        XCTAssertNotNil(NSSound(named: NSSound.Name(WorkspaceSound.needsYouSound)))
        XCTAssertNotNil(NSSound(named: NSSound.Name(WorkspaceSound.finishedSound)))
        XCTAssertLessThan(WorkspaceSound.Kind.finished.volume, WorkspaceSound.Kind.needsYou.volume)
    }

    func testOnlyClassEntrySounds() {
        for before in ["key", "question", "error"] {
            for after in ["key", "question", "error"] { transition([before], [after]) }
        }
        for icon in ["", "bolt", "robot", "spiral", "warning", "stop"] { transition([icon], [icon]) }
        transition(["bolt"], ["robot"])
        transition(["bolt"], ["warning"])
        transition([], ["key"])
        XCTAssertTrue(played.isEmpty)
        transition(["stop"], ["error"])
        time += 2
        transition(["key"], ["stop"])
        XCTAssertEqual(played, [.needsYou, .finished])
    }

    func testMuteSnoozePreferenceVisibilityAndFeedGatesDoNotSpendBudget() {
        for target in ["key", "stop"] {
            transition(["bolt"], [target], muted: [0])
            transition(["bolt"], [target], wasCurrent: false)
            transition(["bolt"], [target], current: false)
            transition(["bolt"], [target], enabled: false)
            transition(["bolt"], [target], snoozed: true)
            transition(["bolt"], [target], visible: [0])
        }
        XCTAssertTrue(played.isEmpty)
        transition(["bolt"], ["stop"])
        XCTAssertEqual(played, [.finished])
    }

    func testSharedTwoSecondBudgetAndNeedsYouPriority() {
        transition(["bolt", "bolt"], ["stop", "question"])
        XCTAssertEqual(played, [.needsYou])
        time = 1.999
        transition(["bolt"], ["stop"])
        XCTAssertEqual(played, [.needsYou])
        time = 2
        transition(["bolt"], ["stop"])
        XCTAssertEqual(played, [.needsYou, .finished])
        time = 4
        transition(["stop"], ["stop"])
        XCTAssertEqual(played.count, 2, "throttled transitions are never replayed")
        transition(["bolt", "bolt"], ["error", "stop"], muted: [0])
        XCTAssertEqual(played.last, .finished, "ineligible needs-you does not win")
        XCTAssertEqual(played.count, 3)
    }
}

@MainActor
final class WorkspaceSoundIntegrationTests: XCTestCase {
    typealias F = WorkspaceFixture
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!
    private var played: [WorkspaceSound.Kind] = []
    private var time: TimeInterval = 100

    override func setUp() async throws {
        app = FakeWorkspaceApp()
        workspace = WorkspaceWindowController(delegate: app, now: { Date(timeIntervalSince1970: 100) })
        workspace.sounds = WorkspaceSound(clock: { [unowned self] in self.time }, player: { [unowned self] in self.played.append($0) })
        workspace.sidebarMotionEnabled = { false }
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
    }

    override func tearDown() async throws {
        if workspace.isOpen { workspace.window.close() }
        app.store.onChange = nil
        workspace.sounds = nil
        workspace = nil
        app = nil
    }

    private func apply(_ icon: String, notification: CompanionNotification = CompanionNotification(), muted: Bool = false) {
        var session = F.tmux(1, project: "api", icon: icon)
        session.muted = muted
        var snapshot = F.snapshot([session])
        snapshot.notification = notification
        app.store.apply(snapshot: snapshot)
    }

    func testClosedWorkspaceInitialAndReconnectSnapshotsAreSilent() {
        apply("key")
        XCTAssertTrue(played.isEmpty)
        apply("stop")
        XCTAssertEqual(played, [.finished])
        XCTAssertFalse(workspace.isOpen)
        for reason in [SubscriptionClient.Disconnect.heartbeatLost, .reconnectRequested] {
            time += 2
            app.store.apply(.disconnected(reason, retryIn: 0))
            app.store.apply(.subscribed(SubscriptionAck()))
            workspace.refresh() // A redraw of the held snapshot is not a new baseline.
            apply("error")
            XCTAssertEqual(played.last, .finished)
            apply("stop")
            XCTAssertEqual(played.last, .finished)
        }
        XCTAssertEqual(played, [.finished, .finished, .finished])
    }

    func testLiveSnoozeMuteAndPreferenceDoNotReplaySuppressedTransitions() {
        apply("bolt")
        var notice = CompanionNotification()
        notice.indefinite = true
        apply("key", notification: notice)
        notice.indefinite = false
        apply("key", notification: notice)
        XCTAssertTrue(played.isEmpty)
        notice.snoozedUntil = 101
        apply("stop", notification: notice)
        XCTAssertTrue(played.isEmpty)
        notice.snoozedUntil = 100
        apply("error", notification: notice)
        XCTAssertEqual(played, [.needsYou], "expired live snooze allows a new transition")
        time += 2
        apply("stop", muted: true)
        apply("stop")
        workspace.playSounds = false
        apply("key")
        workspace.playSounds = true
        workspace.refresh()
        XCTAssertEqual(played.count, 1)
        apply("stop")
        XCTAssertEqual(played, [.needsYou, .finished])
    }

    func testAppProcessesSoundsBeforeOpeningWorkspaceAndAppliesPreferenceLive() throws {
        let suite = "WorkspaceSoundIntegrationTests-\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let menu = NSApp.mainMenu
        let badge = NSApp.dockTile.badgeLabel
        defer {
            defaults.removePersistentDomain(forName: suite)
            NSApp.mainMenu = menu
            NSApp.dockTile.badgeLabel = badge
        }
        let delegate = AppDelegate(preferencesModel: PreferencesModel(defaults: defaults))
        delegate.store.onChange = { [weak delegate] in delegate?.storeChanged() }
        var session = F.tmux(1, project: "api", icon: "bolt")
        delegate.store.apply(snapshot: F.snapshot([session]))
        let controller = try XCTUnwrap(delegate.workspace)
        defer { controller.constructedWindow?.close() }
        controller.sounds = WorkspaceSound(clock: { [unowned self] in self.time },
                                          player: { [unowned self] in self.played.append($0) })
        session.iconState = "key"
        delegate.store.apply(snapshot: F.snapshot([session]))
        XCTAssertEqual(played, [.needsYou])
        XCTAssertFalse(controller.isOpen)
        XCTAssertFalse(controller.constructedWindow?.isVisible ?? false)
        XCTAssertTrue(controller.terminals.isEmpty)

        let center = FakeNotificationCenter()
        let presenter = NotificationPresenter(center: center, enabled: true, schedule: { $0() })
        delegate.notifications = presenter
        let old = delegate.preferencesModel.preferences
        var updated = old
        updated.playSounds = false
        delegate.preferencesChanged(from: old, to: updated)
        XCTAssertFalse(presenter.playSounds)
        time += 2
        session.iconState = "stop"
        delegate.store.apply(snapshot: F.snapshot([session]))
        XCTAssertEqual(played.count, 1)
        delegate.preferencesChanged(from: updated, to: old)
        XCTAssertTrue(presenter.playSounds)
        session.iconState = "error"
        delegate.store.apply(snapshot: F.snapshot([session]))
        XCTAssertEqual(played, [.needsYou, .needsYou])
    }

    func testKeyWorkspaceSuppressesBothVisiblePanesButBackgroundWindowSounds() async throws {
        workspace.openForTesting()
        var live = [F.tmux(1, project: "api", icon: "bolt"), F.tmux(2, project: "api", icon: "bolt")]
        app.store.apply(snapshot: F.snapshot(live))
        workspace.activate(try XCTUnwrap(workspace.state.row(.embedded(F.runtimeID(1)))))
        XCTAssertTrue(workspace.split(with: .embedded(F.runtimeID(2))))
        NSApp.activate(ignoringOtherApps: true)
        workspace.window.makeKeyAndOrderFront(nil)
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertTrue(workspace.window.isKeyWindow)
        XCTAssertNotNil(workspace.otherShowing)
        live[0].iconState = "key"
        live[1].iconState = "stop"
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertTrue(played.isEmpty)
        let other = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 100, height: 100), styleMask: [.titled], backing: .buffered, defer: false)
        other.isReleasedWhenClosed = false
        defer { other.close() }
        other.makeKeyAndOrderFront(nil)
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertFalse(workspace.window.isKeyWindow)
        live[0].iconState = "stop"
        app.store.apply(snapshot: F.snapshot(live))
        XCTAssertEqual(played, [.finished])
    }
}

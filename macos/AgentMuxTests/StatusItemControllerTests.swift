import AppKit
import XCTest
@testable import AgentMux

@MainActor
final class StatusItemControllerTests: XCTestCase {
    private final class FakeDelegate: StatusItemDelegate {
        let store: SessionStore
        var actionsInput = MenuBuilder.ActionsInput()
        var attentionOrder = WorkspaceOrder()
        init(store: SessionStore) { self.store = store }
        func refreshDaemonStatus() {}
        func focus(_ row: MenuSessionRow) {}
        func perform(_ action: MenuAction) {}
        func toggleMute(_ row: MenuSessionRow) {}
        func showPreferences(_ sender: Any?) {}
        func menuWillOpen() {}
    }

    private var clock = Date(timeIntervalSince1970: 1_000)
    private var store: SessionStore!
    private var delegate: FakeDelegate!
    private var controller: StatusItemController!

    override func setUp() async throws {
        store = SessionStore(now: { [unowned self] in self.clock })
        delegate = FakeDelegate(store: store)
        controller = StatusItemController(delegate: delegate)
        store.onChange = { [unowned self] in self.controller.refresh() }
    }

    override func tearDown() async throws {
        NSStatusBar.system.removeStatusItem(controller.statusItem)
        controller = nil
    }

    private func snapshot() -> CompanionSnapshot {
        var snapshot = CompanionSnapshot()
        snapshot.daemon.ready = true
        return snapshot
    }

    private var titles: [String] { controller.menu.items.map(\.title) }

    func testTheIconDimsWhenTheFeedIsNotLive() {
        XCTAssertFalse(StatusItemController.appearsDisabled(.live))
        for feed in [FeedState.connecting, .notRunning, .daemonTooOld, .reconnecting("x")] {
            XCTAssertTrue(StatusItemController.appearsDisabled(feed), "\(feed)")
        }
        store.apply(snapshot: snapshot())
        XCTAssertEqual(controller.statusItem.button?.appearsDisabled, false)
        store.apply(.disconnected(.notRunning, retryIn: 1))
        XCTAssertEqual(controller.statusItem.button?.appearsDisabled, true)
    }

    func testTheAccessibilityLabelCountsAndNamesTheSnooze() {
        XCTAssertEqual(StatusItemController.accessibilityLabel(
            feedLive: true, attention: CompanionAttention(total: 2, stop: 2, working: 1), snoozed: true
        ), "Agent Mux, 2 waiting, 1 working, notifications snoozed")
        XCTAssertEqual(StatusItemController.accessibilityLabel(feedLive: true, attention: CompanionAttention(), snoozed: false),
                       "Agent Mux, nothing waiting")
        XCTAssertEqual(StatusItemController.accessibilityLabel(feedLive: false, attention: CompanionAttention(total: 3), snoozed: true),
                       "Agent Mux, daemon not connected")

        var live = snapshot()
        live.attention = CompanionAttention(total: 1, question: 1)
        store.apply(snapshot: live)
        XCTAssertEqual(controller.statusItem.button?.accessibilityLabel(), "Agent Mux, 1 waiting")
    }

    func testTheIconMarksASnooze() {
        store.apply(snapshot: snapshot())
        XCTAssertFalse(controller.showsSnoozed)
        let plainWidth = controller.statusItem.button?.image?.size.width ?? 0

        var snoozed = snapshot()
        snoozed.notification.indefinite = true
        store.apply(snapshot: snoozed)
        XCTAssertTrue(controller.showsSnoozed)
        XCTAssertGreaterThan(controller.statusItem.button?.image?.size.width ?? 0, plainWidth, "the bell-slash sits beside the icon")
        XCTAssertEqual(controller.statusItem.button?.image?.isTemplate, true)
        XCTAssertTrue(controller.statusItem.button?.toolTip?.contains("Snoozed until resumed") == true)
        XCTAssertTrue(controller.statusItem.button?.accessibilityLabel()?.hasSuffix("notifications snoozed") == true)

        store.apply(snapshot: snapshot())
        XCTAssertFalse(controller.showsSnoozed)
        XCTAssertEqual(controller.statusItem.button?.image?.size.width, plainWidth)
    }

    func testAnOpenMenuFollowsTheStoreAndSeesItsNotice() {
        store.apply(snapshot: snapshot())
        controller.menuNeedsUpdate(controller.menu)
        controller.menuWillOpen(controller.menu)
        XCTAssertTrue(controller.isMenuOpen)

        store.show(notice: "Kitty is not running.")
        XCTAssertTrue(titles.contains("Kitty is not running."), "the open menu shows a notice set while it is open")

        // Seen while open: once the hold passes, a snapshot clears it, and the
        // open menu drops the line.
        clock += SessionStore.noticeHold
        store.apply(snapshot: snapshot())
        XCTAssertFalse(titles.contains("Kitty is not running."))

        controller.menuDidClose(controller.menu)
        XCTAssertFalse(controller.isMenuOpen)
        store.show(notice: "Later")
        XCTAssertFalse(titles.contains("Later"), "a closed menu is rebuilt when it next opens")
        clock += SessionStore.noticeHold
        store.apply(snapshot: snapshot())
        XCTAssertEqual(store.notice, "Later", "a notice never shown stays")
    }

    func testAnOpenSubmenuHoldsTheRebuildUntilItCloses() throws {
        store.apply(snapshot: snapshot())
        controller.menuNeedsUpdate(controller.menu)
        controller.menuWillOpen(controller.menu)
        let submenu = try XCTUnwrap(controller.submenus.values.first)
        controller.menuWillOpen(submenu)
        let parents = controller.menu.items.filter { $0.submenu != nil }

        store.show(notice: "Kitty is not running.")
        XCTAssertFalse(titles.contains("Kitty is not running."), "the items an open submenu hangs from stay")
        XCTAssertEqual(controller.menu.items.filter { $0.submenu != nil }.map(ObjectIdentifier.init),
                       parents.map(ObjectIdentifier.init))

        controller.menuDidClose(submenu)
        XCTAssertTrue(titles.contains("Kitty is not running."), "shown once the submenu closes")
    }
}

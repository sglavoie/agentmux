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
}

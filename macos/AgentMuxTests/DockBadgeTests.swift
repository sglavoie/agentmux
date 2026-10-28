import AppKit
import ApplicationServices
import XCTest
import UserNotifications
@testable import AgentMux

@MainActor
final class DockBadgeTests: XCTestCase {
    func testBadgeReachesDockAfterAccessorySnapshotAndReopening() throws {
        guard AXIsProcessTrusted() else {
            throw XCTSkip("Accessibility access is needed to verify the badge in the Dock.")
        }
        let settingsRead = expectation(description: "notification settings")
        var badgesEnabled = false
        SystemNotificationCenter().authorizationStatus { _ in
            UNUserNotificationCenter.current().getNotificationSettings { settings in
                badgesEnabled = settings.badgeSetting == .enabled
                settingsRead.fulfill()
            }
        }
        wait(for: [settingsRead], timeout: 3)
        guard badgesEnabled else {
            throw XCTSkip("Dock badges must be allowed in System Settings to verify their appearance.")
        }
        let previousPolicy = NSApp.activationPolicy()
        let previousBadge = NSApp.dockTile.badgeLabel
        defer {
            NSApp.dockTile.badgeLabel = previousBadge
            NSApp.setActivationPolicy(previousPolicy)
        }
        NSApp.setActivationPolicy(.accessory)
        let delegate = AppDelegate()
        delegate.store.onChange = { [weak delegate] in delegate?.storeChanged() }
        var snapshot = CompanionSnapshot()
        snapshot.attention = CompanionAttention(total: 2, key: 2)
        delegate.store.apply(snapshot: snapshot)
        XCTAssertEqual(NSApp.dockTile.badgeLabel, "2")

        // The first snapshot can arrive before any window puts us in the Dock.
        delegate.workspaceVisible = true
        delegate.syncActivationPolicy()
        assertDockBadge("2")

        // An unchanged count must also survive closing and reopening a window.
        delegate.workspaceVisible = false
        delegate.syncActivationPolicy()
        delegate.settingsVisible = true
        delegate.syncActivationPolicy()
        assertDockBadge("2")

        snapshot.attention = CompanionAttention(total: 1, key: 1)
        delegate.store.apply(snapshot: snapshot)
        assertDockBadge("1")
        delegate.store.apply(.disconnected(.heartbeatLost, retryIn: 1))
        assertDockBadge(nil)
        delegate.store.apply(snapshot: snapshot)
        assertDockBadge("1")
        snapshot.attention = CompanionAttention()
        delegate.store.apply(snapshot: snapshot)
        assertDockBadge(nil)
    }

    /// Read the Dock process itself: badgeLabel alone can hold a count the
    /// Dock has never received. Match this test host's URL, not the installed app.
    private func dockItem() -> AXUIElement? {
        guard let dock = NSRunningApplication.runningApplications(withBundleIdentifier: "com.apple.dock").first else { return nil }
        func find(_ element: AXUIElement, depth: Int) -> AXUIElement? {
            guard depth < 5 else { return nil }
            if let url = attribute(element, "AXURL") as? URL,
               url.standardizedFileURL == Bundle.main.bundleURL.standardizedFileURL { return element }
            for child in attribute(element, kAXChildrenAttribute) as? [AXUIElement] ?? [] {
                if let found = find(child, depth: depth + 1) { return found }
            }
            return nil
        }
        return find(AXUIElementCreateApplication(dock.processIdentifier), depth: 0)
    }

    private func attribute(_ element: AXUIElement, _ name: String) -> CFTypeRef? {
        var value: CFTypeRef?
        AXUIElementCopyAttributeValue(element, name as CFString, &value)
        return value
    }

    private func assertDockBadge(_ expected: String?, file: StaticString = #filePath, line: UInt = #line) {
        let deadline = Date().addingTimeInterval(3)
        repeat {
            if let item = dockItem(), attribute(item, "AXStatusLabel") as? String == expected { return }
            RunLoop.main.run(until: Date().addingTimeInterval(0.05))
        } while Date() < deadline
        let item = dockItem()
        XCTAssertNotNil(item, "The test host must have a Dock icon", file: file, line: line)
        XCTAssertEqual(item.flatMap { attribute($0, "AXStatusLabel") as? String }, expected, file: file, line: line)
    }
}

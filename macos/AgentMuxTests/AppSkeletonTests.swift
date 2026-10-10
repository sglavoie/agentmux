import AppKit
import XCTest
@testable import AgentMux

final class AppSkeletonTests: XCTestCase {
    func testHostBundleIdentity() {
        XCTAssertEqual(Bundle.main.bundleIdentifier, "io.github.sglavoie.agentmux")
        XCTAssertEqual(Bundle.main.bundleURL.lastPathComponent, "Agent Mux.app")
    }

    func testAppIsAnAccessoryWithoutADockIcon() {
        XCTAssertEqual(Bundle.main.infoDictionary?["LSUIElement"] as? Bool, true)
        XCTAssertEqual(NSApp.activationPolicy(), .accessory)
    }

    @MainActor
    func testHostedTestsAreDetected() {
        XCTAssertTrue(AppDelegate.isRunningTests)
        // The guard keeps the hosted app from building its status item or
        // resolving amx, so the delegate is still idle.
        let delegate = NSApp.delegate as? AppDelegate
        XCTAssertNotNil(delegate)
        XCTAssertNil(delegate?.statusItem)
        XCTAssertNil(delegate?.command)
        // No terminal is wanted yet, so Ghostty has no app object either, and
        // an attachment has nothing to run until amx is resolved.
        XCTAssertNil(delegate?.terminals)
        XCTAssertNil(delegate?.attachmentConfiguration(runtimeSessionID: "rs-0123456789abcdef0123456789abcdef"))
    }

    func testAppIconShipsInTheBundle() throws {
        let info = Bundle.main.infoDictionary ?? [:]
        XCTAssertEqual(info["CFBundleIconName"] as? String, "AppIcon")
        let file = try XCTUnwrap(info["CFBundleIconFile"] as? String)
        XCTAssertNotNil(Bundle.main.url(forResource: file, withExtension: file.hasSuffix(".icns") ? nil : "icns"))
    }

    func testStatusIconIsTheTemplateAsset() throws {
        let asset = try XCTUnwrap(NSImage(named: "StatusIcon"))
        XCTAssertEqual(asset.size, NSSize(width: 18, height: 18))
        let icon = StatusItemController.icon()
        XCTAssertTrue(icon.isTemplate)
        XCTAssertEqual(icon.size, NSSize(width: 18, height: 18))
        XCTAssertEqual(icon.tiffRepresentation, asset.tiffRepresentation, "the SF Symbol fallback was used")
    }

    func testSingleInstanceIgnoresItself() {
        let others = SingleInstance.otherInstances(bundleIdentifier: "x", currentPID: 42) { _ in [42, 7, 0] }
        XCTAssertEqual(others, [7])
        XCTAssertEqual(SingleInstance.otherInstances(bundleIdentifier: "x", currentPID: 42) { _ in [42] }, [])
    }
}

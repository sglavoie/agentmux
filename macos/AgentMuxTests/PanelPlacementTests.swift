import AppKit
import XCTest
@testable import AgentMux

/// Where the answer panels open, as frames in and a frame out.
final class PanelPlacementTests: XCTestCase {
    @MainActor
    func testReviewWindowRestoresSavedPlacementInsteadOfCentering() throws {
        let screen = try XCTUnwrap(NSScreen.main).visibleFrame
        let name = "ReviewPlacementTest-" + UUID().uuidString
        defer { NSWindow.removeFrame(usingName: name) }
        func window() -> NSWindow {
            let result = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 500, height: 300),
                                  styleMask: [.titled, .resizable], backing: .buffered, defer: false)
            result.isReleasedWhenClosed = false
            return result
        }
        let first = window()
        let saved = NSRect(x: screen.minX + 30, y: screen.minY + 40, width: 560, height: 340)
        first.setFrame(saved, display: false)
        first.saveFrame(usingName: name)
        first.close()
        let reopened = window()
        defer { reopened.close() }
        ReviewWindowPlacement.restore(reopened, name: name)
        XCTAssertEqual(reopened.frame, saved)
    }
    /// A laptop with an external display to its right, menu bar on the left.
    private let left = PanelPlacement.Screen(
        frame: CGRect(x: 0, y: 0, width: 1440, height: 900),
        visibleFrame: CGRect(x: 0, y: 70, width: 1440, height: 805)
    )
    private let right = PanelPlacement.Screen(
        frame: CGRect(x: 1440, y: -200, width: 2560, height: 1440),
        visibleFrame: CGRect(x: 1440, y: -200, width: 2560, height: 1440)
    )
    private var placement: PanelPlacement { PanelPlacement(screens: [left, right]) }
    private let size = CGSize(width: 520, height: 440)

    func testTheWorkspaceWindowsScreenWinsOverThePointer() {
        let workspace = CGRect(x: 1600, y: 100, width: 1100, height: 700)
        XCTAssertEqual(placement.screen(workspace: workspace, pointer: CGPoint(x: 100, y: 100)), right)
    }

    func testAWorkspaceAcrossTwoScreensPicksTheOneMostOfItIsOn() {
        let mostlyLeft = CGRect(x: 800, y: 100, width: 1000, height: 600)
        XCTAssertEqual(placement.screen(workspace: mostlyLeft, pointer: CGPoint(x: 3000, y: 0)), left)
        let mostlyRight = CGRect(x: 1200, y: 100, width: 1000, height: 600)
        XCTAssertEqual(placement.screen(workspace: mostlyRight, pointer: CGPoint(x: 100, y: 100)), right)
    }

    func testWithoutAWorkspaceThePointersScreenIsUsed() {
        XCTAssertEqual(placement.screen(workspace: nil, pointer: CGPoint(x: 2000, y: 500)), right)
        XCTAssertEqual(placement.screen(workspace: nil, pointer: CGPoint(x: 10, y: 10)), left)
        // The top edge of a screen is where the pointer rests at the menu bar.
        XCTAssertEqual(placement.screen(workspace: nil, pointer: CGPoint(x: 10, y: 900)), left)
    }

    func testAWorkspaceOffEveryScreenFallsBackToThePointer() {
        let gone = CGRect(x: -5000, y: 0, width: 800, height: 600)
        XCTAssertEqual(placement.screen(workspace: gone, pointer: CGPoint(x: 2000, y: 500)), right)
    }

    func testAPointerOffEveryScreenFallsBackToTheFirst() {
        XCTAssertEqual(placement.screen(workspace: nil, pointer: CGPoint(x: -100, y: -100)), left)
        XCTAssertNil(PanelPlacement(screens: []).screen(workspace: nil, pointer: .zero))
        XCTAssertNil(PanelPlacement(screens: []).frame(size: size, saved: nil, workspace: nil, pointer: .zero))
    }

    func testThePanelIsCentredOnTheChosenScreensUsableArea() {
        let frame = placement.frame(size: size, saved: nil, workspace: nil, pointer: CGPoint(x: 2000, y: 500))
        XCTAssertEqual(frame, CGRect(x: 2460, y: 300, width: 520, height: 440))
        let onLeft = placement.frame(size: size, saved: nil, workspace: nil, pointer: CGPoint(x: 10, y: 10))
        XCTAssertEqual(onLeft, CGRect(x: 460, y: 253, width: 520, height: 440))
    }

    func testAPanelLargerThanTheScreenIsShrunkToFit() {
        let frame = placement.frame(size: CGSize(width: 3000, height: 2000), saved: nil, workspace: nil, pointer: CGPoint(x: 10, y: 10))
        XCTAssertEqual(frame, left.visibleFrame)
    }

    func testADraggedFrameIsKeptWhileItIsOnAConnectedScreen() {
        let dragged = CGRect(x: 3000, y: 600, width: 600, height: 500)
        let workspace = CGRect(x: 100, y: 100, width: 800, height: 600)
        XCTAssertEqual(placement.frame(size: size, saved: dragged, workspace: workspace, pointer: .zero), dragged)
    }

    /// A panel resized on a large display and reopened after the display
    /// grew smaller keeps its place but fits the usable area again.
    func testASavedFrameLargerThanItsScreenIsMadeToFit() {
        let resized = CGRect(x: 200, y: 100, width: 1400, height: 900)
        XCTAssertEqual(
            placement.frame(size: size, saved: resized, workspace: nil, pointer: .zero),
            CGRect(x: 40, y: 70, width: 1400, height: 805)
        )
    }

    func testADraggedFrameOnADisconnectedScreenIsDropped() {
        let dragged = CGRect(x: 3000, y: 600, width: 600, height: 500)
        let laptopOnly = PanelPlacement(screens: [left])
        XCTAssertFalse(laptopOnly.isOnScreen(dragged))
        XCTAssertEqual(
            laptopOnly.frame(size: size, saved: dragged, workspace: nil, pointer: CGPoint(x: 3100, y: 700)),
            CGRect(x: 460, y: 253, width: 520, height: 440)
        )
    }

    func testAFrameMostlyOffScreenOrEmptyIsNotOnScreen() {
        // Only a corner is left on the laptop: its middle is off every screen.
        XCTAssertFalse(placement.isOnScreen(CGRect(x: -500, y: -400, width: 600, height: 500)))
        // Under the menu bar is not usable.
        XCTAssertFalse(placement.isOnScreen(CGRect(x: 100, y: 870, width: 200, height: 20)))
        XCTAssertFalse(placement.isOnScreen(CGRect(x: 100, y: 100, width: 0, height: 0)))
        XCTAssertTrue(placement.isOnScreen(CGRect(x: 100, y: 100, width: 300, height: 200)))
    }

    func testADraggedFrameIsKeptInDefaults() throws {
        let suite = "amx.tests.panel-frame.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }

        let store = PanelFrameStore(key: "frame", defaults: defaults)
        XCTAssertNil(store.load())
        let frame = CGRect(x: 1500.5, y: -120, width: 520, height: 440)
        store.save(frame)
        XCTAssertEqual(store.load(), frame)
        defaults.set("not a rect", forKey: "frame")
        XCTAssertNil(store.load())
    }

    func testWithoutDefaultsNothingIsKept() {
        let store = PanelFrameStore(key: "frame", defaults: nil)
        store.save(CGRect(x: 1, y: 2, width: 3, height: 4))
        XCTAssertNil(store.load())
    }
}

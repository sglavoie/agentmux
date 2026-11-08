import AppKit
import GhosttyKit
import XCTest
@testable import AgentMux

/// What a Control-click and Look Up do, each on a surface of its own that
/// prints `hello world` and may first turn on mouse tracking.
final class GhosttyClickMenuTests: XCTestCase {
    private func surface(mouseModes: [Int] = [], _ body: (Ghostty.SurfaceView, NSWindow) throws -> Void) throws {
        let ghostty = Ghostty.App { Ghostty.Config(userConfig: nil) }
        defer { ghostty.shutdown() }
        let window = GhosttyTestWindow.offscreen()
        defer { window.close() }
        let enable = mouseModes.isEmpty ? "" : "printf '" + mouseModes.map { "\\033[?\($0)h" }.joined() + "'; "
        let view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", enable + "printf 'hello world\\n'; exec cat"])))
        window.contentView = view
        defer { window.contentView = nil }
        XCTAssertTrue(window.makeFirstResponder(view))
        let surface = try XCTUnwrap(view.surface)
        if !mouseModes.isEmpty {
            ghosttyWait("the program to take the mouse") { ghostty_surface_mouse_captured(surface) }
        }
        try body(view, window)
    }

    private func mouse(_ type: NSEvent.EventType, _ p: NSPoint, _ flags: NSEvent.ModifierFlags = [], in window: NSWindow) -> NSEvent {
        NSEvent.mouseEvent(with: type, location: p, modifierFlags: flags, timestamp: ProcessInfo.processInfo.systemUptime,
                           windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1)!
    }

    func testAControlClickShowsTheMenuUnlessTheProgramTracksTheMouse() throws {
        try surface { view, window in
            let p = NSPoint(x: 10, y: 10)
            XCTAssertNil(view.menu(for: mouse(.leftMouseDown, p, in: window)), "a plain click has no menu")
            XCTAssertEqual(view.menu(for: mouse(.leftMouseDown, p, .control, in: window))?.items.map(\.title),
                           ["Copy", "Paste", "Select All"])
            XCTAssertNotNil(view.menu(for: mouse(.rightMouseDown, p, in: window)))
            XCTAssertNil(view.menu(for: mouse(.otherMouseDown, p, in: window)))
        }
        try surface(mouseModes: [1002, 1006]) { view, window in
            XCTAssertNil(view.menu(for: mouse(.leftMouseDown, NSPoint(x: 10, y: 10), .control, in: window)),
                         "a program tracking the mouse is sent the Control-click")
        }
    }

    /// Look Up defines the word under the pointer, in the terminal's font.
    func testLookUpNamesTheWordUnderThePointer() throws {
        try surface { view, window in
            XCTAssertTrue(view.responds(to: #selector(NSResponder.quickLook(with:))))
            let cell = view.cellSize
            let p = NSPoint(x: 2 + 7.5 * cell.width, y: view.bounds.height - 2 - cell.height / 2)
            ghosttyWait("the line to be drawn") {
                view.mouseMoved(with: self.mouse(.mouseMoved, p, in: window))
                return view.lookUpWord()?.0.string == "world"
            }
            let (word, at) = try XCTUnwrap(view.lookUpWord())
            XCTAssertNotNil(word.attribute(.font, at: 0, effectiveRange: nil), "drawn in the terminal's font")
            XCTAssertEqual(at.x, 2 + 6 * cell.width, accuracy: 1, "where the word starts")

            view.mouseMoved(with: mouse(.mouseMoved, NSPoint(x: view.bounds.width - 20, y: 20), in: window))
            XCTAssertNil(view.lookUpWord(), "nothing to define over blank cells")
        }
    }
}

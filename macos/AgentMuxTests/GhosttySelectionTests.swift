import AppKit
import GhosttyKit
import XCTest
@testable import AgentMux

/// Where a drag selection starts. Each case runs on a surface of its own: a
/// press with Shift, or on a surface that already has a selection, extends
/// what is there instead of starting where it lands.
final class GhosttySelectionTests: XCTestCase {
    private func mouse(_ type: NSEvent.EventType, _ p: NSPoint, in window: NSWindow) -> NSEvent {
        NSEvent.mouseEvent(with: type, location: p, modifierFlags: [], timestamp: ProcessInfo.processInfo.systemUptime,
                           windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1)!
    }

    private func selectedText(_ view: Ghostty.SurfaceView) -> String? {
        guard let surface = view.surface else { return nil }
        var text = ghostty_text_s()
        guard ghostty_surface_read_selection(surface, &text) else { return nil }
        defer { ghostty_surface_free_text(surface, &text) }
        return String(cString: text.text)
    }

    /// Drags along the first row of `LINE 2990 needle-2990` from `fraction`
    /// of the way across cell `column` to past the end of the line, and
    /// returns what ended up selected.
    private func dragSelection(fromColumn column: Int, at fraction: Double, moveFirst: Bool = true) throws -> String? {
        let ghostty = Ghostty.App { Ghostty.Config(userConfig: nil) }
        defer { ghostty.shutdown() }
        let window = GhosttyTestWindow.offscreen()
        defer { window.close() }
        let view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", "printf 'LINE 2990 needle-2990\\n'; exec cat"])))
        window.contentView = view
        defer { window.contentView = nil }
        XCTAssertTrue(window.makeFirstResponder(view))
        window.reportsKey = true
        ghosttyWait("the line to be drawn") {
            view.performBindingAction("select_all")
            return self.selectedText(view)?.contains("needle-2990") == true
        }
        // A plain click away from the text drops that selection and leaves
        // the pointer, as Ghostty knows it, far from where the drag starts.
        let away = NSPoint(x: view.bounds.width - 20, y: 20)
        view.mouseMoved(with: mouse(.mouseMoved, away, in: window))
        view.mouseDown(with: mouse(.leftMouseDown, away, in: window))
        view.mouseUp(with: mouse(.leftMouseUp, away, in: window))
        XCTAssertNil(selectedText(view), "a plain click clears the selection")

        // Ghostty's default padding is 2pt; the view's origin is bottom left.
        let cell = view.cellSize
        let y = view.bounds.height - 2 - cell.height / 2
        let start = NSPoint(x: 2 + (Double(column) + fraction) * cell.width, y: y)
        let end = NSPoint(x: 2 + 21.5 * cell.width, y: y)
        if moveFirst { view.mouseMoved(with: mouse(.mouseMoved, start, in: window)) }
        view.mouseDown(with: mouse(.leftMouseDown, start, in: window))
        for step in 1...40 {
            let x = start.x + (end.x - start.x) * Double(step) / 40
            view.mouseDragged(with: mouse(.leftMouseDragged, NSPoint(x: x, y: y), in: window))
        }
        view.mouseUp(with: mouse(.leftMouseUp, end, in: window))
        return selectedText(view)
    }

    func testASelectionStartsAtTheCellThePressIsIn() throws {
        XCTAssertEqual(try dragSelection(fromColumn: 0, at: 0.25), "LINE 2990 needle-2990")
        XCTAssertEqual(try dragSelection(fromColumn: 0, at: 0.5), "LINE 2990 needle-2990")
        XCTAssertEqual(try dragSelection(fromColumn: 5, at: 0.25), "2990 needle-2990")
        XCTAssertEqual(try dragSelection(fromColumn: 5, at: 0.5), "2990 needle-2990")
    }

    /// Ghostty's own rule, pinned so a new pin that moves it is noticed: a
    /// press well past the middle of a cell starts at the next one.
    func testAPressLateInACellStartsAtTheNextCell() throws {
        XCTAssertEqual(try dragSelection(fromColumn: 0, at: 0.75), "INE 2990 needle-2990")
        XCTAssertEqual(try dragSelection(fromColumn: 5, at: 0.75), "990 needle-2990")
    }

    /// The press carries its own position. Without that, a press no move
    /// preceded was anchored wherever the pointer was last reported, and the
    /// drag selected nothing.
    func testAPressWithNoMoveBeforeItStillAnchorsWhereItLands() throws {
        XCTAssertEqual(try dragSelection(fromColumn: 5, at: 0.5, moveFirst: false), "2990 needle-2990")
        XCTAssertEqual(try dragSelection(fromColumn: 0, at: 0.25, moveFirst: false), "LINE 2990 needle-2990")
    }
}

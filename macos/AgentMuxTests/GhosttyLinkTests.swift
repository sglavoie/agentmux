import AppKit
import GhosttyKit
import XCTest
@testable import AgentMux

/// Which click opens a link. Nothing is opened on the machine running the
/// tests: the app's opener is replaced for each case.
final class GhosttyLinkTests: XCTestCase {
    private static let link = "http://127.0.0.1:47615/s15-link"
    private var savedOpener: ((URL) -> Void)!

    override func setUp() {
        savedOpener = Ghostty.App.openLink
    }

    override func tearDown() {
        Ghostty.App.openLink = savedOpener
    }

    private func mouse(_ type: NSEvent.EventType, _ p: NSPoint, _ flags: NSEvent.ModifierFlags, in window: NSWindow) -> NSEvent {
        NSEvent.mouseEvent(with: type, location: p, modifierFlags: flags, timestamp: ProcessInfo.processInfo.systemUptime,
                           windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1)!
    }

    /// Clicks the link with `flags` held, on a surface of its own whose
    /// program first turns on the given mouse-tracking modes, and returns
    /// what the app was asked to open and whether the program had the mouse.
    private func click(
        _ flags: NSEvent.ModifierFlags, mouseModes: [Int] = [],
        hovering: (Ghostty.SurfaceView) throws -> Void = { _ in }
    ) throws -> (opened: [String], captured: Bool, hovered: String?) {
        var opened: [String] = []
        Ghostty.App.openLink = { opened.append($0.absoluteString) }
        let ghostty = Ghostty.App { Ghostty.Config(userConfig: nil) }
        defer { ghostty.shutdown() }
        let window = GhosttyTestWindow.offscreen()
        defer { window.close() }
        let enable = mouseModes.isEmpty ? "" : "printf '" + mouseModes.map { "\\033[?\($0)h" }.joined() + "'; "
        let view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", enable + "printf 'link: \(Self.link)\\n'; exec cat"])))
        window.contentView = view
        defer { window.contentView = nil }
        XCTAssertTrue(window.makeFirstResponder(view))
        window.reportsKey = true
        let surface = try XCTUnwrap(view.surface)
        ghosttyWait("the link to be drawn") {
            view.performBindingAction("select_all")
            var text = ghostty_text_s()
            guard ghostty_surface_read_selection(surface, &text) else { return false }
            defer { ghostty_surface_free_text(surface, &text) }
            return String(cString: text.text).contains("s15-link")
        }
        if !mouseModes.isEmpty {
            ghosttyWait("the program to take the mouse") { ghostty_surface_mouse_captured(surface) }
        }
        let captured = ghostty_surface_mouse_captured(surface)
        // The link starts at column 6; this is well inside it, on row 0.
        let cell = view.cellSize
        let p = NSPoint(x: 2 + 18.5 * cell.width, y: view.bounds.height - 2 - cell.height / 2)
        view.mouseMoved(with: mouse(.mouseMoved, NSPoint(x: p.x - 3, y: p.y), flags, in: window))
        view.mouseMoved(with: mouse(.mouseMoved, p, flags, in: window))
        RunLoop.current.run(until: Date().addingTimeInterval(0.15))
        let hovered = view.hoveredLink
        try hovering(view)
        view.mouseDown(with: mouse(.leftMouseDown, p, flags, in: window))
        view.mouseUp(with: mouse(.leftMouseUp, p, flags, in: window))
        RunLoop.current.run(until: Date().addingTimeInterval(0.4))
        return (opened, captured, hovered)
    }

    func testCommandClickOpensALinkAndAPlainClickDoesNot() throws {
        let commanded = try click(.command)
        XCTAssertFalse(commanded.captured)
        XCTAssertEqual(commanded.opened, [Self.link])
        XCTAssertEqual(commanded.hovered, Self.link, "the link under the pointer is reported for the workspace to show")
        let plain = try click([])
        XCTAssertEqual(plain.opened, [])
        XCTAssertNil(plain.hovered)
    }

    /// While the pointer is on a link, the terminal's right-click menu
    /// opens it the way a click does, and copies it; off one it has neither.
    func testTheContextMenuOpensAndCopiesTheLinkUnderThePointer() throws {
        let savedCopier = Ghostty.SurfaceView.copyLink
        defer { Ghostty.SurfaceView.copyLink = savedCopier }
        var copied: [String] = []
        Ghostty.SurfaceView.copyLink = { copied.append($0) }
        var titles: [String] = []
        let hovered = try click(.command) { view in
            let menu = view.contextMenu()
            titles = menu.items.map(\.title)
            for title in [Ghostty.SurfaceView.openLinkTitle, Ghostty.SurfaceView.copyLinkTitle] {
                let item = try XCTUnwrap(menu.item(withTitle: title))
                XCTAssertTrue(item.target === view)
                _ = view.perform(try XCTUnwrap(item.action), with: item)
            }
        }
        XCTAssertEqual(titles, ["Copy", "Paste", "Select All", "", "Open Link", "Copy Link"])
        XCTAssertEqual(copied, [Self.link])
        // Opened once by the menu, once by the Command+click after it.
        XCTAssertEqual(hovered.opened, [Self.link, Self.link])

        _ = try click([]) { view in
            XCTAssertEqual(view.contextMenu().items.map(\.title), ["Copy", "Paste", "Select All"], "no link, no link items")
        }
    }

    /// A program that tracks the mouse is sent the click instead, Command
    /// held or not; Shift is what takes a click back from it. tmux tracks the
    /// mouse for every session view (1002 or 1003 with 1006), so there the
    /// link gesture is Command+Shift+click.
    func testUnderAMouseTrackingProgramShiftIsNeededToOpenALink() throws {
        for modes in [[1000, 1006], [1002, 1006], [1003, 1006]] {
            let plain = try click(.command, mouseModes: modes)
            XCTAssertTrue(plain.captured, "\(modes)")
            XCTAssertEqual(plain.opened, [], "Command+click under modes \(modes) goes to the program")
            XCTAssertEqual(try click([.command, .shift], mouseModes: modes).opened, [Self.link], "modes \(modes)")
        }
        XCTAssertEqual(try click(.shift, mouseModes: [1002, 1006]).opened, [], "Shift alone selects; it opens nothing")
    }

    /// One surface under mouse tracking: a first Command+Shift+click that
    /// opens the link, then `between`, then a second one. Returns how many
    /// links each click opened.
    private func clickTwice(
        between: (Ghostty.SurfaceView, NSWindow, (Double, Int) -> NSPoint) -> Void
    ) throws -> (first: Int, second: Int) {
        var opened = 0
        Ghostty.App.openLink = { _ in opened += 1 }
        let ghostty = Ghostty.App { Ghostty.Config(userConfig: nil) }
        defer { ghostty.shutdown() }
        let window = GhosttyTestWindow.offscreen()
        defer { window.close() }
        let view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", "printf '\\033[?1002h\\033[?1006h'; printf 'LINE 2990 needle-2990\\nlink: \(Self.link)\\n'; stty raw -echo; exec cat > /dev/null"])))
        window.contentView = view
        defer { window.contentView = nil }
        XCTAssertTrue(window.makeFirstResponder(view))
        window.reportsKey = true
        let surface = try XCTUnwrap(view.surface)
        ghosttyWait("the link to be drawn") {
            view.performBindingAction("select_all")
            var text = ghostty_text_s()
            guard ghostty_surface_read_selection(surface, &text) else { return false }
            defer { ghostty_surface_free_text(surface, &text) }
            return String(cString: text.text).contains("s15-link")
        }
        ghosttyWait("the program to take the mouse") { ghostty_surface_mouse_captured(surface) }
        let cell = view.cellSize
        let at: (Double, Int) -> NSPoint = { column, row in
            NSPoint(x: 2 + column * cell.width, y: view.bounds.height - 2 - (Double(row) + 0.5) * cell.height)
        }
        let link = at(18.5, 1)
        let held: NSEvent.ModifierFlags = [.command, .shift]
        func clickLink() -> Int {
            let before = opened
            view.mouseMoved(with: mouse(.mouseMoved, NSPoint(x: link.x - 3, y: link.y), held, in: window))
            view.mouseMoved(with: mouse(.mouseMoved, link, held, in: window))
            RunLoop.current.run(until: Date().addingTimeInterval(0.15))
            view.mouseDown(with: mouse(.leftMouseDown, link, held, in: window))
            view.mouseUp(with: mouse(.leftMouseUp, link, held, in: window))
            RunLoop.current.run(until: Date().addingTimeInterval(0.4))
            return opened - before
        }
        let first = clickLink()
        between(view, window, at)
        RunLoop.current.run(until: Date().addingTimeInterval(0.1))
        return (first, clickLink())
    }

    private func press(_ flags: NSEvent.ModifierFlags, keyCode: UInt16, on view: Ghostty.SurfaceView, in window: NSWindow) {
        view.flagsChanged(with: NSEvent.keyEvent(
            with: .flagsChanged, location: .zero, modifierFlags: flags, timestamp: ProcessInfo.processInfo.systemUptime,
            windowNumber: window.windowNumber, context: nil, characters: "", charactersIgnoringModifiers: "",
            isARepeat: false, keyCode: keyCode)!)
    }

    /// The ordinary way to click a link: the pointer wanders without any
    /// modifier, then Command and Shift are pressed, then the click. The
    /// view must pass the key presses on (`flagsChanged`), because under
    /// mouse tracking Ghostty takes a click back from the program only for
    /// modifiers it saw pressed as keys.
    func testALinkOpensAfterThePointerMovedWithoutModifiersOnceTheKeysArePressed() throws {
        let wandered = try clickTwice { view, window, at in
            view.mouseMoved(with: self.mouse(.mouseMoved, at(40, 1), [], in: window))
            self.press(.command, keyCode: 0x37, on: view, in: window)
            self.press([.command, .shift], keyCode: 0x38, on: view, in: window)
        }
        XCTAssertEqual(wandered.first, 1)
        XCTAssertEqual(wandered.second, 1)

        // Hover the link, press the keys, click without moving again.
        let hovered = try clickTwice { view, window, at in
            view.mouseMoved(with: self.mouse(.mouseMoved, at(18.5, 1), [], in: window))
            self.press(.command, keyCode: 0x37, on: view, in: window)
            self.press([.command, .shift], keyCode: 0x38, on: view, in: window)
        }
        XCTAssertEqual(hovered.second, 1)
    }

    /// Ghostty's rule, pinned because it is easy to mistake for a broken
    /// link: once the pointer has moved with no modifier held, modifiers
    /// that only ride on mouse events (as an automation tool sends them, with
    /// no key press) no longer open a link. Anything that drives the app with
    /// synthetic clicks has to press the keys too.
    func testModifiersOnMouseEventsAloneStopOpeningLinksAfterAPlainMove() throws {
        let untouched = try clickTwice { _, _, _ in }
        XCTAssertEqual(untouched.second, 1, "with no plain move in between, the second click still opens")
        let plainMove = try clickTwice { view, window, at in
            view.mouseMoved(with: self.mouse(.mouseMoved, at(40, 1), [], in: window))
        }
        XCTAssertEqual(plainMove.first, 1)
        XCTAssertEqual(plainMove.second, 0)
    }
}

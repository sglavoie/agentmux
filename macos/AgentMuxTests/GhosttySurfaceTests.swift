import AppKit
import GhosttyKit
import XCTest
@testable import AgentMux

/// A window that reports itself key on demand, so focus can be exercised from
/// a hosted test, whose windows never become key on their own.
final class GhosttyTestWindow: NSWindow {
    var reportsKey = false {
        didSet {
            guard reportsKey != oldValue else { return }
            NotificationCenter.default.post(
                name: reportsKey ? NSWindow.didBecomeKeyNotification : NSWindow.didResignKeyNotification,
                object: self)
        }
    }

    override var isKeyWindow: Bool { reportsKey }

    static func offscreen() -> GhosttyTestWindow {
        let window = GhosttyTestWindow(
            contentRect: NSRect(x: -10_000, y: -10_000, width: 800, height: 500),
            styleMask: [.titled, .resizable],
            backing: .buffered,
            defer: false
        )
        window.isReleasedWhenClosed = false
        return window
    }
}

/// Spins the main run loop until `condition` holds; Ghostty's wakeups and the
/// surface's deferred events both arrive through it.
func ghosttyWait(
    _ what: String,
    timeout: TimeInterval = 15,
    file: StaticString = #filePath,
    line: UInt = #line,
    until condition: () -> Bool
) {
    let deadline = Date().addingTimeInterval(timeout)
    while !condition() {
        if Date() > deadline {
            XCTFail("timed out waiting for \(what)", file: file, line: line)
            return
        }
        RunLoop.current.run(until: Date().addingTimeInterval(0.02))
    }
}

/// Drives one real surface in an offscreen window. The hosting app has run
/// `ghostty_init` but, under XCTest, makes no terminal of its own; the surface
/// here runs `/bin/cat`, which waits on its pty until freed.
final class GhosttySurfaceTests: XCTestCase {
    private var ghostty: Ghostty.App!
    private var window: GhosttyTestWindow!
    private var view: Ghostty.SurfaceView!

    private static let cat = Ghostty.SurfaceConfiguration(program: URL(fileURLWithPath: "/bin/cat"))

    override func setUpWithError() throws {
        ghostty = Ghostty.App { Ghostty.Config(userConfig: nil) }
        window = GhosttyTestWindow.offscreen()
        view = try XCTUnwrap(ghostty.makeSurface(Self.cat))
        window.contentView = view
    }

    override func tearDownWithError() throws {
        window.contentView = nil
        view = nil
        window.close()
        window = nil
        ghostty.shutdown()
        ghostty = nil
    }

    private func surfaceSize() throws -> ghostty_surface_size_s {
        ghostty_surface_size(try XCTUnwrap(view.surface))
    }

    func testSurfaceIsCreatedRegisteredAndHostsItself() throws {
        let surface = try XCTUnwrap(view.surface)
        XCTAssertTrue(Ghostty.surfaceHost(from: ghostty_surface_userdata(surface)) === view)
        XCTAssertTrue(ghostty.surfaceView(for: surface) === view)
        XCTAssertEqual(ghostty.surfaces.count, 1)
        XCTAssertEqual(view.configuration, Self.cat)
        XCTAssertTrue(view.acceptsFirstResponder)
        XCTAssertFalse(view.processExited)
    }

    func testTheSurfaceAcceptsDroppedFilesURLsAndText() throws {
        XCTAssertTrue(Set(view.registeredDraggedTypes).isSuperset(of: Ghostty.SurfaceView.dropTypes))

        let pasteboard = NSPasteboard(name: .init("amx-drop-\(UUID().uuidString)"))
        defer { pasteboard.releaseGlobally() }
        pasteboard.clearContents()
        pasteboard.writeObjects([
            URL(fileURLWithPath: "/tmp/Screen Shot (1).png") as NSURL,
            URL(fileURLWithPath: "/tmp/b.txt") as NSURL,
        ])
        XCTAssertEqual(Ghostty.SurfaceView.dropText(from: pasteboard), "/tmp/Screen\\ Shot\\ \\(1\\).png /tmp/b.txt")

        pasteboard.clearContents()
        pasteboard.setString("https://example.com/x", forType: .string)
        XCTAssertEqual(Ghostty.SurfaceView.dropText(from: pasteboard), "https://example.com/x")

        pasteboard.clearContents()
        XCTAssertNil(Ghostty.SurfaceView.dropText(from: pasteboard))
    }

    func testFramebufferTracksTheViewInBackingPixels() throws {
        let backing = view.convertToBacking(view.bounds.size)
        let size = try surfaceSize()
        XCTAssertEqual(size.width_px, UInt32(backing.width))
        XCTAssertEqual(size.height_px, UInt32(backing.height))
        XCTAssertGreaterThan(size.columns, 0)
        XCTAssertGreaterThan(size.rows, 0)
    }

    func testResizeChangesTheGridBothWays() throws {
        let initial = try surfaceSize()

        window.setContentSize(NSSize(width: 400, height: 250))
        let smaller = try surfaceSize()
        XCTAssertLessThan(smaller.columns, initial.columns)
        XCTAssertLessThan(smaller.rows, initial.rows)
        XCTAssertEqual(smaller.width_px, UInt32(view.convertToBacking(view.bounds.size).width))

        window.setContentSize(NSSize(width: 800, height: 500))
        let restored = try surfaceSize()
        XCTAssertEqual(restored.columns, initial.columns)
        XCTAssertEqual(restored.rows, initial.rows)
    }

    /// The display's scale reaches the layer and the framebuffer stays the
    /// view's size in that display's pixels.
    func testBackingChangeKeepsTheFramebufferAtTheDisplaysScale() throws {
        let before = try surfaceSize()
        view.viewDidChangeBackingProperties()
        let after = try surfaceSize()
        XCTAssertEqual(after.width_px, before.width_px)
        XCTAssertEqual(after.height_px, before.height_px)
        XCTAssertEqual(view.layer?.contentsScale, window.backingScaleFactor)
        XCTAssertEqual(after.width_px, UInt32((view.bounds.width * window.backingScaleFactor).rounded(.down)))
    }

    func testRendererInstallsItsOwnLayer() {
        XCTAssertTrue(view.wantsLayer)
        XCTAssertNotNil(view.layer)
    }

    func testFocusFollowsFirstResponderInTheKeyWindow() {
        // Not key: first responder alone is not focus.
        XCTAssertTrue(window.makeFirstResponder(view))
        XCTAssertFalse(view.focused)

        window.reportsKey = true
        XCTAssertTrue(view.focused)
        window.reportsKey = false
        XCTAssertFalse(view.focused)

        window.reportsKey = true
        XCTAssertTrue(view.focused)
        XCTAssertTrue(window.makeFirstResponder(nil))
        XCTAssertFalse(view.focused)
    }

    func testPixelSizeRoundsDownAndNeverReachesZero() {
        XCTAssertTrue(Ghostty.SurfaceView.pixelSize(CGSize(width: 1601.7, height: 999.2)) == (1601, 999))
        XCTAssertTrue(Ghostty.SurfaceView.pixelSize(.zero) == (1, 1))
        XCTAssertTrue(Ghostty.SurfaceView.pixelSize(CGSize(width: -4, height: 0.5)) == (1, 1))
    }

    func testColorSchemeFollowsTheAppearance() throws {
        let dark = try XCTUnwrap(NSAppearance(named: .darkAqua))
        let light = try XCTUnwrap(NSAppearance(named: .aqua))
        XCTAssertEqual(Ghostty.colorScheme(for: dark), GHOSTTY_COLOR_SCHEME_DARK)
        XCTAssertEqual(Ghostty.colorScheme(for: light), GHOSTTY_COLOR_SCHEME_LIGHT)
    }

    func testDisplayIDComesFromTheScreenNumber() throws {
        let screen = try XCTUnwrap(NSScreen.main)
        XCTAssertEqual(Ghostty.SurfaceView.displayID(of: screen), CGMainDisplayID())
    }

    /// A new default zoom reaches a zoomed surface only through the reset
    /// that follows the reload.
    func testReloadedFontSizeAppliesToAZoomedSurfaceOnReset() throws {
        window.contentView = nil
        view = nil
        ghostty.shutdown()
        var overrides = Ghostty.ConfigOverrides()
        ghostty = Ghostty.App { Ghostty.Config(userConfig: nil, overrides: overrides) }
        view = try XCTUnwrap(ghostty.makeSurface(Self.cat))
        window.contentView = view
        let initial = try surfaceSize().cell_width_px

        XCTAssertTrue(view.performBindingAction("increase_font_size:20"))
        let zoomed = try surfaceSize().cell_width_px
        XCTAssertGreaterThan(zoomed, initial)

        overrides.fontSize = 20
        XCTAssertTrue(ghostty.reloadConfig())
        XCTAssertEqual(ghostty.config.get("font-size", as: Float.self, initial: 0), 20)
        XCTAssertEqual(try surfaceSize().cell_width_px, zoomed, "a reload keeps a manual zoom")

        XCTAssertTrue(view.performBindingAction("reset_font_size"))
        let reset = try surfaceSize().cell_width_px
        XCTAssertGreaterThan(reset, initial)
        XCTAssertLessThan(reset, zoomed)
    }

    func testAPerSurfaceFontSizeOverridesTheAppConfiguration() throws {
        var large = Self.cat
        large.fontSize = 30
        let other = try XCTUnwrap(ghostty.makeSurface(large))
        defer { other.close() }
        let otherSize = ghostty_surface_size(try XCTUnwrap(other.surface))
        XCTAssertGreaterThan(otherSize.cell_width_px, try surfaceSize().cell_width_px)
    }
}

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

        // A URL is one shell word: what it carries starts nothing.
        pasteboard.clearContents()
        pasteboard.writeObjects([try XCTUnwrap(URL(string: "https://example.com/a?b=1&c=(2)")) as NSURL])
        XCTAssertEqual(Ghostty.SurfaceView.dropText(from: pasteboard), "https://example.com/a\\?b=1\\&c=\\(2\\)")

        pasteboard.clearContents()
        XCTAssertNil(Ghostty.SurfaceView.dropText(from: pasteboard))

        XCTAssertFalse(Ghostty.SurfaceView.dropIsPaste("ls -la"))
        XCTAssertTrue(Ghostty.SurfaceView.dropIsPaste("echo one\necho two"))
    }

    /// Dropped text of several lines is pasted, so Ghostty's paste protection
    /// asks about it, naming the view, and the clipboard is not touched.
    func testAMultiLineDropIsAskedAboutAsAPaste() throws {
        let saved = Ghostty.App.confirmClipboard
        defer { Ghostty.App.confirmClipboard = saved }
        var asked: [Ghostty.App.ClipboardQuestion] = []
        Ghostty.App.confirmClipboard = { question, answer in
            asked.append(question)
            answer(false)
        }
        let clipboard = NSPasteboard.general.changeCount
        view.hostName = "api — work"
        XCTAssertTrue(view.drop("echo one\necho two"))
        XCTAssertNil(view.pendingPaste, "the dropped text is not left for a later paste")
        ghosttyWait("the paste question") { !asked.isEmpty }
        XCTAssertEqual(asked.first?.text, "echo one\necho two")
        XCTAssertEqual(asked.first?.requester, "api — work")
        XCTAssertEqual(asked.first?.request, GHOSTTY_CLIPBOARD_REQUEST_PASTE)
        XCTAssertEqual(NSPasteboard.general.changeCount, clipboard)

        // One line is typed: there is nothing to ask.
        XCTAssertTrue(view.drop("ls"))
        RunLoop.current.run(until: Date().addingTimeInterval(0.1))
        XCTAssertEqual(asked.count, 1)
    }

    /// The question shows the whole text, scrollable, and who asked.
    func testThePasteQuestionShowsAllOfTheTextAndWhoAsked() throws {
        let long = (1...400).map { "line \($0)" }.joined(separator: "\n")
        let alert = Ghostty.App.clipboardAlert(Ghostty.App.ClipboardQuestion(
            text: long, request: GHOSTTY_CLIPBOARD_REQUEST_PASTE, requester: "Dashboard"))
        XCTAssertEqual(alert.messageText, "Paste this text into Dashboard?")
        let scroll = try XCTUnwrap(alert.accessoryView as? NSScrollView)
        XCTAssertTrue(scroll.hasVerticalScroller)
        let text = try XCTUnwrap(scroll.documentView as? NSTextView)
        XCTAssertEqual(text.string, long, "nothing is cut from the preview")
        XCTAssertFalse(text.isEditable)
        XCTAssertEqual(alert.buttons.map(\.title), ["Allow", "Deny"])

        let read = Ghostty.App.clipboardAlert(Ghostty.App.ClipboardQuestion(
            text: "x", request: GHOSTTY_CLIPBOARD_REQUEST_OSC_52_READ, requester: ""))
        XCTAssertEqual(read.messageText, "Allow the program to read the clipboard?")
    }

    func testOnlyTheWebAndMailOpenAtOnceAndFilesAreRevealed() throws {
        let disposition = { (link: String) in Ghostty.App.linkDisposition(Ghostty.App.link(from: link)) }
        XCTAssertEqual(disposition("https://example.com"), .open)
        XCTAssertEqual(disposition("HTTP://example.com"), .open)
        XCTAssertEqual(disposition("mailto:a@example.com"), .open)
        XCTAssertEqual(disposition("file:///tmp/a.sh"), .reveal)
        XCTAssertEqual(disposition("~/notes.txt"), .reveal, "a bare path is a file")
        XCTAssertEqual(disposition("ssh://host"), .confirm)
        XCTAssertEqual(disposition("x-apple.systempreferences:com.apple.preference"), .confirm)
        XCTAssertEqual(disposition("vscode://file/tmp/a"), .confirm)

        let saved = (Ghostty.App.openLink, Ghostty.App.revealFile, Ghostty.App.confirmLink)
        defer { (Ghostty.App.openLink, Ghostty.App.revealFile, Ghostty.App.confirmLink) = saved }
        var opened: [String] = []
        var revealed: [String] = []
        var allow = false
        Ghostty.App.openLink = { opened.append($0.absoluteString) }
        Ghostty.App.revealFile = { revealed.append($0.path) }
        Ghostty.App.confirmLink = { _, answer in answer(allow) }
        Ghostty.App.follow(Ghostty.App.link(from: "ssh://host"))
        XCTAssertEqual(opened, [], "refused")
        allow = true
        Ghostty.App.follow(Ghostty.App.link(from: "ssh://host"))
        Ghostty.App.follow(Ghostty.App.link(from: "https://example.com"))
        Ghostty.App.follow(Ghostty.App.link(from: "/tmp/run me.sh"))
        XCTAssertEqual(opened, ["ssh://host", "https://example.com"])
        XCTAssertEqual(revealed, ["/tmp/run me.sh"], "a file is shown in the Finder, never opened")
    }

    /// A hidden terminal is not resized frame by frame; it takes the size it
    /// has when it is shown again.
    func testAHiddenSurfaceIsResizedWhenShown() throws {
        let initial = try surfaceSize()
        view.isHidden = true
        window.setContentSize(NSSize(width: 400, height: 250))
        XCTAssertTrue(view.sizePending)
        XCTAssertEqual(try surfaceSize().columns, initial.columns, "nothing reaches the surface while hidden")
        view.isHidden = false
        XCTAssertFalse(view.sizePending)
        let shown = try surfaceSize()
        XCTAssertLessThan(shown.columns, initial.columns)
        XCTAssertEqual(shown.width_px, UInt32(view.convertToBacking(view.bounds.size).width))
    }

    /// A scale change reaching a hidden terminal is deferred with its size:
    /// the surface hears of both once the view is shown, never of a size
    /// left over from before it was hidden.
    func testAHiddenSurfaceDefersAScaleChangeWithItsSize() throws {
        view.isHidden = true
        XCTAssertFalse(view.sizePending)
        view.viewDidChangeBackingProperties()
        XCTAssertTrue(view.sizePending, "the new scale waits for the view to be shown")
        window.setContentSize(NSSize(width: 400, height: 250))
        view.isHidden = false
        XCTAssertFalse(view.sizePending)
        let shown = try surfaceSize()
        XCTAssertEqual(shown.width_px, UInt32(view.convertToBacking(view.bounds.size).width))
        XCTAssertEqual(shown.height_px, UInt32(view.convertToBacking(view.bounds.size).height))
    }

    /// The zoom a terminal shows is read back from it, so the next terminal
    /// for the same session can be made at it.
    func testTheFontSizeFollowsTheZoomAndSeedsANewSurface() throws {
        window.contentView = nil
        view = nil
        ghostty.shutdown()
        ghostty = Ghostty.App { Ghostty.Config(userConfig: nil, overrides: Ghostty.ConfigOverrides(fontSize: 14)) }
        view = try XCTUnwrap(ghostty.makeSurface(Self.cat))
        window.contentView = view
        XCTAssertEqual(view.fontSize, 14)
        view.performBindingAction("increase_font_size:3")
        XCTAssertEqual(view.fontSize, 17)

        var configuration = Self.cat
        configuration.fontSize = 17
        let again = try XCTUnwrap(ghostty.makeSurface(configuration))
        defer { again.close() }
        XCTAssertEqual(again.fontSize, 17)
    }

    /// QuickLook asks for a range that is not the selection's: it is put at
    /// the selection, above the cursor, rather than where the input method
    /// draws.
    func testQuickLookIsPlacedAtTheSelection() throws {
        window.contentView = nil
        view = nil
        view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", "printf 'first\\nsecond\\nthird\\n'; exec cat"])))
        window.contentView = view
        ghosttyWait("the text to be drawn") {
            view.performBindingAction("select_all")
            return view.selectedRange().length > 0
        }
        let selection = view.selectedRange()
        let cursor = view.firstRect(forCharacterRange: NSRange(location: 0, length: 0), actualRange: nil)
        let quickLook = view.firstRect(forCharacterRange: NSRange(location: selection.location + 1, length: 2), actualRange: nil)
        XCTAssertGreaterThan(quickLook.minY, cursor.minY, "the selection starts on the first row, above the cursor")
    }

    /// VoiceOver reads a terminal as a text area holding the screen's text,
    /// with the selection.
    func testATerminalIsATextAreaAssistiveTechnologiesCanRead() throws {
        window.contentView = nil
        view = nil
        view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", "printf 'first\\nsécond\\nthird\\n'; exec cat"])))
        window.contentView = view
        XCTAssertTrue(view.isAccessibilityElement())
        XCTAssertEqual(view.accessibilityRole(), .textArea)
        XCTAssertEqual(view.accessibilityHelp(), "Terminal content area")
        XCTAssertNil(view.accessibilitySelectedText(), "nothing is selected yet")

        var screen = ""
        ghosttyWait("the text to be drawn") {
            view.accessibilityContents = nil
            screen = view.accessibilityValue() as? String ?? ""
            return screen.contains("third")
        }
        let text = screen as NSString
        XCTAssertEqual(view.accessibilityNumberOfCharacters(), text.length)
        XCTAssertEqual(view.accessibilityVisibleCharacterRange(), NSRange(location: 0, length: text.length))
        let second = text.range(of: "sécond")
        XCTAssertEqual(view.accessibilityString(for: second), "sécond")
        XCTAssertEqual(view.accessibilityAttributedString(for: second)?.string, "sécond")
        XCTAssertEqual(view.accessibilityLine(for: second.location), 1)
        XCTAssertEqual(view.accessibilityLine(for: text.range(of: "third").location), 2)

        view.performBindingAction("select_all")
        ghosttyWait("the selection") { view.accessibilitySelectedText() != nil }
        XCTAssertTrue(try XCTUnwrap(view.accessibilitySelectedText()).contains("third"))
        XCTAssertEqual(
            view.accessibilityString(for: view.accessibilitySelectedTextRange()),
            view.accessibilitySelectedText())
    }

    /// The selected range names the selection in the screen's text, not the
    /// grid cells Ghostty counts it in: line breaks, short rows, wide and
    /// multi-unit characters all set the two apart.
    func testAccessibilitySelectionRangeMatchesVisibleText() throws {
        window.contentView = nil
        view = nil
        view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", "printf 'first\\nsécond 日本 😀 x\\nthird\\n'; exec cat"])))
        window.contentView = view
        XCTAssertTrue(window.makeFirstResponder(view))
        window.reportsKey = true
        ghosttyWait("the text to be drawn") {
            view.accessibilityContents = nil
            return (view.accessibilityValue() as? String ?? "").contains("third")
        }
        XCTAssertEqual(view.accessibilitySelectedTextRange(), NSRange(location: 0, length: 0), "nothing is selected")

        func mouse(_ type: NSEvent.EventType, _ p: NSPoint) -> NSEvent {
            NSEvent.mouseEvent(with: type, location: p, modifierFlags: [], timestamp: ProcessInfo.processInfo.systemUptime,
                               windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1)!
        }
        // Ghostty's default padding is 2pt; the view's origin is bottom left.
        // A drag takes in its last cell only past that cell's middle.
        func cell(row: Int, column: Int, at fraction: Double = 0.25) -> NSPoint {
            let size = view.cellSize
            return NSPoint(x: 2 + (Double(column) + fraction) * size.width,
                           y: view.bounds.height - 2 - (Double(row) + 0.5) * size.height)
        }
        func drag(from start: NSPoint, to end: NSPoint) {
            let away = NSPoint(x: view.bounds.width - 20, y: 20)
            view.mouseMoved(with: mouse(.mouseMoved, away))
            view.mouseDown(with: mouse(.leftMouseDown, away))
            view.mouseUp(with: mouse(.leftMouseUp, away))
            view.mouseMoved(with: mouse(.mouseMoved, start))
            view.mouseDown(with: mouse(.leftMouseDown, start))
            for step in 1...20 {
                let t = Double(step) / 20
                view.mouseDragged(with: mouse(.leftMouseDragged, NSPoint(
                    x: start.x + (end.x - start.x) * t, y: start.y + (end.y - start.y) * t)))
            }
            view.mouseUp(with: mouse(.leftMouseUp, end))
        }
        func assertRangeRecoversSelection(_ expected: String, file: StaticString = #filePath, line: UInt = #line) {
            view.accessibilityContents = nil
            XCTAssertEqual(view.accessibilitySelectedText(), expected, file: file, line: line)
            let range = view.accessibilitySelectedTextRange()
            XCTAssertGreaterThan(range.length, 0, file: file, line: line)
            XCTAssertLessThanOrEqual(NSMaxRange(range), view.accessibilityNumberOfCharacters(), file: file, line: line)
            XCTAssertEqual(view.accessibilityString(for: range), view.accessibilitySelectedText(), file: file, line: line)
            let screen = view.accessibilityValue() as? String ?? ""
            XCTAssertEqual((screen as NSString).range(of: expected), range, file: file, line: line)
        }

        // A word on the third row: grid cells put it at 2 × columns.
        drag(from: cell(row: 2, column: 0), to: cell(row: 2, column: 4, at: 0.75))
        ghosttyWait("the third row's selection") { view.accessibilitySelectedText() == "third" }
        assertRangeRecoversSelection("third")

        // From the wide characters on the second row, past an emoji of two
        // UTF-16 units, into the third row.
        drag(from: cell(row: 1, column: 7), to: cell(row: 2, column: 2, at: 0.75))
        ghosttyWait("the selection across rows") { view.accessibilitySelectedText()?.hasSuffix("thi") == true }
        assertRangeRecoversSelection("日本 😀 x\nthi")
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

    func testDefaultZoomChangeResetsVisibleAndHiddenSurfacesAndNewSurfacesInheritIt() throws {
        window.contentView = nil
        view = nil
        ghostty.shutdown()
        var overrides = Ghostty.ConfigOverrides(fontSize: 14)
        ghostty = Ghostty.App { Ghostty.Config(userConfig: nil, overrides: overrides) }
        view = try XCTUnwrap(ghostty.makeSurface(Self.cat))
        window.contentView = view
        let hidden = try XCTUnwrap(ghostty.makeSurface(Self.cat))
        defer { hidden.close() }
        let initial = try surfaceSize().cell_width_px
        view.performBindingAction("increase_font_size:20")
        hidden.performBindingAction("increase_font_size:10")

        overrides.fontSize = 22
        XCTAssertTrue(ghostty.reloadConfig(resetFontSize: true))
        let reset = try surfaceSize().cell_width_px
        XCTAssertGreaterThan(reset, initial)
        let hiddenSize = ghostty_surface_size(try XCTUnwrap(hidden.surface))
        XCTAssertEqual(hiddenSize.cell_width_px, reset)

        let fresh = try XCTUnwrap(ghostty.makeSurface(Self.cat))
        defer { fresh.close() }
        XCTAssertEqual(ghostty_surface_size(try XCTUnwrap(fresh.surface)).cell_width_px, reset)

        view.zoomIn(nil)
        view.resetZoom(nil)
        XCTAssertEqual(try surfaceSize().cell_width_px, reset, "⌘⇧- uses the saved default")
        overrides.fontSize = nil
        XCTAssertTrue(ghostty.reloadConfig(resetFontSize: true))
        XCTAssertLessThan(try surfaceSize().cell_width_px, reset, "Reset restores the config's font size")
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

import AppKit
import GhosttyKit
import XCTest
@testable import AgentMux

/// Records what a surface reports.
private final class SurfaceEvents: GhosttySurfaceDelegate {
    var titles: [String] = []
    var directories: [String] = []
    var exits = 0
    var closeRequests: [Bool] = []
    var notifications: [String] = []
    var bells = 0
    var links: [String?] = []
    var health: [Bool] = []
    /// Run on a close request, to prove a delegate may free the surface there.
    var onCloseRequest: ((Ghostty.SurfaceView) -> Void)?

    func surfaceDidChangeTitle(_ surface: Ghostty.SurfaceView) { titles.append(surface.title) }
    func surfaceDidChangeWorkingDirectory(_ surface: Ghostty.SurfaceView) {
        directories.append(surface.workingDirectory ?? "")
    }
    func surfaceChildDidExit(_ surface: Ghostty.SurfaceView) { exits += 1 }
    func surfaceDidRequestClose(_ surface: Ghostty.SurfaceView, needsConfirm: Bool) {
        closeRequests.append(needsConfirm)
        onCloseRequest?(surface)
    }
    func surface(_ surface: Ghostty.SurfaceView, didRequestNotificationTitled title: String, body: String) {
        notifications.append("\(title)|\(body)")
    }
    func surfaceDidRingBell(_ surface: Ghostty.SurfaceView) { bells += 1 }
    func surfaceDidChangeHoveredLink(_ surface: Ghostty.SurfaceView) { links.append(surface.hoveredLink) }
    func surfaceDidChangeRendererHealth(_ surface: Ghostty.SurfaceView) { health.append(surface.rendererHealthy) }
}

/// Several real surfaces on one `Ghostty.App`: each runs its own program with
/// its own environment, and each is freed without disturbing the others.
final class GhosttySurfaceLifecycleTests: XCTestCase {
    private var ghostty: Ghostty.App!
    private var dir: URL!
    private var overrides = Ghostty.ConfigOverrides()

    override func setUpWithError() throws {
        overrides = Ghostty.ConfigOverrides()
        ghostty = Ghostty.App { [unowned self] in Ghostty.Config(userConfig: nil, overrides: self.overrides) }
        XCTAssertEqual(ghostty.readiness, .ready)
        dir = FileManager.default.temporaryDirectory
            .appendingPathComponent("amx-ghostty-surface-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        // The physical path, as the program reports its directory.
        let physical = try XCTUnwrap(realpath(dir.path, nil))
        defer { free(physical) }
        dir = URL(fileURLWithPath: String(cString: physical))
    }

    override func tearDownWithError() throws {
        ghostty.shutdown()
        ghostty = nil
        try? FileManager.default.removeItem(at: dir)
    }

    /// What a probe program wrote about itself once it was running.
    private struct Report {
        var pid: pid_t
        var mark: String
        var term: String
        var directory: String
    }

    /// A surface whose program writes its pid, `$MARK`, `$TERM`, and directory
    /// to a file of its own, then waits on its terminal like an agent would.
    private func probe(_ name: String, in directory: URL? = nil, then tail: String = "exec cat") -> Ghostty.SurfaceConfiguration {
        let out = dir.appendingPathComponent("\(name).out").path
        return Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", #"printf '%s\n%s\n%s\n%s\n' "$$" "$MARK" "$TERM" "$(pwd -P)" > "$OUT.tmp" && mv "$OUT.tmp" "$OUT"; "# + tail],
            environment: ["OUT": out, "MARK": "mark of \(name)", "TERM": "dumb"],
            workingDirectory: directory
        )
    }

    private func report(_ name: String, file: StaticString = #filePath, line: UInt = #line) -> Report? {
        let url = dir.appendingPathComponent("\(name).out")
        ghosttyWait("\(name) to start", file: file, line: line) { FileManager.default.fileExists(atPath: url.path) }
        guard let text = try? String(contentsOf: url, encoding: .utf8) else { return nil }
        let lines = text.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        guard lines.count >= 4, let pid = pid_t(lines[0]) else {
            XCTFail("unreadable report from \(name): \(text)", file: file, line: line)
            return nil
        }
        return Report(pid: pid, mark: lines[1], term: lines[2], directory: lines[3])
    }

    private func isRunning(_ pid: pid_t) -> Bool {
        kill(pid, 0) == 0 || errno != ESRCH
    }

    private func waitForExit(_ pid: pid_t, _ what: String, file: StaticString = #filePath, line: UInt = #line) {
        ghosttyWait("\(what) (pid \(pid)) to exit", file: file, line: line) { !self.isRunning(pid) }
    }

    // MARK: Ownership and environment

    func testEachSurfaceRunsItsOwnCommandEnvironmentAndDirectory() throws {
        let workA = dir.appendingPathComponent("work a")
        let workB = dir.appendingPathComponent("work-b")
        try FileManager.default.createDirectory(at: workA, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(at: workB, withIntermediateDirectories: true)

        let a = try XCTUnwrap(ghostty.makeSurface(probe("a", in: workA)))
        let b = try XCTUnwrap(ghostty.makeSurface(probe("b", in: workB)))
        XCTAssertEqual(ghostty.surfaces.count, 2)
        XCTAssertFalse(a.surface == b.surface)

        let reportA = try XCTUnwrap(report("a"))
        let reportB = try XCTUnwrap(report("b"))
        XCTAssertNotEqual(reportA.pid, reportB.pid)
        XCTAssertEqual(reportA.mark, "mark of a")
        XCTAssertEqual(reportB.mark, "mark of b")
        XCTAssertEqual(reportA.directory, workA.path)
        XCTAssertEqual(reportB.directory, workB.path)
        // The terminal's own identity is Ghostty's, whatever a caller passed.
        XCTAssertEqual(reportA.term, "xterm-ghostty")
        XCTAssertEqual(reportB.term, "xterm-ghostty")

        a.close()
        b.close()
        waitForExit(reportA.pid, "a")
        waitForExit(reportB.pid, "b")
    }

    func testSurfacesAreDestroyedIndependentlyInAnyOrder() throws {
        let names = ["first", "second", "third", "fourth"]
        var views = try names.map { try XCTUnwrap(ghostty.makeSurface(probe($0))) }
        let pids = try names.map { try XCTUnwrap(report($0)).pid }
        XCTAssertEqual(ghostty.surfaces.count, 4)

        // Middle, last, first: no order is special.
        for (closing, remaining) in [(1, 3), (3, 2), (0, 1)] {
            views[closing].close()
            XCTAssertNil(views[closing].surface)
            XCTAssertEqual(ghostty.surfaces.count, remaining)
            waitForExit(pids[closing], names[closing])
        }
        XCTAssertTrue(isRunning(pids[2]), "the surviving surface's program was disturbed")
        XCTAssertTrue(ghostty.surfaces.first === views[2])
        XCTAssertFalse(views[2].processExited)

        // A new surface after others were freed is as good as the first.
        let late = try XCTUnwrap(ghostty.makeSurface(probe("late")))
        let latePID = try XCTUnwrap(report("late")).pid
        XCTAssertEqual(ghostty.surfaces.count, 2)

        late.close()
        waitForExit(latePID, "late")
        XCTAssertTrue(isRunning(pids[2]))
        views[2].close()
        XCTAssertEqual(ghostty.surfaces.count, 0)
        waitForExit(pids[2], "third")
        views.removeAll()
    }

    func testClosingIsIdempotentAndLeavesAnInertView() throws {
        let window = GhosttyTestWindow.offscreen()
        defer { window.close() }
        let view = try XCTUnwrap(ghostty.makeSurface(probe("solo")))
        window.contentView = view
        let pid = try XCTUnwrap(report("solo")).pid

        view.close()
        view.close()
        XCTAssertNil(view.surface)
        XCTAssertTrue(view.processExited)
        XCTAssertFalse(view.acceptsFirstResponder)
        XCTAssertFalse(view.focused)
        XCTAssertEqual(ghostty.surfaces.count, 0)
        waitForExit(pid, "solo")

        // Everything a window still sends a closed view is ignored.
        window.setContentSize(NSSize(width: 300, height: 200))
        view.viewDidChangeBackingProperties()
        window.reportsKey = true
        XCTAssertFalse(view.focused)
        XCTAssertFalse(view.performBindingAction("select_all"))
        XCTAssertTrue(view.registeredDraggedTypes.isEmpty)
        window.contentView = nil
    }

    func testReleasingAViewFreesItsSurface() throws {
        weak var released: Ghostty.SurfaceView?
        var pid: pid_t = 0
        try autoreleasepool {
            let view = try XCTUnwrap(ghostty.makeSurface(probe("released")))
            released = view
            pid = try XCTUnwrap(report("released")).pid
        }
        ghosttyWait("the view to be released") { released == nil }
        XCTAssertEqual(ghostty.surfaces.count, 0)
        waitForExit(pid, "released")
    }

    func testAnAppOutlivesEverySurfaceMadeFromIt() throws {
        var app: Ghostty.App? = Ghostty.App { Ghostty.Config(userConfig: nil) }
        weak let weakApp = app
        let view = try XCTUnwrap(app?.makeSurface(probe("orphan")))
        let pid = try XCTUnwrap(report("orphan")).pid

        app = nil
        XCTAssertNotNil(weakApp, "the app was freed under a live surface")
        XCTAssertNotNil(view.surface)
        XCTAssertTrue(isRunning(pid))
        XCTAssertEqual(ghostty_surface_size(try XCTUnwrap(view.surface)).columns > 0, true)

        view.close()
        waitForExit(pid, "orphan")
        ghosttyWait("the app to be freed") { weakApp == nil }
    }

    func testShutdownClosesEverySurfaceAndRefusesNewOnes() throws {
        let views = try ["x", "y", "z"].map { try XCTUnwrap(ghostty.makeSurface(probe($0))) }
        let pids = try ["x", "y", "z"].map { try XCTUnwrap(report($0)).pid }

        ghostty.shutdown()
        XCTAssertTrue(views.allSatisfy { $0.surface == nil })
        XCTAssertEqual(ghostty.surfaces.count, 0)
        XCTAssertNil(ghostty.app)
        XCTAssertNil(ghostty.makeSurface(probe("after")))
        XCTAssertFalse(ghostty.reloadConfig())
        for (pid, name) in zip(pids, ["x", "y", "z"]) {
            waitForExit(pid, name)
        }
        ghostty.shutdown()
    }

    // MARK: Configuration reload

    func testAReloadReachesEverySurface() throws {
        let cat = Ghostty.SurfaceConfiguration(program: URL(fileURLWithPath: "/bin/cat"))
        let a = try XCTUnwrap(ghostty.makeSurface(cat))
        let b = try XCTUnwrap(ghostty.makeSurface(cat))
        func cellWidth(_ view: Ghostty.SurfaceView) throws -> UInt32 {
            ghostty_surface_size(try XCTUnwrap(view.surface)).cell_width_px
        }
        let before = (try cellWidth(a), try cellWidth(b))
        XCTAssertEqual(before.0, before.1)

        overrides.fontSize = 28
        XCTAssertTrue(ghostty.reloadConfig())
        XCTAssertEqual(ghostty.config.get("font-size", as: Float.self, initial: 0), 28)
        XCTAssertGreaterThan(try cellWidth(a), before.0)
        XCTAssertEqual(try cellWidth(a), try cellWidth(b))

        // A surface made after the reload starts from the new configuration.
        let c = try XCTUnwrap(ghostty.makeSurface(cat))
        XCTAssertEqual(try cellWidth(c), try cellWidth(a))

        // A config that cannot be built leaves the current one applied.
        let broken = Ghostty.App { Ghostty.Config(userConfig: nil) }
        broken.shutdown()
        XCTAssertFalse(broken.reloadConfig())
        [a, b, c].forEach { $0.close() }
    }

    // MARK: Focus, visibility, scale

    func testOnlyTheFirstResponderOfTheKeyWindowIsFocused() throws {
        let cat = Ghostty.SurfaceConfiguration(program: URL(fileURLWithPath: "/bin/cat"))
        let window = GhosttyTestWindow.offscreen()
        defer { window.close() }
        let container = NSView(frame: NSRect(x: 0, y: 0, width: 800, height: 500))
        window.contentView = container
        let left = try XCTUnwrap(ghostty.makeSurface(cat))
        let right = try XCTUnwrap(ghostty.makeSurface(cat))
        left.frame = NSRect(x: 0, y: 0, width: 400, height: 500)
        right.frame = NSRect(x: 400, y: 0, width: 400, height: 500)
        container.addSubview(left)
        container.addSubview(right)
        window.reportsKey = true

        XCTAssertTrue(window.makeFirstResponder(left))
        XCTAssertEqual([left.focused, right.focused], [true, false])
        XCTAssertTrue(window.makeFirstResponder(right))
        XCTAssertEqual([left.focused, right.focused], [false, true])

        window.reportsKey = false
        XCTAssertEqual([left.focused, right.focused], [false, false])
        window.reportsKey = true
        XCTAssertEqual([left.focused, right.focused], [false, true])

        // Another window's key change is not this surface's.
        let other = GhosttyTestWindow.offscreen()
        defer { other.close() }
        other.reportsKey = true
        other.reportsKey = false
        XCTAssertEqual([left.focused, right.focused], [false, true])

        // Leaving the window gives focus up.
        right.removeFromSuperview()
        XCTAssertFalse(right.focused)

        // Each sizes to its own frame.
        let leftSize = ghostty_surface_size(try XCTUnwrap(left.surface))
        XCTAssertEqual(leftSize.width_px, UInt32(left.convertToBacking(left.bounds.size).width))

        window.contentView = nil
        left.close()
        right.close()
    }

    func testAHiddenOrDetachedSurfaceStopsRendering() throws {
        let cat = Ghostty.SurfaceConfiguration(program: URL(fileURLWithPath: "/bin/cat"))
        let window = GhosttyTestWindow.offscreen()
        defer { window.close() }
        let container = NSView(frame: NSRect(x: 0, y: 0, width: 800, height: 500))
        window.contentView = container
        let view = try XCTUnwrap(ghostty.makeSurface(cat))
        container.addSubview(view)
        let windowVisible = window.occlusionState.contains(.visible)
        XCTAssertEqual(view.visible, windowVisible)

        view.isHidden = true
        XCTAssertFalse(view.visible)
        view.isHidden = false
        XCTAssertEqual(view.visible, windowVisible)

        // Hiding an ancestor hides the terminal with it.
        container.isHidden = true
        XCTAssertFalse(view.visible)
        container.isHidden = false
        XCTAssertEqual(view.visible, windowVisible)

        view.removeFromSuperview()
        XCTAssertFalse(view.visible)
        // The terminal keeps running unseen.
        XCTAssertFalse(view.processExited)
        view.close()
    }

    // MARK: Callbacks

    private func surfaceTarget(_ view: Ghostty.SurfaceView) throws -> ghostty_target_s {
        var target = ghostty_target_s()
        target.tag = GHOSTTY_TARGET_SURFACE
        target.target.surface = try XCTUnwrap(view.surface)
        return target
    }

    func testSurfaceActionsReachOnlyTheSurfaceTheyTarget() throws {
        let cat = Ghostty.SurfaceConfiguration(program: URL(fileURLWithPath: "/bin/cat"))
        let a = try XCTUnwrap(ghostty.makeSurface(cat))
        let b = try XCTUnwrap(ghostty.makeSurface(cat))
        let eventsA = SurfaceEvents()
        let eventsB = SurfaceEvents()
        a.delegate = eventsA
        b.delegate = eventsB
        let app = try XCTUnwrap(ghostty.app)

        var action = ghostty_action_s()
        action.tag = GHOSTTY_ACTION_SET_TITLE
        try "for b".withCString { ptr in
            action.action.set_title = ghostty_action_set_title_s(title: ptr)
            XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(b), action: action))
            // Refused for the app as a whole: no surface owns it.
            var appTarget = ghostty_target_s()
            appTarget.tag = GHOSTTY_TARGET_APP
            XCTAssertFalse(Ghostty.App.action(app, target: appTarget, action: action))
        }
        // Nothing is delivered from inside the callback.
        XCTAssertEqual(b.title, "")
        ghosttyWait("the title") { b.title == "for b" }
        XCTAssertEqual(a.title, "")
        XCTAssertEqual(eventsA.titles, [])
        XCTAssertEqual(eventsB.titles, ["for b"])

        action = ghostty_action_s()
        action.tag = GHOSTTY_ACTION_PWD
        try "/tmp/somewhere".withCString { ptr in
            action.action.pwd = ghostty_action_pwd_s(pwd: ptr)
            XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(a), action: action))
        }
        action = ghostty_action_s()
        action.tag = GHOSTTY_ACTION_DESKTOP_NOTIFICATION
        try "done".withCString { title in
            try "all green".withCString { body in
                action.action.desktop_notification = ghostty_action_desktop_notification_s(title: title, body: body)
                XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(a), action: action))
            }
        }
        ghosttyWait("a's events") { !eventsA.notifications.isEmpty }
        XCTAssertEqual(a.workingDirectory, "/tmp/somewhere")
        XCTAssertEqual(eventsA.directories, ["/tmp/somewhere"])
        XCTAssertEqual(eventsA.notifications, ["done|all green"])
        XCTAssertEqual(eventsB.directories, [])
        XCTAssertEqual(eventsB.notifications, [])

        // Layout actions are refused whoever they target.
        action = ghostty_action_s()
        action.tag = GHOSTTY_ACTION_NEW_SPLIT
        XCTAssertFalse(Ghostty.App.action(app, target: try surfaceTarget(a), action: action))

        // An event still queued when its surface closes is dropped.
        action = ghostty_action_s()
        action.tag = GHOSTTY_ACTION_CLOSE_WINDOW
        XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(b), action: action))
        b.close()
        RunLoop.current.run(until: Date().addingTimeInterval(0.1))
        XCTAssertEqual(eventsB.closeRequests, [])
        a.close()
    }

    /// A bell, a hovered link, and a failed renderer are the surface's own,
    /// reported to its delegate after the callback; with no delegate a
    /// desktop notification is refused.
    func testABellALinkAndTheRendererAreReportedForTheirSurface() throws {
        let cat = Ghostty.SurfaceConfiguration(program: URL(fileURLWithPath: "/bin/cat"))
        let a = try XCTUnwrap(ghostty.makeSurface(cat))
        let b = try XCTUnwrap(ghostty.makeSurface(cat))
        let eventsA = SurfaceEvents()
        a.delegate = eventsA
        let app = try XCTUnwrap(ghostty.app)

        var action = ghostty_action_s()
        action.tag = GHOSTTY_ACTION_RING_BELL
        XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(a), action: action))
        var appTarget = ghostty_target_s()
        appTarget.tag = GHOSTTY_TARGET_APP
        XCTAssertFalse(Ghostty.App.action(app, target: appTarget, action: action), "a bell rings in a terminal, not the app")

        action = ghostty_action_s()
        action.tag = GHOSTTY_ACTION_MOUSE_OVER_LINK
        let link = "https://example.com/a?b=1"
        try link.withCString { ptr in
            action.action.mouse_over_link = ghostty_action_mouse_over_link_s(url: ptr, len: link.utf8.count)
            XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(a), action: action))
        }
        ghosttyWait("the bell and the link") { eventsA.bells == 1 && a.hoveredLink == link }
        action.action.mouse_over_link = ghostty_action_mouse_over_link_s(url: nil, len: 0)
        XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(a), action: action))
        ghosttyWait("the pointer off the link") { a.hoveredLink == nil }
        XCTAssertEqual(eventsA.links, [link, nil])

        action = ghostty_action_s()
        action.tag = GHOSTTY_ACTION_RENDERER_HEALTH
        action.action.renderer_health = GHOSTTY_RENDERER_HEALTH_UNHEALTHY
        XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(a), action: action))
        ghosttyWait("the renderer failure") { !a.rendererHealthy }
        action.action.renderer_health = GHOSTTY_RENDERER_HEALTH_HEALTHY
        XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(a), action: action))
        ghosttyWait("the renderer recovery") { a.rendererHealthy }
        XCTAssertEqual(eventsA.health, [false, true])

        action = ghostty_action_s()
        action.tag = GHOSTTY_ACTION_DESKTOP_NOTIFICATION
        try "t".withCString { title in
            try "b".withCString { body in
                action.action.desktop_notification = ghostty_action_desktop_notification_s(title: title, body: body)
                XCTAssertFalse(Ghostty.App.action(app, target: try surfaceTarget(b), action: action),
                               "nobody shows b's notification, so Ghostty hears it was not shown")
            }
        }
        XCTAssertNil(b.hoveredLink)
        XCTAssertTrue(b.rendererHealthy)
        a.close()
        b.close()
    }

    /// Secure Keyboard Entry is held by a surface only while it is asked for
    /// and the surface has the keyboard, and never past its close.
    func testSecureInputIsHeldOnlyWhileFocusedAndReleasedOnClose() throws {
        let (savedEnable, savedDisable) = (Ghostty.SecureInput.enable, Ghostty.SecureInput.disable)
        var enabled = 0
        var disabled = 0
        Ghostty.SecureInput.enable = { enabled += 1 }
        Ghostty.SecureInput.disable = { disabled += 1 }
        defer {
            Ghostty.SecureInput.enable = savedEnable
            Ghostty.SecureInput.disable = savedDisable
        }
        let holders = Ghostty.SecureInput.holders
        let view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(program: URL(fileURLWithPath: "/bin/cat"))))
        let app = try XCTUnwrap(ghostty.app)
        func secure(_ change: ghostty_action_secure_input_e) throws {
            var action = ghostty_action_s()
            action.tag = GHOSTTY_ACTION_SECURE_INPUT
            action.action.secure_input = change
            XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(view), action: action))
        }

        // Taken at once on the main thread: the next key may be a password's.
        try secure(GHOSTTY_SECURE_INPUT_ON)
        XCTAssertTrue(view.secureInputRequested)
        XCTAssertFalse(view.holdsSecureInput, "a surface without the keyboard secures nothing")
        view.focusDidChange(true)
        XCTAssertTrue(view.holdsSecureInput)
        XCTAssertEqual(Ghostty.SecureInput.holders, holders + 1)
        view.focusDidChange(false)
        XCTAssertFalse(view.holdsSecureInput)
        view.focusDidChange(true)
        try secure(GHOSTTY_SECURE_INPUT_TOGGLE)
        XCTAssertFalse(view.secureInputRequested)
        XCTAssertFalse(view.holdsSecureInput)
        try secure(GHOSTTY_SECURE_INPUT_TOGGLE)
        XCTAssertTrue(view.holdsSecureInput)
        XCTAssertEqual([enabled, disabled], [3, 2])

        view.close()
        XCTAssertFalse(view.holdsSecureInput)
        XCTAssertEqual([enabled, disabled], [3, 3], "every reference taken is given back")
        XCTAssertEqual(Ghostty.SecureInput.holders, holders)
    }

    /// A key sequence or key table in force is known before the callback
    /// returns, so the next key is not taken by the menu.
    func testKeySequencesAndKeyTablesAreTrackedAtOnce() throws {
        let view = try XCTUnwrap(ghostty.makeSurface(Ghostty.SurfaceConfiguration(program: URL(fileURLWithPath: "/bin/cat"))))
        let app = try XCTUnwrap(ghostty.app)
        var action = ghostty_action_s()
        action.tag = GHOSTTY_ACTION_KEY_SEQUENCE
        action.action.key_sequence.active = true
        XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(view), action: action))
        XCTAssertTrue(view.inKeySequence)
        action.action.key_sequence.active = false
        XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(view), action: action))
        XCTAssertFalse(view.inKeySequence)

        action = ghostty_action_s()
        action.tag = GHOSTTY_ACTION_KEY_TABLE
        for name in ["vi", "inner"] {
            try name.withCString { ptr in
                action.action.key_table.tag = GHOSTTY_KEY_TABLE_ACTIVATE
                action.action.key_table.value.activate.name = ptr
                action.action.key_table.value.activate.len = name.utf8.count
                XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(view), action: action))
            }
        }
        XCTAssertEqual(view.keyTables, ["vi", "inner"])
        XCTAssertTrue(view.inKeySequence)
        action.action.key_table.tag = GHOSTTY_KEY_TABLE_DEACTIVATE
        XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(view), action: action))
        XCTAssertEqual(view.keyTables, ["vi"])
        action.action.key_table.tag = GHOSTTY_KEY_TABLE_DEACTIVATE_ALL
        XCTAssertTrue(Ghostty.App.action(app, target: try surfaceTarget(view), action: action))
        XCTAssertEqual(view.keyTables, [])
        XCTAssertFalse(view.inKeySequence)
        view.close()
    }

    func testADelegateMayCloseTheSurfaceFromItsCloseRequest() throws {
        let view = try XCTUnwrap(ghostty.makeSurface(probe("closing")))
        let pid = try XCTUnwrap(report("closing")).pid
        let events = SurfaceEvents()
        events.onCloseRequest = { $0.close() }
        view.delegate = events

        Ghostty.App.closeSurface(ghostty_surface_userdata(try XCTUnwrap(view.surface)), needsConfirm: false)
        ghosttyWait("the close request") { !events.closeRequests.isEmpty }
        XCTAssertEqual(events.closeRequests, [false])
        XCTAssertNil(view.surface)
        XCTAssertEqual(ghostty.surfaces.count, 0)
        waitForExit(pid, "closing")
    }

    /// End to end through libghostty: the program's title escape and its exit
    /// come back as callbacks for the surface that ran it, and only that one.
    func testAProgramsTitleAndExitAreReportedForItsOwnSurface() throws {
        let quiet = try XCTUnwrap(ghostty.makeSurface(probe("quiet")))
        let quietEvents = SurfaceEvents()
        quiet.delegate = quietEvents
        let quietPID = try XCTUnwrap(report("quiet")).pid

        let loud = try XCTUnwrap(ghostty.makeSurface(
            probe("loud", then: #"printf '\033]2;agent at work\007'; sleep 1; exit 7"#)))
        let loudEvents = SurfaceEvents()
        loud.delegate = loudEvents
        let loudPID = try XCTUnwrap(report("loud")).pid

        ghosttyWait("the program's title") { loud.title == "agent at work" }
        ghosttyWait("the program's exit") { loudEvents.exits > 0 || !loudEvents.closeRequests.isEmpty }
        waitForExit(loudPID, "loud")
        XCTAssertEqual(loudEvents.exits, 1)
        XCTAssertTrue(loud.childExited)
        XCTAssertTrue(loud.processExited)
        // The exited surface stays open, readable, until its host closes it.
        XCTAssertNotNil(loud.surface)
        XCTAssertEqual(ghostty.surfaces.count, 2)

        XCTAssertEqual(quietEvents.exits, 0)
        XCTAssertEqual(quietEvents.closeRequests, [])
        XCTAssertFalse(quietEvents.titles.contains("agent at work"))
        XCTAssertFalse(quiet.childExited)
        XCTAssertTrue(isRunning(quietPID))

        loud.close()
        quiet.close()
        waitForExit(quietPID, "quiet")
    }
}

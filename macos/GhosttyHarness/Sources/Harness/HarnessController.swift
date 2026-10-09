import AppKit
import GhosttyKit

/// Owns the harness window and its one surface, and executes the driver's
/// control commands. Every input command goes through the same AppKit entry
/// points a person's keyboard, trackpad, or Cmd+V would reach, so a passing
/// case exercises the ported SurfaceView glue rather than bypassing it.
///
/// Protocol: the driver writes `<id> <verb> [args]` lines to the control FIFO;
/// the harness appends `<id> ok [detail]` or `<id> err <reason>` to the log.
/// Unsolicited events are logged with the id `-`.
final class HarnessController: NSObject, NSApplicationDelegate, GhosttyAppDelegate {
    private let command: String
    private let controlPath: String
    private let log: HarnessLog
    private let environment: [String: String]

    private var ghostty: Ghostty.App?
    private var window: NSWindow?
    private var surfaceView: Ghostty.SurfaceView?

    init(command: String, controlPath: String, logPath: String, environment: [String: String]) {
        self.command = command
        self.controlPath = controlPath
        self.log = HarnessLog(path: logPath)
        self.environment = environment
    }

    // MARK: Lifecycle

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.mainMenu = MainMenu.make()

        let overrides = Ghostty.ConfigOverrides(
            command: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", command],
            environment: environment,
            waitAfterCommand: false,
            fontSize: 13,
            extraLines: [
                // Pastes are driven unattended; a confirmation sheet would hang the run.
                "clipboard-paste-protection = false",
            ]
        )
        // userConfig nil: the harness never reads the operator's Ghostty files,
        // so a run is reproducible from the pins alone.
        let app = Ghostty.App(makeConfig: {
            Ghostty.Config(userConfig: nil, overrides: overrides)
        })
        app.delegate = self
        ghostty = app
        guard app.readiness == .ready, let ghosttyApp = app.app else {
            log.event("fatal ghostty-app-\(app.readiness.rawValue)")
            NSApp.terminate(nil)
            return
        }

        let view = Ghostty.SurfaceView(app: ghosttyApp)
        surfaceView = view
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1000, height: 640),
            styleMask: [.titled, .closable, .resizable, .miniaturizable],
            backing: .buffered,
            defer: false
        )
        window.title = "AgentMux Ghostty Harness"
        window.contentView = view
        window.center()
        window.makeKeyAndOrderFront(nil)
        window.makeFirstResponder(view)
        self.window = window
        NSApp.activate(ignoringOtherApps: true)

        log.event("ready pid=\(getpid()) ghostty=\(Ghostty.versionDescription.replacingOccurrences(of: " ", with: "_"))")
        startControlReader()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }

    // MARK: GhosttyAppDelegate

    func ghosttyApp(_ app: Ghostty.App, perform action: ghostty_action_s, target: ghostty_target_s) -> Bool {
        switch action.tag {
        case GHOSTTY_ACTION_SET_TITLE:
            if let ptr = action.action.set_title.title {
                let title = String(cString: ptr)
                window?.title = title
                surfaceView?.setTitle(title)
            }
            return true
        case GHOSTTY_ACTION_SHOW_CHILD_EXITED:
            log.event("child-exited")
            return true
        case GHOSTTY_ACTION_CLOSE_WINDOW:
            NSApp.terminate(nil)
            return true
        case GHOSTTY_ACTION_MOUSE_SHAPE:
            surfaceView?.setCursorShape(action.action.mouse_shape)
            return true
        default:
            return false
        }
    }

    func ghosttyApp(_ app: Ghostty.App, closeSurface host: GhosttySurfaceHost?, needsConfirm: Bool) {
        // The viewer's command ended (tmux detached or exited): the viewer goes
        // with it, and whatever tmux holds keeps running.
        log.event("surface-closed")
        NSApp.terminate(nil)
    }

    // MARK: Control channel

    private func startControlReader() {
        let path = controlPath
        Thread.detachNewThread { [weak self] in
            while true {
                // Opening a FIFO for reading blocks until a writer appears; at
                // EOF the driver closed its end, so wait for the next writer.
                guard let file = fopen(path, "r") else {
                    self?.log.event("fatal control-open-failed")
                    return
                }
                var line: UnsafeMutablePointer<CChar>?
                var cap = 0
                while getline(&line, &cap, file) > 0 {
                    let text = String(cString: line!).trimmingCharacters(in: .newlines)
                    DispatchQueue.main.async { self?.handle(line: text) }
                }
                free(line)
                fclose(file)
            }
        }
    }

    private func handle(line: String) {
        guard !line.isEmpty else { return }
        let parts = line.split(separator: " ", maxSplits: 2, omittingEmptySubsequences: false).map(String.init)
        guard parts.count >= 2 else {
            log.write("- err malformed \(line)")
            return
        }
        let id = parts[0]
        let verb = parts[1]
        let rest = parts.count > 2 ? parts[2] : ""
        do {
            let detail = try perform(verb: verb, rest: rest)
            log.write("\(id) ok\(detail.isEmpty ? "" : " " + detail)")
        } catch {
            log.write("\(id) err \(error)")
        }
    }

    private struct CommandError: Error, CustomStringConvertible {
        let description: String
    }

    private func perform(verb: String, rest: String) throws -> String {
        guard let view = surfaceView, let surface = view.surface, let window else {
            throw CommandError(description: "no-surface")
        }
        switch verb {
        case "size":
            return Self.describe(ghostty_surface_size(surface))

        case "resize":
            let dims = rest.split(separator: " ").compactMap { Double($0) }
            guard dims.count == 2 else { throw CommandError(description: "usage: resize W H") }
            window.setContentSize(NSSize(width: dims[0], height: dims[1]))
            return Self.describe(ghostty_surface_size(surface))

        case "screen":
            guard !rest.isEmpty else { throw CommandError(description: "usage: screen PATH") }
            let text = Self.viewportText(surface)
            try text.write(toFile: rest, atomically: true, encoding: .utf8)
            return "bytes=\(text.utf8.count)"

        case "key":
            let specs = rest.split(separator: " ").map(String.init)
            guard !specs.isEmpty else { throw CommandError(description: "usage: key SPEC...") }
            for spec in specs {
                guard let key = KeySpec(spec) else { throw CommandError(description: "unknown-key \(spec)") }
                press(key, in: window, view: view)
            }
            return "keys=\(specs.count)"

        case "type":
            // Every character as a key press, as typing would produce it.
            for char in rest {
                if let key = KeySpec(character: char) {
                    press(key, in: window, view: view)
                } else {
                    let s = String(char)
                    s.withCString { ghostty_surface_text(surface, $0, UInt(s.utf8.count)) }
                }
            }
            return "chars=\(rest.count)"

        case "paste":
            // `\n` in the argument is a newline, so one line can carry a
            // multi-line paste. Delivered by Cmd+V, like a person's paste.
            let text = rest.replacingOccurrences(of: "\\n", with: "\n")
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
            guard let key = KeySpec("cmd+v") else { throw CommandError(description: "no-cmd-v") }
            press(key, in: window, view: view)
            return "bytes=\(text.utf8.count)"

        case "scroll":
            guard let lines = Int32(rest) else { throw CommandError(description: "usage: scroll LINES") }
            try scroll(lines: lines, in: window, view: view)
            return "lines=\(lines)"

        case "pid":
            return "pid=\(getpid())"

        case "quit":
            DispatchQueue.main.async { NSApp.terminate(nil) }
            return ""

        default:
            throw CommandError(description: "unknown-verb \(verb)")
        }
    }

    // MARK: Input synthesis

    private func press(_ key: KeySpec, in window: NSWindow, view: Ghostty.SurfaceView) {
        // The window may not be key when the driver runs unattended; the
        // surface is told it is focused so its key handling matches a focused one.
        window.makeFirstResponder(view)
        view.focusDidChange(true)
        for type in [NSEvent.EventType.keyDown, .keyUp] {
            guard let event = NSEvent.keyEvent(
                with: type,
                location: .zero,
                modifierFlags: key.flags,
                timestamp: ProcessInfo.processInfo.systemUptime,
                windowNumber: window.windowNumber,
                context: nil,
                characters: key.characters,
                charactersIgnoringModifiers: key.charactersIgnoringModifiers,
                isARepeat: false,
                keyCode: key.keyCode
            ) else { continue }
            // NSApplication offers Cmd and Ctrl chords as key equivalents
            // before keyDown; the harness keeps that order.
            if type == .keyDown, !key.flags.isDisjoint(with: [.command, .control]),
               view.performKeyEquivalent(with: event) {
                continue
            }
            if type == .keyUp, key.flags.contains(.command) {
                // Command key-ups skip the responder chain in AppKit too.
                view.keyUp(with: event)
                continue
            }
            window.sendEvent(event)
        }
    }

    private func scroll(lines: Int32, in window: NSWindow, view: Ghostty.SurfaceView) throws {
        let center = NSPoint(x: view.bounds.midX, y: view.bounds.midY)
        let inWindow = view.convert(center, to: nil)
        if let moved = NSEvent.mouseEvent(
            with: .mouseMoved, location: inWindow, modifierFlags: [],
            timestamp: ProcessInfo.processInfo.systemUptime, windowNumber: window.windowNumber,
            context: nil, eventNumber: 0, clickCount: 0, pressure: 0
        ) {
            view.mouseMoved(with: moved)
        }
        guard let cg = CGEvent(
            scrollWheelEvent2Source: nil, units: .line, wheelCount: 1,
            wheel1: lines, wheel2: 0, wheel3: 0
        ), let event = NSEvent(cgEvent: cg) else {
            throw CommandError(description: "scroll-event-failed")
        }
        view.scrollWheel(with: event)
    }

    // MARK: Reporting

    private static func describe(_ size: ghostty_surface_size_s) -> String {
        "cols=\(size.columns) rows=\(size.rows) width_px=\(size.width_px) height_px=\(size.height_px)"
    }

    /// The text Ghostty itself shows in the viewport: the viewer-side capture,
    /// compared against tmux's capture-pane of the same moment.
    private static func viewportText(_ surface: ghostty_surface_t) -> String {
        let selection = ghostty_selection_s(
            top_left: ghostty_point_s(tag: GHOSTTY_POINT_VIEWPORT, coord: GHOSTTY_POINT_COORD_TOP_LEFT, x: 0, y: 0),
            bottom_right: ghostty_point_s(tag: GHOSTTY_POINT_VIEWPORT, coord: GHOSTTY_POINT_COORD_BOTTOM_RIGHT, x: 0, y: 0),
            rectangle: false
        )
        var text = ghostty_text_s()
        guard ghostty_surface_read_text(surface, selection, &text) else { return "" }
        defer { ghostty_surface_free_text(surface, &text) }
        guard let ptr = text.text else { return "" }
        return String(decoding: Data(bytes: ptr, count: Int(text.text_len)), as: UTF8.self)
    }
}

/// Appends one line per result, flushed immediately, so a driver polling the
/// file sees each result as soon as it exists.
final class HarnessLog {
    private let handle: FileHandle?

    init(path: String) {
        FileManager.default.createFile(atPath: path, contents: nil)
        handle = FileHandle(forWritingAtPath: path)
        handle?.seekToEndOfFile()
    }

    func write(_ line: String) {
        handle?.write((line + "\n").data(using: .utf8)!)
    }

    func event(_ detail: String) {
        write("- \(detail)")
    }
}

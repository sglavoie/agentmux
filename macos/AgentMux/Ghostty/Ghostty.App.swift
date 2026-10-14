// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import AppKit
import GhosttyKit
import UniformTypeIdentifiers

/// Receives what only the application can decide.
protocol GhosttyAppDelegate: AnyObject {
    /// A `quit` binding fired in a terminal. Agent Mux.app outlives its
    /// terminals, so nothing quits unless the delegate does it.
    func ghosttyAppDidRequestQuit(_ app: Ghostty.App)
}

extension Ghostty {
    /// Owns the one `ghostty_app_t`, answers its runtime callbacks, and keeps
    /// track of every surface made from it.
    ///
    /// Ownership runs one way: a live surface holds its app strongly and the
    /// app only lists its surfaces weakly, so the app can never be freed under
    /// a surface. `shutdown()` is the explicit end: it closes every surface,
    /// then frees the app.
    final class App {
        enum Readiness: String {
            /// `closed` is after `shutdown()`: no app object and no surfaces.
            case loading, error, ready, closed
        }

        weak var delegate: GhosttyAppDelegate?

        private(set) var readiness: Readiness = .loading

        private(set) var config: Config

        private var appearanceObserver: NSKeyValueObservation?

        /// Rebuilds the layered config, so a reload re-applies the overrides.
        private let makeConfig: () -> Config

        /// Every surface view made from this app that has not been closed.
        private let registry = NSHashTable<SurfaceView>.weakObjects()

        private(set) var app: ghostty_app_t? {
            didSet {
                guard let old = oldValue else { return }
                ghostty_app_free(old)
            }
        }

        init(makeConfig: @escaping () -> Config = { Config() }) {
            self.makeConfig = makeConfig
            config = makeConfig()
            Self.logDiagnostics(config)
            guard let cfg = config.config else {
                readiness = .error
                return
            }

            var runtime = ghostty_runtime_config_s(
                userdata: Unmanaged.passUnretained(self).toOpaque(),
                supports_selection_clipboard: true,
                wakeup_cb: { userdata in App.wakeup(userdata) },
                action_cb: { app, target, action in App.action(app!, target: target, action: action) },
                read_clipboard_cb: { userdata, loc, state in App.readClipboard(userdata, location: loc, state: state) },
                confirm_read_clipboard_cb: { userdata, str, state, request in
                    App.confirmReadClipboard(userdata, string: str, state: state, request: request)
                },
                write_clipboard_cb: { userdata, loc, content, len, confirm in
                    App.writeClipboard(userdata, location: loc, content: content, len: len, confirm: confirm)
                },
                close_surface_cb: { userdata, needsConfirm in App.closeSurface(userdata, needsConfirm: needsConfirm) }
            )

            guard let app = ghostty_app_new(&runtime, cfg) else {
                Ghostty.logger.critical("ghostty_app_new failed")
                readiness = .error
                return
            }
            self.app = app

            ghostty_app_set_focus(app, NSApp.isActive)
            let center = NotificationCenter.default
            center.addObserver(
                self, selector: #selector(keyboardSelectionDidChange),
                name: NSTextInputContext.keyboardSelectionDidChangeNotification, object: nil)
            center.addObserver(
                self, selector: #selector(applicationDidBecomeActive),
                name: NSApplication.didBecomeActiveNotification, object: nil)
            center.addObserver(
                self, selector: #selector(applicationDidResignActive),
                name: NSApplication.didResignActiveNotification, object: nil)

            // Conditional themes and color-scheme queries follow the system
            // appearance, reported once now and on every change.
            appearanceObserver = NSApplication.shared.observe(\.effectiveAppearance, options: [.initial, .new]) {
                [weak self] application, _ in
                guard let app = self?.app else { return }
                ghostty_app_set_color_scheme(app, Ghostty.colorScheme(for: application.effectiveAppearance))
            }

            readiness = .ready
        }

        deinit {
            shutdown()
        }

        // MARK: Surfaces

        /// The surfaces made from this app that are still open.
        var surfaces: [SurfaceView] {
            registry.allObjects.filter { $0.surface != nil }
        }

        /// A new terminal running `configuration`, or nil when the app is not
        /// ready or Ghostty could not create the surface. The caller owns the
        /// view; closing or releasing it frees the surface and ends what it ran.
        func makeSurface(_ configuration: SurfaceConfiguration = SurfaceConfiguration()) -> SurfaceView? {
            guard readiness == .ready, app != nil else { return nil }
            let view = SurfaceView(app: self, configuration: configuration)
            guard view.surface != nil else { return nil }
            return view
        }

        func register(_ view: SurfaceView) {
            registry.add(view)
        }

        func unregister(_ view: SurfaceView) {
            registry.remove(view)
        }

        /// The open surface view hosting `surface`, if this app made it.
        func surfaceView(for surface: ghostty_surface_t?) -> SurfaceView? {
            guard let surface,
                  let view = Ghostty.surfaceHost(from: ghostty_surface_userdata(surface)) as? SurfaceView,
                  view.surface == surface else {
                return nil
            }
            return view
        }

        /// Closes every surface, then frees the app. Idempotent; afterwards
        /// the app makes no surfaces and its views are inert.
        func shutdown() {
            for view in registry.allObjects {
                view.close()
            }
            registry.removeAllObjects()
            NotificationCenter.default.removeObserver(self)
            appearanceObserver = nil
            app = nil
            if readiness == .ready {
                readiness = .closed
            }
        }

        // MARK: App operations

        func appTick() {
            guard let app else { return }
            ghostty_app_tick(app)
        }

        /// Reloads the layered config from disk and hands it to the app, which
        /// passes it to every surface; the applied value comes back through
        /// `GHOSTTY_ACTION_CONFIG_CHANGE`. Every surface has taken it by the
        /// time this returns. `resetFontSize` also moves manually zoomed
        /// surfaces to the new default. Returns whether the config applied.
        @discardableResult
        func reloadConfig(resetFontSize: Bool = false) -> Bool {
            guard let app else { return false }
            let fresh = makeConfig()
            Self.logDiagnostics(fresh)
            guard let cfg = fresh.config else {
                Ghostty.logger.warning("config reload failed; keeping the current config")
                return false
            }
            // Ghostty clones what it keeps, so the old config can go.
            ghostty_app_update_config(app, cfg)
            config = fresh
            if resetFontSize {
                for surface in surfaces { surface.resetZoom(nil) }
            }
            return true
        }

        /// Opens the Agent Mux Ghostty config file, creating an empty one on
        /// first use.
        static func openConfig(at url: URL = Config.userConfigURL()) {
            guard ensureConfigFile(at: url) else { return }
            // A .conf file often has no default app; TextEdit always can.
            if NSWorkspace.shared.urlForApplication(toOpen: url) != nil {
                NSWorkspace.shared.open(url)
            } else if let textEdit = NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.apple.TextEdit") {
                NSWorkspace.shared.open([url], withApplicationAt: textEdit, configuration: NSWorkspace.OpenConfiguration())
            }
        }

        /// Whether a config file exists at `url` afterwards. An existing file
        /// is never rewritten.
        @discardableResult
        static func ensureConfigFile(at url: URL) -> Bool {
            let fm = FileManager.default
            if fm.fileExists(atPath: url.path) { return true }
            do {
                try fm.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
                try "# Ghostty settings for Agent Mux.app's terminals. See https://ghostty.org/docs/config\n"
                    .write(to: url, atomically: true, encoding: .utf8)
                return true
            } catch {
                Ghostty.logger.warning("creating \(url.path, privacy: .public) failed: \(error.localizedDescription, privacy: .public)")
                return false
            }
        }

        static func logDiagnostics(_ config: Config) {
            for message in config.diagnostics {
                Ghostty.logger.warning("config: \(message, privacy: .public)")
            }
        }

        // MARK: Notifications

        @objc private func keyboardSelectionDidChange(_ notification: Notification) {
            guard let app else { return }
            ghostty_app_keyboard_changed(app)
        }

        @objc private func applicationDidBecomeActive(_ notification: Notification) {
            guard let app else { return }
            ghostty_app_set_focus(app, true)
        }

        @objc private func applicationDidResignActive(_ notification: Notification) {
            guard let app else { return }
            ghostty_app_set_focus(app, false)
        }

        // MARK: Runtime callbacks

        private static func appState(from userdata: UnsafeMutableRawPointer?) -> App? {
            guard let userdata else { return nil }
            return Unmanaged<App>.fromOpaque(userdata).takeUnretainedValue()
        }

        private static func appState(from app: ghostty_app_t) -> App? {
            appState(from: ghostty_app_userdata(app))
        }

        static func wakeup(_ userdata: UnsafeMutableRawPointer?) {
            guard let state = appState(from: userdata) else { return }
            // Wakeup may arrive on any thread; the tick belongs to the main one.
            DispatchQueue.main.async { state.appTick() }
        }

        static func action(_ app: ghostty_app_t, target: ghostty_target_s, action: ghostty_action_s) -> Bool {
            switch target.tag {
            case GHOSTTY_TARGET_APP, GHOSTTY_TARGET_SURFACE:
                break
            default:
                Ghostty.logger.warning("unknown action target=\(target.tag.rawValue)")
                return false
            }

            switch Ghostty.disposition(of: action.tag) {
            case .ignored:
                Ghostty.logger.debug("ignored action=\(action.tag.rawValue)")
                return false

            case .surface:
                // The view is found now, while the target is certainly alive;
                // what it does with the action may happen later.
                guard target.tag == GHOSTTY_TARGET_SURFACE,
                      let view = appState(from: app)?.surfaceView(for: target.target.surface) else {
                    return false
                }
                return view.perform(action)

            case .app:
                return performAppAction(app, action: action)
            }
        }

        private static func performAppAction(_ app: ghostty_app_t, action: ghostty_action_s) -> Bool {
            switch action.tag {
            case GHOSTTY_ACTION_QUIT:
                guard let state = appState(from: app) else { return false }
                Ghostty.onMain { [weak state] in
                    guard let state else { return }
                    state.delegate?.ghosttyAppDidRequestQuit(state)
                }
                return true

            case GHOSTTY_ACTION_OPEN_URL:
                return openURL(action.action.open_url)

            case GHOSTTY_ACTION_OPEN_CONFIG:
                Ghostty.onMain { openConfig() }
                return true

            case GHOSTTY_ACTION_RELOAD_CONFIG:
                appState(from: app)?.reloadConfig()
                return true

            case GHOSTTY_ACTION_CONFIG_CHANGE:
                // Only an app-targeted change replaces the app config; a
                // surface's own change is that surface's business.
                return true

            default:
                return false
            }
        }

        /// Opens a link a terminal asked for. Replaced in tests, which must
        /// not open anything on the machine they run on.
        static var openLink: (URL) -> Void = { NSWorkspace.shared.open($0) }

        /// Shows a file a terminal linked to in the Finder. Replaced in tests.
        static var revealFile: (URL) -> Void = { NSWorkspace.shared.activateFileViewerSelecting([$0]) }

        /// Stands in for the question asked before a link of an unusual
        /// scheme is opened, for tests. Answers on the main thread.
        static var confirmLink: ((URL, @escaping (Bool) -> Void) -> Void)?

        /// What is done with a link a terminal asked to open.
        enum LinkDisposition: Equatable {
            /// The web and mail: opened at once.
            case open
            /// A file: shown in the Finder, never opened or run.
            case reveal
            /// Anything else may launch an app with arguments from the
            /// terminal, so the operator is asked first.
            case confirm
        }

        static let safeLinkSchemes: Set<String> = ["http", "https", "mailto"]

        static func linkDisposition(_ url: URL) -> LinkDisposition {
            if url.isFileURL { return .reveal }
            return safeLinkSchemes.contains(url.scheme?.lowercased() ?? "") ? .open : .confirm
        }

        /// The link as Ghostty sent it: a URL with a scheme, else a path.
        static func link(from str: String) -> URL {
            if let parsed = URL(string: str), parsed.scheme != nil { return parsed }
            return URL(fileURLWithPath: (str as NSString).expandingTildeInPath)
        }

        /// Opens, reveals, or asks about a link, on the main thread.
        static func follow(_ url: URL) {
            switch linkDisposition(url) {
            case .open:
                openLink(url)
            case .reveal:
                revealFile(url)
            case .confirm:
                let answer: (Bool) -> Void = { allowed in
                    guard allowed else { return }
                    openLink(url)
                }
                if let confirmLink { return confirmLink(url, answer) }
                askToOpen(url, answer)
            }
        }

        private static func askToOpen(_ url: URL, _ answer: @escaping (Bool) -> Void) {
            let alert = NSAlert()
            alert.alertStyle = .warning
            alert.messageText = "Open this \(url.scheme ?? "") link?"
            alert.informativeText = "A program in the terminal asked to open:\n\(url.absoluteString)\n\nIt may start another app."
            alert.addButton(withTitle: "Open")
            alert.addButton(withTitle: "Cancel")
            present(alert, on: NSApp.keyWindow) { answer($0 == .alertFirstButtonReturn) }
        }

        /// A sheet on `window` when it can take one, else app-modal.
        static func present(_ alert: NSAlert, on window: NSWindow?, _ done: @escaping (NSApplication.ModalResponse) -> Void) {
            if let window, window.isVisible {
                alert.beginSheetModal(for: window, completionHandler: done)
            } else {
                done(alert.runModal())
            }
        }

        private static func openURL(_ v: ghostty_action_open_url_s) -> Bool {
            guard let ptr = v.url else { return false }
            let data = Data(bytes: ptr, count: Int(v.len))
            guard let str = String(data: data, encoding: .utf8), !str.isEmpty else { return false }
            let url = link(from: str)
            Ghostty.logger.notice("opening a link with scheme \(url.scheme ?? "none", privacy: .public)")
            Ghostty.onMain { follow(url) }
            return true
        }

        static func closeSurface(_ userdata: UnsafeMutableRawPointer?, needsConfirm: Bool) {
            guard let view = Ghostty.surfaceHost(from: userdata) as? SurfaceView else { return }
            view.closeRequested(needsConfirm: needsConfirm)
        }

        // MARK: Clipboard

        /// Where a selection goes, as upstream Ghostty.app keeps it: a private
        /// pasteboard of the app's own. With one, copy-on-select fills it
        /// rather than the general pasteboard, and a middle click pastes from
        /// it, in every terminal; ⌘C still copies to the general one.
        static let selectionPasteboard = NSPasteboard(name: .init("io.github.sglavoie.agentmux.selection"))

        static func pasteboard(for location: ghostty_clipboard_e) -> NSPasteboard? {
            switch location {
            case GHOSTTY_CLIPBOARD_STANDARD: return .general
            case GHOSTTY_CLIPBOARD_SELECTION: return selectionPasteboard
            default: return nil
            }
        }

        static func readClipboard(
            _ userdata: UnsafeMutableRawPointer?,
            location: ghostty_clipboard_e,
            state: UnsafeMutableRawPointer?
        ) -> Bool {
            guard let host = Ghostty.surfaceHost(from: userdata), let surface = host.surface else { return false }
            // A drop of several lines pastes through here (`SurfaceView.paste`).
            if let view = host as? SurfaceView, let text = view.pendingPaste {
                view.pendingPaste = nil
                completeClipboardRequest(surface, data: text, state: state)
                return true
            }
            guard let pasteboard = pasteboard(for: location) else { return false }
            // No text-like content: let a performable paste binding fall
            // through to the terminal.
            guard let str = stringContents(of: pasteboard) else { return false }
            completeClipboardRequest(surface, data: str, state: state)
            return true
        }

        /// File URLs paste as shell-escaped paths, other URLs as text.
        static func stringContents(of pasteboard: NSPasteboard) -> String? {
            if let urls = pasteboard.readObjects(forClasses: [NSURL.self]) as? [URL], !urls.isEmpty {
                return urls
                    .map { $0.isFileURL ? shellEscape($0.path) : $0.absoluteString }
                    .joined(separator: " ")
            }
            return pasteboard.string(forType: .string)
        }

        static func shellEscape(_ str: String) -> String {
            let special = "\\ ()[]{}<>\"'`!#$&;|*?\t"
            var result = str
            for char in special {
                result = result.replacingOccurrences(of: String(char), with: "\\\(char)")
            }
            return result
        }

        static func confirmReadClipboard(
            _ userdata: UnsafeMutableRawPointer?,
            string: UnsafePointer<CChar>?,
            state: UnsafeMutableRawPointer?,
            request: ghostty_clipboard_request_e
        ) {
            guard let host = Ghostty.surfaceHost(from: userdata) else { return }
            // Ghostty waits on every request it makes: one whose text cannot
            // be read is answered with nothing rather than left hanging.
            guard let string, let text = String(cString: string, encoding: .utf8) else {
                if let surface = host.surface {
                    completeClipboardRequest(surface, data: "", state: state, confirmed: true)
                }
                return
            }
            // Held weakly: the terminal may close while the question is up,
            // and then there is no request left to answer.
            weak let asker = host
            DispatchQueue.main.async {
                guard let view = asker else { return }
                let question = ClipboardQuestion(text: text, request: request, requester: (view as? SurfaceView)?.hostName ?? "")
                askClipboard(question, window: (view as? NSView)?.window) { allowed in
                    guard let surface = asker?.surface else { return }
                    completeClipboardRequest(surface, data: allowed ? text : "", state: state, confirmed: true)
                }
            }
        }

        static func completeClipboardRequest(
            _ surface: ghostty_surface_t,
            data: String,
            state: UnsafeMutableRawPointer?,
            confirmed: Bool = false
        ) {
            data.withCString { ptr in
                ghostty_surface_complete_clipboard_request(surface, ptr, state, confirmed)
            }
        }

        static func writeClipboard(
            _ userdata: UnsafeMutableRawPointer?,
            location: ghostty_clipboard_e,
            content: UnsafePointer<ghostty_clipboard_content_s>?,
            len: Int,
            confirm: Bool
        ) {
            guard let pasteboard = pasteboard(for: location), let content, len > 0 else { return }
            let items: [(type: NSPasteboard.PasteboardType, data: String)] = (0..<len).compactMap { i in
                guard let mime = content[i].mime, let data = content[i].data,
                      let type = pasteboardType(mime: String(cString: mime)) else {
                    return nil
                }
                return (type, String(cString: data))
            }
            guard !items.isEmpty else { return }

            let write = {
                pasteboard.declareTypes(items.map(\.type), owner: nil)
                for item in items {
                    pasteboard.setString(item.data, forType: item.type)
                }
            }
            guard confirm else {
                write()
                return
            }
            // A confirmed write (OSC 52) asks about its plain-text half only.
            guard let text = items.first(where: { $0.type == .string })?.data else { return }
            weak let asker = Ghostty.surfaceHost(from: userdata)
            DispatchQueue.main.async {
                let question = ClipboardQuestion(
                    text: text, request: GHOSTTY_CLIPBOARD_REQUEST_OSC_52_WRITE,
                    requester: (asker as? SurfaceView)?.hostName ?? "")
                askClipboard(question, window: (asker as? NSView)?.window) { allowed in
                    if allowed { write() }
                }
            }
        }

        static func pasteboardType(mime: String) -> NSPasteboard.PasteboardType? {
            if mime == "text/plain" { return .string }
            guard let utType = UTType(mimeType: mime) else { return nil }
            return NSPasteboard.PasteboardType(utType.identifier)
        }

        /// What a program asked to do with the clipboard, and who asked.
        struct ClipboardQuestion {
            let text: String
            let request: ghostty_clipboard_request_e
            /// The view or session the terminal is, as the workspace names
            /// it; empty when the host named none.
            let requester: String
        }

        /// Stands in for the clipboard sheet, for tests. Answers on the main
        /// thread.
        static var confirmClipboard: ((ClipboardQuestion, @escaping (Bool) -> Void) -> Void)?

        /// Asks whether a program may touch the clipboard: a sheet on the
        /// asking terminal's window, app-modal when it has none.
        private static func askClipboard(_ question: ClipboardQuestion, window: NSWindow?, _ answer: @escaping (Bool) -> Void) {
            if let confirmClipboard { return confirmClipboard(question, answer) }
            present(clipboardAlert(question), on: window) { answer($0 == .alertFirstButtonReturn) }
        }

        /// The question, with the whole text in a scrollable preview: what
        /// is pasted is exactly what can be read here.
        static func clipboardAlert(_ question: ClipboardQuestion) -> NSAlert {
            let alert = NSAlert()
            let who = question.requester.isEmpty ? "the program" : question.requester
            switch question.request {
            case GHOSTTY_CLIPBOARD_REQUEST_OSC_52_READ:
                alert.messageText = "Allow \(who) to read the clipboard?"
                alert.informativeText = "This is what it would read."
            case GHOSTTY_CLIPBOARD_REQUEST_OSC_52_WRITE:
                alert.messageText = "Allow \(who) to write to the clipboard?"
                alert.informativeText = "This is what it would write."
            default:
                alert.messageText = question.requester.isEmpty ? "Paste this text?" : "Paste this text into \(question.requester)?"
                alert.informativeText = "It may run commands when pasted."
            }
            let scroll = NSScrollView(frame: NSRect(x: 0, y: 0, width: 440, height: 180))
            scroll.hasVerticalScroller = true
            scroll.hasHorizontalScroller = false
            scroll.borderType = .bezelBorder
            let preview = NSTextView(frame: NSRect(origin: .zero, size: scroll.contentSize))
            preview.isEditable = false
            preview.isSelectable = true
            preview.isRichText = false
            preview.font = .monospacedSystemFont(ofSize: NSFont.smallSystemFontSize, weight: .regular)
            preview.textContainerInset = NSSize(width: 4, height: 4)
            preview.autoresizingMask = [.width]
            preview.isVerticallyResizable = true
            preview.textContainer?.widthTracksTextView = true
            preview.string = question.text
            scroll.documentView = preview
            alert.accessoryView = scroll
            alert.addButton(withTitle: "Allow")
            alert.addButton(withTitle: "Deny")
            return alert
        }
    }
}

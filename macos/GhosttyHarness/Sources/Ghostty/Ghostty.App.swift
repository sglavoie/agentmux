// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import AppKit
import GhosttyKit
import UniformTypeIdentifiers

/// Receives the runtime actions `Ghostty.disposition(of:)` routes to the
/// window and surface, plus a surface's close request.
protocol GhosttyAppDelegate: AnyObject {
    /// Returns whether the action was performed.
    func ghosttyApp(_ app: Ghostty.App, perform action: ghostty_action_s, target: ghostty_target_s) -> Bool
    /// `needsConfirm` is Ghostty's "would closing need confirmation", which
    /// upstream calls processAlive; it is not whether the process is alive.
    func ghosttyApp(_ app: Ghostty.App, closeSurface host: GhosttySurfaceHost?, needsConfirm: Bool)
}

extension Ghostty {
    /// Owns the one `ghostty_app_t` and answers its runtime callbacks.
    final class App {
        enum Readiness: String {
            case loading, error, ready
        }

        weak var delegate: GhosttyAppDelegate?

        private(set) var readiness: Readiness = .loading

        private(set) var config: Config

        private var appearanceObserver: NSKeyValueObservation?

        /// Rebuilds the layered config, so a reload re-applies the overrides.
        private let makeConfig: () -> Config

        private(set) var app: ghostty_app_t? {
            didSet {
                guard let old = oldValue else { return }
                ghostty_app_free(old)
            }
        }

        init(makeConfig: @escaping () -> Config) {
            self.makeConfig = makeConfig
            config = makeConfig()
            Self.logDiagnostics(config)
            guard let cfg = config.config else {
                readiness = .error
                return
            }

            var runtime = ghostty_runtime_config_s(
                userdata: Unmanaged.passUnretained(self).toOpaque(),
                supports_selection_clipboard: false,
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
            NotificationCenter.default.removeObserver(self)
            appearanceObserver = nil
            app = nil
        }

        // MARK: App operations

        func appTick() {
            guard let app else { return }
            ghostty_app_tick(app)
        }

        /// Reloads the layered config from disk and hands it to the app; the
        /// applied value comes back through `GHOSTTY_ACTION_CONFIG_CHANGE`.
        /// Every surface has taken it by the time this returns. Returns
        /// whether the new config was applied.
        @discardableResult
        func reloadConfig() -> Bool {
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
            return true
        }

        /// Opens the agentmux-ghostty-harness config file, creating an empty one on first use.
        static func openConfig() {
            let url = Config.userConfigURL()
            let fm = FileManager.default
            if !fm.fileExists(atPath: url.path) {
                do {
                    try fm.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
                    try "# Ghostty settings for tt.app. See https://ghostty.org/docs/config\n"
                        .write(to: url, atomically: true, encoding: .utf8)
                } catch {
                    Ghostty.logger.warning("creating \(url.path, privacy: .public) failed: \(error.localizedDescription, privacy: .public)")
                    return
                }
            }
            // A .conf file often has no default app; TextEdit always can.
            if NSWorkspace.shared.urlForApplication(toOpen: url) != nil {
                NSWorkspace.shared.open(url)
            } else if let textEdit = NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.apple.TextEdit") {
                NSWorkspace.shared.open([url], withApplicationAt: textEdit, configuration: NSWorkspace.OpenConfiguration())
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
                guard let state = appState(from: app), let delegate = state.delegate else { return false }
                return delegate.ghosttyApp(state, perform: action, target: target)

            case .app:
                return performAppAction(app, action: action)
            }
        }

        private static func performAppAction(_ app: ghostty_app_t, action: ghostty_action_s) -> Bool {
            switch action.tag {
            case GHOSTTY_ACTION_QUIT:
                NSApplication.shared.terminate(nil)
                return true

            case GHOSTTY_ACTION_OPEN_URL:
                return openURL(action.action.open_url)

            case GHOSTTY_ACTION_OPEN_CONFIG:
                openConfig()
                return true

            case GHOSTTY_ACTION_RELOAD_CONFIG:
                appState(from: app)?.reloadConfig()
                return true

            case GHOSTTY_ACTION_CONFIG_CHANGE:
                // Only an app-targeted change replaces the app config; a
                // surface's own change is that surface's business.
                return true

            case GHOSTTY_ACTION_RING_BELL:
                // A bell from a hidden or background tt cannot be heard as
                // tt's, so it bounces the Dock icon once instead.
                if NSApp.isActive {
                    NSSound.beep()
                } else {
                    NSApp.requestUserAttention(.informationalRequest)
                }
                return true

            default:
                return false
            }
        }

        private static func openURL(_ v: ghostty_action_open_url_s) -> Bool {
            guard let ptr = v.url else { return false }
            let data = Data(bytes: ptr, count: Int(v.len))
            guard let str = String(data: data, encoding: .utf8), !str.isEmpty else { return false }
            let url: URL
            if let parsed = URL(string: str), parsed.scheme != nil {
                url = parsed
            } else {
                url = URL(fileURLWithPath: (str as NSString).expandingTildeInPath)
            }
            NSWorkspace.shared.open(url)
            return true
        }

        static func closeSurface(_ userdata: UnsafeMutableRawPointer?, needsConfirm: Bool) {
            let host = Ghostty.surfaceHost(from: userdata)
            guard let surface = host?.surface,
                  let app = ghostty_surface_app(surface),
                  let state = appState(from: app) else {
                return
            }
            state.delegate?.ghosttyApp(state, closeSurface: host, needsConfirm: needsConfirm)
        }

        // MARK: Clipboard

        static func pasteboard(for location: ghostty_clipboard_e) -> NSPasteboard? {
            // Selection clipboard support is declared off, so only the
            // standard one is ever requested.
            location == GHOSTTY_CLIPBOARD_STANDARD ? .general : nil
        }

        static func readClipboard(
            _ userdata: UnsafeMutableRawPointer?,
            location: ghostty_clipboard_e,
            state: UnsafeMutableRawPointer?
        ) -> Bool {
            guard let surface = Ghostty.surfaceHost(from: userdata)?.surface,
                  let pasteboard = pasteboard(for: location) else {
                return false
            }
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
            guard let host = Ghostty.surfaceHost(from: userdata),
                  let string,
                  let text = String(cString: string, encoding: .utf8) else {
                return
            }
            DispatchQueue.main.async {
                // The surface may be gone by the time the question is answered.
                let allowed = confirmClipboard(text: text, request: request)
                guard let surface = host.surface else { return }
                completeClipboardRequest(surface, data: allowed ? text : "", state: state, confirmed: true)
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
            DispatchQueue.main.async {
                if confirmClipboard(text: text, request: GHOSTTY_CLIPBOARD_REQUEST_OSC_52_WRITE) {
                    write()
                }
            }
        }

        static func pasteboardType(mime: String) -> NSPasteboard.PasteboardType? {
            if mime == "text/plain" { return .string }
            guard let utType = UTType(mimeType: mime) else { return nil }
            return NSPasteboard.PasteboardType(utType.identifier)
        }

        /// Asks, app-modally, whether a program may touch the clipboard.
        private static func confirmClipboard(text: String, request: ghostty_clipboard_request_e) -> Bool {
            let alert = NSAlert()
            switch request {
            case GHOSTTY_CLIPBOARD_REQUEST_OSC_52_READ:
                alert.messageText = "Allow the program to read the clipboard?"
            case GHOSTTY_CLIPBOARD_REQUEST_OSC_52_WRITE:
                alert.messageText = "Allow the program to write to the clipboard?"
            default:
                alert.messageText = "Paste this text?"
                alert.informativeText = "It may run commands when pasted."
            }
            let preview = NSTextField(wrappingLabelWithString: String(text.prefix(2000)))
            preview.isSelectable = true
            preview.frame = NSRect(x: 0, y: 0, width: 360, height: 120)
            alert.accessoryView = preview
            alert.addButton(withTitle: "Allow")
            alert.addButton(withTitle: "Deny")
            return alert.runModal() == .alertFirstButtonReturn
        }
    }
}

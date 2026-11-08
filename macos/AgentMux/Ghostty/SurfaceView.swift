// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import AppKit
import GhosttyKit

extension Ghostty {
    /// One terminal: an AppKit view hosting a `ghostty_surface_t`. Any number
    /// share a `Ghostty.App`, each running its own `SurfaceConfiguration`.
    /// Rendering, sizing, scale, display, occlusion, focus, color scheme,
    /// keyboard, IME, and mouse are forwarded here.
    ///
    /// The view does not create its own layer. libghostty's Metal renderer
    /// makes it layer-hosting when the surface is created, assigning its own
    /// Metal-drawn layer before setting `wantsLayer`, exactly as upstream.
    ///
    /// The surface lives until `close()` or until the view is released,
    /// whichever comes first; freeing it ends the program it ran. A closed
    /// view is inert and can be left in or removed from a window freely.
    final class SurfaceView: NSView, GhosttySurfaceHost {
        private(set) var surface: ghostty_surface_t?

        /// What the surface was created to run.
        let configuration: SurfaceConfiguration

        weak var delegate: GhosttySurfaceDelegate?

        /// The app this surface belongs to, held until the surface is closed
        /// so the app outlives it.
        private var owner: App?

        /// The terminal's title (OSC 0/2), or empty until the program sets one.
        private(set) var title: String = ""

        /// The directory the program last reported (OSC 7), if any.
        private(set) var workingDirectory: String?

        /// The terminal's background once a program changed it (OSC 11); nil
        /// means the configured one.
        private(set) var backgroundColor: NSColor?

        /// Whether Ghostty has reported the surface's program as exited.
        private(set) var childExited = false

        /// Whether the surface was last told it has focus.
        private(set) var focused: Bool = false

        /// Whether the surface was last told it can be seen.
        var visible: Bool = true

        /// The link under the pointer, as Ghostty reports it; nil when none.
        private(set) var hoveredLink: String?

        /// Whether the renderer last said it is drawing.
        private(set) var rendererHealthy = true

        /// Where the viewport sits in the terminal's own buffer, once Ghostty
        /// has said; see SurfaceView+Search.swift.
        private(set) var scrollbar: Scrollbar?

        /// The program's last progress report (OSC 9;4), until it removes
        /// it or `Progress.expiry` passes without another.
        private(set) var progress: Progress?
        /// Counts reports, so an expiry overtaken by a later one is dropped.
        private var progressCount = 0

        /// Whether the program asked for Secure Keyboard Entry. It is in
        /// force only while the surface is focused (`holdsSecureInput`).
        private(set) var secureInputRequested = false

        /// Whether this surface holds one of the system's secure input
        /// references; see `Ghostty.SecureInput`.
        private(set) var holdsSecureInput = false

        /// Whether a multi-key binding has started and waits for its next
        /// key, and the key tables in force, innermost last. While either
        /// is set every key is Ghostty's, so the menu claims none.
        private(set) var keySequenceActive = false
        var keyTables: [String] = []

        /// What the operator knows this terminal as, named when a program
        /// in it asks for the clipboard; empty when the host set none.
        var hostName = ""

        /// Text a drop hands to Ghostty's paste, read back by the clipboard
        /// request that paste makes; see `paste(_:)`.
        var pendingPaste: String?

        /// Set when the view was resized while hidden: the surface is told
        /// once it is shown, not on every frame of a window's live resize.
        var sizePending = false

        /// The last size requested in points, reused when only the scale
        /// changes, because mid-animation the frame may lag the target size.
        var contentSize: CGSize = .zero

        private var appliedColorScheme: ghostty_color_scheme_e?

        /// The screen's text as last read for accessibility, and when; see
        /// SurfaceView+Accessibility.swift.
        var accessibilityContents: (text: String, read: ContinuousClock.Instant)?

        // MARK: Input state (see SurfaceView+Input.swift)

        /// The input method's uncommitted composition, mirrored to the
        /// surface as preedit.
        var markedText = NSMutableAttributedString()

        /// Non-nil only inside `keyDown`, collecting what `insertText`
        /// commits so the key event carries it rather than a bare paste.
        var keyTextAccumulator: [String]?

        /// The timestamp of a Cmd or Ctrl key equivalent handed back to AppKit
        /// unhandled; see `performKeyEquivalent`.
        var lastPerformKeyEvent: TimeInterval?

        /// Set when a click only focused the surface, so its release is not
        /// reported without a press.
        var suppressNextLeftMouseUp = false

        /// The pointer the terminal last asked for.
        var pointerCursor: NSCursor = .iBeam

        /// Who holds what scrolled off this terminal; see
        /// SurfaceView+History.swift. Part of the configuration, so it never
        /// changes under Ghostty's threads.
        var scrollback: Scrollback { configuration.scrollback }

        private var eventMonitor: Any?

        override var acceptsFirstResponder: Bool { surface != nil }

        /// Use `Ghostty.App.makeSurface(_:)`, which also reports failure.
        init(app owner: App, configuration: SurfaceConfiguration) {
            self.configuration = configuration
            // Non-zero so the renderer's layer has bounds before the first
            // layout; the real size arrives through `setFrameSize`.
            super.init(frame: NSRect(x: 0, y: 0, width: 800, height: 600))

            guard let app = owner.app else { return }

            // Registered before the surface exists so no change is missed.
            let center = NotificationCenter.default
            center.addObserver(
                self, selector: #selector(windowDidChangeScreen),
                name: NSWindow.didChangeScreenNotification, object: nil)
            center.addObserver(
                self, selector: #selector(windowDidChangeOcclusionState),
                name: NSWindow.didChangeOcclusionStateNotification, object: nil)
            center.addObserver(
                self, selector: #selector(windowKeyDidChange),
                name: NSWindow.didBecomeKeyNotification, object: nil)
            center.addObserver(
                self, selector: #selector(windowKeyDidChange),
                name: NSWindow.didResignKeyNotification, object: nil)

            // Command key-ups skip the responder chain, and a click that
            // only focuses the window must not reach the terminal; upstream
            // watches both locally rather than taking acceptsFirstMouse.
            eventMonitor = NSEvent.addLocalMonitorForEvents(
                matching: [.keyUp, .leftMouseDown]
            ) { [weak self] event in
                guard let self else { return event }
                return self.localEventHandler(event)
            }

            let created: ghostty_surface_t? = configuration.withSurfaceConfig { config in
                config.userdata = Unmanaged.passUnretained(self).toOpaque()
                config.platform_tag = GHOSTTY_PLATFORM_MACOS
                config.platform = ghostty_platform_u(macos: ghostty_platform_macos_s(
                    nsview: Unmanaged.passUnretained(self).toOpaque()
                ))
                // A guess until the view joins a window, whose screen then
                // supplies the real scale.
                config.scale_factor = Double(NSScreen.main?.backingScaleFactor ?? 2)
                config.context = GHOSTTY_SURFACE_CONTEXT_WINDOW
                return ghostty_surface_new(app, &config)
            }
            guard let created else {
                Ghostty.logger.critical("ghostty_surface_new failed")
                releaseObservers()
                return
            }
            surface = created
            self.owner = owner
            owner.register(self)
            registerForDraggedTypes(Self.dropTypes)
            sizeDidChange(frame.size)
            syncColorScheme()
            updateTrackingAreas()
        }

        required init?(coder: NSCoder) {
            fatalError("init(coder:) is not supported for this view")
        }

        deinit {
            // The app lists its surfaces weakly, so there is nothing to
            // unregister from here.
            teardown()
        }

        // MARK: Lifetime

        /// Frees the surface, which ends the program it ran, and drops
        /// everything held for it. Idempotent. Afterwards the view draws
        /// nothing and ignores input; it reports no further events.
        func close() {
            guard surface != nil else { return }
            unregisterDraggedTypes()
            owner?.unregister(self)
            teardown()
        }

        private func teardown() {
            releaseObservers()
            guard let surface else { return }
            self.surface = nil
            focused = false
            syncSecureInput()
            ghostty_surface_free(surface)
            owner = nil
        }

        private func releaseObservers() {
            NotificationCenter.default.removeObserver(self)
            if let eventMonitor {
                NSEvent.removeMonitor(eventMonitor)
                self.eventMonitor = nil
            }
        }

        /// Whether the surface's program has exited.
        var processExited: Bool {
            guard let surface else { return true }
            return ghostty_surface_process_exited(surface)
        }

        /// The font size in points the terminal shows now, zoom included;
        /// nil without a surface.
        var fontSize: Float? {
            guard let surface else { return nil }
            let size = ghostty_surface_inherited_config(surface, GHOSTTY_SURFACE_CONTEXT_WINDOW).font_size
            return size > 0 ? size : nil
        }

        /// Whether closing the surface would end a program Ghostty thinks
        /// should be asked about first.
        var needsConfirmQuit: Bool {
            guard let surface else { return false }
            return ghostty_surface_needs_confirm_quit(surface)
        }

        // MARK: Focus

        override func becomeFirstResponder() -> Bool {
            let result = super.becomeFirstResponder()
            if result { syncFocus() }
            return result
        }

        override func resignFirstResponder() -> Bool {
            let result = super.resignFirstResponder()
            if result { focusDidChange(false) }
            return result
        }

        @objc private func windowKeyDidChange(_ notification: Notification) {
            guard let window, notification.object as? NSWindow === window else { return }
            syncFocus()
        }

        /// Focused means first responder in the key window, as upstream's
        /// controller decides it; the cursor blinks and focus reports follow.
        /// With several surfaces in a window at most one is first responder,
        /// so at most one is focused.
        func syncFocus() {
            guard let window else {
                focusDidChange(false)
                return
            }
            focusDidChange(window.isKeyWindow && window.firstResponder === self)
        }

        func focusDidChange(_ focused: Bool) {
            guard let surface, self.focused != focused else { return }
            self.focused = focused
            ghostty_surface_set_focus(surface, focused)
            syncSecureInput()
        }

        // MARK: Secure input

        /// SECURE_INPUT from Ghostty: a program (or the binding) asked for
        /// Secure Keyboard Entry, or to end it.
        func setSecureInput(_ change: ghostty_action_secure_input_e) {
            switch change {
            case GHOSTTY_SECURE_INPUT_ON: secureInputRequested = true
            case GHOSTTY_SECURE_INPUT_OFF: secureInputRequested = false
            case GHOSTTY_SECURE_INPUT_TOGGLE: secureInputRequested.toggle()
            default: return
            }
            syncSecureInput()
        }

        /// Takes or lets go of this surface's secure input reference: held
        /// only while it is asked for and the surface has the keyboard, and
        /// never past `close()`.
        private func syncSecureInput() {
            let wanted = secureInputRequested && focused && surface != nil
            guard wanted != holdsSecureInput else { return }
            holdsSecureInput = wanted
            if wanted { Ghostty.SecureInput.take() } else { Ghostty.SecureInput.release() }
        }

        // MARK: Key sequences

        /// Whether Ghostty is in the middle of a key sequence or a key table,
        /// where the next key is its own whatever the menu holds.
        var inKeySequence: Bool { keySequenceActive || !keyTables.isEmpty }

        func setKeyTable(_ table: ghostty_action_key_table_s) {
            switch table.tag {
            case GHOSTTY_KEY_TABLE_ACTIVATE:
                let value = table.value.activate
                let name = value.name.map {
                    String(data: Data(bytes: $0, count: Int(value.len)), encoding: .utf8) ?? ""
                } ?? ""
                keyTables.append(name)
            case GHOSTTY_KEY_TABLE_DEACTIVATE:
                _ = keyTables.popLast()
            case GHOSTTY_KEY_TABLE_DEACTIVATE_ALL:
                keyTables.removeAll()
            default:
                break
            }
        }

        func setKeySequenceActive(_ active: Bool) {
            keySequenceActive = active
        }

        // MARK: Color scheme

        override func viewDidChangeEffectiveAppearance() {
            super.viewDidChangeEffectiveAppearance()
            syncColorScheme()
        }

        /// Reports the system appearance so a `theme = light:…,dark:…` config
        /// and color-scheme queries (mode 2031) follow it.
        private func syncColorScheme() {
            guard let surface else { return }
            let scheme = Ghostty.colorScheme(for: NSApp.effectiveAppearance)
            guard scheme != appliedColorScheme else { return }
            appliedColorScheme = scheme
            ghostty_surface_set_color_scheme(surface, scheme)
        }

        /// Takes a link the context menu's Copy Link copies; the general
        /// pasteboard unless a test says otherwise.
        static var copyLink: (String) -> Void = { link in
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(link, forType: .string)
        }

        /// What a bell does, as the app's config says.
        var bellFeatures: BellFeatures { owner?.config.bellFeatures ?? [] }

        /// Whether the config lets a scrollbar show (`scrollbar`, not `never`).
        var scrollbarAllowed: Bool { owner?.config.scrollbarAllowed ?? true }

        /// Whether the config lets progress reports show (`progress-style`).
        var progressAllowed: Bool { owner?.config.progressAllowed ?? true }

        func searchDidChange(_ update: SearchUpdate) {
            delegate?.surface(self, didUpdateSearch: update)
        }

        func setScrollbar(_ scrollbar: Scrollbar) {
            guard scrollbarAllowed, scrollbar != self.scrollbar else { return }
            self.scrollbar = scrollbar
            delegate?.surfaceDidChangeScrollbar(self)
        }

        /// Takes a progress report, or its removal, and drops it once
        /// `Progress.expiry` passes without another.
        func setProgress(_ progress: Progress?) {
            let progress = progressAllowed ? progress : nil
            progressCount += 1
            if progress != nil {
                let count = progressCount
                DispatchQueue.main.asyncAfter(deadline: .now() + Progress.expiry) { [weak self] in
                    guard let self, self.surface != nil, self.progressCount == count else { return }
                    self.setProgress(nil)
                }
            }
            guard progress != self.progress else { return }
            self.progress = progress
            delegate?.surfaceDidChangeProgress(self)
        }

        func setHoveredLink(_ url: String?) {
            guard url != hoveredLink else { return }
            hoveredLink = url
            delegate?.surfaceDidChangeHoveredLink(self)
        }

        func setRendererHealthy(_ healthy: Bool) {
            guard healthy != rendererHealthy else { return }
            rendererHealthy = healthy
            delegate?.surfaceDidChangeRendererHealth(self)
        }

        func setTitle(_ title: String) {
            guard title != self.title else { return }
            self.title = title
            delegate?.surfaceDidChangeTitle(self)
        }

        func setWorkingDirectory(_ path: String) {
            guard path != workingDirectory else { return }
            workingDirectory = path
            delegate?.surfaceDidChangeWorkingDirectory(self)
        }

        func setBackgroundColor(_ color: NSColor) {
            guard color != backgroundColor else { return }
            backgroundColor = color
            delegate?.surfaceDidChangeBackground(self)
        }

        func childDidExit() {
            guard !childExited else { return }
            childExited = true
            delegate?.surfaceChildDidExit(self)
        }
    }

    static func colorScheme(for appearance: NSAppearance) -> ghostty_color_scheme_e {
        appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            ? GHOSTTY_COLOR_SCHEME_DARK
            : GHOSTTY_COLOR_SCHEME_LIGHT
    }
}

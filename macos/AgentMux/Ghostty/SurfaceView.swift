// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import AppKit
import GhosttyKit

/// Hears what one surface reports. Every call arrives on the main thread and
/// never from inside a Ghostty callback, so a delegate may close the surface
/// in response.
protocol GhosttySurfaceDelegate: AnyObject {
    func surfaceDidChangeTitle(_ surface: Ghostty.SurfaceView)
    func surfaceDidChangeWorkingDirectory(_ surface: Ghostty.SurfaceView)
    func surfaceDidChangeBackground(_ surface: Ghostty.SurfaceView)
    /// The program the surface ran has exited. The surface stays open, showing
    /// its last output, until the delegate closes it. No exit status comes
    /// with it: Ghostty starts the program under login(1), whose own status
    /// is all it sees, so how an agent ended is the backend's to say.
    func surfaceChildDidExit(_ surface: Ghostty.SurfaceView)
    /// Ghostty wants the surface closed: its program ended and nothing keeps
    /// it open, or a close binding fired. `needsConfirm` is Ghostty's "would
    /// closing need confirmation"; it is not whether the process is alive.
    func surfaceDidRequestClose(_ surface: Ghostty.SurfaceView, needsConfirm: Bool)
    /// The program asked for a desktop notification (OSC 9 / OSC 777).
    func surface(_ surface: Ghostty.SurfaceView, didRequestNotificationTitled title: String, body: String)
    /// The program rang the bell. Whether anything is heard or shown is the
    /// host's: only it knows whether this terminal is the one in view.
    func surfaceDidRingBell(_ surface: Ghostty.SurfaceView)
    /// The pointer moved onto a link or off one (`hoveredLink`).
    func surfaceDidChangeHoveredLink(_ surface: Ghostty.SurfaceView)
    /// The renderer reported itself unhealthy, or healthy again
    /// (`rendererHealthy`). An unhealthy one draws nothing new.
    func surfaceDidChangeRendererHealth(_ surface: Ghostty.SurfaceView)
}

extension GhosttySurfaceDelegate {
    func surfaceDidChangeTitle(_ surface: Ghostty.SurfaceView) {}
    func surfaceDidChangeWorkingDirectory(_ surface: Ghostty.SurfaceView) {}
    func surfaceDidChangeBackground(_ surface: Ghostty.SurfaceView) {}
    func surfaceChildDidExit(_ surface: Ghostty.SurfaceView) {}
    func surfaceDidRequestClose(_ surface: Ghostty.SurfaceView, needsConfirm: Bool) {}
    func surface(_ surface: Ghostty.SurfaceView, didRequestNotificationTitled title: String, body: String) {}
    func surfaceDidRingBell(_ surface: Ghostty.SurfaceView) {}
    func surfaceDidChangeHoveredLink(_ surface: Ghostty.SurfaceView) {}
    func surfaceDidChangeRendererHealth(_ surface: Ghostty.SurfaceView) {}
}

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
        private(set) var visible: Bool = true

        /// The link under the pointer, as Ghostty reports it; nil when none.
        private(set) var hoveredLink: String?

        /// Whether the renderer last said it is drawing.
        private(set) var rendererHealthy = true

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
        private(set) var keyTables: [String] = []

        /// What the operator knows this terminal as, named when a program
        /// in it asks for the clipboard; empty when the host set none.
        var hostName = ""

        /// Text a drop hands to Ghostty's paste, read back by the clipboard
        /// request that paste makes; see `paste(_:)`.
        var pendingPaste: String?

        /// Set when the view was resized while hidden: the surface is told
        /// once it is shown, not on every frame of a window's live resize.
        private(set) var sizePending = false

        /// The last size requested in points, reused when only the scale
        /// changes, because mid-animation the frame may lag the target size.
        private var contentSize: CGSize = .zero

        private var appliedColorScheme: ghostty_color_scheme_e?

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
        /// SurfaceView+History.swift.
        var scrollback: Scrollback = .terminal

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

        /// Whether closing the surface would end a program Ghostty thinks
        /// should be asked about first.
        var needsConfirmQuit: Bool {
            guard let surface else { return false }
            return ghostty_surface_needs_confirm_quit(surface)
        }

        // MARK: Size and scale

        /// Tells the surface its framebuffer size for a view size in points.
        /// Ghostty derives the grid from it, so this is what keeps rows and
        /// columns matching the view.
        func sizeDidChange(_ size: CGSize) {
            contentSize = size
            let scaled = convertToBacking(size)
            setSurfaceSize(Self.pixelSize(scaled))
        }

        /// Backing-store points rounded down to whole pixels, never zero, since
        /// a zero-sized surface has no grid to lay a program out on.
        static func pixelSize(_ backing: CGSize) -> (width: UInt32, height: UInt32) {
            let width = max(1, backing.width.rounded(.down))
            let height = max(1, backing.height.rounded(.down))
            return (UInt32(clamping: Int(width)), UInt32(clamping: Int(height)))
        }

        private func setSurfaceSize(_ size: (width: UInt32, height: UInt32)) {
            guard let surface else { return }
            ghostty_surface_set_size(surface, size.width, size.height)
        }

        override func setFrameSize(_ newSize: NSSize) {
            super.setFrameSize(newSize)
            // A hidden terminal behind the one shown follows every frame of a
            // window's live resize; its grid only matters once it is seen.
            if isHiddenOrHasHiddenAncestor {
                sizePending = true
                return
            }
            sizeDidChange(newSize)
        }

        /// Gives the surface a size deferred while it was hidden.
        private func applyPendingSize() {
            guard sizePending, !isHiddenOrHasHiddenAncestor else { return }
            sizePending = false
            sizeDidChange(frame.size)
        }

        override func viewDidEndLiveResize() {
            super.viewDidEndLiveResize()
            applyPendingSize()
        }

        override func viewDidChangeBackingProperties() {
            super.viewDidChangeBackingProperties()

            // The renderer scales its own contents; without matching the
            // layer's contentsScale the compositor would scale them again when
            // the window moves between Retina and non-Retina displays.
            if let window {
                CATransaction.begin()
                CATransaction.setDisableActions(true)
                layer?.contentsScale = window.backingScaleFactor
                CATransaction.commit()
            }

            guard let surface else { return }
            let backing = convertToBacking(frame)
            if frame.width > 0, frame.height > 0 {
                ghostty_surface_set_content_scale(
                    surface, backing.width / frame.width, backing.height / frame.height)
            }
            // A new scale is a new framebuffer size for the same point size.
            setSurfaceSize(Self.pixelSize(convertToBacking(contentSize)))
        }

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            // Leaving a window is the same as being hidden: nothing to draw
            // for, and no focus to hold.
            updateOcclusion()
            syncFocus()
            guard window != nil else { return }
            updateDisplayID()
            viewDidChangeBackingProperties()
        }

        // MARK: Display and occlusion

        @objc private func windowDidChangeScreen(_ notification: Notification) {
            guard let window, notification.object as? NSWindow === window else { return }
            updateDisplayID()
            // The new screen may carry a different scale factor; upstream
            // re-runs the backing change asynchronously for the same reason.
            DispatchQueue.main.async { [weak self] in
                self?.viewDidChangeBackingProperties()
            }
        }

        /// Points the renderer's display link at the window's current screen.
        private func updateDisplayID() {
            guard let surface, let screen = window?.screen else { return }
            ghostty_surface_set_display_id(surface, Self.displayID(of: screen) ?? 0)
        }

        static func displayID(of screen: NSScreen) -> UInt32? {
            (screen.deviceDescription[NSDeviceDescriptionKey("NSScreenNumber")] as? NSNumber)?.uint32Value
        }

        @objc private func windowDidChangeOcclusionState(_ notification: Notification) {
            guard let window, notification.object as? NSWindow === window else { return }
            updateOcclusion()
        }

        override func viewDidHide() {
            super.viewDidHide()
            updateOcclusion()
        }

        override func viewDidUnhide() {
            super.viewDidUnhide()
            applyPendingSize()
            updateOcclusion()
        }

        /// A surface nobody can see stops rendering; its terminal keeps
        /// running. That is a covered or miniaturized window, and equally a
        /// view hidden behind the one terminal a workspace shows at a time.
        private func updateOcclusion() {
            guard let window else {
                setVisible(false)
                return
            }
            setVisible(window.occlusionState.contains(.visible) && !isHiddenOrHasHiddenAncestor)
        }

        private func setVisible(_ visible: Bool) {
            guard let surface, self.visible != visible else { return }
            self.visible = visible
            ghostty_surface_set_occlusion(surface, visible)
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
        private func syncFocus() {
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

        // MARK: Drag and drop

        /// Files arrive as shell-escaped paths, other URLs shell-escaped
        /// too, and text as text.
        static let dropTypes: [NSPasteboard.PasteboardType] = [.fileURL, .URL, .string]

        /// What a drop types into the terminal. A URL is a word for the
        /// shell, so `&` or `?` in it never start anything.
        static func dropText(from pasteboard: NSPasteboard) -> String? {
            if let urls = pasteboard.readObjects(forClasses: [NSURL.self]) as? [URL], !urls.isEmpty {
                return urls
                    .map { Ghostty.App.shellEscape($0.isFileURL ? $0.path : $0.absoluteString) }
                    .joined(separator: " ")
            }
            return pasteboard.string(forType: .string).flatMap { $0.isEmpty ? nil : $0 }
        }

        /// Dropped text of more than one line is pasted, so Ghostty's paste
        /// protection asks about it first and a bracketed paste stays one.
        static func dropIsPaste(_ text: String) -> Bool {
            text.contains(where: \.isNewline)
        }

        /// Types dropped text into the terminal, or pastes it when it spans
        /// lines. Returns whether Ghostty took it.
        @discardableResult
        func drop(_ text: String) -> Bool {
            guard let surface else { return false }
            if Self.dropIsPaste(text) { return paste(text) }
            let len = text.utf8CString.count
            text.withCString { ptr in
                ghostty_surface_text(surface, ptr, UInt(len - 1))
            }
            return true
        }

        /// Runs Ghostty's own paste on `text` instead of the clipboard's: the
        /// request that paste makes is answered from `pendingPaste`
        /// (`Ghostty.App.readClipboard`), and the clipboard is left alone.
        @discardableResult
        func paste(_ text: String) -> Bool {
            pendingPaste = text
            defer { pendingPaste = nil }
            return performBindingAction("paste_from_clipboard")
        }

        override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
            guard surface != nil else { return [] }
            return Self.dropText(from: sender.draggingPasteboard) == nil ? [] : .copy
        }

        override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
            guard surface != nil, let text = Self.dropText(from: sender.draggingPasteboard), drop(text) else { return false }
            // A drop from Finder leaves Finder in front; keep typing here.
            NSApp.activate()
            window?.makeKeyAndOrderFront(nil)
            window?.makeFirstResponder(self)
            return true
        }

        // MARK: Runtime actions

        /// Answers a surface-targeted action, returning whether it was
        /// performed. Ghostty may call from any thread and is still inside its
        /// own callback, so state changes hop to the main thread and delegate
        /// calls are always deferred: a delegate may then free the surface.
        func perform(_ action: ghostty_action_s) -> Bool {
            switch action.tag {
            case GHOSTTY_ACTION_SET_TITLE:
                guard let ptr = action.action.set_title.title else { return false }
                let title = String(cString: ptr)
                later { $0.setTitle(title) }
                return true

            case GHOSTTY_ACTION_PWD:
                guard let ptr = action.action.pwd.pwd else { return false }
                let pwd = String(cString: ptr)
                later { $0.setWorkingDirectory(pwd) }
                return true

            case GHOSTTY_ACTION_MOUSE_SHAPE:
                let shape = action.action.mouse_shape
                later { $0.setCursorShape(shape) }
                return true

            case GHOSTTY_ACTION_MOUSE_VISIBILITY:
                let visible = action.action.mouse_visibility == GHOSTTY_MOUSE_VISIBLE
                later { $0.setCursorVisibility(visible) }
                return true

            case GHOSTTY_ACTION_COLOR_CHANGE:
                let change = action.action.color_change
                guard change.kind == GHOSTTY_ACTION_COLOR_KIND_BACKGROUND else { return false }
                let color = Ghostty.color(ghostty_config_color_s(r: change.r, g: change.g, b: change.b))
                later { $0.setBackgroundColor(color) }
                return true

            case GHOSTTY_ACTION_RENDERER_HEALTH:
                let healthy = action.action.renderer_health == GHOSTTY_RENDERER_HEALTH_HEALTHY
                if !healthy {
                    Ghostty.logger.error("renderer reported unhealthy")
                }
                later { $0.setRendererHealthy(healthy) }
                return true

            case GHOSTTY_ACTION_DESKTOP_NOTIFICATION:
                // Nobody to show it: Ghostty is told it was not.
                guard delegate != nil else { return false }
                let note = action.action.desktop_notification
                let title = note.title.map { String(cString: $0) } ?? ""
                let body = note.body.map { String(cString: $0) } ?? ""
                later { $0.delegate?.surface($0, didRequestNotificationTitled: title, body: body) }
                return true

            case GHOSTTY_ACTION_RING_BELL:
                later { $0.delegate?.surfaceDidRingBell($0) }
                return true

            case GHOSTTY_ACTION_MOUSE_OVER_LINK:
                let link = action.action.mouse_over_link
                let url = link.url.flatMap { ptr in
                    link.len == 0 ? nil : String(data: Data(bytes: ptr, count: Int(link.len)), encoding: .utf8)
                }
                later { $0.setHoveredLink(url) }
                return true

            case GHOSTTY_ACTION_SECURE_INPUT:
                let change = action.action.secure_input
                now { $0.setSecureInput(change) }
                return true

            // The next key may already be on its way: these take effect
            // before this callback returns whenever it runs on the main thread.
            case GHOSTTY_ACTION_KEY_SEQUENCE:
                let active = action.action.key_sequence.active
                now { $0.setKeySequenceActive(active) }
                return true

            case GHOSTTY_ACTION_KEY_TABLE:
                let table = action.action.key_table
                // The name is Ghostty's only for the length of the callback.
                if table.tag == GHOSTTY_KEY_TABLE_ACTIVATE, !Thread.isMainThread {
                    let value = table.value.activate
                    let name = value.name.map {
                        String(data: Data(bytes: $0, count: Int(value.len)), encoding: .utf8) ?? ""
                    } ?? ""
                    later { $0.keyTables.append(name) }
                    return true
                }
                now { $0.setKeyTable(table) }
                return true

            case GHOSTTY_ACTION_SHOW_CHILD_EXITED:
                // Returning true tells Ghostty the host shows the exit, so it
                // writes nothing of its own into the terminal.
                later { $0.childDidExit() }
                return true

            case GHOSTTY_ACTION_CLOSE_WINDOW:
                // An embedded terminal's "window" is its view.
                closeRequested(needsConfirm: false)
                return true

            default:
                return false
            }
        }

        /// Ghostty asked for this surface to be closed.
        func closeRequested(needsConfirm: Bool) {
            later { $0.delegate?.surfaceDidRequestClose($0, needsConfirm: needsConfirm) }
        }

        /// Runs `work` on the main thread after the current Ghostty callback
        /// has returned, and only if the surface is still open then.
        private func later(_ work: @escaping (SurfaceView) -> Void) {
            DispatchQueue.main.async { [weak self] in
                guard let self, self.surface != nil else { return }
                work(self)
            }
        }

        /// Runs `work` at once when on the main thread, else as `later`. Only
        /// for state changes: it calls no delegate.
        private func now(_ work: @escaping (SurfaceView) -> Void) {
            guard Thread.isMainThread else { return later(work) }
            guard surface != nil else { return }
            work(self)
        }

        /// What a bell does, as the app's config says.
        var bellFeatures: BellFeatures { owner?.config.bellFeatures ?? [] }

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

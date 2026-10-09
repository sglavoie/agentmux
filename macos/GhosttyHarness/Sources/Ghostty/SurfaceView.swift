// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import AppKit
import GhosttyKit

extension Ghostty {
    /// The one terminal surface tt.app draws: an AppKit view hosting a
    /// `ghostty_surface_t`. Rendering, sizing, scale, display, occlusion,
    /// focus, and color scheme are forwarded here; keyboard, IME, and mouse
    /// input are not yet.
    ///
    /// The view does not create its own layer. libghostty's Metal renderer
    /// makes it layer-hosting when the surface is created, assigning its own
    /// Metal-drawn layer before setting `wantsLayer`, exactly as upstream.
    final class SurfaceView: NSView, GhosttySurfaceHost {
        private(set) var surface: ghostty_surface_t?

        /// The terminal's title (OSC 0/2), or empty until the program sets one.
        private(set) var title: String = ""

        /// Called on the main thread whenever `title` changes.
        var onTitleChange: ((String) -> Void)?

        /// Whether the surface was last told it has focus.
        private(set) var focused: Bool = false

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

        private var eventMonitor: Any?

        override var acceptsFirstResponder: Bool { true }

        init(app: ghostty_app_t) {
            // Non-zero so the renderer's layer has bounds before the first
            // layout; the real size arrives through `setFrameSize`.
            super.init(frame: NSRect(x: 0, y: 0, width: 800, height: 600))

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

            var config = ghostty_surface_config_new()
            config.userdata = Unmanaged.passUnretained(self).toOpaque()
            config.platform_tag = GHOSTTY_PLATFORM_MACOS
            config.platform = ghostty_platform_u(macos: ghostty_platform_macos_s(
                nsview: Unmanaged.passUnretained(self).toOpaque()
            ))
            config.scale_factor = Double(NSScreen.main?.backingScaleFactor ?? 2)
            // The command, its environment, and wait-after-command come from
            // the app config (see `Ghostty.ConfigOverrides`); zero font size
            // inherits the configured one.
            config.font_size = 0
            config.context = GHOSTTY_SURFACE_CONTEXT_WINDOW
            // Command key-ups skip the responder chain, and a click that
            // only focuses the window must not reach the terminal; upstream
            // watches both locally rather than taking acceptsFirstMouse.
            eventMonitor = NSEvent.addLocalMonitorForEvents(
                matching: [.keyUp, .leftMouseDown]
            ) { [weak self] event in
                guard let self else { return event }
                return self.localEventHandler(event)
            }

            guard let surface = ghostty_surface_new(app, &config) else {
                Ghostty.logger.critical("ghostty_surface_new failed")
                return
            }
            self.surface = surface
            registerForDraggedTypes(Self.dropTypes)
            sizeDidChange(frame.size)
            syncColorScheme()
            updateTrackingAreas()
        }

        required init?(coder: NSCoder) {
            fatalError("init(coder:) is not supported for this view")
        }

        deinit {
            NotificationCenter.default.removeObserver(self)
            if let eventMonitor {
                NSEvent.removeMonitor(eventMonitor)
            }
            if let surface {
                ghostty_surface_free(surface)
            }
        }

        // MARK: Size and scale

        /// Tells the surface its framebuffer size for a view size in points.
        /// Ghostty derives the grid from it, so this is what keeps rows and
        /// columns — and tt's footer on the last row — matching the window.
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
            sizeDidChange(newSize)
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
            guard window != nil else { return }
            updateDisplayID()
            viewDidChangeBackingProperties()
            updateOcclusion()
            syncFocus()
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

        /// A hidden or covered window stops rendering; the terminal keeps running.
        private func updateOcclusion() {
            guard let surface, let window else { return }
            ghostty_surface_set_occlusion(surface, window.occlusionState.contains(.visible))
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

        // MARK: Drag and drop

        /// Files arrive as shell-escaped paths, which tt's `&link` and its
        /// Attachments panel both read as one path; URLs and text as text.
        static let dropTypes: [NSPasteboard.PasteboardType] = [.fileURL, .URL, .string]

        /// What a drop types into tt: the same text a paste of that pasteboard would.
        static func dropText(from pasteboard: NSPasteboard) -> String? {
            Ghostty.App.stringContents(of: pasteboard).flatMap { $0.isEmpty ? nil : $0 }
        }

        override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
            Self.dropText(from: sender.draggingPasteboard) == nil ? [] : .copy
        }

        override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
            guard let surface, let text = Self.dropText(from: sender.draggingPasteboard) else { return false }
            let len = text.utf8CString.count
            text.withCString { ptr in
                ghostty_surface_text(surface, ptr, UInt(len - 1))
            }
            // A drop from Finder leaves Finder in front; keep typing in tt.
            NSApp.activate(ignoringOtherApps: true)
            window?.makeKeyAndOrderFront(nil)
            window?.makeFirstResponder(self)
            return true
        }

        // MARK: Title

        func setTitle(_ title: String) {
            guard title != self.title else { return }
            self.title = title
            onTitleChange?(title)
        }
    }

    static func colorScheme(for appearance: NSAppearance) -> ghostty_color_scheme_e {
        appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            ? GHOSTTY_COLOR_SCHEME_DARK
            : GHOSTTY_COLOR_SCHEME_LIGHT
    }
}

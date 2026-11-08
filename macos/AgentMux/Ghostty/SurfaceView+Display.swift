// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import AppKit
import GhosttyKit

extension Ghostty.SurfaceView {
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

    /// Gives the surface a size, and any scale, deferred while it was
    /// hidden.
    private func applyPendingSize() {
        guard sizePending, !isHiddenOrHasHiddenAncestor else { return }
        sizePending = false
        setContentScale()
        sizeDidChange(frame.size)
    }

    /// Tells the surface the scale its frame has in the backing store.
    private func setContentScale() {
        guard let surface else { return }
        let backing = convertToBacking(frame)
        if frame.width > 0, frame.height > 0 {
            ghostty_surface_set_content_scale(
                surface, backing.width / frame.width, backing.height / frame.height)
        }
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

        // A hidden terminal's last size may be stale: the new scale goes
        // to the surface with its current size once it is shown, so
        // tmux never hears of a grid nobody sees.
        if isHiddenOrHasHiddenAncestor {
            sizePending = true
            return
        }
        setContentScale()
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

    @objc func windowDidChangeScreen(_ notification: Notification) {
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

    @objc func windowDidChangeOcclusionState(_ notification: Notification) {
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
}

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
    /// A search through the terminal's own buffer began, was asked for
    /// again, ended, or was counted. Only a terminal holding its own
    /// history (`Scrollback.terminal`) reports one.
    func surface(_ surface: Ghostty.SurfaceView, didUpdateSearch update: Ghostty.SearchUpdate)
    func surfaceRequestedServerFind(_ surface: Ghostty.SurfaceView)
    /// The viewport moved in the terminal's own buffer, or the buffer grew
    /// (`scrollbar`). Never reported when the config says `scrollbar = never`
    /// or the terminal's history is the server's.
    func surfaceDidChangeScrollbar(_ surface: Ghostty.SurfaceView)
    /// The program reported progress, or its report was removed or expired
    /// (`progress`).
    func surfaceDidChangeProgress(_ surface: Ghostty.SurfaceView)
    /// The terminal put text on the clipboard: a Copy, or, in a session
    /// view, tmux copying a selection made with the mouse (OSC 52), which
    /// otherwise happens with nothing to show for it.
    func surfaceDidWriteClipboard(_ surface: Ghostty.SurfaceView)
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
    func surface(_ surface: Ghostty.SurfaceView, didUpdateSearch update: Ghostty.SearchUpdate) {}
    func surfaceRequestedServerFind(_ surface: Ghostty.SurfaceView) {}
    func surfaceDidChangeScrollbar(_ surface: Ghostty.SurfaceView) {}
    func surfaceDidChangeProgress(_ surface: Ghostty.SurfaceView) {}
    func surfaceDidWriteClipboard(_ surface: Ghostty.SurfaceView) {}
}

import AppKit
import SwiftUI

/// The window behind Since Last Looked, Conversation History, Checkout
/// Changes, and ASL Runs. One place sets what they share: the style, the
/// minimum size, ⌘W through `WorkspaceReviewHost`, the saved placement, and
/// how showing, reactivation, occlusion, and closing reach the review.
///
/// A subclass supplies its content and overrides the hooks it needs; it never
/// builds or orders the window itself.
@MainActor
class ReviewWindowController: NSObject, NSWindowDelegate {
    /// Every review window's minimum: the widest review (Runs' list beside
    /// its controls) at its smallest usable size, so none of them can be
    /// made too small to read and all of them resize alike.
    static let minimumSize = NSSize(width: 780, height: 500)

    let title: String
    private let autosaveName: String
    private let initialSize: NSSize
    private(set) var window: NSWindow?
    var onVisibilityChanged: (Bool) -> Void = { _ in }
    private var observers: [NSObjectProtocol] = []
    private var readingLive: Bool?

    init(title: String, autosaveName: String, size: NSSize) {
        self.title = title
        self.autosaveName = autosaveName
        self.initialSize = size
        super.init()
        for name in [NSApplication.didBecomeActiveNotification, NSApplication.didResignActiveNotification] {
            observers.append(NotificationCenter.default.addObserver(forName: name, object: nil, queue: .main) { [weak self] note in
                MainActor.assumeIsolated {
                    guard let self else { return }
                    if note.name == NSApplication.didBecomeActiveNotification, self.window?.isVisible == true {
                        self.applicationDidBecomeActive()
                    }
                    self.updateReadingLive()
                }
            })
        }
    }

    deinit { observers.forEach(NotificationCenter.default.removeObserver) }

    // MARK: Hooks

    /// The review's content, hosted in a `WorkspaceReviewHost` that closes the
    /// window on ⌘W. Called once, when the window is first built.
    func makeContentView() -> NSView { preconditionFailure("\(type(of: self)) must supply its content") }

    /// Each time the window is shown, before it comes forward: read again
    /// whatever may have gone stale while it was hidden.
    func willPresent() {}

    /// The app became active while the window is open: data that changes
    /// outside the app (files, journals) may be stale.
    func applicationDidBecomeActive() {}

    /// Whether the window can currently be seen by an active app: a review
    /// that polls reads only while this is true.
    func readingLiveChanged(_ live: Bool) {}

    /// False while closing would abandon work in progress.
    var canClose: Bool { true }

    /// The window closed: cancel reads and drop what the review holds.
    func didClose() {}

    // MARK: Showing

    /// Builds the window the first time, then shows it and brings it forward.
    func present() {
        if window == nil { window = makeWindow() }
        onVisibilityChanged(true)
        willPresent()
        reveal(notify: false)
        updateReadingLive()
    }

    /// Brings the existing window forward without reading anything again,
    /// for feedback raised while it was behind another window.
    func reveal() { reveal(notify: true) }

    private func reveal(notify: Bool) {
        guard let window else { return }
        if notify { onVisibilityChanged(true) }
        window.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }

    private func makeWindow() -> NSWindow {
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: initialSize),
                              styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        window.title = title
        window.minSize = Self.minimumSize
        window.isReleasedWhenClosed = false
        window.delegate = self
        window.contentView = makeContentView()
        ReviewWindowPlacement.restore(window, name: autosaveName)
        return window
    }

    /// A hosting view for a review's SwiftUI content, closing on ⌘W.
    static func host<Content: View>(_ view: Content) -> WorkspaceReviewHost<Content> {
        let host = WorkspaceReviewHost(rootView: view)
        host.closesWindow = true
        return host
    }

    private func updateReadingLive() {
        let live = NSApp.isActive && window?.isVisible == true && window?.occlusionState.contains(.visible) == true
        guard live != readingLive else { return }
        readingLive = live
        readingLiveChanged(live)
    }

    // MARK: NSWindowDelegate

    func windowDidChangeOcclusionState(_ notification: Notification) { updateReadingLive() }
    func windowShouldClose(_ sender: NSWindow) -> Bool { canClose }
    func windowWillClose(_ notification: Notification) {
        didClose()
        readingLive = nil
        onVisibilityChanged(false)
    }
}

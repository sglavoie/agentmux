import AppKit

/// The main menu the app has while it is a regular app with the workspace
/// open. The embedded terminals offer every key equivalent to it before they
/// encode the key for their program, so what is listed here is what the
/// workspace keeps for itself; everything else reaches the agent.
///
/// Every shortcut here holds ⌘, except Shift+Page Up and Shift+Page Down and
/// the function keys F1 to F11, which do here what they do in Kitty
/// (`functionKeys`): any other key with Control, Option, or no modifier at all
/// is the focused program's. Where the keyboard goes then depends on what has it: a
/// session's terminal gives it to the agent, the Dashboard's to the TUI, and
/// the session list keeps the arrow keys and Return for itself.
///
/// The digits are the sessions' places in the sidebar, from the top down; the
/// Dashboard and the Overview have ⌘D and ⌘O. Going to a session with ⌘ alone stays
/// in the workspace; with Shift, a session that lives in Kitty comes forward
/// there. ⌘] and ⌘[ stop at every session and every terminal under one; with
/// Shift they go from session to session. ⌘L goes back to where the pane was
/// before, and again returns. ⌘P lists every session and view to go to by
/// name.
enum WorkspaceMenu {
    /// What each function key does, in the order the menu lists them: the
    /// keys Kitty binds for its tabs, acting on the workspace's sessions.
    /// F1 shows the same list (`kittykeys.Workspace`), and
    /// `TestWorkspaceFunctionKeysMatchTheApp` holds the two together. F3 has
    /// no target: it goes to the terminal that has the keyboard.
    @MainActor
    static let functionKeys: [(number: Int, title: String, action: Selector)] = [
        (1, "Keybinding Reference", #selector(AppDelegate.showKeybindings(_:))),
        (2, "Overview", #selector(AppDelegate.openOverviewFromMenu(_:))),
        (3, "Toggle Error and Warning Markers", #selector(Ghostty.SurfaceView.toggleMarkers(_:))),
        (4, "Next Needing Attention", #selector(AppDelegate.nextAttention(_:))),
        (5, "Set Session Icon…", #selector(AppDelegate.pickSessionIcon(_:))),
        (6, "Set Session Color…", #selector(AppDelegate.pickSessionColor(_:))),
        (7, "Previous Session", #selector(AppDelegate.focusPreviousSession(_:))),
        (8, "Clear Session State", #selector(AppDelegate.clearSessionState(_:))),
        (9, "Next Session", #selector(AppDelegate.focusNextSession(_:))),
        (10, "Toggle Session Mute", #selector(AppDelegate.toggleSessionMute(_:))),
        (11, "Mark Session Active", #selector(AppDelegate.markSessionActive(_:))),
    ]

    /// The keys of the first sessions in the sidebar, in its order: ⌘1 is the
    /// first one listed and ⌘0 the tenth.
    static let sessionKeys = ["1", "2", "3", "4", "5", "6", "7", "8", "9", "0"]
    /// What Shift types on each of `sessionKeys`.
    static let shiftedSessionKeys = ["!", "@", "#", "$", "%", "^", "&", "*", "(", ")"]

    @MainActor
    static func make(target: AppDelegate) -> NSMenu {
        let main = NSMenu()

        let app = submenu("Agent Mux", in: main)
        app.addItem(item("Settings…", #selector(AppDelegate.showPreferences(_:)), ",", target: target))
        app.addItem(.separator())
        app.addItem(item("Hide Agent Mux", #selector(NSApplication.hide(_:)), "h"))
        app.addItem(.separator())
        app.addItem(item("Quit Agent Mux", #selector(NSApplication.terminate(_:)), "q"))

        let edit = submenu("Edit", in: main)
        edit.addItem(item("Copy", #selector(NSText.copy(_:)), "c"))
        edit.addItem(item("Paste", #selector(NSText.paste(_:)), "v"))
        edit.addItem(item("Select All", #selector(NSResponder.selectAll(_:)), "a"))
        edit.addItem(.separator())
        edit.addItem(item("Find…", #selector(Ghostty.SurfaceView.find(_:)), "f"))
        edit.addItem(item("Find Next", #selector(Ghostty.SurfaceView.findNext(_:)), "g"))
        edit.addItem(item("Find Previous", #selector(Ghostty.SurfaceView.findPrevious(_:)), "G"))

        let view = submenu("View", in: main)
        view.addItem(item("Bigger", #selector(Ghostty.SurfaceView.zoomIn(_:)), "+"))
        // Accept the unshifted + key too, as in TT and other Mac terminals.
        let bigger = item("Bigger", #selector(Ghostty.SurfaceView.zoomIn(_:)), "=")
        bigger.isHidden = true
        bigger.allowsKeyEquivalentWhenHidden = true
        view.addItem(bigger)
        view.addItem(item("Smaller", #selector(Ghostty.SurfaceView.zoomOut(_:)), "-"))
        // ⌘0 and ⌘⇧0 are the tenth session's.
        view.addItem(item("Actual Size", #selector(Ghostty.SurfaceView.resetZoom(_:)), "_"))
        view.addItem(.separator())
        view.addItem(item("Page Up", #selector(NSResponder.scrollPageUp(_:)), key(NSPageUpFunctionKey), mask: .shift))
        view.addItem(item("Page Down", #selector(NSResponder.scrollPageDown(_:)), key(NSPageDownFunctionKey), mask: .shift))
        // ⌘Page Up and ⌘Page Down page too, as in Ghostty and Terminal: in a
        // session view Ghostty's own binding would page an empty buffer.
        for (title, action, pageKey) in [
            ("Page Up", #selector(NSResponder.scrollPageUp(_:)), NSPageUpFunctionKey),
            ("Page Down", #selector(NSResponder.scrollPageDown(_:)), NSPageDownFunctionKey),
        ] {
            let alias = item(title, action, key(pageKey))
            alias.isHidden = true
            alias.allowsKeyEquivalentWhenHidden = true
            view.addItem(alias)
        }
        view.addItem(item("Scroll to Top", #selector(NSResponder.scrollToBeginningOfDocument(_:)), key(NSHomeFunctionKey)))
        view.addItem(item("Scroll to Bottom", #selector(NSResponder.scrollToEndOfDocument(_:)), key(NSEndFunctionKey)))
        view.addItem(.separator())
        view.addItem(item("Toggle Sidebar", #selector(AppDelegate.toggleWorkspaceSidebar(_:)), "b", target: target))
        view.addItem(item("Toggle Inspector", #selector(AppDelegate.toggleWorkspaceInspector(_:)), "B", target: target))
        view.addItem(.separator())
        // The digits are all the sessions'.
        view.addItem(item("Dashboard", #selector(AppDelegate.openDashboardFromMenu(_:)), "d", target: target))
        view.addItem(item("Overview", #selector(AppDelegate.openOverviewFromMenu(_:)), "o", target: target))
        view.addItem(item("New Shell", #selector(AppDelegate.newShellFromMenu(_:)), "t", target: target))
        view.addItem(.separator())
        // ⌘W closes what the pane is on, as it closes a tab elsewhere; the
        // window itself is ⌘⇧W. Its title follows what that is
        // (`AppDelegate.validateMenuItem`): Close View, or End Session….
        view.addItem(item(WorkspaceWindowController.closeTitle, #selector(AppDelegate.closeWorkspaceView(_:)), "w", target: target))

        let session = submenu("Session", in: main)
        session.addItem(item("Go to…", #selector(AppDelegate.showSwitcher(_:)), "p", target: target))
        session.addItem(item("Next Needing Attention", #selector(AppDelegate.nextAttention(_:)), "A", target: target))
        session.addItem(item("New Terminal Below", #selector(AppDelegate.newSessionTerminal(_:)), "\r", target: target))
        session.addItem(item(WorkspaceWindowController.nameSessionTitle, #selector(AppDelegate.nameSession(_:)), "r", target: target))
        session.addItem(item("Pin or Unpin Session", #selector(AppDelegate.togglePinSession(_:)), "", target: target))
        session.addItem(.separator())
        session.addItem(item("Select Next Session", #selector(AppDelegate.selectNextSession(_:)), "]", target: target))
        session.addItem(item("Select Previous Session", #selector(AppDelegate.selectPreviousSession(_:)), "[", target: target))
        session.addItem(item("Back to Last Shown", #selector(AppDelegate.toggleRecentPlace(_:)), "l", target: target))
        session.addItem(.separator())
        let keys = submenu("Function Keys", in: session)
        for entry in functionKeys {
            let own = entry.action != #selector(Ghostty.SurfaceView.toggleMarkers(_:))
            keys.addItem(item(entry.title, entry.action, key(NSF1FunctionKey + entry.number - 1), mask: [],
                              target: own ? target : nil))
        }
        session.addItem(.separator())
        for (index, key) in sessionKeys.enumerated() {
            let place = item("Session \(index + 1)", #selector(AppDelegate.goToSession(_:)), key, target: target)
            place.tag = index
            session.addItem(place)
        }
        session.addItem(.separator())
        // The same places with Shift: a Kitty session is raised in Kitty, and
        // the steps pass over the terminals under a session.
        let kitty = submenu("Show in Kitty", in: session)
        kitty.addItem(item("Next Session in Kitty", #selector(AppDelegate.showNextSessionInKitty(_:)), "}", target: target))
        kitty.addItem(item("Previous Session in Kitty", #selector(AppDelegate.showPreviousSessionInKitty(_:)), "{", target: target))
        kitty.addItem(.separator())
        for (index, key) in shiftedSessionKeys.enumerated() {
            let place = item("Session \(index + 1) in Kitty", #selector(AppDelegate.showSessionInKitty(_:)), key, target: target)
            place.tag = index
            kitty.addItem(place)
        }

        let window = submenu("Window", in: main)
        window.addItem(item("Minimize", #selector(NSWindow.performMiniaturize(_:)), "m"))
        window.addItem(item("Close Window", #selector(NSWindow.performClose(_:)), "W"))
        window.addItem(.separator())
        let workspace = NSMenuItem(title: "Workspace", action: #selector(AppDelegate.openWorkspace(_:)), keyEquivalent: "")
        workspace.target = target
        window.addItem(workspace)
        window.addItem(item("Session List", #selector(AppDelegate.focusSessionList(_:)), "L", target: target))
        NSApp.windowsMenu = window
        return main
    }

    private static func submenu(_ title: String, in main: NSMenu) -> NSMenu {
        let menu = NSMenu(title: title)
        let holder = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        holder.submenu = menu
        main.addItem(holder)
        return menu
    }

    /// An item with a key equivalent, ⌘ unless `mask` says otherwise; an
    /// uppercase or shifted key carries its own Shift. With no target it goes
    /// to the first responder.
    private static func item(
        _ title: String, _ action: Selector, _ key: String,
        mask: NSEvent.ModifierFlags = .command, target: AnyObject? = nil
    ) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: action, keyEquivalent: key)
        item.keyEquivalentModifierMask = mask
        item.target = target
        return item
    }

    /// The key equivalent of a function key such as Page Up.
    private static func key(_ functionKey: Int) -> String {
        UnicodeScalar(functionKey).map { String(Character($0)) } ?? ""
    }
}

import AppKit

/// The main menu the app has while it is a regular app with the workspace
/// open. The embedded terminals offer every key equivalent to it before they
/// encode the key for their program, so what is listed here is what the
/// workspace keeps for itself; everything else reaches the agent.
///
/// Every shortcut here holds ⌘, except Shift+Page Up and Shift+Page Down:
/// keys with Control, Option, or no modifier at all are the focused
/// program's. Where the keyboard goes then depends on what has it: a
/// session's terminal gives it to the agent, the Dashboard's to the TUI, and
/// the session list keeps the arrow keys and Return for itself.
///
/// The digits are places in the sidebar: ⌘1 the Dashboard, ⌘2 the Overview,
/// then the sessions from the top down. Going to a session with ⌘ alone stays
/// in the workspace; with Shift, a session that lives in Kitty comes forward
/// there. ⌘] and ⌘[ stop at every session and every terminal under one; with
/// Shift they go from session to session.
enum WorkspaceMenu {
    /// The keys of the first sessions in the sidebar, in its order: ⌘3 is the
    /// first one listed and ⌘0 the eighth.
    static let sessionKeys = ["3", "4", "5", "6", "7", "8", "9", "0"]
    /// What Shift types on each of `sessionKeys`.
    static let shiftedSessionKeys = ["#", "$", "%", "^", "&", "*", "(", ")"]

    @MainActor
    static func make(target: AppDelegate) -> NSMenu {
        let main = NSMenu()

        let app = submenu("Agent Mux", in: main)
        app.addItem(item("Preferences…", #selector(AppDelegate.showPreferences(_:)), ",", target: target))
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
        // ⌘0 and ⌘⇧0 are the eighth session's.
        view.addItem(item("Actual Size", #selector(Ghostty.SurfaceView.resetZoom(_:)), "_"))
        view.addItem(.separator())
        view.addItem(item("Page Up", #selector(NSResponder.scrollPageUp(_:)), key(NSPageUpFunctionKey), mask: .shift))
        view.addItem(item("Page Down", #selector(NSResponder.scrollPageDown(_:)), key(NSPageDownFunctionKey), mask: .shift))
        view.addItem(item("Scroll to Top", #selector(NSResponder.scrollToBeginningOfDocument(_:)), key(NSHomeFunctionKey)))
        view.addItem(item("Scroll to Bottom", #selector(NSResponder.scrollToEndOfDocument(_:)), key(NSEndFunctionKey)))
        view.addItem(.separator())
        view.addItem(item("Dashboard", #selector(AppDelegate.openDashboardFromMenu(_:)), "1", target: target))
        view.addItem(item("Overview", #selector(AppDelegate.openOverviewFromMenu(_:)), "2", target: target))
        // ⌘D and ⌘O still go where they did before the digits.
        for (title, action, key) in [
            ("Dashboard", #selector(AppDelegate.openDashboardFromMenu(_:)), "d"),
            ("Overview", #selector(AppDelegate.openOverviewFromMenu(_:)), "o"),
        ] {
            let earlier = item(title, action, key, target: target)
            earlier.isHidden = true
            earlier.allowsKeyEquivalentWhenHidden = true
            view.addItem(earlier)
        }
        view.addItem(item("New Shell", #selector(AppDelegate.newShellFromMenu(_:)), "t", target: target))
        view.addItem(.separator())
        view.addItem(item("Close View", #selector(AppDelegate.closeWorkspaceView(_:)), "W", target: target))

        let session = submenu("Session", in: main)
        session.addItem(item("Next Needing Attention", #selector(AppDelegate.nextAttention(_:)), "A", target: target))
        session.addItem(item("New Terminal Below", #selector(AppDelegate.newSessionTerminal(_:)), "\r", target: target))
        session.addItem(.separator())
        session.addItem(item("Select Next Session", #selector(AppDelegate.selectNextSession(_:)), "]", target: target))
        session.addItem(item("Select Previous Session", #selector(AppDelegate.selectPreviousSession(_:)), "[", target: target))
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
        window.addItem(item("Close", #selector(NSWindow.performClose(_:)), "w"))
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

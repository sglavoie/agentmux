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
enum WorkspaceMenu {
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
        view.addItem(item("Actual Size", #selector(Ghostty.SurfaceView.resetZoom(_:)), "0"))
        view.addItem(.separator())
        view.addItem(item("Page Up", #selector(NSResponder.scrollPageUp(_:)), key(NSPageUpFunctionKey), mask: .shift))
        view.addItem(item("Page Down", #selector(NSResponder.scrollPageDown(_:)), key(NSPageDownFunctionKey), mask: .shift))
        view.addItem(item("Scroll to Top", #selector(NSResponder.scrollToBeginningOfDocument(_:)), key(NSHomeFunctionKey)))
        view.addItem(item("Scroll to Bottom", #selector(NSResponder.scrollToEndOfDocument(_:)), key(NSEndFunctionKey)))
        view.addItem(.separator())
        view.addItem(item("Dashboard", #selector(AppDelegate.openDashboardFromMenu(_:)), "d", target: target))
        view.addItem(item("Overview", #selector(AppDelegate.openOverviewFromMenu(_:)), "o", target: target))
        view.addItem(item("New Shell", #selector(AppDelegate.newShellFromMenu(_:)), "t", target: target))
        view.addItem(.separator())
        view.addItem(item("Close View", #selector(AppDelegate.closeWorkspaceView(_:)), "W", target: target))

        let session = submenu("Session", in: main)
        session.addItem(item("Next Needing Attention", #selector(AppDelegate.nextAttention(_:)), "A", target: target))
        session.addItem(.separator())
        session.addItem(item("Select Next Session", #selector(AppDelegate.selectNextSession(_:)), "}", target: target))
        session.addItem(item("Select Previous Session", #selector(AppDelegate.selectPreviousSession(_:)), "{", target: target))

        let window = submenu("Window", in: main)
        window.addItem(item("Minimize", #selector(NSWindow.performMiniaturize(_:)), "m"))
        window.addItem(item("Close", #selector(NSWindow.performClose(_:)), "w"))
        window.addItem(.separator())
        window.addItem(item("Workspace", #selector(AppDelegate.openWorkspace(_:)), "1", target: target))
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

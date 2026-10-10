import AppKit

/// The main menu the app has while it is a regular app with the workspace
/// open. The embedded terminals offer every key equivalent to it before they
/// encode the key for their program, so what is listed here is what the
/// workspace keeps for itself; everything else reaches the agent.
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

        let view = submenu("View", in: main)
        view.addItem(item("Bigger", #selector(Ghostty.SurfaceView.zoomIn(_:)), "+"))
        view.addItem(item("Smaller", #selector(Ghostty.SurfaceView.zoomOut(_:)), "-"))
        view.addItem(item("Actual Size", #selector(Ghostty.SurfaceView.resetZoom(_:)), "0"))
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

    /// An item with a ⌘ key equivalent; an uppercase or shifted key carries
    /// its own Shift. With no target it goes to the first responder.
    private static func item(_ title: String, _ action: Selector, _ key: String, target: AnyObject? = nil) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: action, keyEquivalent: key)
        item.target = target
        return item
    }
}

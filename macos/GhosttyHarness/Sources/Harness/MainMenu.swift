import AppKit

/// The harness has no menu of its own beyond Quit, so a Ghostty binding the
/// menu would otherwise claim always falls through to the surface.
enum MainMenu {
    static func performShortcut(for event: NSEvent, in menu: NSMenu) -> Bool {
        false
    }

    static func make() -> NSMenu {
        let main = NSMenu()
        let appItem = NSMenuItem()
        main.addItem(appItem)
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "Quit Harness", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "")
        appItem.submenu = appMenu
        return main
    }
}

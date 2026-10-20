import AppKit


extension AppDelegate: NSMenuItemValidation {
    /// ⌘W's title follows what it would close; the Session menu's places
    /// name the sessions they go to and are greyed out past the last one, and
    /// its items about the shown session are greyed out with none shown.
    /// Every other item the app targets stays enabled.
    /// The items that act on what the workspace shows, and only make sense
    /// while it has the keyboard: with Settings or an answer panel in front,
    /// they would act on a window behind it, or one that is not even open.
    /// What opens the workspace (the Dashboard, the Overview, a shell, the
    /// next session needing attention, the session list) stays enabled, and
    /// ⌘W closes whichever window has the keyboard.
    static let workspaceOnlyActions: Set<Selector> = [
        #selector(showSwitcher(_:)), #selector(showCommandPalette(_:)),
        #selector(splitWorkspaceShell(_:)), #selector(splitWorkspaceChanges(_:)),
        #selector(focusOtherWorkspacePane(_:)), #selector(swapWorkspacePanes(_:)), #selector(collapseWorkspacePane(_:)),
        #selector(goToSession(_:)), #selector(showSessionInKitty(_:)),
        #selector(selectNextSession(_:)), #selector(selectPreviousSession(_:)),
        #selector(selectNextProjectTab(_:)), #selector(selectPreviousProjectTab(_:)),
        #selector(showNextSessionInKitty(_:)), #selector(showPreviousSessionInKitty(_:)),
        #selector(toggleRecentPlace(_:)),
        #selector(toggleWorkspaceSidebar(_:)), #selector(toggleWorkspaceInspector(_:)),
        #selector(biggerWorkspaceSidebars(_:)), #selector(smallerWorkspaceSidebars(_:)), #selector(resetWorkspaceSidebars(_:)),
        #selector(newSessionTerminal(_:)), #selector(nameSession(_:)), #selector(togglePinSession(_:)),
        #selector(endWorkspaceSession(_:)), #selector(searchConversation(_:)),
        #selector(focusNextSession(_:)), #selector(focusPreviousSession(_:)),
        #selector(pickSessionIcon(_:)), #selector(pickSessionColor(_:)),
        #selector(clearSessionState(_:)), #selector(toggleSessionMute(_:)), #selector(markSessionActive(_:)),
    ]

    /// Whether the workspace is open and is the window with the keyboard.
    var workspaceIsKey: Bool {
        guard let workspace, workspace.isOpen, let key = NSApp.keyWindow else { return false }
        return key === workspace.window
    }

    func validateMenuItem(_ item: NSMenuItem) -> Bool {
        let enabled = validateWorkspaceItem(item)
        guard let action = item.action, Self.workspaceOnlyActions.contains(action) else { return enabled }
        return enabled && workspaceIsKey
    }

    func validateWorkspaceItem(_ item: NSMenuItem) -> Bool {
        switch item.action {
        case #selector(nameSession(_:)):
            item.title = workspace?.nameShownTitle ?? WorkspaceWindowController.nameSessionTitle
            return workspace?.canNameShown == true
        case #selector(selectNextProjectTab(_:)), #selector(selectPreviousProjectTab(_:)):
            return workspace?.canSelectProjectTab == true
        case #selector(searchConversation(_:)):
            item.toolTip = "Search the selected agent's recorded conversation, including messages outside the visible terminal screen."
            return workspace?.shownSessionRow?.conversation != nil
        case #selector(endWorkspaceSession(_:)):
            return workspace?.shownSessionRow?.embeddedRuntimeSessionID != nil
        case #selector(closeWorkspaceView(_:)):
            item.title = closeWorkspaceViewTitle
        case #selector(goToSession(_:)), #selector(showSessionInKitty(_:)):
            let name = workspace?.sessionName(at: item.tag)
            let inKitty = item.action == #selector(showSessionInKitty(_:)) && workspace?.sessionIsInKitty(at: item.tag) == true
            item.title = WorkspaceWindowController.sessionItemTitle(at: item.tag, name: name, inKitty: inKitty)
            return name != nil
        case #selector(togglePinSession(_:)):
            item.title = workspace?.pinShownTitle ?? WorkspaceWindowController.pinShownSessionTitle
            return workspace?.shownSessionRow != nil
        // F5, F6, F8, F10, and F11 act on the session the pane is on; with
        // none shown they are greyed out rather than beeping.
        case #selector(pickSessionIcon(_:)), #selector(pickSessionColor(_:)),
             #selector(clearSessionState(_:)), #selector(markSessionActive(_:)),
             #selector(toggleSessionMute(_:)):
            return workspace?.shownSessionRow != nil
        case #selector(newSessionTerminal(_:)):
            return workspace?.canOpenTerminalBesideSession == true
        case #selector(toggleWorkspaceSidebar(_:)):
            item.title = Self.sidebarItemTitle(shown: workspace.map { !$0.isSidebarCollapsed } ?? false)
        case #selector(toggleWorkspaceInspector(_:)):
            item.title = Self.inspectorItemTitle(shown: workspace.map { !$0.isInspectorCollapsed } ?? false)
        default:
            break
        }
        return true
    }

    /// What the sidebar's item does now, as a Mac app's View menu says it.
    nonisolated static func sidebarItemTitle(shown: Bool) -> String {
        shown ? "Hide Sidebar" : "Show Sidebar"
    }

    /// What the inspector's item does now.
    nonisolated static func inspectorItemTitle(shown: Bool) -> String {
        shown ? "Hide Inspector" : "Show Inspector"
    }
}

extension AppDelegate: NSMenuDelegate {
    /// The Session menu, as it opens: its Show in Kitty submenu follows the
    /// sessions the digits go to.
    func menuNeedsUpdate(_ menu: NSMenu) {
        WorkspaceMenu.updateKittyItems(in: menu, workspace: workspace)
    }
}

import AppKit

extension WorkspaceWindowController {
    func setSidebarSort(_ sorting: WorkspaceSidebarSort) {
        guard sidebarSort != sorting else { return }
        sidebarSort = sorting
        state.arrange(order, grouping: sidebarGrouping, sorting: sorting)
        guard sidebar.dataSource != nil else { return }
        reloadSidebar(allowReorder: true)
    }

    func setSidebarWaitingOnly(_ waitingOnly: Bool) {
        guard sidebarWaitingOnly != waitingOnly else { return }
        sidebarWaitingOnly = waitingOnly
        groupFilter.showWaitingOnly(waitingOnly)
        guard sidebar.dataSource != nil else { return }
        reloadSidebar(allowReorder: true)
    }

    func setSidebarGrouping(_ grouping: WorkspaceSidebarGrouping) {
        guard sidebarGrouping != grouping else { return }
        sidebarGrouping = grouping
        state.arrange(order, grouping: grouping, sorting: sidebarSort)
        guard sidebar.dataSource != nil else { return }
        reloadSidebar(allowReorder: true)
    }

    func setShowOverviewInSidebar(_ show: Bool) {
        guard showOverviewInSidebar != show else { return }
        showOverviewInSidebar = show
        guard sidebar.dataSource != nil else { return }
        reloadSidebar(force: true, allowReorder: true)
    }

    func setCompactSidebar(_ compact: Bool) {
        guard compactSidebar != compact else { return }
        compactSidebar = compact
        guard sidebar.dataSource != nil else { return }
        applying = true
        sidebar.reloadData()
        applying = false
        syncSidebarSelection()
    }

    func zoomSidebars(_ change: WorkspaceZoomWindow.Zoom) {
        let step = AppPreferences.sidebarZoomStep
        let percent = switch change {
        case .bigger: sidebarZoomPercent + step
        case .smaller: sidebarZoomPercent - step
        case .reset: 100
        }
        guard setSidebarZoom(percent: percent) else { return }
        onSidebarZoomChanged?(sidebarZoomPercent)
    }

    /// Applies a zoom, clamped to `AppPreferences.sidebarZoomRange`; false
    /// when it was already the zoom. It never makes the window: one made
    /// now would open before the saved widths are given to it.
    @discardableResult
    func setSidebarZoom(percent: Int) -> Bool {
        let percent = AppPreferences.clamp(percent, AppPreferences.sidebarZoomRange)
        guard percent != sidebarZoomPercent else { return false }
        sidebarZoomPercent = percent
        guard let sidebarZoomView else { return true }
        sidebarZoomView.zoom = sidebarZoom
        welcomeView.zoom = sidebarZoom
        inspectorZoomView?.zoom = sidebarZoom
        window.contentView?.layoutSubtreeIfNeeded()
        return true
    }
}

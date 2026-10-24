import AppKit
import os

// The inspector beside the pane.
// Part of WorkspaceWindowController (WorkspaceWindow.swift).
extension WorkspaceWindowController {
    // MARK: Inspector

    /// Whether the inspector is hidden, as it is until it is asked for.
    var isInspectorCollapsed: Bool {
        _ = window
        return inspectorItem?.isCollapsed ?? true
    }

    @objc func toggleInspector(_ sender: Any? = nil) {
        setInspectorCollapsed(!isInspectorCollapsed, animated: true)
    }

    func setInspectorCollapsed(_ collapsed: Bool, animated: Bool = false) {
        guard collapsed != isInspectorCollapsed, let inspectorItem else { return }
        if animated, !AppDelegate.isRunningTests {
            inspectorItem.animator().isCollapsed = collapsed
        } else {
            inspectorItem.isCollapsed = collapsed
        }
    }

    /// Turns the inspector to a tab. It stays hidden if it is.
    func setInspectorTab(_ tab: WorkspaceInspectorTab) {
        guard tab != inspectorTab else { return }
        inspectorTab = tab
        reloadInspector()
        delegate?.workspaceInspectorChanged(collapsed: reportedInspectorCollapsed, tab: tab)
    }

    func inspectorCollapseChanged() {
        let collapsed = inspectorItem?.isCollapsed ?? true
        guard collapsed != reportedInspectorCollapsed else { return }
        reportedInspectorCollapsed = collapsed
        if collapsed { inspector.discardContext() }
        delegate?.workspaceInspectorChanged(collapsed: collapsed, tab: inspectorTab)
    }

    /// Brings the inspector in line with the store and with where the pane
    /// is. The session it describes is the one the session keys act on.
    func reloadInspector() {
        guard let store = delegate?.store else { return }
        let socket = store.snapshot?.kitty.socket ?? ""
        // Only what is listed can be gone to, so only that is shown.
        let sessions = store.sessions.filter { state.row(AttentionCycler.key($0, snapshotSocket: socket)) != nil }
        let shown = shownSessionRow.flatMap { row in
            sessions.first { AttentionCycler.key($0, snapshotSocket: socket) == row.key }
        }
        inspector.show(WorkspaceInspectorView.Model(
            tab: inspectorTab,
            waiting: WorkspaceInspector.waiting(
                sessions, asl: store.snapshot?.asl.waiting ?? [], snapshotSocket: socket, now: now(), glyphs: glyphs, order: order
            ),
            inspection: shown.map { WorkspaceInspector.inspection($0, snapshotSocket: socket, now: now(), glyphs: glyphs, displayName: shownSessionRow?.displayName) },
            current: state.current,
            feedNotice: WorkspaceSidebar.feedNotice(store.feed)
        ))
    }

    /// Return in the session list: focuses the selected row, as a click does.
    func focusSelectedItem() {
        focusItem(at: sidebar.selectedRow)
    }

    /// Does what a click on the line at `index` does.
    func focusItem(at index: Int) {
        guard index >= 0, index < items.count else { return }
        switch items[index] {
        case let .row(row):
            if let current = state.row(row.key) { activate(current, reattaching: true) }
        case let .view(view): showView(view.id, focus: true)
        case let .group(group):
            if !group.needsAttention { setCollapsed(project: group.project, pinned: group.pinned, !group.collapsed) }
        case .section: break
        }
    }

    /// ← and → in the session list: collapse or expand the project of the
    /// session the list is on, which is the one the pane is on once its row
    /// was collapsed away.
    func setSelectedProjectCollapsed(_ collapsed: Bool) {
        let index = sidebar.selectedRow
        var key = state.selection
        if index >= 0, index < items.count {
            switch items[index] {
            case let .row(row): key = row.key
            case let .view(view): key = view.besideRuntimeSessionID.map(AttentionCycler.Key.embedded)
            case .section, .group: key = nil
            }
        } else if let beside = views.view(viewSelection)?.besideRuntimeSessionID {
            key = .embedded(beside)
        }
        guard let group = group(of: key), !group.needsAttention else { return }
        setCollapsed(project: group.project, pinned: group.pinned, collapsed)
    }
}

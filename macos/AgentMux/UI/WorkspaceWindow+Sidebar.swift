import AppKit
import os

// The sidebar: its rows, dragging them, and its width.
// Part of WorkspaceWindowController (WorkspaceWindow.swift).
extension WorkspaceWindowController {
    // MARK: Sidebar

    func reloadSidebar(force: Bool = false, allowReorder: Bool = false) {
        // A session's terminals follow its row and are closed with it by refresh.
        let listed = state.embeddedRuntimeSessionIDs
        for row in state.rows {
            if let id = row.embeddedRuntimeSessionID {
                sessionNames[id] = row.displayName
                (terminals[id]?.view as? Ghostty.SurfaceView)?.hostName = row.displayName
            }
        }
        let about = Set(views.all.compactMap(\.aboutRuntimeSessionID))
        sessionNames = sessionNames.filter { listed.contains($0.key) || about.contains($0.key) }
        let own = views.all.filter { view in view.besideRuntimeSessionID.map { !listed.contains($0) } ?? true }
        var wanted = [Item.section(Self.viewsSection)] + own.map(Item.view)
            + state.groups.flatMap { group in
                // A collapsed project lists its header only, which still
                // counts what waits under it.
                [Item.group(group)] + (group.collapsed ? [] : group.rows).flatMap { row in
                    [Item.row(row)] + views.terminals(beside: row.embeddedRuntimeSessionID).map(Item.view)
                }
            }
        let holdingHover = sidebar.pointerInside && !Self.hasNewSidebarTargets(wanted, comparedTo: items)
        if !allowReorder, !items.isEmpty, holdingHover || sidebarMenuOpen || dragged != nil {
            wanted = Self.holdingSidebar(items, latest: wanted)
        }
        let dimmed: CGFloat = state.current ? 1 : 0.5
        let decorations = wanted.map(decoration)
        let ended = IndexSet(wanted.indices.filter { index in
            if case let .row(row) = wanted[index] { return state.row(row.key) == nil }
            return false
        })
        if force || wanted != items || sidebar.alphaValue != dimmed || decorations != drawnDecorations || !ended.isEmpty {
            // A snapshot mostly moves ages and glyphs: only those lines are
            // drawn again, and the pointer's × stays where it is. A line
            // whose ⌘ digit or session name moved is one of them, though
            // its own item is the same: a session started above it, in a
            // collapsed project, or the session beside it was renamed.
            var changed = force ? nil : Self.changedLines(from: items, to: wanted)
            changed?.formUnion(ended)
            if changed != nil, decorations.count == drawnDecorations.count {
                changed?.formUnion(IndexSet(decorations.indices.filter { decorations[$0] != drawnDecorations[$0] }))
            }
            items = wanted
            sidebar.bands = Item.bands(in: wanted)
            drawnDecorations = decorations
            sidebar.alphaValue = dimmed
            if let changed {
                if !changed.isEmpty { reload(changed) }
            } else {
                applying = true
                sidebar.reloadData()
                applying = false
                syncHover(IndexSet(integersIn: 0..<sidebar.numberOfRows))
            }
        }
        syncSidebarSelection()
        reloadInspector()
        if isSwitching { switcher.update(switcherEntries()) }
    }

    /// What a line draws that its item does not carry: a session's ⌘ digit,
    /// or the name and profile marker inherited by a view.
    private func decoration(_ item: Item) -> LineDecoration {
        let label: String?
        switch item {
        case let .row(row): label = sessionKey(of: row.key)
        case let .view(view): label = view.aboutRuntimeSessionID.flatMap { sessionNames[$0] }
        case .section, .group: label = nil
        }
        return LineDecoration(label: label, profileColor: profileColor(of: item))
    }

    func syncSidebarSelection() {
        let wanted = shownLine.map { IndexSet(integer: $0) } ?? IndexSet()
        guard sidebar.selectedRowIndexes != wanted else { return }
        applying = true
        sidebar.selectRowIndexes(wanted, byExtendingSelection: false)
        applying = false
    }

    /// The line of what the pane is on, if the list has it.
    private var shownLine: Int? {
        items.firstIndex { item in
            switch item {
            case let .view(view): return view.id == viewSelection
            case let .row(row): return viewSelection == nil && row.key == state.selection
            case .section, .group: return false
            }
        }
    }

    /// The session the pane is on, or the one a terminal it is on is beside.
    private var shownSessionKey: AttentionCycler.Key? {
        guard let viewSelection else { return state.selection }
        return views.all.first { $0.id == viewSelection }?.besideRuntimeSessionID.map { .embedded($0) }
    }

    /// Finds what the pane is on in the list: a hidden sidebar is shown, a
    /// collapsed project holding the line is expanded, and the line is
    /// scrolled into view and flashes. The keyboard stays where it is.
    /// False when the list has no line for it.
    @discardableResult
    func revealInSidebar() -> Bool {
        if isSidebarCollapsed {
            setSidebarCollapsed(false)
            window.layoutIfNeeded()
        }
        if shownLine == nil, let key = shownSessionKey,
           let group = state.groups.first(where: { $0.collapsed && !$0.needsAttention && $0.rows.contains { $0.key == key } }) {
            setCollapsed(project: group.project, pinned: group.pinned, false)
        }
        guard let index = shownLine else { return false }
        syncSidebarSelection()
        sidebar.scrollRowToVisible(index)
        (sidebar.rowView(atRow: index, makeIfNecessary: true) as? WorkspaceRowView)?.flash()
        return true
    }

    func numberOfRows(in tableView: NSTableView) -> Int { items.count }

    func tableView(_ tableView: NSTableView, isGroupRow row: Int) -> Bool {
        switch items[row] {
        case .section, .group: return true
        case .view, .row: return false
        }
    }

    func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool {
        !self.tableView(tableView, isGroupRow: row)
    }

    /// What the operator's click or key may select: never a heading, and
    /// never nothing. A click on the list's empty space, or ⌘-click on the
    /// selected line, keeps the line selected, so the pane stays where it
    /// is and so does the place restored at the next launch. The workspace
    /// still clears the selection itself (`syncSidebarSelection`).
    func tableView(_ tableView: NSTableView, selectionIndexesForProposedSelection proposed: IndexSet) -> IndexSet {
        let selectable = proposed.filteredIndexSet { $0 < items.count && self.tableView(tableView, shouldSelectRow: $0) }
        if selectable.isEmpty, !applying { return tableView.selectedRowIndexes }
        return selectable
    }

    /// What typing in the list matches: a line's name. The label a cell
    /// exposes starts with glyphs and, for a session, is not its name.
    func tableView(_ tableView: NSTableView, typeSelectStringFor tableColumn: NSTableColumn?, row: Int) -> String? {
        guard row < items.count else { return nil }
        return Self.typeSelectString(items[row])
    }

    static func typeSelectString(_ item: Item) -> String? {
        switch item {
        case let .row(row): return row.displayName
        case let .view(view): return view.displayName
        case .section, .group: return nil
        }
    }

    func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
        switch items[row] {
        case .section, .group: return 34
        case let .row(row) where !row.name.isEmpty: return 54
        case .view, .row: return 38
        }
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        switch items[row] {
        case let .section(title):
            let cell = tableView.makeView(withIdentifier: WorkspaceGroupCellView.identifier, owner: nil) as? WorkspaceGroupCellView
                ?? WorkspaceGroupCellView()
            cell.show(section: title)
            return cell
        case let .view(view):
            let cell = tableView.makeView(withIdentifier: WorkspaceRowCellView.identifier, owner: nil) as? WorkspaceRowCellView
                ?? WorkspaceRowCellView()
            cell.show(view, session: view.aboutRuntimeSessionID.flatMap { sessionNames[$0] },
                      program: programTitles[view.id], bell: ringingViews.contains(view.id),
                      progress: progresses[view.id])
            cell.onClose = { [weak self] in self?.closeView(view.id) }
            return cell
        case let .group(group):
            let cell = tableView.makeView(withIdentifier: WorkspaceGroupCellView.identifier, owner: nil) as? WorkspaceGroupCellView
                ?? WorkspaceGroupCellView()
            cell.show(group)
            cell.onToggle = group.needsAttention ? nil : { [weak self] in
                self?.setCollapsed(project: group.project, pinned: group.pinned, !group.collapsed)
            }
            return cell
        case let .row(row):
            let cell = tableView.makeView(withIdentifier: WorkspaceRowCellView.identifier, owner: nil) as? WorkspaceRowCellView
                ?? WorkspaceRowCellView()
            cell.show(row, muteGlyph: glyphs.mute, closing: row.embeddedRuntimeSessionID.map { closing[$0] != nil } ?? false,
                      sessionKey: sessionKey(of: row.key), bell: row.embeddedRuntimeSessionID.map(ringingSessions.contains) ?? false)
            let current = state.row(row.key) != nil
            cell.alphaValue = current ? 1 : 0.5
            cell.closeButton.isEnabled = current
            if !current { cell.detailLabel.stringValue = "Session ended" }
            cell.onClose = { [weak self] in
                guard let self, self.state.row(row.key) != nil else { return }
                self.detachSession(row.key)
            }
            return cell
        }
    }

    /// Session and child-terminal rows carry a small profile-color marker.
    /// Every selection uses the same system accent.
    func tableView(_ tableView: NSTableView, rowViewForRow row: Int) -> NSTableRowView? {
        let view: WorkspaceRowView
        if case let .view(utility) = items[row], utility.kind == .terminal {
            view = WorkspaceTerminalRowView()
        } else if case let .row(session) = items[row] {
            let sessionView = WorkspaceSessionRowView()
            sessionView.hasStateGlyph = !session.glyph.isEmpty
            view = sessionView
        } else {
            view = WorkspaceRowView()
        }
        view.tabColor = profileColor(of: items[row])
        view.selectionBorder.color = .controlAccentColor
        return view
    }

    /// Terminals listed beneath a session share its profile marker.
    func profileColor(of item: Item) -> NSColor? {
        if case let .view(view) = item,
           let id = view.besideRuntimeSessionID,
           let row = state.row(.embedded(id)) {
            return Self.tabColor(of: .row(row))
        }
        return Self.tabColor(of: item)
    }

    static func tabColor(of item: Item) -> NSColor? {
        guard case let .row(row) = item, let color = row.tabColor else { return nil }
        return NSColor(srgbRed: color.red, green: color.green, blue: color.blue, alpha: 1)
    }

    func tableView(_ tableView: NSTableView, didAdd rowView: NSTableRowView, forRow row: Int) {
        (rowView as? WorkspaceRowView)?.syncCell()
    }

    /// The arrow keys moved the selection: show it, and stay in the sidebar.
    func tableViewSelectionDidChange(_ notification: Notification) {
        guard !applying else { return }
        let index = sidebar.selectedRow
        // The operator cannot empty the list's selection
        // (`selectionIndexesForProposedSelection`); should it empty anyway,
        // the pane stays where it is rather than going blank.
        guard index >= 0, index < items.count else { return }
        switch items[index] {
        case let .row(row): select(row.key)
        case let .view(view): showView(view.id, focus: false)
        case .section, .group: select(nil)
        }
    }

    // MARK: Dragging

    static let dragType = NSPasteboard.PasteboardType("io.github.sglavoie.agentmux.workspace-line")

    /// Where the line at `index` may be dropped, as the lines it would land
    /// above; the last is the line after its last sibling. A session goes
    /// among the sessions of its project and a terminal among the ones
    /// beside its session; no other line moves.
    func dropSlots(forItemAt index: Int) -> [Int] {
        Item.dropSlots(forItemAt: index, in: items)
    }

    /// The slot a line dragged from `index` takes when it is let go over
    /// `proposed`: the first one at or below it. Nil outside the line's
    /// project or session, and where it would land back where it is.
    func dropSlot(forItemAt index: Int, proposed: Int) -> Int? {
        Item.dropSlot(forItemAt: index, proposed: proposed, in: items)
    }

    /// Moves the line at `index` to above the line at `proposed`; false when
    /// it may not go there.
    @discardableResult
    func moveItem(at index: Int, to proposed: Int) -> Bool {
        guard let slot = dropSlot(forItemAt: index, proposed: proposed) else { return false }
        switch items[index] {
        case .section, .group:
            return false
        case let .row(row):
            guard let group = state.groups.first(where: { !$0.needsAttention && $0.pinned == row.pinned && (row.pinned || $0.project == row.project) })
            else { return false }
            var keys = group.rows.map(\.key).filter { $0 != row.key }
            var at = keys.count
            if slot < items.count, case let .row(next) = items[slot] { at = keys.firstIndex(of: next.key) ?? at }
            keys.insert(row.key, at: at)
            // Keep the saved places of siblings temporarily in Needs attention.
            let base = WorkspaceSidebar.groups(state.rows, order: order, prioritizeAttention: false)
                .first { $0.pinned == row.pinned && (row.pinned || $0.project == row.project) }
            let moved = Set(keys)
            var next = keys.makeIterator()
            let saved = (base?.rows.map(\.key) ?? keys).map { moved.contains($0) ? next.next() ?? $0 : $0 }
            if row.pinned {
                order.setPinned(saved)
            } else {
                order.set(saved, for: row.project)
            }
            state.arrange(order)
            delegate?.workspaceOrderChanged(order)
        case let .view(view):
            var next: String?
            if slot < items.count, case let .view(other) = items[slot], other.besideRuntimeSessionID == view.besideRuntimeSessionID {
                next = other.id
            }
            views.moveTerminal(view.id, before: next)
        }
        reloadSidebar(allowReorder: true)
        // The waiting sessions are listed in the order they have here.
        reloadInspector()
        return true
    }

    private func draggedIndex() -> Int? {
        guard let dragged else { return nil }
        return items.firstIndex { item in
            switch item {
            case let .row(row): return dragged == .session(row.key)
            case let .view(view): return dragged == .view(view.id)
            case .section, .group: return false
            }
        }
    }

    /// A line is dragged only when there is somewhere else for it to go.
    func tableView(_ tableView: NSTableView, pasteboardWriterForRow row: Int) -> NSPasteboardWriting? {
        guard dropSlots(forItemAt: row).count > 2 else { return nil }
        let item = NSPasteboardItem()
        item.setString("", forType: Self.dragType)
        return item
    }

    func tableView(_ tableView: NSTableView, draggingSession session: NSDraggingSession, willBeginAt screenPoint: NSPoint, forRowIndexes rowIndexes: IndexSet) {
        dragged = nil
        guard let index = rowIndexes.first, items.indices.contains(index) else { return }
        switch items[index] {
        case let .row(row): dragged = .session(row.key)
        case let .view(view): dragged = .view(view.id)
        case .section, .group: break
        }
    }

    func tableView(_ tableView: NSTableView, draggingSession session: NSDraggingSession, endedAt screenPoint: NSPoint, operation: NSDragOperation) {
        dragged = nil
        reloadSidebar()
    }

    func tableView(_ tableView: NSTableView, validateDrop info: NSDraggingInfo, proposedRow row: Int, proposedDropOperation dropOperation: NSTableView.DropOperation) -> NSDragOperation {
        guard info.draggingSource as? NSTableView === sidebar, let index = draggedIndex(),
              let slot = dropSlot(forItemAt: index, proposed: row) else { return [] }
        tableView.setDropRow(slot, dropOperation: .above)
        return .move
    }

    func tableView(_ tableView: NSTableView, acceptDrop info: NSDraggingInfo, row: Int, dropOperation: NSTableView.DropOperation) -> Bool {
        guard info.draggingSource as? NSTableView === sidebar, let index = draggedIndex() else { return false }
        return moveItem(at: index, to: row)
    }

    /// A click on a row focuses it and scrolls the whole line into view; one
    /// on a project's header collapses or expands the project.
    @objc func rowClicked(_ sender: Any?) {
        let index = sidebar.clickedRow
        focusItem(at: index)
        guard index >= 0, index < items.count, tableView(sidebar, shouldSelectRow: index) else { return }
        // Focusing may have reloaded the list: scroll to where the line is now.
        let selected = sidebar.selectedRow
        sidebar.scrollRowToVisible(selected >= 0 ? selected : index)
    }

    /// Gives the keyboard to the session list: the arrow keys then move the
    /// selection and Return goes into what is selected. A hidden list is
    /// shown first.
    func focusSidebar() {
        setSidebarCollapsed(false)
        window.makeFirstResponder(sidebar)
    }

    /// Whether the sidebar is hidden and the pane has the whole window.
    var isSidebarCollapsed: Bool {
        _ = window
        return sidebarItem?.isCollapsed ?? false
    }

    @objc func toggleSidebar(_ sender: Any? = nil) {
        setSidebarCollapsed(!isSidebarCollapsed, animated: true)
    }

    func setSidebarCollapsed(_ collapsed: Bool, animated: Bool = false) {
        guard collapsed != isSidebarCollapsed, let sidebarItem else { return }
        if animated, !AppDelegate.isRunningTests {
            sidebarItem.animator().isCollapsed = collapsed
        } else {
            sidebarItem.isCollapsed = collapsed
        }
    }

    /// The sidebar was hidden or shown, by its buttons, the menu, or a drag
    /// of the divider.
    func sidebarCollapseChanged() {
        let collapsed = sidebarItem?.isCollapsed ?? false
        guard collapsed != reportedCollapsed else { return }
        reportedCollapsed = collapsed
        if collapsed, window.firstResponder === window
            || (window.firstResponder as? NSView)?.isDescendant(of: sidebar) == true {
            // A list that cannot be seen does not keep the keyboard.
            focusListOrPane()
        }
        delegate?.workspaceSidebarCollapsedChanged(collapsed)
    }

    /// Gives the keyboard to the session list, so the arrow keys work; while
    /// the list is hidden, to what the pane shows instead: its terminal, the
    /// placeholder's button, or else the window. A list that cannot be seen
    /// must not switch sessions under keys typed into it.
    func focusListOrPane() {
        guard isSidebarCollapsed else {
            window.makeFirstResponder(sidebar)
            return
        }
        var shown: NSView?
        if case let .terminal(id) = content { shown = terminals[id]?.view }
        if case let .view(id) = content { shown = viewTerminals[id]?.view }
        if case .review = content { shown = reviewView }
        if let shown, !shown.isHidden, window.makeFirstResponder(shown) { return }
        if !placeholder.isHidden, !placeholder.button.isHidden, window.makeFirstResponder(placeholder.button) { return }
        window.makeFirstResponder(nil)
    }

    // MARK: Widths

    static let sidebarWidths: ClosedRange<CGFloat> = 180...420

    static let inspectorWidths: ClosedRange<CGFloat> = 220...420

    static let defaultSidebarWidth: CGFloat = 240

    static let defaultInspectorWidth: CGFloat = 260

    /// The width to open at: the saved one, kept within what the item
    /// allows, else the default.
    static func width(_ saved: CGFloat?, default fallback: CGFloat, in range: ClosedRange<CGFloat>) -> CGFloat {
        guard let saved, saved.isFinite, saved > 0 else { return fallback }
        return min(max(saved, range.lowerBound), range.upperBound)
    }

    /// The split view was laid out again: by a drag of a divider, a hidden
    /// item shown, or the window resized. The width of what is shown is
    /// reported when it changed; a hidden one reports nothing.
    func splitResized() {
        if isOpen && isSplit { fitPanes() }
        let sidebarShown = sidebarItem.flatMap { $0.isCollapsed ? nil : $0.viewController.view.frame.width }
        let inspectorShown = inspectorItem.flatMap { $0.isCollapsed ? nil : $0.viewController.view.frame.width }
        // Mid-animation an item is narrower than it may be, so a width is
        // kept only within its limits.
        let sidebar = sidebarShown.flatMap { Self.sidebarWidths.contains($0) ? $0 : nil }
        let inspector = inspectorShown.flatMap { Self.inspectorWidths.contains($0) ? $0 : nil }
        let sidebarChanged = sidebar != nil && sidebar != reportedSidebarWidth
        let inspectorChanged = inspector != nil && inspector != reportedInspectorWidth
        guard sidebarChanged || inspectorChanged else { return }
        if sidebarChanged { reportedSidebarWidth = sidebar }
        if inspectorChanged { reportedInspectorWidth = inspector }
        delegate?.workspaceLayoutChanged(sidebarWidth: sidebarChanged ? sidebar : nil,
                                         inspectorWidth: inspectorChanged ? inspector : nil)
    }
}

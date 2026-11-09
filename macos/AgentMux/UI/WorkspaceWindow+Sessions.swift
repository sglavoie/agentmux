import AppKit
import SwiftUI
import os

// Naming and pinning sessions.
// Part of WorkspaceWindowController (WorkspaceWindow.swift).
extension WorkspaceWindowController {
    // MARK: Naming

    static let nameSessionTitle = "Name Session…"

    static let nameTerminalTitle = "Name Terminal…"

    static let namingNeedsEmbedded = "Select a running session to name it."

    var nameShownTitle: String {
        views.view(viewSelection)?.canName == true ? Self.nameTerminalTitle : Self.nameSessionTitle
    }

    var canNameShown: Bool {
        views.view(viewSelection)?.canName == true || shownSessionRow?.embeddedRuntimeSessionID != nil
    }

    /// ⌘R: names the session, terminal, or shell the pane is on.
    @discardableResult
    func nameShownSession() -> Bool {
        if let view = views.view(viewSelection), view.canName {
            nameTerminal(view.id)
            return true
        }
        guard let row = shownSessionRow, row.embeddedRuntimeSessionID != nil else {
            Self.logger.notice("workspace: no embedded session is shown; nothing to name")
            return false
        }
        nameSession(row.key)
        return true
    }

    /// Asks for a session's name and has amx write it. An empty answer
    /// removes the name; a cancelled one changes nothing. The line shows the
    /// change once a snapshot carries it.
    func nameSession(_ key: AttentionCycler.Key) {
        guard let row = state.row(key), let id = row.embeddedRuntimeSessionID else { return }
        (askName ?? askForName)(row) { [weak self] answer in
            guard let self, let answer else { return }
            let name = answer.trimmingCharacters(in: .whitespacesAndNewlines)
            guard name != row.name else { return }
            Self.logger.notice("workspace: naming session \(id, privacy: .public)")
            self.delegate?.nameSession(runtimeSessionID: id, name: name) { [weak self] problem in
                guard let self, let problem else { return }
                let title = row.displayName
                if let report = self.reportNameProblem {
                    report(title, problem)
                } else {
                    self.showError(title: "\(title) could not be named", message: problem)
                }
            }
        }
    }

    private func askForName(_ row: WorkspaceRow, _ answer: @escaping @MainActor (String?) -> Void) {
        let model = WorkspaceTaskName(row: row, command: { [weak self] in self?.delegate?.workspaceRecapCommand() })
        let alert = NSAlert()
        alert.messageText = "Name the task"
        alert.informativeText = row.project
        let host = NSHostingView(rootView: WorkspaceTaskNameView(model: model))
        host.frame = NSRect(x: 0, y: 0, width: 340, height: 145)
        alert.accessoryView = host
        alert.addButton(withTitle: "Save Name")
        alert.addButton(withTitle: "Cancel")
        model.load()
        alert.beginSheetModal(for: window) { response in
            MainActor.assumeIsolated {
                model.close()
                answer(response == .alertFirstButtonReturn ? model.name : nil)
            }
        }
    }

    func nameTerminal(_ id: String) {
        guard let view = views.view(id), view.canName else { return }
        let terminal = viewTerminals[id]?.view
        (askTerminalName ?? askForTerminalName)(view) { [weak self] answer in
            guard let self, let answer, self.views.view(id) == view,
                  self.viewTerminals[id]?.view === terminal else { return }
            self.views.name(id, answer)
            guard let named = self.views.view(id), named != view else { return }
            (terminal as? Ghostty.SurfaceView)?.hostName = named.displayName
            self.reloadSidebar()
            self.window.title = self.windowTitle
            self.paneLayout.titles(active: self.title(for: self.place), other: self.title(for: self.otherPlace))
            self.reportLayout()
        }
    }

    private func askForTerminalName(_ view: WorkspaceUtilityView, _ answer: @escaping @MainActor (String?) -> Void) {
        askForName(title: view.displayName, name: view.name ?? "",
                   explanation: "The name is shown in the sidebar. Leave it empty to restore the default name.", answer)
    }

    private func askForName(title: String, name: String, explanation: String, _ answer: @escaping @MainActor (String?) -> Void) {
        let field = NSTextField(string: name)
        field.placeholderString = "No name"
        field.frame = NSRect(x: 0, y: 0, width: 260, height: 24)
        let alert = NSAlert()
        alert.messageText = "Name \(title)"
        alert.informativeText = explanation
        alert.accessoryView = field
        alert.addButton(withTitle: "Name")
        alert.addButton(withTitle: "Cancel")
        alert.window.initialFirstResponder = field
        alert.beginSheetModal(for: window) { response in
            MainActor.assumeIsolated { answer(response == .alertFirstButtonReturn ? field.stringValue : nil) }
        }
    }

    /// The menu a right-click on the session list shows: what can be done to
    /// the line under the pointer. A line with nothing to offer shows no menu
    /// (`WorkspaceSidebarView.hasMenu`).
    func menuWillOpen(_ menu: NSMenu) { sidebarMenuOpen = true }

    func menuDidClose(_ menu: NSMenu) {
        sidebarMenuOpen = false
        reloadSidebar()
    }

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        for item in contextMenuItems(forRow: sidebar.clickedRow) { menu.addItem(item) }
    }

    static let newTerminalTitle = "New Terminal Below"
    static let newSessionTitle = "New Session Below…"

    static let copyRuntimeSessionIDTitle = "Copy Runtime Session ID"

    func contextMenuItems(forRow index: Int) -> [NSMenuItem] {
        if index < 0 { return listMenuItems() }
        guard index < items.count else { return [] }
        switch items[index] {
        case let .row(row): return state.row(row.key).map(sessionMenuItems) ?? []
        case let .view(view): return utilityMenuItems(view)
        case let .group(group): return groupMenuItems(group)
        case .navigation, .section, .overview: return []
        }
    }

    func utilityMenuItems(_ view: WorkspaceUtilityView) -> [NSMenuItem] {
        // The Dashboard and the Overview have nothing to close until their
        // program runs.
        guard !view.isPermanent || viewTerminals[view.id] != nil else { return [] }
        let close = NSMenuItem(title: Self.closeTitle, action: #selector(closeClickedView(_:)), keyEquivalent: "")
        close.representedObject = view.id
        close.target = self
        var items: [NSMenuItem] = []
        if view.canName {
            let name = NSMenuItem(title: Self.nameTerminalTitle, action: #selector(nameClickedTerminal(_:)), keyEquivalent: "")
            name.representedObject = view.id
            name.target = self
            items.append(name)
        }
        if compactSidebar, view.kind == .terminal, let beside = view.besideRuntimeSessionID {
            let terminals = views.terminals(beside: beside)
            if let position = terminals.firstIndex(where: { $0.id == view.id }), terminals.count > 1 {
                for (title, offset) in [("Move Terminal Left", -1), ("Move Terminal Right", 1)] {
                    let move = NSMenuItem(title: title, action: #selector(moveClickedTerminal(_:)), keyEquivalent: "")
                    move.representedObject = [view.id, offset] as [Any]
                    move.target = self
                    move.isEnabled = terminals.indices.contains(position + offset)
                    items.append(move)
                }
            }
        }
        return items + [close]
    }

    @objc private func nameClickedTerminal(_ sender: Any?) {
        guard let id = (sender as? NSMenuItem)?.representedObject as? String else { return }
        nameTerminal(id)
    }

    @objc private func moveClickedTerminal(_ sender: Any?) {
        guard let values = (sender as? NSMenuItem)?.representedObject as? [Any],
              values.count == 2, let id = values[0] as? String, let offset = values[1] as? Int,
              let view = views.view(id), let beside = view.besideRuntimeSessionID else { return }
        let terminals = views.terminals(beside: beside)
        guard let index = terminals.firstIndex(where: { $0.id == id }),
              terminals.indices.contains(index + offset) else { return }
        let destination = offset < 0 ? terminals[index - 1].id
            : terminals.indices.contains(index + 2) ? terminals[index + 2].id : nil
        views.moveTerminal(id, before: destination)
        reloadSidebar(allowReorder: true)
    }

    /// The list's empty space: where to go, and what to open, as the main
    /// menu offers them. Each goes to the app, as the main menu's do.
    private func listMenuItems() -> [NSMenuItem] {
        let entries: [(String, Selector)] = [
            ("Go to…", #selector(AppDelegate.showSwitcher(_:))),
            ("New Shell", #selector(AppDelegate.newShellFromMenu(_:))),
            ("Dashboard", #selector(AppDelegate.openDashboardFromMenu(_:))),
            ("Overview", #selector(AppDelegate.openOverviewFromMenu(_:))),
        ]
        return entries.map { title, action in
            let item = NSMenuItem(title: title, action: action, keyEquivalent: "")
            item.target = delegate
            return item
        }
    }

    static let collapseProjectTitle = "Collapse Project"

    static let expandProjectTitle = "Expand Project"

    static let collapsePinnedTitle = "Collapse Pinned"

    static let expandPinnedTitle = "Expand Pinned"

    static let collapseSectionTitle = "Collapse Section"

    static let expandSectionTitle = "Expand Section"


    static let reattachTitle = "Reattach"

    private func groupMenuItems(_ group: WorkspaceGroup) -> [NSMenuItem] {
        guard group.canCollapse else { return [] }
        let title = group.branch != nil
            ? (group.collapsed ? "Expand Branch" : "Collapse Branch")
            : group.isSectionHeading
            ? (group.collapsed ? Self.expandSectionTitle : Self.collapseSectionTitle)
            : group.pinned
            ? (group.collapsed ? Self.expandPinnedTitle : Self.collapsePinnedTitle)
            : (group.collapsed ? Self.expandProjectTitle : Self.collapseProjectTitle)
        let toggle = NSMenuItem(title: title, action: #selector(toggleClickedGroup(_:)), keyEquivalent: "")
        toggle.representedObject = WorkspaceOrder.groupToken(group)
        toggle.target = self
        return [toggle] + (group.isSectionHeading || group.pinned || group.isFlat ? [] : reviewMenuItems(rows: state.rows.filter { $0.project == group.project }, project: group.project))
    }

    @objc private func toggleClickedGroup(_ sender: Any?) {
        guard let token = (sender as? NSMenuItem)?.representedObject as? String,
              let group = state.groups.first(where: { $0.canCollapse && WorkspaceOrder.groupToken($0) == token })
        else { return }
        setCollapsed(group, !group.collapsed)
    }

    /// Collapses a project's group, or the pinned one, to its header, or
    /// expands it. It is kept with the order sessions were dragged into, so
    /// it outlasts the window and the app. The pane stays where it is, even
    /// on a session the collapse took off the list.
    func setCollapsed(project: String, pinned: Bool, _ collapsed: Bool) {
        changeCollapsed { $0.setCollapsed(project: project, pinned: pinned, collapsed) }
    }

    /// Collapses or expands a listed group: a project's, the pinned one, or
    /// a state section.
    func setCollapsed(_ group: WorkspaceGroup, _ collapsed: Bool) {
        guard group.canCollapse else { return }
        changeCollapsed { $0.setCollapsed(group, collapsed) }
    }

    /// Expands what keeps a session's line off the list: its project's
    /// group, the pinned one, and the state section it is in.
    func expandGroups(of key: AttentionCycler.Key?) {
        guard let group = group(of: key), group.canCollapse else { return }
        changeCollapsed { order in
            order.setCollapsed(group, false)
            order.setCollapsed(project: group.project, pinned: group.pinned, false)
            if !group.isNestedState, let section = group.stateSection { order.setCollapsed(section, false) }
        }
    }

    private func changeCollapsed(_ change: (inout WorkspaceOrder) -> Void) {
        var changed = order
        change(&changed)
        guard changed != order else { return }
        order = changed
        state.arrange(order, grouping: sidebarGrouping, sorting: sidebarSort)
        delegate?.workspaceOrderChanged(order)
        reloadSidebar(allowReorder: true)
    }

    private func sessionMenuItems(_ row: WorkspaceRow) -> [NSMenuItem] {
        guard let id = row.embeddedRuntimeSessionID else { return [] }
        let peek = NSMenuItem(title: "Peek at Session", action: #selector(peekClickedSession(_:)), keyEquivalent: "")
        peek.representedObject = row.key; peek.target = self
        let name = NSMenuItem(title: Self.nameSessionTitle, action: #selector(nameClickedSession(_:)), keyEquivalent: "")
        name.representedObject = row.key
        name.target = self
        // The inspector's Color…, Icon…, and Mute, for the line under the
        // pointer rather than the session the pane is on.
        let looks = [WorkspaceInspectorAction.color, .icon, .mute].map { action in
            let item = NSMenuItem(title: action.title(muted: row.muted), action: #selector(clickedSessionAction(_:)), keyEquivalent: "")
            item.representedObject = [row.key, action] as [Any]
            item.target = self
            return item
        }
        let pinTitle = sidebarGrouping == .stateThenProject ? Self.pinSessionTitle : Self.pinShownSessionTitle
        let pin = NSMenuItem(title: row.pinned ? Self.unpinSessionTitle : pinTitle,
                             action: #selector(pinClickedSession(_:)), keyEquivalent: "")
        pin.representedObject = row.key
        pin.target = self
        let handoff = NSMenuItem(title: WorkspaceHandoffModel.title, action: #selector(handoffClickedSession(_:)), keyEquivalent: "")
        handoff.representedObject = row.key; handoff.target = self
        handoff.isEnabled = row.conversation != nil
        // Session actions for the line under the pointer.
        let terminal = NSMenuItem(title: Self.newTerminalTitle, action: #selector(openTerminalBesideClickedSession(_:)), keyEquivalent: "")
        terminal.representedObject = id
        terminal.target = self
        let session = NSMenuItem(title: Self.newSessionTitle, action: #selector(newSessionBelowClickedSession(_:)), keyEquivalent: "")
        session.representedObject = row.key
        session.target = self
        session.isEnabled = closing[id] == nil
        handoff.isEnabled = handoff.isEnabled && closing[id] == nil
        let copy = NSMenuItem(title: Self.copyRuntimeSessionIDTitle, action: #selector(copyClickedRuntimeSessionID(_:)), keyEquivalent: "")
        copy.representedObject = id
        copy.target = self
        let end = NSMenuItem(title: Self.endSessionTitle, action: #selector(endClickedSession(_:)), keyEquivalent: "")
        end.representedObject = row.key
        end.target = self
        end.isEnabled = closing[id] == nil
        guard detached.contains(id) else { return [peek, name] + looks + [pin, .separator(), terminal, session, handoff, copy, .separator(), end] + reviewMenuItems(rows: [row]) }
        // What a click or Return on the line does: attach again, taking the
        // session from whichever terminal has it.
        let reattach = NSMenuItem(title: Self.reattachTitle, action: #selector(reattachClickedSession(_:)), keyEquivalent: "")
        reattach.representedObject = row.key
        reattach.target = self
        return [peek, name] + looks + [pin, .separator(), reattach, terminal, session, handoff, copy, .separator(), end] + reviewMenuItems(rows: [row])
    }

    @objc private func clickedSessionAction(_ sender: Any?) {
        guard let named = (sender as? NSMenuItem)?.representedObject as? [Any],
              let key = named.first as? AttentionCycler.Key, let action = named.last as? WorkspaceInspectorAction,
              let row = state.row(key)
        else { return }
        switch action {
        case .color: openPicker(.colorPicker, for: row)
        case .icon: openPicker(.iconPicker, for: row)
        case .mute: delegate?.workspaceToggleMute(row)
        case .name, .clearState, .markActive, .reviewChanges: return
        }
    }

    @objc private func activateClickedSession(_ sender: Any?) {
        guard let key = (sender as? NSMenuItem)?.representedObject as? AttentionCycler.Key, let row = state.row(key) else { return }
        activate(row)
    }

    @objc private func reattachClickedSession(_ sender: Any?) {
        guard let key = (sender as? NSMenuItem)?.representedObject as? AttentionCycler.Key, let row = state.row(key) else { return }
        activate(row, reattaching: true)
    }

    @objc private func closeClickedView(_ sender: Any?) {
        guard let id = (sender as? NSMenuItem)?.representedObject as? String else { return }
        closeView(id)
    }

    @objc private func openTerminalBesideClickedSession(_ sender: Any?) {
        guard let id = (sender as? NSMenuItem)?.representedObject as? String, state.row(.embedded(id)) != nil else { return }
        open(views.openTerminal(beside: id))
    }

    @objc private func copyClickedRuntimeSessionID(_ sender: Any?) {
        guard let id = (sender as? NSMenuItem)?.representedObject as? String else { return }
        copyToPasteboard(id)
    }

    @objc private func handoffClickedSession(_ sender: Any?) {
        guard state.current, let key = (sender as? NSMenuItem)?.representedObject as? AttentionCycler.Key,
              let row = state.row(key), row.conversation != nil else { return }
        if let id = row.embeddedRuntimeSessionID, closing[id] != nil { return }
        delegate?.workspaceHandoff(from: row)
    }

    @objc private func newSessionBelowClickedSession(_ sender: Any?) {
        guard let key = (sender as? NSMenuItem)?.representedObject as? AttentionCycler.Key,
              let row = state.row(key), let id = row.embeddedRuntimeSessionID, closing[id] == nil else { return }
        delegate?.workspaceNewSession(below: row)
    }

    @objc private func endClickedSession(_ sender: Any?) {
        guard let key = (sender as? NSMenuItem)?.representedObject as? AttentionCycler.Key else { return }
        closeSession(key)
    }

    @objc private func pinClickedSession(_ sender: Any?) {
        guard let key = (sender as? NSMenuItem)?.representedObject as? AttentionCycler.Key, let row = state.row(key) else { return }
        setPinned(key, !row.pinned)
    }

    // MARK: Pinning

    static let pinSessionTitle = "Pin to Top"

    static let unpinSessionTitle = "Unpin"

    /// The Session menu's item, which says which of the two it does.
    static let pinShownSessionTitle = "Pin Session"

    static let unpinShownSessionTitle = "Unpin Session"

    /// What the Session menu's pin item does to the session the pane is on.
    var pinShownTitle: String {
        shownSessionRow?.pinned == true ? Self.unpinShownSessionTitle : Self.pinShownSessionTitle
    }

    /// Pins a listed session, which then leaves its project for the Pinned
    /// group above them all, or unpins it, which puts it back. Any session
    /// can be pinned. The pin is the session's own (the ★
    /// in its title), so the dashboard lists it above Needs you as well;
    /// what is kept here is only where it goes among the pinned.
    func setPinned(_ key: AttentionCycler.Key, _ pinned: Bool) {
        guard let row = state.row(key), row.pinned != pinned else { return }
        // Only a live snapshot says a session is gone, and its place with it.
        if state.current { order.forgetPins(notAmong: state.rows.map(\.key)) }
        order.setPinned(key, pinned)
        delegate?.workspaceOrderChanged(order)
        // The store shows the pin at once, which refreshes the sidebar.
        delegate?.workspaceSetPinned(row, pinned)
    }

    /// Pins the session the pane is on, or unpins it when it is pinned, and
    /// reports whether there was one.
    @discardableResult
    func togglePinOfShownSession() -> Bool {
        guard let row = shownSessionRow else { return false }
        setPinned(row.key, !row.pinned)
        return true
    }

    @objc private func nameClickedSession(_ sender: Any?) {
        guard let key = (sender as? NSMenuItem)?.representedObject as? AttentionCycler.Key else { return }
        nameSession(key)
    }

    /// F5 and F6: opens the icon or color picker for the embedded session the
    /// pane is on, and reports whether there was one.
    @discardableResult
    func openPicker(_ kind: WorkspaceUtilityView.Kind) -> Bool {
        guard let row = shownSessionRow, openPicker(kind, for: row) else {
            Self.logger.notice("workspace: no embedded session is shown; no picker opened")
            return false
        }
        return true
    }

    /// Opens the icon or color picker for a listed embedded session, and
    /// reports whether it is one.
    @discardableResult
    func openPicker(_ kind: WorkspaceUtilityView.Kind, for row: WorkspaceRow) -> Bool {
        guard let id = row.embeddedRuntimeSessionID,
              let picker = WorkspaceUtilityView.picker(kind, runtimeSessionID: id) else { return false }
        open(picker)
        return true
    }

    /// Notes where the pane is, keeping where it was before.
    func notePlace() {
        guard let here = place, here != recent.here else { return }
        if case let .view(id) = here, views.view(id)?.isOverlay == true { return }
        recent = (here, recent.here)
        let key: AttentionCycler.Key?
        switch here {
        case let .session(session): key = session
        case let .view(id): key = views.view(id)?.besideRuntimeSessionID.map(AttentionCycler.Key.embedded)
        case .review: key = nil
        }
        if let key, state.row(key) != nil, order.visited.first != WorkspaceOrder.token(key) {
            order.visit(key)
            delegate?.workspaceOrderChanged(order)
            if sidebarSort == .recentlyVisited {
                state.arrange(order, grouping: sidebarGrouping, sorting: sidebarSort)
                reloadSidebar()
            }
        }
    }

    /// ⌘L: goes back to where the pane was before it came here, so that
    /// pressing it again returns. Reports whether that place is still there.
    @discardableResult
    func toggleRecentPlace() -> Bool {
        switch recent.before {
        case let .session(key)?:
            guard let row = state.row(key) else { return false }
            go(to: row, raising: false)
        case let .view(id)?:
            guard showView(id, focus: true) else { return false }
        case .review?:
            guard reviewView != nil else { return false }
            if otherPlace == .review { return focusOtherPane() }
            reviewSelected = true
            state.select(nil)
            viewSelection = nil
            updateContent(focus: true)
        case nil:
            return false
        }
        return true
    }

    /// Where the pane is now.
    var place: Place? {
        if reviewSelected { return .review }
        if let viewSelection { return .view(viewSelection) }
        return state.selection.map(Place.session)
    }

    /// Unlists an opened view and goes back to where the pane was before it.
    func retire(_ view: WorkspaceUtilityView) {
        views.close(view.id)
        let back = returnPlaces.removeValue(forKey: view.id)
        reloadSidebar()
        if otherPlace == .view(view.id) {
            otherPlace = nil
            otherContent = .empty
            updateContent(focus: false)
        }
        guard viewSelection == view.id else { return }
        viewSelection = nil
        switch back {
        case let .session(key)? where state.row(key) != nil:
            select(key, focus: true)
        case let .view(id)? where views.view(id) != nil:
            showView(id, focus: true)
        default:
            if view.kind == .setup { updateContent(focus: false) }
            else { showView(WorkspaceUtilityView.dashboard.id, focus: true) }
        }
    }

    func discardViewTerminal(_ id: String) {
        guard let terminal = viewTerminals.removeValue(forKey: id) else { return }
        ringingViews.remove(id)
        rendererFailedViews.remove(id)
        programTitles[id] = nil
        searches[id] = nil
        searchGenerations[id] = nil
        scrollbars[id] = nil
        progresses[id] = nil
        if isOpen, let responder = window.firstResponder as? NSView, responder.isDescendant(of: terminal.view) {
            focusListOrPane()
        }
        terminal.view.removeFromSuperview()
        terminal.close()
    }

    /// Moves `offset` places through the sessions and the terminals listed
    /// under them, in sidebar order, wrapping; from anywhere else, to the
    /// first (or last) of them. Stepping through the sidebar stays in the app.
    ///
    /// With `raising` the step is from session to session: terminals are
    /// passed over, one of them counts as standing just below its session,
    /// With `sessionsOnly` the step is the same one, as F7 and F9 do.
    func selectNeighbor(_ offset: Int, raising: Bool = false, sessionsOnly: Bool = false) {
        switch neighborStop(offset, skipping: raising || sessionsOnly) {
        case let .row(row)?: go(to: row, raising: raising)
        case let .view(view)?: showView(view.id, focus: true)
        case .navigation?, .section?, .overview?, .group?, nil: break
        }
    }

    /// The project of the active session or one of its terminals. Global
    /// utility views have no project, even if a session was shown before.
    private var tabProject: String? {
        guard !reviewSelected else { return nil }
        if let viewSelection {
            return views.view(viewSelection)?.besideRuntimeSessionID
                .flatMap { state.row(.embedded($0))?.project }
        }
        return state.selectedRow?.project
    }

    var canSelectProjectTab: Bool { tabProject != nil }

    /// ⌘← / ⌘→ wrap through this project's sessions and their terminals in
    /// sidebar order, including pinned, promoted, and collapsed sessions.
    func selectProjectTab(_ offset: Int) {
        guard let project = tabProject else { return }
        let stops = scopedSidebar.rows.filter { $0.project == project }.flatMap { row in
            [Item.row(row)] + views.terminals(beside: row.embeddedRuntimeSessionID).map(Item.view)
        }
        guard let here = stops.firstIndex(where: { item in
            switch item {
            case let .row(row): return viewSelection == nil && row.key == state.selection
            case let .view(view): return view.id == viewSelection
            case .navigation, .section, .overview, .group: return false
            }
        }) else { return }
        let next = stops[((here + offset) % stops.count + stops.count) % stops.count]
        let key: AttentionCycler.Key?
        switch next {
        case let .row(row): key = row.key
        case let .view(view): key = view.besideRuntimeSessionID.map { .embedded($0) }
        case .navigation, .section, .overview, .group: return
        }
        expandGroups(of: key)
        switch next {
        case let .row(row): go(to: row, raising: false)
        case let .view(view): showView(view.id, focus: true)
        case .navigation, .section, .overview, .group: break
        }
    }

    /// ↑ and ↓ in the session list while none of its lines is selected,
    /// because the pane's session, or the terminal beside it, was collapsed
    /// away: the step is from there, and stays in the list as an arrow key
    /// does. False when the pane is on nothing, which leaves the key to the
    /// list.
    func stepFromUnlistedPlace(_ offset: Int) -> Bool {
        guard place != nil else { return false }
        switch neighborStop(offset, skipping: false) {
        case let .row(row)?: select(row.key)
        case let .view(view)?: showView(view.id, focus: false)
        case .navigation?, .section?, .overview?, .group?, nil: return false
        }
        return true
    }

    /// The line `offset` steps from where the pane is; see `selectNeighbor`.
    private func neighborStop(_ offset: Int, skipping: Bool) -> Item? {
        let listed = state.embeddedRuntimeSessionIDs
        // A terminal beside a session counts as standing just below it when
        // terminals are passed over, or when its line was collapsed away.
        let shownView = views.view(viewSelection)
        let viewListed = shownView.map { items.contains(.view($0)) } ?? false
        let beside = skipping || !viewListed ? shownView?.besideRuntimeSessionID : nil
        // A collapsed project lists none of its sessions, but the one the
        // pane is on still stands where its row would be, so the step is from
        // there rather than from the top.
        let stops = items.flatMap { item -> [Item] in
            switch item {
            case .row: return [item]
            case let .view(view): return !skipping && view.besideRuntimeSessionID.map(listed.contains) ?? false ? [item] : []
            case let .group(group) where group.collapsed:
                return scopedSidebar.rows(under: group).filter { row in
                    if let beside { return row.embeddedRuntimeSessionID == beside }
                    return viewSelection == nil && row.key == state.selection
                }.map(Item.row)
            case .navigation, .section, .overview, .group: return []
            }
        }
        guard !stops.isEmpty else { return nil }
        let here = stops.firstIndex { item in
            switch item {
            case let .row(row):
                if let beside { return row.embeddedRuntimeSessionID == beside }
                return viewSelection == nil && row.key == state.selection
            case let .view(view): return view.id == viewSelection
            case .navigation, .section, .overview, .group: return false
            }
        }
        // Up from a terminal, the nearest session is its own.
        let step = beside != nil && offset < 0 ? offset + 1 : offset
        let next = here.map { (($0 + step) % stops.count + stops.count) % stops.count }
            ?? (offset < 0 ? stops.count - 1 : 0)
        return stops[next]
    }

    /// Goes to the first session of a section the overview line counts,
    /// and finds its line as ⌘J does: expanded, scrolled into view, flashed.
    func selectSection(_ count: WorkspaceSectionCount) {
        guard let row = state.row(count.first) else { return }
        expandGroups(of: row.key)
        go(to: row, raising: false)
        revealInSidebar()
    }

    /// Goes to the session at `index` in sidebar order; nothing when fewer
    /// sessions are listed.
    /// Sessions in collapsed groups follow the visible ones; going to one
    /// expands what holds it.
    func selectSession(at index: Int, raising: Bool = false) {
        let rows = sessionShortcutRows
        guard rows.indices.contains(index) else { return }
        let row = rows[index]
        expandGroups(of: row.key)
        go(to: row, raising: raising)
    }

    /// The group a session is listed in.
    func group(of key: AttentionCycler.Key?) -> WorkspaceGroup? {
        guard let key else { return nil }
        return state.groups.first { $0.rows.contains { $0.key == key } }
    }

    /// The name of the session at `index` in sidebar order, which ⌘ and
    /// that digit go to; nil when fewer sessions are listed.
    func sessionName(at index: Int) -> String? {
        let rows = sessionShortcutRows
        return rows.indices.contains(index) ? rows[index].displayName : nil
    }

    /// What the Session menu's place items say: the session they go to, or
    /// only their number when there is none there.
    static func sessionItemTitle(at index: Int, name: String?) -> String {
        let place = "Session \(index + 1)"
        guard let name else { return place }
        return "\(place): \(name)"
    }

    /// The digit that goes to a session with ⌘, which its line shows; nil
    /// past the tenth, which has none.
    func sessionKey(of key: AttentionCycler.Key) -> String? {
        guard let index = sessionShortcutRows.firstIndex(where: { $0.key == key }),
              WorkspaceMenu.sessionKeys.indices.contains(index) else { return nil }
        return WorkspaceMenu.sessionKeys[index]
    }

    /// A session takes the keyboard either way.
    func go(to row: WorkspaceRow, raising: Bool) {
        if raising {
            activate(row)
        } else {
            select(row.key, focus: row.embeddedRuntimeSessionID != nil)
        }
    }

    /// A terminal's program ended, or it asked to be closed: its attachment
    /// is over. The session is not: it stays listed until the daemon drops it.
    func terminalEnded(_ view: NSView) {
        if let id = viewTerminals.first(where: { $0.value.view === view })?.key {
            utilityEnded(id)
            return
        }
        guard let id = terminals.first(where: { $0.value.view === view })?.key else { return }
        if closing[id] != nil {
            discardTerminal(id)
            return
        }
        Self.logger.notice("workspace: the terminal for \(id, privacy: .public) ended; detached")
        discardTerminal(id)
        detached.insert(id)
        updateContent(focus: false)
    }

    /// A terminal asked to be closed. A utility view whose program Ghostty
    /// says is still running (`needsConfirm`) asks first when confirmation
    /// is enabled; an attachment's close only detaches, so it is never
    /// asked about.
    func terminalAskedToClose(_ view: NSView, needsConfirm: Bool) {
        guard needsConfirm,
              let id = viewTerminals.first(where: { $0.value.view === view })?.key,
              views.view(id)?.asksBeforeClosing == true else {
            terminalEnded(view)
            return
        }
        withConfirmationPolicy("terminal:" + String(describing: ObjectIdentifier(view))) { [weak self, weak view] required in
            guard let self, let view else { return }
            self.terminalAskedToClose(view, needsConfirm: needsConfirm, confirming: required)
        }
    }

    private func terminalAskedToClose(_ view: NSView, needsConfirm: Bool, confirming: Bool) {
        guard confirming, needsConfirm, let id = viewTerminals.first(where: { $0.value.view === view })?.key,
              let utility = views.view(id), utility.asksBeforeClosing else {
            terminalEnded(view)
            return
        }
        Self.logger.notice("workspace: view \(id, privacy: .public) asked to close with its program running; asking")
        (confirmViewClose ?? askToCloseView)(utility.displayName) { [weak self] confirmed in
            // Still the same terminal: it may have ended meanwhile.
            guard let self, confirmed, self.viewTerminals[id]?.view === view else { return }
            self.utilityEnded(id)
        }
    }

    func askToCloseView(_ title: String, _ answer: @escaping @MainActor (Bool) -> Void) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "Close the view \(title)?"
        alert.informativeText = "Closing the view ends that process. No session is affected."
        alert.addButton(withTitle: "Close View").hasDestructiveAction = true
        alert.addButton(withTitle: "Cancel")
        alert.beginSheetModal(for: window) { response in
            MainActor.assumeIsolated { answer(response == .alertFirstButtonReturn) }
        }
    }

    func askToQuit(_ titles: [String], _ answer: @escaping @MainActor (Bool) -> Void) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = WorkspaceViews.quitQuestion(titles)
        alert.informativeText = "Quitting ends those processes. Sessions live on and are shown again at the next launch."
        alert.addButton(withTitle: "Quit").hasDestructiveAction = true
        alert.addButton(withTitle: "Cancel")
        guard isOpen else {
            // No window to hang a sheet on, and opening one to ask would be
            // saved as open for the next launch: an alert of its own instead,
            // once the app has been told the answer is coming.
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    NSApp.activate()
                    answer(alert.runModal() == .alertFirstButtonReturn)
                }
            }
            return
        }
        alert.beginSheetModal(for: window) { response in
            MainActor.assumeIsolated { answer(response == .alertFirstButtonReturn) }
        }
    }
}

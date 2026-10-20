import AppKit
import os

// The workspace's utility views: the Dashboard, the Overview, and the shells, editors, and pagers beside them.
// Part of WorkspaceWindowController (WorkspaceWindow.swift).
extension WorkspaceWindowController {
    // MARK: Utility views

    /// Shows a utility view in the pane, starting its program if it is not
    /// running. With `focus`, its terminal takes the keyboard.
    @discardableResult
    func showView(_ id: String, focus: Bool = true) -> Bool {
        guard views.view(id) != nil else { return false }
        if otherPlace == .view(id) { return focusOtherPane() }
        reviewSelected = false
        state.select(nil)
        viewSelection = id
        awaited = nil
        leaveRecovery()
        syncSidebarSelection()
        updateContent(focus: focus)
        return true
    }

    /// Does what a view request forwarded by the daemon asks: shows a
    /// session's native view, or opens and shows a utility view. A request
    /// this workspace cannot act on is logged and dropped.
    func open(_ request: WorkspaceViewRequest) {
        if request.kind == WorkspaceViewRequest.sessionKind {
            let id = request.runtimeSessionID
            if let row = state.row(.embedded(id)) {
                Self.logger.notice("workspace: view request for session \(id, privacy: .public)")
                activate(row)
            } else if request.awaited, WorkspaceUtilityView.isRuntimeSessionID(id) {
                Self.logger.notice("workspace: awaiting just-launched session \(id, privacy: .public)")
                awaited = (id, now().addingTimeInterval(Self.awaitLimit))
            } else {
                Self.logger.notice("workspace: view request for \(id, privacy: .public), which is not listed; ignored")
            }
            return
        }
        guard let wanted = WorkspaceUtilityView.from(request) else {
            Self.logger.notice("workspace: view request of kind \(request.kind, privacy: .public) cannot be shown; ignored")
            return
        }
        open(wanted)
    }

    /// Lists and shows a utility view. A pager is read afresh every time it
    /// is asked for; any other view that is already open is shown as it is.
    /// New Shell (⌘T): always one more shell, never the one already open.
    func openNewShell(in directory: String) {
        open(views.nextShell(in: directory))
    }

    func open(_ wanted: WorkspaceUtilityView) {
        let here = place
        let view = views.open(wanted)
        if view.kind == .pager { discardViewTerminal(view.id) }
        if !view.isPermanent, let here, here != .view(view.id) { returnPlaces[view.id] = here }
        Self.logger.notice("workspace: opening \(view.kind.rawValue, privacy: .public) view \(view.id, privacy: .public)")
        reloadSidebar()
        showView(view.id, focus: true)
    }

    /// Closes the utility view the pane shows, ending its program. An opened
    /// view is unlisted; the Dashboard and the Overview stay listed and start
    /// again the next time they are shown. Reports whether the pane was on
    /// a view.
    @discardableResult
    func closeSelectedView() -> Bool {
        guard let id = viewSelection else { return false }
        closeView(id)
        return true
    }

    /// ⌘W closes a utility view or detaches an embedded session terminal.
    /// Reports whether there was anything to close: a Kitty session is not
    /// the workspace's.
    @discardableResult
    func closeShown() -> Bool {
        if reviewSelected {
            guard reviewCanClose() else { return true }
            if isSplit { _ = focusOtherPane(); collapseOtherPane() }
            else { reviewSelected = false; releaseReview(); showView(WorkspaceUtilityView.dashboard.id) }
            return true
        }
        if closeSelectedView() { return true }
        guard let row = state.selectedRow, row.embeddedRuntimeSessionID != nil else { return false }
        detachSession(row.key)
        return true
    }

    /// Releases only this viewer. The live session stays listed and can be reopened.
    func detachSession(_ key: AttentionCycler.Key) {
        guard let row = state.row(key), let id = row.embeddedRuntimeSessionID, closing[id] == nil else { return }
        detached.insert(id)
        discardTerminal(id)
        if place == .session(key), isSplit { _ = focusOtherPane(); collapseOtherPane() }
        reloadSidebar(force: true)
        updateContent(focus: false)
    }

    /// Closes a utility view, ending its program, whether or not the pane
    /// shows it. A shell, editor, or pager whose program still needs it is
    /// confirmed first when the close confirmation setting is enabled.
    func closeView(_ id: String) {
        guard let view = views.view(id) else { return }
        guard confirmBeforeClosing, needsConfirmBeforeClosing(id) else {
            closeViewNow(id)
            return
        }
        let terminal = viewTerminals[id]?.view
        Self.logger.notice("workspace: closing view \(id, privacy: .public) with its program running; asking")
        (confirmViewClose ?? askToCloseView)(view.displayName) { [weak self] confirmed in
            // Still the same terminal: it may have ended meanwhile.
            guard let self, confirmed, self.viewTerminals[id]?.view === terminal else { return }
            self.closeViewNow(id)
        }
    }

    /// Whether closing the view would end a program that should be asked
    /// about first.
    private func needsConfirmBeforeClosing(_ id: String) -> Bool {
        guard let view = views.view(id), view.asksBeforeClosing, let terminal = viewTerminals[id] else { return false }
        return terminal.needsConfirm()
    }

    /// The titles of the views whose programs should be asked about before
    /// the app quits, in sidebar order.
    var viewsNeedingConfirmation: [String] {
        views.all.filter { needsConfirmBeforeClosing($0.id) }.map(\.displayName)
    }

    /// Asks once, naming every view that needs it, before the app quits.
    /// Reports whether it asked; when it did, `answer` gets the operator's
    /// reply, on the main actor. A closed window keeps its views' programs,
    /// so it asks too.
    func confirmQuit(_ answer: @escaping @MainActor (Bool) -> Void) -> Bool {
        let titles = viewsNeedingConfirmation
        guard !titles.isEmpty else { return false }
        Self.logger.notice("workspace: quitting with \(titles.count) view programs running; asking")
        (confirmQuitWithViews ?? askToQuit)(titles, answer)
        return true
    }

    private func closeViewNow(_ id: String) {
        guard let view = views.view(id) else { return }
        Self.logger.notice("workspace: closing view \(id, privacy: .public)")
        discardViewTerminal(id)
        endedViews.remove(id)
        if !view.isPermanent {
            retire(view)
        } else if viewSelection == id || otherPlace == .view(id) {
            // The pane stays on it and says it closed, with Reopen, as when
            // its program quits on its own.
            endedViews.insert(id)
            updateContent(focus: false)
        }
    }

    /// What ⌘W does where the pane is now, as its menu item says it.
    var closeShownTitle: String {
        if reviewSelected || viewSelection != nil { return Self.closeViewTitle }
        if state.selectedRow?.embeddedRuntimeSessionID != nil { return Self.detachSessionTitle }
        return Self.closeTitle
    }

    static let closeTitle = "Close"

    static let closeViewTitle = "Close View"

    static let endSessionTitle = "End Session…"

    static let detachSessionTitle = "Detach Terminal"

    /// Ends an embedded session's agent, asking first by default: an idle agent's
    /// conversation is kept, but the agent is not. The row stays until a live
    /// snapshot drops it; if the pane was on the session it goes to the
    /// Dashboard.
    func closeSession(_ key: AttentionCycler.Key) {
        guard let row = state.row(key), let id = row.embeddedRuntimeSessionID, closing[id] == nil else { return }
        guard confirmBeforeClosing else {
            endSession(row, id)
            return
        }
        (confirmClose ?? askToClose)(row) { [weak self] confirmed in
            guard let self, confirmed, self.closing[id] == nil, let row = self.state.row(key) else { return }
            self.endSession(row, id)
        }
    }

    /// What ending a session keeps. It names Past Sessions, where the pane
    /// sends the operator once an agent has stopped (`resumeTitle`).
    static let conversationKept = "The conversation stays on disk and can be resumed from Past Sessions."

    private func askToClose(_ row: WorkspaceRow, _ answer: @escaping @MainActor (Bool) -> Void) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = "End \(row.displayName)?"
        if row.attention || row.working {
            alert.informativeText = (row.attention ? "Its agent is waiting on you." : "Its agent is working.")
                + " Ending it loses the work in progress. \(Self.conversationKept)"
        } else {
            alert.informativeText = "Its agent is idle and stops when the session ends. \(Self.conversationKept)"
        }
        alert.addButton(withTitle: "End Session").hasDestructiveAction = true
        alert.addButton(withTitle: "Cancel")
        alert.beginSheetModal(for: window) { response in
            MainActor.assumeIsolated { answer(response == .alertFirstButtonReturn) }
        }
    }

    /// Says why a session could not be ended: an alert on the window while
    /// it is open, or on its own when it was closed meanwhile (`showError`).
    private func sayCloseFailed(_ name: String, _ message: String) {
        showError(title: "\(name) could not be ended", message: message)
    }

    private func endSession(_ row: WorkspaceRow, _ id: String) {
        let name = row.displayName
        Self.logger.notice("workspace: closing session \(id, privacy: .public)")
        closing[id] = (place == .session(row.key), false)
        // Its terminal goes first: the attachment ending with the agent is
        // not a detach to report.
        discardTerminal(id)
        detached.remove(id)
        reloadSidebar(force: true)
        updateContent(focus: false)
        delegate?.closeSession(runtimeSessionID: id) { [weak self] problem in
            guard let self, let wasShown = self.closing[id]?.wasShown else { return }
            if let problem {
                Self.logger.notice("workspace: could not close \(id, privacy: .public): \(problem, privacy: .public)")
                self.closing[id] = nil
                self.reloadSidebar(force: true)
                self.updateContent(focus: false, retry: true)
                (self.reportCloseProblem ?? self.sayCloseFailed)(name, problem)
                return
            }
            Self.logger.notice("workspace: closed session \(id, privacy: .public)")
            if self.state.current, !self.state.embeddedRuntimeSessionIDs.contains(id) {
                self.closing[id] = nil
            } else {
                self.closing[id]?.answered = true
            }
            // Still where the operator left it: on the row, or on nothing
            // because a snapshot already dropped the row.
            let stayed = self.place == .session(row.key) || (self.place == nil && self.restoring == nil)
            if wasShown, self.isOpen, stayed {
                self.showView(WorkspaceUtilityView.dashboard.id, focus: true)
            }
        }
    }

    /// Opens a shell where the session the pane is on is working, listed
    /// right below that session, and gives it the keyboard. From one of a
    /// session's terminals it opens another beside the same session. A Kitty
    /// session has its terminals in Kitty, so there is nothing to do for one.
    /// Reports whether it opened one.
    @discardableResult
    func openTerminalBesideSession() -> Bool {
        guard let id = sessionForTerminalBeside else {
            Self.logger.notice("workspace: no embedded session is shown; no terminal opened beside one")
            return false
        }
        open(views.openTerminal(beside: id))
        return true
    }

    /// Whether ⌘↩ has an embedded session to open a terminal beside.
    var canOpenTerminalBesideSession: Bool { sessionForTerminalBeside != nil }

    private var sessionForTerminalBeside: String? {
        let id = views.view(viewSelection)?.besideRuntimeSessionID ?? state.selectedRow?.embeddedRuntimeSessionID
        guard let id, state.row(.embedded(id)) != nil else { return nil }
        return id
    }

    static let terminalNeedsEmbedded = "A terminal opens below a session the workspace embeds; a Kitty session has its terminals in Kitty."

    /// The session the pane is on, or the one the terminal, pager, or picker
    /// it shows is about: what the session function keys act on.
    var shownSessionRow: WorkspaceRow? {
        if let view = views.view(viewSelection) {
            return view.aboutRuntimeSessionID.flatMap { state.row(.embedded($0)) }
        }
        return state.selectedRow
    }
}

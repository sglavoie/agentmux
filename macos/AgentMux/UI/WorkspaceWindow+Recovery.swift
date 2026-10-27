import AppKit
import os

// Coming back to where the workspace was, and going to a session or view by name.
// Part of WorkspaceWindowController (WorkspaceWindow.swift).
extension WorkspaceWindowController {
    // MARK: Recovery

    func restoreLayout(_ layout: WorkspaceRestoration.Layout, conversation: ConversationIdentity? = nil) {
        var layout = layout
        if layout.other == layout.active { layout.other = nil }
        pendingLayout = layout
        layoutConversation = conversation
        layoutChecking = false
        layoutGeneration += 1
        continueRestoringLayout()
    }

    func continueRestoringLayout() {
        guard let layout = pendingLayout, !layoutChecking, state.current, isOpen else { return }
        // The active pane goes through restore(_:), including its ownership
        // check. Check the inactive session before allowing it to make a terminal.
        if case let .session(encoded)? = layout.other,
           case let .session(id)? = WorkspaceRestoration.Place(encoded: encoded),
           state.row(.embedded(id)) != nil {
            layoutChecking = true
            let generation = layoutGeneration
            delegate?.sessionStatus(runtimeSessionID: id) { [weak self] status in
                guard let self, generation == self.layoutGeneration, self.pendingLayout != nil else { return }
                self.layoutChecking = false
                if status != .live(clients: 0) { self.detached.insert(id) }
                self.applyLayout(layout)
            }
        } else { applyLayout(layout) }
    }

    private func applyLayout(_ layout: WorkspaceRestoration.Layout) {
        applyingLayout = true
        pendingLayout = nil
        otherPlace = layout.other.flatMap(restorePane)
        activeOnRight = otherPlace != nil && layout.activeOnRight
        restoringFraction = layout.fraction
        if case let .session(encoded) = layout.active, let place = WorkspaceRestoration.Place(encoded: encoded) {
            restore(place, conversation: layoutConversation)
        } else {
            let active = restorePane(layout.active) ?? .view(WorkspaceUtilityView.dashboard.id)
            state.select(nil)
            viewSelection = nil
            reviewSelected = false
            switch active {
            case .review: reviewSelected = true
            case let .view(id): viewSelection = id
            case let .session(key): state.select(key)
            }
        }
        applyingLayout = false
        reloadSidebar()
        updateContent(focus: true)
    }

    private func restorePane(_ pane: WorkspaceRestoration.Pane) -> Place? {
        switch pane {
        case let .session(encoded):
            return WorkspaceRestoration.Place(encoded: encoded)?.sessionKey.map(Place.session)
        case let .utility(view):
            guard !view.isOverlay else { return nil }
            if let id = view.besideRuntimeSessionID {
                guard state.row(.embedded(id)) != nil, closing[id] == nil else { return nil }
            }
            views.open(view)
            if !view.isPermanent { endedViews.insert(view.id) }
            return .view(view.id)
        case let .review(directory):
            guard let review = makeRestoredReview?(directory) else { return nil }
            reviewView = review.view
            reviewCanClose = review.canClose
            reviewClosed = review.close
            reviewDirectory = review.directory
            return .review
        }
    }

    private func savedPane(_ place: Place?) -> WorkspaceRestoration.Pane? {
        switch place {
        case let .session(key)?:
            return .session((key.runtimeSessionID.isEmpty
                ? WorkspaceRestoration.Place.kitty(socket: key.socket, windowID: key.windowID)
                : .session(runtimeSessionID: key.runtimeSessionID)).encoded)
        case let .view(id)?: return views.view(id).flatMap { $0.isOverlay ? nil : .utility($0) }
        case .review?: return reviewDirectory().map(WorkspaceRestoration.Pane.review)
        case nil: return nil
        }
    }

    func reportLayout() {
        guard isOpen, pendingLayout == nil, !applyingLayout else { return }
        let active = restoring.map { WorkspaceRestoration.Pane.session($0.encoded) }
            ?? savedPane(place)
            ?? gone.map { .session(WorkspaceRestoration.Place.session(runtimeSessionID: $0.runtimeSessionID).encoded) }
        if let active { onLayoutChanged(.init(active: active, other: savedPane(otherPlace), activeOnRight: activeOnRight, fraction: paneLayout.fraction)) }
    }

    /// Comes back to where the workspace was when the app last ran. A view is
    /// shown at once. A session is shown once a live snapshot lists it and
    /// amx confirms nobody else attached to it meanwhile; until then the pane
    /// says it is waiting. A session that is not running any more is reported
    /// as exited or lost, with the conversation saved for it when there is
    /// one. Nothing is ever started.
    func restore(_ place: WorkspaceRestoration.Place, conversation: ConversationIdentity? = nil) {
        Self.logger.notice("workspace: restoring \(place.encoded, privacy: .public)")
        switch place {
        case let .view(id):
            if !showView(id, focus: true) { showView(WorkspaceUtilityView.dashboard.id) }
        case .session, .kitty:
            state.select(nil)
            viewSelection = nil
            gone = nil
            restoring = place
            reportedPlace = place
            shownConversation = nil
            if case let .session(id) = place, let conversation {
                shownConversation = (id, conversation)
            }
            reportedConversation = shownConversation?.conversation
            syncSidebarSelection()
            continueRestoring()
            updateContent(focus: false)
        }
    }

    /// Takes the next step of a restore once the feed is live.
    func continueRestoring() {
        guard let place = restoring, !restoreChecking, state.current, let key = place.sessionKey else { return }
        guard case let .session(id) = place else {
            // A Kitty session is selected, never raised: coming back must not
            // pull another app forward.
            restoring = nil
            if state.row(key) != nil {
                Self.logger.notice("workspace: restored the selection of Kitty window \(key.windowID)")
                select(key)
            } else {
                Self.logger.notice("workspace: Kitty window \(key.windowID) is no longer listed; nothing restored")
                reportPlace()
            }
            return
        }
        let listed = state.row(key) != nil
        let name = state.row(key)?.displayName ?? "The session this window was on"
        restoring = nil
        if !listed {
            Self.logger.notice("workspace: \(id, privacy: .public) is not listed after the restart; asking what became of it")
            sessionGone(id, name: name)
            return
        }
        // Listed, so running. Whether this window may take it is the server's
        // to say: a terminal that attached while the app was away owns it now.
        statusQuestion += 1
        let question = statusQuestion
        restoring = place
        restoreChecking = true
        delegate?.sessionStatus(runtimeSessionID: id) { [weak self] status in
            guard let self, question == self.statusQuestion, self.restoring == place else { return }
            self.restoring = nil
            self.restoreChecking = false
            guard self.state.row(key) != nil else {
                self.sessionGone(id, name: name)
                self.updateContent(focus: false)
                return
            }
            switch status {
            case let .live(clients) where clients > 0:
                Self.logger.notice("workspace: \(id, privacy: .public) is attached elsewhere (\(clients) terminal(s)); restored detached")
                self.detached.insert(id)
                self.select(key)
            case .live:
                Self.logger.notice("workspace: restored \(id, privacy: .public); reattaching its terminal")
                self.select(key, focus: true)
            case .exited, .lost, .ended, .unknownSession:
                self.sessionGone(id, name: name, known: status)
                self.updateContent(focus: false)
            case let .unknown(why):
                // amx could not say. The session is listed, so it is shown,
                // but its terminal waits to be asked for.
                Self.logger.notice("workspace: could not check \(id, privacy: .public) (\(why, privacy: .public)); restored detached")
                self.detached.insert(id)
                self.select(key)
            }
        }
    }

    /// The session the pane was on stopped being listed: asks what became of
    /// it and says so in the pane, until the operator goes somewhere else.
    func sessionGone(_ id: String, name: String, known: WorkspaceSessionStatus? = nil) {
        guard isOpen else { return }
        discardTerminal(id)
        detached.remove(id)
        statusQuestion += 1
        let question = statusQuestion
        gone = (id, name, known, shownConversation?.runtimeSessionID == id ? shownConversation?.conversation : nil)
        if let known {
            Self.logger.notice("workspace: \(id, privacy: .public) is not running: \(String(describing: known), privacy: .public); nothing is relaunched")
            reportPlace()
            return
        }
        delegate?.sessionStatus(runtimeSessionID: id) { [weak self] status in
            guard let self, question == self.statusQuestion, self.gone?.runtimeSessionID == id else { return }
            Self.logger.notice("workspace: \(id, privacy: .public) is not running: \(String(describing: status), privacy: .public); nothing is relaunched")
            self.gone?.status = status
            self.updateContent(focus: false)
        }
        reportPlace()
    }

    /// Whether the pane is somewhere: on a session or a view, coming back to
    /// one, or saying what became of one. Opening the window on nothing in
    /// particular must leave such a place alone.
    var hasPlace: Bool {
        reviewSelected || state.selection != nil || viewSelection != nil || restoring != nil || gone != nil
    }

    /// Lands the pane on the Dashboard when it has no place, as opening the
    /// window on nothing in particular does.
    func showDashboardUnlessPlaced() {
        guard !hasPlace else { return }
        showView(WorkspaceUtilityView.dashboard.id)
    }

    /// The operator went somewhere: whatever the pane was waiting to say or
    /// to come back to is over.
    func leaveRecovery() {
        if !applyingLayout {
            pendingLayout = nil
            layoutChecking = false
            layoutGeneration += 1
        }
        statusQuestion += 1
        restoring = nil
        restoreChecking = false
        gone = nil
    }

    /// Tells the app where the pane is, when that changed. While a restore
    /// is pending the place it is heading for stays the reported one.
    func reportPlace() {
        guard isOpen else { return }
        reportLayout()
        let now: WorkspaceRestoration.Place?
        if let restoring {
            now = restoring
        } else if reviewSelected {
            now = reportedPlace
        } else if let viewSelection {
            now = views.view(viewSelection)?.isPermanent == true ? .view(id: viewSelection) : reportedPlace
        } else if let key = state.selection {
            now = key.runtimeSessionID.isEmpty
                ? .kitty(socket: key.socket, windowID: key.windowID)
                : .session(runtimeSessionID: key.runtimeSessionID)
        } else {
            now = nil
        }
        reportConversation(of: now)
        guard now != reportedPlace else { return }
        reportedPlace = now
        delegate?.workspacePlaceChanged(now)
    }

    /// Tells the app the conversation of the session the pane is on, when
    /// that changed. A session whose row stops naming one keeps the last it
    /// had: an agent between two conversations is still that agent.
    private func reportConversation(of place: WorkspaceRestoration.Place?) {
        if case let .session(id)? = place {
            if let conversation = state.row(.embedded(id))?.conversation {
                shownConversation = (id, conversation)
            } else if shownConversation?.runtimeSessionID != id {
                shownConversation = nil
            }
        } else if gone == nil {
            shownConversation = nil
        }
        let now = place == nil ? nil : shownConversation?.conversation
        guard now != reportedConversation else { return }
        reportedConversation = now
        delegate?.workspaceConversationChanged(now)
    }

    /// Selects a listed session and shows what there is to show for it. With
    /// `focus`, keyboard focus moves into its terminal when it has one.
    @discardableResult
    func select(_ key: AttentionCycler.Key?, focus: Bool = false) -> Bool {
        if let key, otherPlace == .session(key) { return focusOtherPane() }
        guard state.select(key) else { return false }
        reviewSelected = false
        viewSelection = nil
        awaited = nil
        leaveRecovery()
        syncSidebarSelection()
        updateContent(focus: focus)
        return true
    }

    /// Focuses a row the way a click does: an embedded session takes the
    /// keyboard here, a Kitty session comes forward in Kitty. With
    /// `reattaching`, which only the operator's own click or Return on the
    /// row asks for, a session whose terminal was detached is attached
    /// again, taking it from whichever terminal has it.
    func activate(_ row: WorkspaceRow, reattaching: Bool = false) {
        switch row.target {
        case let .embedded(id):
            Self.logger.notice("workspace: focus embedded \(id, privacy: .public)")
            if reattaching, detached.remove(id) != nil {
                Self.logger.notice("workspace: reattaching \(id, privacy: .public)")
            }
            select(row.key, focus: true)
        case let .kitty(_, windowID):
            Self.logger.notice("workspace: focus Kitty window \(windowID) externally")
            select(row.key)
            delegate?.focusExternally(row)
        }
    }

    /// Shows the window on an embedded session and gives it the keyboard:
    /// what the menu bar, the next-attention shortcut, and a banner do.
    func focus(runtimeSessionID: String) {
        show()
        guard let row = state.row(.embedded(runtimeSessionID)) else {
            Self.logger.notice("workspace: \(runtimeSessionID, privacy: .public) is not listed; nothing to focus")
            return
        }
        activate(row)
    }

    // MARK: Go to

    /// Whether the Go to… list is up.
    var isSwitching: Bool { !switcher.isHidden }

    /// ⌘P: lists every session and view to go to by name, or puts the list
    /// away when it is up. It goes only where a click on the sidebar would.
    func toggleSwitcher(actions: Bool = false) {
        guard isOpen else { return }
        if isSwitching {
            if actions { switcher.enter("> "); return }
            return closeSwitcher(restoring: true)
        }
        switcherReturn = window.firstResponder
        switcher.show(switcherEntries())
        if actions { switcher.enter("> ") }
        switcher.isHidden = false
        window.makeFirstResponder(switcher.field)
        switcher.placeCaretAtEnd()
    }

    func switcherEntries() -> [WorkspaceSwitcher.Entry] {
        let previous: WorkspaceSwitcher.Entry.Place?
        switch recent.before {
        case let .session(key)?: previous = .session(key)
        case let .view(id)?: previous = .view(id)
        case .review?, nil: previous = nil
        }
        return WorkspaceSwitcher.entries(rows: state.rows, views: views.all, names: sessionNames, previous: previous)
            + (delegate?.workspaceCommands ?? []).map { entry in
                var entry = entry
                if case let .command(command) = entry.place {
                    switch command {
                    case .newTerminal, .splitShell, .splitChanges, .history, .changes:
                        if shownSessionRow != nil { entry.relevance = 10 }
                    case .focusOtherPane, .swapPanes, .collapsePane:
                        if isSplit { entry.relevance = 10 }
                    default: break
                    }
                }
                return entry
            }
    }

    func refreshCommands() {
        if isSwitching { switcher.update(switcherEntries()) }
        if case .empty = content { fillPlaceholder(content) }
    }

    func focusWaiting() {
        if window.firstResponder !== inspector { waitingReturn = window.firstResponder }
        setInspectorCollapsed(false)
        setInspectorTab(.waiting)
        inspector.focusReader()
    }

    /// Puts the list away. With `restoring`, the keyboard goes back to what
    /// had it; without, it stays where a click has just put it.
    func closeSwitcher(restoring: Bool) {
        guard isSwitching else { return }
        switcher.isHidden = true
        let back = switcherReturn
        switcherReturn = nil
        if restoring, let back, (back as? NSView)?.isHiddenOrHasHiddenAncestor != true {
            window.makeFirstResponder(back)
        }
    }

    /// Goes to a place of the list as a click on its sidebar line does.
    func go(to entry: WorkspaceSwitcher.Entry) {
        closeSwitcher(restoring: true)
        switch entry.place {
        case let .session(key):
            if let row = state.row(key) { activate(row, reattaching: true) }
        case let .view(id):
            showView(id, focus: true)
        case let .command(command):
            delegate?.workspacePerform(command)
        }
    }
}

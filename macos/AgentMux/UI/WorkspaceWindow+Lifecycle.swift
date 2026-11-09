import AppKit

extension WorkspaceWindowController {
    /// The runtime sessions that have a terminal right now.
    var attachedRuntimeSessionIDs: Set<String> { Set(terminals.keys) }

    func terminalView(for runtimeSessionID: String) -> NSView? { terminals[runtimeSessionID]?.view }

    /// The utility views that have a terminal right now, by id.
    var runningViewIDs: Set<String> { Set(viewTerminals.keys) }

    func utilityTerminalView(for id: String) -> NSView? { viewTerminals[id]?.view }

    // MARK: Showing

    /// Brings the window forward, opening it if it is closed.
    func show() {
        if !isOpen {
            isOpen = true
            Self.logger.notice("workspace: opened")
            delegate?.workspaceVisibilityChanged(true)
            comeBackToSelection()
        }
        startAgeTimer()
        NSApp.activate()
        window.makeKeyAndOrderFront(nil)
        refresh()
        welcome.showWelcomeOnce()
        workspaceBecameActive()
    }

    /// Opens the window without activating the app or ordering it in, for
    /// tests: everything else behaves as it does on screen.
    func openForTesting() {
        _ = window
        if !isOpen {
            isOpen = true
            comeBackToSelection()
        }
        startAgeTimer()
        refresh()
    }

    /// Reopening comes back to the session the window was on the way a
    /// launch does: a terminal that attached to it while the window was
    /// closed keeps it, so it is attached again only once amx says nobody
    /// else has it.
    private func comeBackToSelection() {
        guard pendingLayout == nil else { return }
        if let context = groupContexts[WorkspaceRestoration.contextKey(selectedGroup)] {
            restoreLayout(context.layout, conversation: context.conversation.flatMap(ConversationIdentity.init(encoded:)))
            return
        }
        guard let key = state.selection, viewSelection == nil, !key.runtimeSessionID.isEmpty else { return }
        let id = key.runtimeSessionID
        let conversation = state.row(key)?.conversation
            ?? (shownConversation?.runtimeSessionID == id ? shownConversation?.conversation : nil)
        restore(.session(runtimeSessionID: id), conversation: conversation)
    }

    private func startAgeTimer() {
        guard ageTimer == nil else { return }
        let timer = Timer(timeInterval: Self.ageRefreshInterval, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.refreshAges() }
        }
        timer.tolerance = Self.ageRefreshInterval / 6
        RunLoop.main.add(timer, forMode: .common)
        ageTimer = timer
    }

    /// Says the ages again, as a snapshot would: only the lines whose text
    /// changed are drawn again, so the selection, the pointer's ×, and the
    /// keyboard stay where they are.
    func refreshAges() {
        guard isOpen else { return }
        refresh()
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool { reviewCanClose() }

    func windowWillClose(_ notification: Notification) {
        guard isOpen else { return }
        workspaceResignedActive()
        returnSummary.close()
        peekPopover.close()
        welcome.close()
        inspector.recap.close()
        inspector.threads.close()
        paneLayout.active.showContext(nil, canAnswer: false)
        paneLayout.other.showContext(nil, canAnswer: false)
        reportLayout()
        pendingLayout = nil
        layoutGeneration += 1
        isOpen = false
        if let paneClickMonitor { NSEvent.removeMonitor(paneClickMonitor) }
        paneClickMonitor = nil
        if reviewSelected {
            switch otherPlace {
            case let .session(key)?: state.select(key); viewSelection = nil
            case let .view(id)?: state.select(nil); viewSelection = id
            default: break
            }
        }
        otherPlace = nil
        otherContent = .empty
        activeOnRight = false
        reviewView?.removeFromSuperview()
        reviewView = nil
        reviewSelected = false
        reviewClosed()
        reviewClosed = {}
        paneLayout.arrange(split: false, activeOnRight: false)
        ageTimer?.invalidate()
        ageTimer = nil
        Self.logger.notice("workspace: closed, detaching \(self.terminals.count) terminal(s)")
        for id in Array(terminals.keys) { discardTerminal(id) }
        detached.removeAll()
        // The utility views keep their programs: an editor with unsaved work
        // or a running shell is not an attachment that can simply be remade.
        for terminal in viewTerminals.values { terminal.view.isHidden = true }
        closeSwitcher(restoring: false)
        inspector.discardContext()
        awaited = nil
        leaveRecovery()
        content = .empty
        reportedShowing = nil
        reportedOtherShowing = nil
        delegate?.workspaceVisibilityChanged(false)
    }

    /// Kept for the next launch. A window closed while in full screen, or
    /// one the app is quitting with, keeps what it had: neither is the
    /// operator leaving full screen.
    func windowDidEnterFullScreen(_ notification: Notification) {
        guard isOpen else { return }
        delegate?.workspaceFullScreenChanged(true)
    }

    func windowDidExitFullScreen(_ notification: Notification) {
        guard isOpen else { return }
        delegate?.workspaceFullScreenChanged(false)
    }

    /// At launch, for a window that was in full screen when the app last ran.
    func enterFullScreen() {
        guard isOpen, !window.styleMask.contains(.fullScreen) else { return }
        window.toggleFullScreen(nil)
    }

    func windowDidBecomeKey(_ notification: Notification) {
        guard isOpen else { return }
        reportedShowing = showing
        if let showing { delegate?.workspaceShowing(showing) }
        if let otherShowing { delegate?.workspaceShowing(otherShowing) }
    }

    // MARK: State

    /// Brings the sidebar and the pane in line with the store.
    func refresh() {
        guard let store = delegate?.store else { return }
        var live: [WorkspaceGroup]?
        if store.feed == .live, store.snapshot != nil {
            live = WorkspaceSidebar.groups(store.sessions, now: now(), glyphs: glyphs, order: order, grouping: sidebarGrouping, sorting: sidebarSort)
        }
        let wasCurrent = state.current
        let previousRows = state.rows
        let previous = state.selection
        let previousName = state.selectedRow?.displayName
        let lost = state.apply(live)
        if state.current != wasCurrent {
            Self.logger.notice("workspace: rows \(self.state.current ? "current" : "held from the last snapshot", privacy: .public) (\(self.state.rows.count) session(s), selection \(Self.describe(self.state.selection), privacy: .public))")
        }
        if lost {
            Self.logger.notice("workspace: selected session \(Self.describe(previous), privacy: .public) is no longer listed")
            // A session the operator closed is not reported as gone.
            if let previous, !previous.runtimeSessionID.isEmpty, closing[previous.runtimeSessionID] == nil {
                sessionGone(previous.runtimeSessionID, name: previousName ?? "This session")
            }
        }
        if state.current {
            // Only a live snapshot says a session is gone, and what was kept
            // of it in the orders dragged into with it.
            let kept = order
            order.forget(notAmong: state.rows)
            if order != kept { delegate?.workspaceOrderChanged(order) }
            let listed = state.embeddedRuntimeSessionIDs
            for id in terminals.keys where !listed.contains(id) {
                Self.logger.notice("workspace: \(id, privacy: .public) is no longer listed; detaching its terminal")
                discardTerminal(id)
            }
            closeTerminals(beside: Set(views.opened.compactMap(\.besideRuntimeSessionID)).subtracting(listed))
            detached.formIntersection(listed)
            closing = closing.filter { !$0.value.answered || listed.contains($0.key) }
        }
        returnSummary.observe(rows: state.rows, current: state.current, active: isOpen && NSApp.isActive, now: now())
        refreshSessionPeek()
        inspector.reconcileSentAnswers(allWaitingRows, current: state.current)
        arrangeNewSessionBelow()
        reloadSidebar()
        if state.current { flashStateChanges(from: previousRows) }
        welcome.pendingAnswers = inspector.sentAnswers.count
        welcome.observe(waiting: AttentionCycler.ordered(store.managedSessions).count,
                        working: state.rows.filter(\.working).count,
                        live: state.current, celebrate: isOpen && NSApp.isActive)
        let notice = WorkspaceSidebar.feedNotice(store.feed)
        feedLabel.stringValue = notice ?? ""
        feedLabel.isHidden = notice == nil
        feedLabel.toolTip = notice
        let action = WorkspaceSidebar.daemonAction(store.feed)
        feedButton.title = action?.title ?? ""
        feedButton.isHidden = action == nil
        if let awaited {
            if let row = state.row(.embedded(awaited.runtimeSessionID)) {
                Self.logger.notice("workspace: awaited session \(awaited.runtimeSessionID, privacy: .public) is listed; showing it")
                self.awaited = nil
                activate(row)
                revealInSidebar()
                return
            }
            if now() >= awaited.until {
                Self.logger.notice("workspace: awaited session \(awaited.runtimeSessionID, privacy: .public) was never listed; giving up")
                self.awaited = nil
            }
        }
        continueRestoring()
        continueChoosingGroupPlace()
        continueRestoringLayout()
        updateContent(focus: false)
    }
}

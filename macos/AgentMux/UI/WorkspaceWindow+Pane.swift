import AppKit
import os

// The pane: what it shows, its split, and the review beside an agent.
// Part of WorkspaceWindowController (WorkspaceWindow.swift).
extension WorkspaceWindowController {
    // MARK: Pane

    /// Preserve the current place on the left and select a different live
    /// session on the right. The original terminal remains attached exactly once.
    @discardableResult
    func split(with key: AttentionCycler.Key) -> Bool {
        guard isOpen, state.row(key)?.embeddedRuntimeSessionID != nil,
              place != .session(key), prepareSplit() else { return false }
        return select(key, focus: true)
    }

    @discardableResult
    func splitWithShell() -> Bool {
        guard canOpenTerminalBesideSession, prepareSplit() else { return false }
        return openTerminalBesideSession()
    }

    @discardableResult
    func splitWithReview(_ view: NSView, canClose: @escaping () -> Bool, onClose: @escaping () -> Void) -> Bool {
        guard prepareSplit() else { return false }
        releaseReview()
        reviewView = view
        reviewCanClose = canClose
        reviewClosed = onClose
        reviewSelected = true
        state.select(nil)
        viewSelection = nil
        leaveRecovery()
        syncSidebarSelection()
        updateContent(focus: true)
        return true
    }

    @discardableResult
    func focusReview() -> Bool {
        guard isOpen, reviewView != nil else { return false }
        if otherPlace == .review { return focusOtherPane() }
        reviewSelected = true
        state.select(nil)
        viewSelection = nil
        leaveRecovery()
        syncSidebarSelection()
        updateContent(focus: true)
        return true
    }

    private func prepareSplit() -> Bool {
        guard isOpen, let place, place != .review, reviewCanClose() else { return false }
        if otherPlace == .review { releaseReview() }
        otherPlace = place
        otherContent = content
        activeOnRight = true
        return true
    }

    /// Clicking an inactive terminal promotes its container and selection
    /// together. The physical terminal stays under the pointer.
    @discardableResult
    func focusOtherPane() -> Bool {
        guard isOpen, let next = otherPlace else { return false }
        let previous = place
        let previousContent = content
        content = otherContent
        otherPlace = previous
        otherContent = previousContent
        activeOnRight.toggle()
        reviewSelected = false
        viewSelection = nil
        state.select(nil)
        leaveRecovery()
        switch next {
        case let .session(key):
            if !state.select(key), !key.runtimeSessionID.isEmpty {
                sessionGone(key.runtimeSessionID, name: key.runtimeSessionID)
            }
        case let .view(id): viewSelection = id
        case .review: reviewSelected = true
        }
        syncSidebarSelection()
        updateContent(focus: true)
        return true
    }

    func swapPanes() {
        guard isSplit else { return }
        activeOnRight.toggle()
        paneLayout.arrange(split: true, activeOnRight: activeOnRight)
        reportLayout()
    }

    /// Hiding a pane never ends its agent or shell. Utility views stay in the
    /// sidebar, and a hidden session keeps its existing workspace attachment.
    func collapseOtherPane() {
        guard isSplit else { return }
        if otherPlace == .review {
            guard reviewCanClose() else { return }
            releaseReview()
        }
        otherPlace = nil
        otherContent = .empty
        activeOnRight = false
        updateContent(focus: true)
    }

    func releaseReview() {
        reviewView?.removeFromSuperview()
        reviewView = nil
        reviewClosed()
        reviewClosed = {}
        reviewCanClose = { true }
    }

    func fitPanes() {
        let middle: CGFloat = isSplit ? 2 * WorkspacePaneLayout.minimumPaneWidth + 1 : 320
        if contentItem?.minimumThickness != middle { contentItem?.minimumThickness = middle }
        let width: CGFloat
        if isSplit {
            let left = sidebarItem.flatMap { $0.isCollapsed ? nil : max(180, $0.viewController.view.frame.width) } ?? 0
            let right = inspectorItem.flatMap { $0.isCollapsed ? nil : max(220, $0.viewController.view.frame.width) } ?? 0
            width = middle + left + right + 2
        } else { width = Self.minimumSize.width }
        let minimum = NSSize(width: width, height: Self.minimumSize.height)
        if window.minSize != minimum { window.minSize = minimum }
        if isSplit, window.frame.width < width {
            var frame = window.frame
            frame.size.width = width
            window.setFrame(frame, display: true)
        }
    }

    private func wantedOtherContent() -> Content {
        switch otherPlace {
        case let .session(key)?:
            guard let row = state.row(key) else { return .gone(runtimeSessionID: key.runtimeSessionID, nil) }
            return wantedSessionContent(row, previous: otherContent, retry: false)
        case let .view(id)?:
            guard let view = views.view(id) else { return .empty }
            return wantedViewContent(view, previous: otherContent, retry: false)
        case .review?: return .review
        case nil: return .empty
        }
    }

    func visibleView(_ content: Content) -> NSView? {
        switch content {
        case let .terminal(id): return terminals[id]?.view
        case let .view(id): return viewTerminals[id]?.view
        case .review: return reviewView
        default: return nil
        }
    }

    private func placeView(_ view: NSView?, in container: NSView, below placeholder: NSView) {
        guard let view else { return }
        if view.superview !== container {
            view.removeFromSuperview()
            container.addSubview(view, positioned: .below, relativeTo: placeholder)
        }
        view.frame = container.bounds
        view.autoresizingMask = [.width, .height]
        view.isHidden = false
    }

    func title(for place: Place?) -> String {
        switch place {
        case let .session(key)?: return state.row(key)?.displayName ?? "Session ended"
        case let .view(id)?: return views.view(id)?.displayName ?? "View closed"
        case .review?: return "Checkout Changes"
        case nil: return "Workspace"
        }
    }

    private var otherExplanation: String {
        otherContent.otherPaneExplanation
    }

    var otherShowing: TerminalNotice.Origin? {
        guard isOpen, isSplit else { return nil }
        return otherContent.terminalNoticeOrigin
    }

    private func installPaneClickMonitor() {
        guard paneClickMonitor == nil else { return }
        paneClickMonitor = NSEvent.addLocalMonitorForEvents(matching: [.leftMouseDown, .rightMouseDown]) { [weak self] event in
            MainActor.assumeIsolated {
                guard let self, self.isOpen, self.isSplit, event.window === self.window else { return }
                let point = self.otherPane.convert(event.locationInWindow, from: nil)
                if self.otherPane.bounds.contains(point) { _ = self.focusOtherPane() }
            }
            return event
        }
    }

    private func wantedViewContent(_ view: WorkspaceUtilityView, previous: Content, retry: Bool) -> Content {
        let id = view.id
        if endedViews.contains(id) { return .viewEnded(id: id) }
        if viewTerminals[id] != nil {
            return rendererFailedViews.contains(id) ? .viewRendererFailed(id: id) : .view(id: id)
        }
        if !retry, case let .viewUnavailable(refused, _) = previous, refused == id { return previous }
        guard let delegate else { return .viewUnavailable(id: id, "The app is shutting down.") }
        switch delegate.makeUtilityTerminal(view) {
        case let .success(terminal):
            Self.logger.notice("workspace: started view \(id, privacy: .public)")
            viewTerminals[id] = terminal
            (terminal.view as? Ghostty.SurfaceView)?.hostName = view.displayName
            terminal.view.frame = pane.bounds
            terminal.view.autoresizingMask = [.width, .height]
            terminal.view.isHidden = true
            pane.addSubview(terminal.view, positioned: .below, relativeTo: placeholder)
            return .view(id: id)
        case let .failure(problem):
            return .viewUnavailable(id: id, problem.message)
        }
    }

    private func wantedContent(retry: Bool) -> Content {
        if reviewSelected { return .review }
        if let view = views.view(viewSelection) { return wantedViewContent(view, previous: content, retry: retry) }
        guard let row = state.selectedRow else {
            if let gone { return .gone(runtimeSessionID: gone.runtimeSessionID, gone.status) }
            if case let .session(id)? = restoring { return .restoring(runtimeSessionID: id) }
            return .empty
        }
        return wantedSessionContent(row, previous: content, retry: retry)
    }

    private func wantedSessionContent(_ row: WorkspaceRow, previous: Content, retry: Bool) -> Content {
        guard let id = row.embeddedRuntimeSessionID else { return .external(row.key) }
        if closing[id] != nil { return .closing(runtimeSessionID: id) }
        if detached.contains(id) { return .detached(runtimeSessionID: id) }
        if terminals[id] != nil {
            return rendererFailed.contains(id) ? .rendererFailed(runtimeSessionID: id) : .terminal(runtimeSessionID: id)
        }
        // A terminal that could not be made is not asked for again on every
        // snapshot; something has to have changed first.
        if !retry, case let .unavailable(refused, _) = previous, refused == id { return previous }
        guard let delegate else { return .unavailable(runtimeSessionID: id, "The app is shutting down.") }
        switch delegate.makeTerminal(runtimeSessionID: id, fontSize: fontSizes[id]) {
        case let .success(terminal):
            Self.logger.notice("workspace: attached a terminal to \(id, privacy: .public)")
            terminals[id] = terminal
            (terminal.view as? Ghostty.SurfaceView)?.hostName = row.displayName
            terminal.view.frame = pane.bounds
            terminal.view.autoresizingMask = [.width, .height]
            terminal.view.isHidden = true
            pane.addSubview(terminal.view, positioned: .below, relativeTo: placeholder)
            return .terminal(runtimeSessionID: id)
        case let .failure(problem):
            return .unavailable(runtimeSessionID: id, problem.message)
        }
    }

    /// Shows the selected session's pane. Terminals exist only while the
    /// window is open, so a closed window shows nothing.
    func updateContent(focus: Bool, retry: Bool = false) {
        guard isOpen else { return }
        notePlace()
        let wanted = wantedContent(retry: retry)
        if wanted != content {
            content = wanted
            Self.logger.notice("workspace: showing \(String(describing: wanted), privacy: .public)")
        }
        otherContent = wantedOtherContent()
        let shown = visibleView(wanted)
        let otherShown = visibleView(otherContent)
        for terminal in terminals.values { terminal.view.isHidden = terminal.view !== shown && terminal.view !== otherShown }
        for terminal in viewTerminals.values { terminal.view.isHidden = terminal.view !== shown && terminal.view !== otherShown }
        reviewView?.isHidden = reviewView !== shown && reviewView !== otherShown
        placeView(shown, in: pane, below: placeholder)
        placeView(otherShown, in: otherPane, below: otherPlaceholder)
        placeholder.isHidden = shown != nil
        otherPlaceholder.isHidden = otherShown != nil
        if shown == nil { fillPlaceholder(wanted) }
        if otherShown == nil {
            otherPlaceholder.show(title: title(for: otherPlace), message: otherExplanation, button: "Focus pane") { [weak self] in
                _ = self?.focusOtherPane()
            }
        }
        fitPanes()
        paneLayout.arrange(split: isSplit, activeOnRight: activeOnRight)
        if let fraction = restoringFraction {
            restoringFraction = nil
            window.contentView?.layoutSubtreeIfNeeded()
            paneLayout.restoreFraction(fraction)
        }
        paneLayout.titles(active: title(for: place), other: title(for: otherPlace))
        installPaneClickMonitor()
        window.title = windowTitle
        window.subtitle = windowSubtitle
        clearBells()
        showLink((shown as? Ghostty.SurfaceView)?.hoveredLink)
        syncOverlays()

        if let shown, focus {
            if !window.makeFirstResponder(shown) { window.makeFirstResponder(nil) }
        } else if window.firstResponder === window
                    || (window.firstResponder as? NSView)?.isHiddenOrHasHiddenAncestor == true {
            // Nothing has the keyboard, or a terminal that is no longer
            // shown had it: the sidebar takes it, so the arrow keys work.
            focusListOrPane()
        }
        reloadInspector()
        reportPlace()
        reportShowing()
    }

    static let resumeTitle = "Resume Conversation…"

    static let startDaemonTitle = WorkspaceSidebar.DaemonAction.start.title

    static let restartDaemonTitle = WorkspaceSidebar.DaemonAction.restart.title

    /// Asks the app to start or restart the daemon, from the pane's button or
    /// the one under the sidebar.
    private func performDaemonAction(_ action: WorkspaceSidebar.DaemonAction) {
        Self.logger.notice("workspace: the operator asked to \(action == .start ? "start" : "restart", privacy: .public) the daemon")
        switch action {
        case .start: delegate?.workspaceStartDaemon()
        case .restart: delegate?.workspaceRestartDaemon()
        }
    }

    @objc func daemonButtonPressed(_ sender: Any?) {
        guard let feed = delegate?.store.feed, let action = WorkspaceSidebar.daemonAction(feed) else { return }
        performDaemonAction(action)
    }

    func fillPlaceholder(_ content: Content) {
        let row = state.selectedRow
        switch content {
        case .empty, .terminal, .view, .review:
            // Rows held from the last snapshot can still be chosen; with none
            // listed, what is wrong is the feed, not the sessions.
            if state.rows.isEmpty, let feed = delegate?.store.feed, let down = WorkspaceSidebar.feedExplanation(feed) {
                placeholder.show(title: down.title, message: down.message, button: down.action?.title) { [weak self] in
                    if let action = down.action { self?.performDaemonAction(action) }
                }
                break
            }
            if state.rows.isEmpty {
                placeholder.show(title: "No live sessions",
                    message: [delegate?.workspaceProjectGuidance ?? "", delegate?.workspaceLaunchDescription ?? ""].joined(separator: "\n\n"),
                    button: delegate?.workspaceOffersSetup == true ? "Set Up First Project…" : "Launch Project…", secondary: [
                        ("Conversation History", { [weak self] in self?.delegate?.workspacePerform(.history) }),
                        ("Open Dashboard", { [weak self] in self?.showView(WorkspaceUtilityView.dashboard.id) }),
                    ]) { [weak self] in
                        guard let self else { return }
                        if self.delegate?.workspaceOffersSetup == true { self.open(.setup) }
                        else { self.delegate?.workspaceLaunchProjects(from: self.placeholder.button) }
                    }
                break
            }
            placeholder.show(
                title: "No session selected",
                message: "Choose a session in the sidebar.",
                button: "Open Dashboard"
            ) { [weak self] in self?.showView(WorkspaceUtilityView.dashboard.id) }
        case let .viewEnded(id):
            let name = views.view(id)?.displayName ?? "This view"
            placeholder.show(
                title: "\(name) closed",
                message: "It is no longer running. No session was affected: the agents keep running.",
                button: "Reopen"
            ) { [weak self] in self?.restartView(id) }
        case let .viewUnavailable(id, message):
            placeholder.show(title: "No terminal yet", message: message, button: "Try Again") { [weak self] in
                self?.restartView(id)
            }
        case let .viewRendererFailed(id):
            let name = views.view(id)?.displayName ?? "This view"
            placeholder.show(
                title: "The terminal stopped drawing",
                message: "\(name)'s renderer failed, so it can no longer be seen here. No session was affected; restarting ends its program and starts it in a new terminal.",
                button: "Restart",
                isDefault: true
            ) { [weak self] in self?.restartViewAfterRendererFailure(id) }
        case .external:
            placeholder.show(
                title: row.map { "\($0.project) runs in Kitty" } ?? "This session runs in Kitty",
                message: "Its terminal is a Kitty window. Agent Mux brings Kitty forward on it.",
                button: "Show in Kitty"
            ) { [weak self] in
                guard let self, let row = self.state.selectedRow else { return }
                self.activate(row)
            }
        case .restoring:
            // A daemon that is down or too old never says: what would bring
            // it back is offered here, not only under the sidebar.
            if !state.current, let feed = delegate?.store.feed, let down = WorkspaceSidebar.feedExplanation(feed),
               let action = down.action {
                placeholder.show(
                    title: down.title,
                    message: "The window comes back to the session it was on once the daemon lists it. \(down.message)",
                    button: action.title,
                    isDefault: true
                ) { [weak self] in self?.performDaemonAction(action) }
                break
            }
            placeholder.show(
                title: "Restoring",
                message: state.current
                    ? "Checking that the session this window was on is still running."
                    : "Waiting for the daemon to say whether the session this window was on is still running. Nothing is started meanwhile.",
                button: "Open Dashboard"
            ) { [weak self] in self?.showView(WorkspaceUtilityView.dashboard.id) }
        case let .gone(_, status):
            let name = gone?.name ?? "This session"
            let conversation = gone?.conversation
            let notice = status?.notice(for: name, resumable: conversation != nil)
                ?? ("No longer listed", status == nil
                    ? "\(name) is no longer running. Checking what became of it."
                    : "\(name) is no longer listed by the daemon. Nothing was restarted.")
            guard let conversation else {
                placeholder.show(title: notice.0, message: notice.1, button: "Open Dashboard") { [weak self] in
                    self?.showView(WorkspaceUtilityView.dashboard.id)
                }
                break
            }
            // The native recovery panel reads the exact conversation and shows
            // its profile and destination before the operator resumes it.
            placeholder.show(title: notice.0, message: notice.1, button: Self.resumeTitle) { [weak self] in
                self?.delegate?.workspaceResumeConversation(conversation)
            }
        case let .detached(id):
            placeholder.show(
                title: "Detached",
                message: "This window let go of the session. The agent is still running; reattach when you want to use it here.",
                button: "Reattach",
                isDefault: true
            ) { [weak self] in self?.reattach(id) }
        case let .unavailable(id, message):
            placeholder.show(title: "No terminal yet", message: message, button: "Try Again") { [weak self] in
                self?.reattach(id)
            }
        case .closing:
            placeholder.show(title: "Ending", message: "This session's agent is being ended.")
        case let .rendererFailed(id):
            placeholder.show(
                title: "The terminal stopped drawing",
                message: "Its renderer failed, so what the agent shows can no longer be seen here. The agent is still running; reattaching makes a new terminal for it.",
                button: "Reattach",
                isDefault: true
            ) { [weak self] in self?.reattachAfterRendererFailure(id) }
        }
    }

    /// Lets go of a terminal whose renderer failed and attaches a new one.
    private func reattachAfterRendererFailure(_ id: String) {
        Self.logger.notice("workspace: reattaching \(id, privacy: .public) after a renderer failure")
        discardTerminal(id)
        reattach(id)
    }

    /// Ends a utility view whose renderer failed and starts it again in a
    /// new terminal.
    private func restartViewAfterRendererFailure(_ id: String) {
        Self.logger.notice("workspace: restarting view \(id, privacy: .public) after a renderer failure")
        discardViewTerminal(id)
        restartView(id)
    }

    private func reattach(_ id: String) {
        detached.remove(id)
        guard state.selection == .embedded(id) else { return }
        updateContent(focus: true, retry: true)
    }

    private func restartView(_ id: String) {
        endedViews.remove(id)
        guard viewSelection == id else { return }
        updateContent(focus: true, retry: true)
    }

    /// Asks again for a terminal that could not be made, now that what it
    /// was missing (a resolved amx) may be there.
    func retryTerminal() {
        updateContent(focus: false, retry: true)
    }
}

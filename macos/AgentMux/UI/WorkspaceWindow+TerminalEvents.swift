import AppKit
import os

// What the embedded terminals report: exits, bells, titles, links, copies, searches, and progress.
// Part of WorkspaceWindowController (WorkspaceWindow.swift).
extension WorkspaceWindowController {
    // MARK: Terminal events

    /// The terminal the pane shows, if it shows one.
    var shownTerminalView: NSView? {
        switch content {
        case let .terminal(id): return terminals[id]?.view
        case let .view(id): return viewTerminals[id]?.view
        default: return nil
        }
    }

    private func sessionID(of view: NSView) -> String? {
        terminals.first(where: { $0.value.view === view })?.key
    }

    private func viewID(of view: NSView) -> String? {
        viewTerminals.first(where: { $0.value.view === view })?.key
    }

    /// What a bell does, given where it rang.
    struct BellResponse: Equatable {
        /// Mark the line of the session or view it rang in.
        var mark: Bool
        var beep: Bool
        /// Ask the Dock for attention, once.
        var requestAttention: Bool

        /// A bell in the terminal shown is heard, and only when the app is
        /// in front and Ghostty's `bell-features` include `system`; one in a
        /// hidden terminal marks its line instead. Either asks for attention
        /// while another app is in front.
        static func of(shown: Bool, appActive: Bool, systemBell: Bool) -> BellResponse {
            BellResponse(mark: !shown, beep: shown && appActive && systemBell, requestAttention: !appActive)
        }
    }

    /// A program rang the bell in one of the workspace's terminals.
    func terminalRangBell(_ view: NSView, systemBell: Bool) {
        let session = sessionID(of: view)
        let utility = session == nil ? viewID(of: view) : nil
        guard session != nil || utility != nil else { return }
        let shown = isOpen && (view === shownTerminalView || view === visibleView(otherContent))
        let response = BellResponse.of(shown: shown, appActive: isAppActive(), systemBell: systemBell)
        if response.beep { beep() }
        if response.requestAttention { requestAttention() }
        guard response.mark else { return }
        if let session { ringingSessions.insert(session) }
        if let utility { ringingViews.insert(utility) }
        reloadLines()
        bellCount += 1
        let count = bellCount
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.bellHold) { [weak self] in
            guard let self, self.bellCount == count else { return }
            self.clearBells(all: true)
        }
    }

    /// Takes the bell mark off what the pane shows, or off every line.
    func clearBells(all: Bool = false) {
        var changed = false
        if all {
            changed = !ringingSessions.isEmpty || !ringingViews.isEmpty
            ringingSessions.removeAll()
            ringingViews.removeAll()
        } else {
            for visible in [content, otherContent] {
                switch visible {
                case let .terminal(id): changed = (ringingSessions.remove(id) != nil) || changed
                case let .view(id): changed = (ringingViews.remove(id) != nil) || changed
                default: break
                }
            }
        }
        if changed { reloadLines() }
    }

    /// Redraws the sidebar's lines in place, keeping its selection.
    private func reloadLines() {
        guard sidebar.numberOfRows > 0 else { return }
        reload(IndexSet(integersIn: 0..<sidebar.numberOfRows))
    }

    /// Redraws the given lines in place, keeping the selection, and gives
    /// each its session's color again: the row views stay.
    func reload(_ indexes: IndexSet) {
        applying = true
        sidebar.reloadData(forRowIndexes: indexes, columnIndexes: IndexSet(integer: 0))
        applying = false
        // A count that changed may wrap the overview's chips differently.
        noteOverviewHeight(in: indexes)
        for index in indexes {
            guard let rowView = sidebar.rowView(atRow: index, makeIfNecessary: false) as? WorkspaceRowView else { continue }
            if let sessionView = rowView as? WorkspaceSessionRowView, case let .row(session) = items[index] {
                sessionView.hasStateGlyph = !session.glyph.isEmpty
            }
            rowView.tabColor = profileColor(of: items[index])
            rowView.selectionBorder.color = .controlAccentColor
        }
        syncHover(indexes)
    }

    /// A line drawn again is a fresh cell that knows nothing of the
    /// pointer: its × follows where the pointer is now. The other lines'
    /// own tracking areas already know.
    func syncHover(_ indexes: IndexSet) {
        guard NSApp.isActive, let window = sidebar.window, window.isVisible else { return }
        syncHover(at: window.mouseLocationOutsideOfEventStream, lines: indexes)
    }

    /// Hovers whichever of `lines` is under `point`, in window
    /// coordinates, and none of the others.
    func syncHover(at point: NSPoint, lines: IndexSet) {
        let inList = sidebar.visibleRect.contains(sidebar.convert(point, from: nil))
        for index in lines where index < sidebar.numberOfRows {
            guard let cell = sidebar.view(atColumn: 0, row: index, makeIfNecessary: false) as? WorkspaceRowCellView else { continue }
            cell.isHovered = inList && cell.bounds.contains(cell.convert(point, from: nil))
        }
    }

    /// A utility view's program set its title, or its shell reported the
    /// directory it is in (`directory`), which decides whether the title
    /// says anything (`WorkspaceUtilityView.programTitle`). A session's
    /// terminal shows what tmux titles it, which says nothing the sidebar
    /// does not.
    func terminalTitleChanged(_ view: NSView, title: String, directory: String? = nil) {
        guard let id = viewID(of: view), let utility = views.view(id) else { return }
        let program = utility.programTitle(title, directory: directory)
        guard programTitles[id] != program else { return }
        programTitles[id] = program
        reloadLines()
        if viewSelection == id { window.title = windowTitle }
    }

    /// The window's title: the view or session the pane is on, and for a
    /// view the title its program set.
    var windowTitle: String {
        if reviewSelected { return "Checkout Changes — Agent Mux" }
        if let view = views.view(viewSelection) {
            guard let program = programTitles[view.id], !program.isEmpty, program != view.displayName else { return view.displayName }
            return "\(view.displayName) — \(program)"
        }
        return state.selectedRow?.displayName ?? "Agent Mux"
    }

    /// Under the title, a session's line from the sidebar (harness, branch,
    /// age, why it waits), so the window says it with the sidebar hidden.
    /// A view's title already says what its program does.
    var windowSubtitle: String {
        guard viewSelection == nil else { return "" }
        return state.selectedRow?.detail ?? ""
    }

    /// The pointer moved onto a link, or off one, in a terminal.
    func terminalHoveredLink(_ view: NSView, url: String?) {
        guard view === shownTerminalView else { return }
        showLink(url)
    }

    static let copiedNotice = "Copied to the clipboard"

    /// The shown terminal put text on the clipboard. Where a selection made
    /// with the mouse is tmux's and is copied when the button is let go,
    /// nothing on screen says so; the link line does, for a moment.
    func terminalCopied(_ view: NSView) {
        guard view === shownTerminalView else { return }
        showLink(Self.copiedNotice)
        WorkspaceCopyNotice.announce(Self.copiedNotice, from: window)
        copiedNoticeEnd?.cancel()
        let end = DispatchWorkItem { [weak self] in
            MainActor.assumeIsolated { self?.endCopiedNotice() }
        }
        copiedNoticeEnd = end
        DispatchQueue.main.asyncAfter(deadline: .now() + WorkspaceCopyNotice.seconds, execute: end)
    }

    func endCopiedNotice() {
        copiedNoticeEnd?.cancel()
        copiedNoticeEnd = nil
        guard linkLabel.stringValue == Self.copiedNotice else { return }
        showLink((shownTerminalView as? Ghostty.SurfaceView)?.hoveredLink)
    }

    func showLink(_ url: String?) {
        linkLabel.stringValue = url ?? ""
        linkLabel.isHidden = url == nil
    }

    /// The utility view the pane shows, and its terminal.
    private var shownUtility: (id: String, view: NSView)? {
        guard case let .view(id) = content, let view = viewTerminals[id]?.view else { return nil }
        return (id, view)
    }

    /// Shows the find bar and the scrollbar of the utility view the pane is
    /// on, and neither for anything else.
    func syncOverlays() {
        conversationSearchButton.isHidden = searchHintSessionID == nil || searchHintSessionID != shownSessionRow?.key.runtimeSessionID || shownSessionRow?.conversation == nil
        let shown = shownUtility
        let search = shown.flatMap { searches[$0.id] }
        findBar.context = shown.flatMap { shown in
            search == nil ? nil : AnyHashable("\(shown.id)#\(searchGenerations[shown.id] ?? 0)")
        }
        if let search { findBar.show(search) }
        findBar.isHidden = search == nil
        scrollbarView.terminal = shown?.view
        scrollbarView.scrollbar = shown.flatMap { scrollbars[$0.id] }
    }

    /// A search began, ended, or was counted in a utility view's buffer.
    /// A search asked for again takes the find bar's field. One begun from
    /// a selection only names its needle (`search_selection`), so it is
    /// searched for here, in place of whatever that view searched before.
    func terminalSearch(_ view: NSView, _ update: Ghostty.SearchUpdate) {
        guard let id = viewID(of: view) else { return }
        let hadKeyboard = findBar.hasKeyboard
        let wasOpen = searches[id] != nil
        searches[id] = Ghostty.Search.applying(update, to: searches[id])
        if wasOpen != (searches[id] != nil) {
            searchGeneration += 1
            searchGenerations[id] = searchGeneration
        }
        var seed: String?
        if case let .started(needle) = update, !needle.isEmpty {
            _ = performTerminalAction(view, "search:\(needle)")
            seed = needle
        }
        guard shownUtility?.view === view else { return }
        if let seed { findBar.seed(seed) }
        syncOverlays()
        switch update {
        case .started: findBar.focus()
        case .ended where hadKeyboard: window.makeFirstResponder(view)
        default: break
        }
    }

    /// The find bar's needle changed, or a match was asked for.
    func findBarNeedleChanged(_ needle: String) {
        guard let shown = shownUtility, searches[shown.id] != nil else { return }
        searches[shown.id]?.needle = needle
        _ = performTerminalAction(shown.view, "search:\(needle)")
    }

    func findBarNavigate(next: Bool) {
        guard let shown = shownUtility, searches[shown.id] != nil else { return }
        _ = performTerminalAction(shown.view, next ? "navigate_search:next" : "navigate_search:previous")
    }

    func showConversationSearchHint(for view: NSView) {
        guard view === shownTerminalView, let row = shownSessionRow, row.conversation != nil else { return }
        searchHintSessionID = row.key.runtimeSessionID
        syncOverlays()
    }

    @objc func searchFullConversation(_ sender: Any?) {
        guard searchHintSessionID == shownSessionRow?.key.runtimeSessionID else { return }
        delegate?.workspaceSearchConversation()
    }

    /// Closes the find bar and ends the search; the terminal takes the
    /// keyboard back if the bar had it.
    func closeFindBar() {
        guard let shown = shownUtility else { return }
        _ = performTerminalAction(shown.view, "end_search")
        terminalSearch(shown.view, .ended)
        if window.firstResponder === window || (window.firstResponder as? NSView)?.isHiddenOrHasHiddenAncestor == true {
            window.makeFirstResponder(shown.view)
        }
    }

    /// Where a utility view's viewport sits in its buffer changed.
    func terminalScrollbar(_ view: NSView, _ scrollbar: Ghostty.Scrollbar?) {
        guard let id = viewID(of: view) else { return }
        scrollbars[id] = scrollbar
        if shownUtility?.view === view { scrollbarView.scrollbar = scrollbar }
    }

    /// The scrollbar's knob was moved.
    func scrollbarMoved(toRow row: UInt64) {
        guard let shown = shownUtility else { return }
        _ = performTerminalAction(shown.view, "scroll_to_row:\(row)")
    }

    /// A utility view's program reported progress, or its report went. Shown
    /// on the view's line, never as attention: a session's terminal reports
    /// none here.
    func terminalProgress(_ view: NSView, _ progress: Ghostty.Progress?) {
        guard let id = viewID(of: view), progresses[id] != progress else { return }
        progresses[id] = progress
        reloadLines()
    }

    /// A terminal's renderer failed or recovered. A terminal that draws
    /// nothing is set aside for a placeholder: a session's offers to
    /// reattach, a utility view's to restart its program.
    func terminalRendererHealthChanged(_ view: NSView, healthy: Bool) {
        let changed: Bool
        if let id = sessionID(of: view) {
            Self.logger.notice("workspace: the renderer of \(id, privacy: .public) is \(healthy ? "healthy again" : "unhealthy", privacy: .public)")
            changed = healthy ? rendererFailed.remove(id) != nil : rendererFailed.insert(id).inserted
        } else if let id = viewID(of: view) {
            Self.logger.notice("workspace: the renderer of view \(id, privacy: .public) is \(healthy ? "healthy again" : "unhealthy", privacy: .public)")
            changed = healthy ? rendererFailedViews.remove(id) != nil : rendererFailedViews.insert(id).inserted
        } else {
            return
        }
        if changed { updateContent(focus: false) }
    }

    /// A program asked for a desktop notification.
    func terminalNotification(_ view: NSView, title: String, body: String) {
        let notice: TerminalNotice
        if let id = sessionID(of: view) {
            notice = TerminalNotice(title: title, body: body, source: sessionNames[id] ?? id, origin: .session(runtimeSessionID: id))
        } else if let id = viewID(of: view), let utility = views.view(id) {
            notice = TerminalNotice(title: title, body: body, source: utility.displayName, origin: .view(id: id))
        } else {
            return
        }
        delegate?.workspaceTerminalNotification(notice, onScreen: window.isKeyWindow && (notice.origin == showing || notice.origin == otherShowing))
    }

    /// The terminal the pane shows, as a terminal notice names it.
    var showing: TerminalNotice.Origin? {
        guard isOpen else { return nil }
        switch content {
        case let .terminal(id): return .session(runtimeSessionID: id)
        case let .view(id): return .view(id: id)
        default: return nil
        }
    }

    /// Whether a terminal notice's origin is being looked at: shown in the
    /// pane of the key window, where a banner would only repeat it.
    static func onScreen(_ origin: TerminalNotice.Origin, showing: TerminalNotice.Origin?, key: Bool) -> Bool {
        key && origin == showing
    }

    /// Tells the app which terminal the pane shows, when that changed.
    func reportShowing() {
        let now = showing
        let other = otherShowing
        if now != reportedShowing, let now { delegate?.workspaceShowing(now) }
        if other != reportedOtherShowing, let other { delegate?.workspaceShowing(other) }
        reportedShowing = now
        reportedOtherShowing = other
    }

    /// A utility view's program exited. A shell, editor, or pager is done
    /// and goes away; the Dashboard and the Overview stay listed, and say
    /// they stopped until they are started again.
    func utilityEnded(_ id: String) {
        Self.logger.notice("workspace: the program of view \(id, privacy: .public) ended")
        discardViewTerminal(id)
        guard let view = views.view(id) else { return }
        if view.isPermanent {
            endedViews.insert(id)
            updateContent(focus: false)
        } else {
            retire(view)
            if view.kind == .setup { delegate?.workspaceSetupFinished() }
        }
    }

    /// A new default zoom resets every open terminal; the sessions whose
    /// terminal was let go come back at it too, not at the zoom they had.
    func forgetFontSizes() {
        fontSizes.removeAll()
    }

    func discardTerminal(_ id: String) {
        guard let terminal = terminals.removeValue(forKey: id) else { return }
        // The session's next terminal comes back at the zoom this one had.
        if let size = terminal.fontSize() { fontSizes[id] = size }
        ringingSessions.remove(id)
        rendererFailed.remove(id)
        if isOpen, let responder = window.firstResponder as? NSView, responder.isDescendant(of: terminal.view) {
            focusListOrPane()
        }
        terminal.view.removeFromSuperview()
        terminal.close()
    }

    static func describe(_ key: AttentionCycler.Key?) -> String {
        guard let key else { return "none" }
        return key.runtimeSessionID.isEmpty ? "kitty window \(key.windowID)" : key.runtimeSessionID
    }
}

extension WorkspaceWindowController: GhosttySurfaceDelegate {
    nonisolated func surfaceRequestedServerFind(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { showConversationSearchHint(for: surface) }
    }
    nonisolated func surfaceChildDidExit(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalEnded(surface) }
    }

    nonisolated func surfaceDidRequestClose(_ surface: Ghostty.SurfaceView, needsConfirm: Bool) {
        MainActor.assumeIsolated { terminalAskedToClose(surface, needsConfirm: needsConfirm) }
    }

    nonisolated func surfaceDidRingBell(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalRangBell(surface, systemBell: surface.bellFeatures.contains(.system)) }
    }

    nonisolated func surfaceDidChangeTitle(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated {
            terminalTitleChanged(surface, title: surface.title, directory: surface.workingDirectory)
        }
    }

    nonisolated func surfaceDidChangeWorkingDirectory(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated {
            terminalTitleChanged(surface, title: surface.title, directory: surface.workingDirectory)
        }
    }

    nonisolated func surfaceDidChangeHoveredLink(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalHoveredLink(surface, url: surface.hoveredLink) }
    }

    nonisolated func surfaceDidWriteClipboard(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalCopied(surface) }
    }

    nonisolated func surfaceDidChangeRendererHealth(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalRendererHealthChanged(surface, healthy: surface.rendererHealthy) }
    }

    nonisolated func surface(_ surface: Ghostty.SurfaceView, didRequestNotificationTitled title: String, body: String) {
        MainActor.assumeIsolated { terminalNotification(surface, title: title, body: body) }
    }

    nonisolated func surface(_ surface: Ghostty.SurfaceView, didUpdateSearch update: Ghostty.SearchUpdate) {
        MainActor.assumeIsolated { terminalSearch(surface, update) }
    }

    nonisolated func surfaceDidChangeScrollbar(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalScrollbar(surface, surface.scrollbar) }
    }

    nonisolated func surfaceDidChangeProgress(_ surface: Ghostty.SurfaceView) {
        MainActor.assumeIsolated { terminalProgress(surface, surface.progress) }
    }
}

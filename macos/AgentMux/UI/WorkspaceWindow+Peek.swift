import AppKit

extension WorkspaceWindowController {
    var allWaitingRows: [WorkspaceWaitingRow] {
        guard let store = delegate?.store else { return [] }
        return WorkspaceInspector.waiting(store.sessions, asl: store.snapshot?.asl.waiting ?? [], now: now(), glyphs: glyphs, order: order, grouping: sidebarGrouping)
    }

    func showSessionPeek(_ key: AttentionCycler.Key, from anchor: NSView? = nil) {
        guard let row = state.row(key), isOpen else { return }
        sessionPeek.readContext = { [weak self] row, completion in self?.delegate?.workspaceContext(row, completion: completion) }
        summaryPopover.close()
        sessionPeek.show(row, waiting: allWaitingRows.first { $0.key == key }, current: state.current, lastObserved: lastObservedActivity(key))
        peekPopover.onClose = { [weak self] in self?.sessionPeek.close() }
        presentingPeek = true
        defer { presentingPeek = false }
        let view = WorkspaceSessionPeekView(model: sessionPeek, close: { [weak self] in self?.peekPopover.close() })
        if let anchor { peekPopover.show(view, of: anchor) }
        else if let index = items.firstIndex(where: { if case let .row(row) = $0 { return row.key == key }; return false }), !isSidebarCollapsed {
            peekPopover.show(view, of: sidebar, rect: sidebar.rect(ofRow: index))
        } else { peekPopover.show(view, of: summaryButton) }
    }

    func refreshSessionPeek() {
        guard let previous = sessionPeek.row else { return }
        guard let row = state.row(previous.key) else { peekPopover.close(); return }
        sessionPeek.show(row, waiting: allWaitingRows.first { $0.key == row.key }, current: state.current, lastObserved: lastObservedActivity(row.key))
    }

    private func lastObservedActivity(_ key: AttentionCycler.Key) -> Date? {
        guard let at = delegate?.store.sessions.first(where: { AttentionCycler.key($0) == key })?.lastEventAt, at > 0 else { return nil }
        return Date(timeIntervalSince1970: TimeInterval(at))
    }

    @objc func peekClickedSession(_ sender: NSMenuItem) {
        guard let key = sender.representedObject as? AttentionCycler.Key else { return }
        showSessionPeek(key)
    }
}

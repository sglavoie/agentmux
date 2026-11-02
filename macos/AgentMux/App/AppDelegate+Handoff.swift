import AppKit

extension AppDelegate {
    @objc func continueWithAnotherAgent(_ sender: Any?) {
        guard let row = workspace?.shownSessionRow else { return refuse(Self.noSessionShown) }
        workspaceHandoff(from: row)
    }

    func workspaceHandoff(from row: WorkspaceRow) {
        guard let source = row.conversation else { return refuse("This session has not reported its conversation yet.") }
        showHandoff(source: source, profile: row.label, on: workspace?.window, below: row.key)
    }

    func showHandoff(source: ConversationIdentity, profile: String, on window: NSWindow?, below parent: AttentionCycler.Key? = nil) {
        guard let window, source.isUsable else { return }
        if let reason = gate.unavailableReason(.sessions) { return refuse(reason) }
        if let panel = handoffPanel, panel.panel.isVisible { panel.panel.makeKeyAndOrderFront(nil); return }
        guard window.attachedSheet == nil else { return }
        let sourceRow = store.feed == .live ? store.sessions.first(where: { $0.conversation == source }) : nil
        let parent = parent ?? sourceRow.map { AttentionCycler.key($0, snapshotSocket: store.snapshot?.kitty.socket ?? "") }
        let model = WorkspaceHandoffModel(source: source, preferredProfile: profile, command: { [weak self] in self?.command })
        let panel = WorkspaceHandoffPanel(model: model)
        handoffPanel = panel
        model.onSource = { [weak self] in self?.openHistory(conversation: source) }
        model.onLaunched = { [weak self, weak panel] id, path in
            guard let self else { return }
            self.handoffOrigins.record(runtimeID: id, source: source, artifactPath: path)
            self.handoffOrigins.observe(self.store.sessions)
            panel?.close(); self.handoffPanel = nil
            let workspace = self.workspaceController()
            workspace.show()
            if let parent { workspace.openNewSession(id, below: parent) }
            else { workspace.open(WorkspaceViewRequest(kind: WorkspaceViewRequest.sessionKind, runtimeSessionID: id, awaited: true)) }
        }
        panel.show(on: window)
    }

    func workspaceHandoffSource(_ row: WorkspaceRow) -> ConversationIdentity? {
        handoffOrigins.origin(runtimeID: row.embeddedRuntimeSessionID ?? "", conversation: row.conversation)?.identity
    }

    func workspaceOpenHandoffSource(_ identity: ConversationIdentity) { openHistory(conversation: identity) }
}

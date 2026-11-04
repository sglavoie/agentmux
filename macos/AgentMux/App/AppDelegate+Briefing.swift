import AppKit

extension AppDelegate {
    @objc func showBriefing(_ sender: Any?) {
        let controller = briefingWindow ?? WorkspaceBriefingWindow(model: WorkspaceBriefingModel(command: { [weak self] in self?.command }))
        briefingWindow = controller
        trackVisibility(of: controller, \.briefingVisible)
        controller.model.onFocus = { [weak self] key in
            guard let self, self.store.feed == .live else { return }
            guard let session = self.store.managedSessions.first(where: { AttentionCycler.key($0) == key }) else {
                self.refuse("This session is no longer live. Refresh the briefing or read its conversation."); return
            }
            self.focus(.focusing(session, title: MenuBuilder.name(of: session)))
        }
        controller.model.onConversation = { [weak self, weak controller] identity, index in
            self?.openHistory(conversation: identity, recapMessage: index, returnTo: controller)
        }
        controller.model.onHandoff = { [weak self, weak controller] conversation in
            self?.showHandoff(source: conversation.identity, profile: conversation.profile, on: controller?.window)
        }
        controller.model.onContinue = { [weak self] conversation, done in
            guard let self else { done("The workspace is unavailable."); return }
            self.openHistoryConversation(conversation, fork: false, completion: done)
        }
        refreshBriefing()
        refreshProjects()
        controller.show(project: workspace?.shownSessionRow?.project)
    }

    /// Snapshots keep live state current without refreshing transcript history
    /// or generating summaries in the background.
    func refreshBriefing() {
        briefingWindow?.model.update(projects: projects.state.value ?? [], sessions: store.managedSessions,
            asl: store.snapshot?.asl.waiting ?? [],
            live: store.feed == .live, order: attentionOrder)
    }
}

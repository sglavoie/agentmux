import AppKit

extension WorkspaceWindowController {
    /// Stage restoration before the first snapshot can remove a stale group.
    /// Older installations contribute their one saved layout to that group.
    func restoreContext(_ restoration: WorkspaceRestoration) {
        groupContexts = restoration.groupContexts
        selectedGroup = restoration.selectedGroup
        let context = groupContexts[WorkspaceRestoration.contextKey(selectedGroup)]
        let layout = context?.layout ?? restoration.layout ?? restoration.place.map { place in
            let pane: WorkspaceRestoration.Pane
            switch place {
            case .session: pane = .session(place.encoded)
            case let .view(id): pane = .utility(id == WorkspaceUtilityView.overview.id ? .overview : .dashboard)
            }
            return .init(active: pane, activeOnRight: false)
        }
        if let layout {
            let conversation: ConversationIdentity?
            if let context { conversation = context.conversation.flatMap(ConversationIdentity.init(encoded:)) }
            else { conversation = restoration.conversation }
            restoreLayout(layout, conversation: conversation)
        }
    }

    /// Only an explicit group switch restores its saved panes. Revealing a
    /// session from a notification or launch changes scope without taking
    /// the operator away from the session they just asked to see.
    func switchGroup(to group: String?) {
        guard group != selectedGroup else { reloadSidebar(allowReorder: true); return }
        guard reviewCanClose() else { reloadSidebar(); return }
        if let pendingLayout {
            rememberGroupLayout(pendingLayout, conversation: layoutConversation)
        } else { reportLayout() }
        leaveRecovery()
        if hasReview { releaseReview() }
        state.select(nil)
        viewSelection = nil
        reviewSelected = false
        shownConversation = nil
        otherPlace = nil
        otherContent = .empty
        activeOnRight = false
        restoringFraction = nil
        awaited = nil
        choosingGroupPlace = isOpen
        selectedGroup = group
        reloadSidebar(allowReorder: true)
        continueChoosingGroupPlace()
        updateContent(focus: true)
    }

    /// Wait for authoritative membership before choosing a group's first
    /// session. A failed catalog read or disconnected feed cannot replace
    /// the group's saved context with an empty or unrelated workspace.
    func continueChoosingGroupPlace() {
        guard choosingGroupPlace, state.current,
              selectedGroup == nil || delegate?.workspaceProjectsLoaded == true else { return }
        let context = groupContexts[WorkspaceRestoration.contextKey(selectedGroup)]
        choosingGroupPlace = false
        if let context, paneBelongsToGroup(context.layout.active) {
            restoreLayout(context.layout, conversation: context.conversation.flatMap(ConversationIdentity.init(encoded:)))
        } else if let row = scopedSidebar.rows.first {
            restoreLayout(.init(active: .session(WorkspaceRestoration.Place.session(runtimeSessionID: row.key.runtimeSessionID).encoded),
                                activeOnRight: false))
        } else {
            restoreLayout(.init(active: .empty, activeOnRight: false))
        }
    }

    /// External navigation can briefly show a session outside the current
    /// filter before Reveal switches to All. Keep the old group's place.
    func paneBelongsToGroup(_ pane: WorkspaceRestoration.Pane) -> Bool {
        guard selectedGroup != nil, delegate?.workspaceProjectsLoaded == true else { return true }
        let key: AttentionCycler.Key?
        switch pane {
        case let .session(encoded): key = WorkspaceRestoration.Place(encoded: encoded)?.sessionKey
        case let .utility(view): key = view.besideRuntimeSessionID.map { .embedded($0) }
        case .review, .empty: key = nil
        }
        guard let key, state.row(key) != nil else { return true }
        return scopedSidebar.row(key) != nil
    }

    func rememberGroupLayout(_ layout: WorkspaceRestoration.Layout, conversation: ConversationIdentity? = nil) {
        guard paneBelongsToGroup(layout.active) else { return }
        let identity: ConversationIdentity?
        if case let .session(encoded) = layout.active,
           let key = WorkspaceRestoration.Place(encoded: encoded)?.sessionKey {
            identity = conversation ?? state.row(key)?.conversation
                ?? (shownConversation?.runtimeSessionID == key.runtimeSessionID ? shownConversation?.conversation : nil)
        } else { identity = nil }
        let key = WorkspaceRestoration.contextKey(selectedGroup)
        let context = WorkspaceRestoration.GroupContext(layout: layout, conversation: identity?.encoded)
        guard groupContexts[key] != context else { return }
        groupContexts[key] = context
        onGroupContextsChanged(groupContexts)
    }
}

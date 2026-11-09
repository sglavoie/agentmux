import AppKit

extension WorkspaceWindowController {
    func configureReturnSummary() {
        summaryPopover.onClose = { [weak self] in self?.returnSummary.close() }
        returnSummary.onReturn = { [weak self] in
            DispatchQueue.main.async { [weak self] in self?.presentReturnSummary() }
        }
        returnSummary.onActivity = { [weak self] in
            self?.summaryPopover.close(); self?.delegate?.workspacePerform(.activity)
        }
        returnSummary.onOpen = { [weak self] update in
            guard let self, self.state.current, let key = update.row?.key, let row = self.state.row(key) else { return }
            self.summaryPopover.close(); self.activate(row)
        }
        returnSummary.onAnswer = { [weak self] key in
            guard let self, self.state.current else { return }
            self.summaryPopover.close()
            if let answer = self.allWaitingRows.first(where: { $0.key == key })?.answer {
                self.delegate?.workspaceAnswer(answer)
            } else if let row = self.state.row(key) { self.activate(row) }
        }
        returnSummary.onReview = { [weak self] key in
            guard let self, self.state.current, let row = self.state.row(key), row.iconState == "stop" else { return }
            self.summaryPopover.close(); self.activate(row)
            self.delegate?.workspacePerform(.splitChanges)
        }
    }
    @objc func showReturnSummary(_ sender: Any? = nil) {
        returnSummary.refresh(now: now())
        presentReturnSummary()
    }
    func presentReturnSummary() {
        guard isOpen, window.isVisible, window.attachedSheet == nil else { return }
        peekPopover.close()
        summaryPopover.show(WorkspaceReturnSummaryView(model: returnSummary, close: { [weak self] in self?.summaryPopover.close() }), of: summaryButton)
    }
    func workspaceBecameActive() {
        guard isOpen else { return }
        returnSummary.arrive(at: now(), automatically: welcome.isEnabled)
    }
    func workspaceResignedActive() {
        returnSummary.leave(at: now())
        summaryPopover.close(); peekPopover.close()
    }
}

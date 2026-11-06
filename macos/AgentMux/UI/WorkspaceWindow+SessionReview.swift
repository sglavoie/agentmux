import AppKit
import SwiftUI

extension WorkspaceWindowController {
    /// A menu keeps the exact reviewed membership and epochs, so arrivals and
    /// a new prompt cannot be silently included while the menu is open.
    func reviewMenuItems(rows: [WorkspaceRow], project: String? = nil) -> [NSMenuItem] {
        guard !rows.isEmpty else { return [] }
        var request = SessionReviewRequest(project: project)
        for row in rows { if let id = row.embeddedRuntimeSessionID { request.targets[id] = row.review.epoch } }
        let all = rows.allSatisfy { $0.review.accounted }
        request.accounted = !all
        let mark = NSMenuItem(title: all ? "Clear Acknowledgement" : "Mark Accounted For", action: #selector(changeSessionReview(_:)), keyEquivalent: "")
        mark.representedObject = ReviewMenuRequest(request)
        mark.target = self
        mark.isEnabled = state.current && !sessionReviewBusy
        request.accounted = nil
        request.note = project == nil ? rows[0].review.note : rows[0].review.projectNote
        let note = NSMenuItem(title: project == nil ? "Edit Session Note…" : "Edit Project Note…", action: #selector(editSessionReviewNote(_:)), keyEquivalent: "")
        note.representedObject = ReviewMenuRequest(request)
        note.target = self
        note.isEnabled = mark.isEnabled
        let undo = NSMenuItem(title: "Undo Annotation", action: #selector(undoSessionReview(_:)), keyEquivalent: "")
        undo.target = self
        undo.isEnabled = state.current && !sessionReviewBusy && sessionReviewUndo != nil
        return [.separator(), mark, note, undo]
    }

    @objc private func changeSessionReview(_ sender: NSMenuItem) {
        guard let request = sender.representedObject as? ReviewMenuRequest else { return }
        saveSessionReview(request.value)
    }

    @objc private func undoSessionReview(_ sender: NSMenuItem) {
        guard let token = sessionReviewUndo else { return }
        saveSessionReview(SessionReviewRequest(undoToken: token))
    }

    func saveSessionReview(_ request: SessionReviewRequest, completion: ((String?) -> Void)? = nil) {
        guard state.current, !sessionReviewBusy else { completion?("Wait for a live connection and the current save to finish."); return }
        sessionReviewBusy = true
        let service = sessionReviewService
        sessionReviewRunner.run({ service(request) }, onMain: { [weak self] result in
            guard let self else { return }
            self.sessionReviewBusy = false
            switch result {
            case let .success(receipt):
                self.sessionReviewUndo = receipt.undoToken
                completion?(nil)
            case let .failure(error):
                if let completion { completion(error.localizedDescription) }
                else { self.showError(title: "Annotation could not be saved", message: error.localizedDescription) }
            }
        })
    }

    @objc private func editSessionReviewNote(_ sender: NSMenuItem) {
        guard let captured = sender.representedObject as? ReviewMenuRequest else { return }
        let popover = NSPopover()
        popover.behavior = .semitransient
        let request = captured.value
        let content = SessionReviewNoteEditor(title: request.project.map { "Note for \($0)" } ?? "Session note", draft: request.note ?? "", save: { [weak self, weak popover] text, finished in
            var edit = request
            edit.note = text
            self?.saveSessionReview(edit) { problem in
                finished(problem)
                if problem == nil { popover?.close() }
            }
        }, cancel: { [weak popover] in popover?.close() })
        popover.contentViewController = NSHostingController(rootView: content)
        sessionReviewPopover?.close()
        sessionReviewPopover = popover
        let row = sidebar.clickedRow
        let rect = row >= 0 ? sidebar.rect(ofRow: row) : sidebar.visibleRect
        popover.show(relativeTo: rect, of: sidebar, preferredEdge: .maxX)
    }
}

private final class ReviewMenuRequest: NSObject {
    let value: SessionReviewRequest
    init(_ value: SessionReviewRequest) { self.value = value }
}

private struct SessionReviewNoteEditor: View {
    let title: String
    @State var draft: String
    var save: (String, @escaping (String?) -> Void) -> Void
    var cancel: () -> Void
    @State private var busy = false
    @State private var problem: String?
    @FocusState private var focused: Bool
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(title).font(.headline)
            TextField("What should you remember?", text: $draft).focused($focused).onSubmit(submit)
                .accessibilityLabel("Session or project note")
            Text("Saved until you edit or clear it. Leave empty to remove.").font(.caption).foregroundStyle(.secondary)
            if let problem { Text(problem).foregroundStyle(.red).fixedSize(horizontal: false, vertical: true) }
            HStack {
                Text("\(draft.unicodeScalars.count)/500").font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button("Cancel", action: cancel).keyboardShortcut(.cancelAction).disabled(busy)
                Button(busy ? "Saving…" : "Save", action: submit).keyboardShortcut(.defaultAction).disabled(busy || draft.unicodeScalars.count > 500)
            }
        }.padding(14).frame(width: 340).onAppear { focused = true }
    }
    private func submit() {
        guard !busy, draft.unicodeScalars.count <= 500 else { return }
        busy = true
        save(draft) { error in busy = false; problem = error }
    }
}

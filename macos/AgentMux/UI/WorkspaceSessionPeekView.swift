import AppKit
import SwiftUI

struct WorkspaceSessionPeekView: View {
    @ObservedObject var model: WorkspaceSessionPeekModel
    let close: () -> Void
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                HStack(alignment: .top) {
                    VStack(alignment: .leading, spacing: 5) {
                        Text(model.row?.displayName ?? "Session unavailable").font(.title3.weight(.semibold))
                        Text([model.row?.project, model.row?.harness, model.row?.branch].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button(action: close) { Image(systemName: "xmark") }.buttonStyle(.plain).accessibilityLabel("Close preview")
                }
                Text((model.current ? "" : "Last known · ") + (model.row?.detail ?? ""))
                    .font(.callout).foregroundStyle(.secondary)
                if let at = model.lastObserved {
                    Text("Last observed activity " + at.formatted(date: .abbreviated, time: .shortened)).font(.caption).foregroundStyle(.secondary)
                }
                if model.waiting != nil {
                    VStack(alignment: .leading, spacing: 8) {
                        HStack {
                            Text("Current question / context").font(.headline)
                            Spacer()
                            Button("Refresh") { model.refreshQuestion() }.disabled(!model.current || model.reading.loading)
                        }
                        if model.reading.loading { ProgressView().controlSize(.small) }
                        if let text = model.reading.content { Text(text.reveal()).font(.callout).textSelection(.enabled) }
                        if let problem = model.reading.problem { Text(problem.reveal()).font(.caption).foregroundStyle(.secondary) }
                        if let at = model.reading.refreshedAt { Text("Read " + at.formatted(date: .omitted, time: .shortened)).font(.caption).foregroundStyle(.secondary) }
                        if !model.current { Text("Reconnect to read the current question.").font(.caption) }
                    }
                }
                WorkspacePeekOutcome(model: model.recap)
                Text("Preview only · your terminal stays where you left it · Esc to close")
                    .font(.caption).foregroundStyle(.secondary)
            }.padding(18).frame(maxWidth: .infinity, alignment: .leading)
        }.frame(width: 410, height: 430)
    }
}

struct WorkspacePeekOutcome: View {
    @ObservedObject var model: WorkspaceRecapModel
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Saved outcome").font(.headline)
            if let recap = model.report?.recap {
                Text(recap.latest.text.reveal()).textSelection(.enabled)
                if !recap.outstanding.text.reveal().isEmpty {
                    Text("Next: " + recap.outstanding.text.reveal()).font(.callout).textSelection(.enabled)
                }
                Text("Saved " + (WorkspaceActivityEntry.date(recap.generatedAt)?.formatted(date: .abbreviated, time: .shortened) ?? recap.generatedAt))
                    .font(.caption).foregroundStyle(.secondary)
                if model.report?.status != "ready" || recap.partial || model.error != nil {
                    Text("May omit newer messages or parts of the conversation.").font(.caption).foregroundStyle(.secondary)
                }
            } else { Text(model.loading ? "Reading saved context…" : "No saved outcome yet.").foregroundStyle(.secondary) }
            if let problem = model.error ?? model.report?.warning { Text(problem).font(.caption).foregroundStyle(.secondary) }
        }
    }
}

/// A read-only popover never takes the terminal's keyboard. Escape belongs to the
/// preview only while it is open; every other key keeps its normal destination.
@MainActor
final class WorkspaceContextPopover: NSObject, NSPopoverDelegate {
    let popover = NSPopover()
    var onClose: (() -> Void)?
    private var escapeMonitor: Any?
    override init() {
        super.init()
        popover.behavior = .semitransient
        popover.animates = WorkspaceMotion.enabled
        popover.delegate = self
    }
    func show<Content: View>(_ content: Content, of anchor: NSView, rect: NSRect? = nil) {
        let responder = anchor.window?.firstResponder
        let editor = responder as? NSTextView
        let field = editor?.isFieldEditor == true ? editor?.delegate as? NSTextField : nil
        let selection = editor?.selectedRange()
        popover.contentViewController = NSHostingController(rootView: content)
        popover.show(relativeTo: rect ?? anchor.bounds, of: anchor, preferredEdge: .maxX)
        if let field {
            anchor.window?.makeFirstResponder(field)
            if let selection { field.currentEditor()?.selectedRange = selection }
        } else if let responder { anchor.window?.makeFirstResponder(responder) }
        if escapeMonitor == nil {
            escapeMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
                let consumed = MainActor.assumeIsolated {
                    guard let self, self.popover.isShown, event.keyCode == 53 else { return false }
                    self.close(); return true
                }
                return consumed ? nil : event
            }
        }
    }
    func close() { popover.close(); cleanUp() }
    func popoverDidClose(_ notification: Notification) { cleanUp(); onClose?() }
    private func cleanUp() {
        if let escapeMonitor { NSEvent.removeMonitor(escapeMonitor) }
        escapeMonitor = nil
    }
}

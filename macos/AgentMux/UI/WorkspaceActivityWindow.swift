import AppKit
import SwiftUI

@MainActor
final class WorkspaceActivityWindow: NSObject, NSWindowDelegate {
    let model: WorkspaceActivityModel
    private(set) var window: NSWindow?
    var onVisibilityChanged: (Bool) -> Void = { _ in }
    init(model: WorkspaceActivityModel) { self.model = model }
    func show() {
        if window == nil {
            let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1000, height: 650),
                                  styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
            window.title = "Since Last Looked"
            window.minSize = NSSize(width: 760, height: 480)
            window.isReleasedWhenClosed = false
            window.delegate = self
            window.contentView = NSHostingView(rootView: WorkspaceActivityView(model: model))
            window.setFrameAutosaveName("WorkspaceActivity")
            window.center()
            self.window = window
        }
        onVisibilityChanged(true)
        model.refresh()
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }
    func windowWillClose(_ notification: Notification) { model.close(); onVisibilityChanged(false) }
}

struct WorkspaceActivityView: View {
    @ObservedObject var model: WorkspaceActivityModel
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Since Last Looked").font(.title2)
            HStack {
                Toggle("Unread only", isOn: $model.unreadOnly).onChange(of: model.unreadOnly) { _, _ in model.filterChanged() }
                Picker("Look back", selection: $model.days) {
                    Text("1 day").tag(1); Text("7 days").tag(7); Text("30 days").tag(30); Text("90 days").tag(90)
                }.frame(width: 180).onChange(of: model.days) { _, _ in model.refresh() }
                Spacer()

                Button("Refresh") { model.refresh() }.disabled(model.loading)
                Button("Mark shown read") { model.markVisibleRead() }.disabled(model.visible.isEmpty || model.loading || model.error != nil)
            }
            Text("Recorded state changes, process exits, and ASL events. Opening this view does not mark events read.")
                .font(.caption).foregroundStyle(.secondary)
            if let date = model.readState.lastMarkedAt { Text("Last marked read: \(date.formatted())").font(.caption).foregroundStyle(.secondary) }
            if model.loading { WorkspaceLoading(title: "Loading activity…", retained: model.report != nil) }
            if let error = model.error {
                WorkspaceNotice(message: error + (model.report == nil ? "" : " Showing the previous activity."), kind: .error,
                                action: { model.refresh() })
            }
            if let error = model.readError { WorkspaceNotice(message: error, kind: .error) }
            if let report = model.report {
                Text("\(model.visible.count) shown · \(report.entries.count) loaded · Retained history starts \(WorkspaceActivityEntry.date(report.earliestAvailable)?.formatted() ?? "at an unknown time")")
                    .font(.caption).foregroundStyle(.secondary)
                if report.truncated { Text("Showing the newest 1,000 of \(report.matched) matching events. Older events remain unread.").font(.caption) }
                if report.corruptLines > 0 { Text("\(report.corruptLines) unreadable journal records were skipped; history may be incomplete.").font(.caption) }
            }
            HSplitView {
                List(selection: Binding(get: { model.selection }, set: { model.select($0) })) {
                    ForEach(model.visible) { entry in
                        VStack(alignment: .leading, spacing: 4) {
                            HStack {
                                if model.readState.read[entry.id] == nil { Image(systemName: "circle.fill").font(.system(size: 6)).foregroundStyle(.blue) }
                                Text(entry.title).font(.headline)
                            }
                            Text(entry.date?.formatted() ?? entry.at).font(.caption).foregroundStyle(.secondary)
                            Text([entry.harness, entry.profile].filter { !$0.isEmpty }.joined(separator: " · ")).font(.caption)
                            Text(entry.detail).font(.caption).lineLimit(2)
                        }.padding(.vertical, 3).tag(entry.id)
                    }
                }.frame(minWidth: 220, idealWidth: 280, maxWidth: 340)
                    .overlay { if model.visible.isEmpty && !model.loading && model.error == nil {
                        WorkspaceEmpty(title: model.unreadOnly ? "All caught up" : "No activity found",
                                       detail: "No \(model.unreadOnly ? "unread" : "recorded") events in this period. Try a longer look-back period.")
                    } }
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        if let entry = model.selected {
                            Text(entry.title).font(.title2)
                            Text(entry.date?.formatted() ?? entry.at).foregroundStyle(.secondary)
                            Text(entry.detail).textSelection(.enabled)
                            HStack {
                                if entry.conversation != nil || !entry.runtimeSessionID.isEmpty {
                                    Button("Focus live session") { model.onFocus?(entry) }
                                }
                                if let identity = entry.conversation { Button("Open conversation") { model.onConversation?(identity) } }
                            }
                            Divider()
                            Text("Supporting output").font(.headline)
                            if model.reading { WorkspaceLoading(title: "Reading conversation…") }
                            if let error = model.transcriptError {
                                WorkspaceNotice(message: error, kind: .error, action: { model.loadTranscript() })
                            }
                            if let excerpt = model.excerpt {
                                Text("Last recorded assistant message at or before this event · \(excerpt.timestamp ?? "")").font(.caption).foregroundStyle(.secondary)
                                Text(excerpt.text.reveal()).textSelection(.enabled)
                            } else if !model.reading {
                                Text(entry.conversation == nil ? "This event has no recorded conversation identity." : "No timestamped assistant output at or before this event is available in the loaded messages.").foregroundStyle(.secondary)
                            }
                            if model.canLoadEarlier { Button("Load earlier messages") { model.loadEarlier() } }
                            Divider()
                            Text("Recorded event: \(entry.event)").font(.caption).textSelection(.enabled)
                            if !entry.runID.isEmpty { Text("Run: \(entry.runID)").font(.caption).textSelection(.enabled) }
                            if !entry.traceID.isEmpty { Text("Trace: \(entry.traceID)").font(.caption).textSelection(.enabled) }
                        } else { Text("Select an event to inspect its recorded details and supporting output.").foregroundStyle(.secondary) }
                    }.padding(12).frame(maxWidth: .infinity, alignment: .leading)
                }.frame(minWidth: 360, maxWidth: .infinity)
            }
        }.padding(12)
    }
}

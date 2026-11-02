import AppKit
import SwiftUI

@MainActor
final class WorkspaceBriefingWindow: ReviewWindowController {
    let model: WorkspaceBriefingModel
    init(model: WorkspaceBriefingModel) {
        self.model = model
        super.init(title: "Project Briefing", autosaveName: "WorkspaceBriefing", size: NSSize(width: 920, height: 760))
    }
    func show(project: String?) { model.open(preferredProject: project); present() }
    override func makeContentView() -> NSView { Self.host(WorkspaceBriefingView(model: model)) }
    override func willPresent() { model.refresh() }
    override var canClose: Bool { !model.acting }
    override func didClose() { model.close() }
}

struct WorkspaceBriefingView: View {
    @ObservedObject var model: WorkspaceBriefingModel
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label("Project Briefing", systemImage: "text.book.closed").font(.title2)
                Spacer()
                if model.acting { ProgressView().controlSize(.small); Text("Resuming conversation…").font(.caption) }
                if model.loading { ProgressView().controlSize(.small).accessibilityLabel("Refreshing project briefing") }
                Button { model.refresh() } label: { Image(systemName: "arrow.clockwise") }
                    .help("Refresh conversations and saved recaps (⌘R)")
                    .accessibilityLabel("Refresh project briefing")
                    .disabled(model.loading || model.acting || model.project.isEmpty)
            }
            HStack {
                Picker("Project", selection: Binding(get: { model.project }, set: { model.select($0) })) {
                    if model.projects.isEmpty { Text("No projects").tag("") }
                    ForEach(model.projects, id: \.self) { Text($0).tag($0) }
                }.frame(maxWidth: 400).disabled(model.acting)
                Spacer()
                if let at = model.updatedAt {
                    Text("Conversations updated " + at.formatted(date: .abbreviated, time: .shortened))
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            if !model.feedLive {
                Label("Live status is unavailable. Session actions and recap generation will return when the daemon reconnects.", systemImage: "wifi.slash")
                    .font(.callout).foregroundStyle(.secondary)
            }
            WorkspaceReadFailure(title: "Project conversations", error: model.error, retained: model.updatedAt != nil,
                                 loading: model.loading, retry: { model.refresh() })
            if let error = model.actionError { Text(error).foregroundStyle(.red).textSelection(.enabled) }
            if model.project.isEmpty {
                WorkspaceEmpty(title: "Choose a project", detail: "Configured projects and projects with live sessions appear here.", symbol: "folder")
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 20) {
                        needsYou
                        liveSessions
                        conversationCards
                    }.padding(4)
                }
            }
        }.padding(14)
            .background(WorkspaceReviewShortcuts { key, modifiers in
                guard key == "r", modifiers == .command else { return false }
                model.refresh(); return true
            }.frame(width: 0, height: 0))
            .accessibilityIdentifier("project-briefing")
    }

    private var needsYou: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Needs you · \(model.waiting.count)").font(.headline)
            if model.waiting.isEmpty {
                Text(model.feedLive ? "No questions, approvals, or other attention are waiting in this project." : "Waiting status cannot be checked yet.")
                    .foregroundStyle(.secondary).font(.callout)
            }
            ForEach(model.waiting, id: \.key) { row in
                HStack(alignment: .top) {
                    Text(row.glyph)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(row.title).font(.callout.weight(.semibold))
                        Text(row.detail).font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("Open Session") { model.focus(row.key) }.disabled(!model.feedLive || model.acting)
                }.padding(10).background(Color.accentColor.opacity(0.07), in: RoundedRectangle(cornerRadius: 8))
            }
        }
    }

    private var liveSessions: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Live sessions · \(model.rows.count)\(model.feedLive ? "" : " · last known")").font(.headline)
            if model.rows.isEmpty { Text("No live sessions listed for this project.").foregroundStyle(.secondary).font(.callout) }
            ForEach(model.rows, id: \.key) { row in
                HStack {
                    Text(row.glyph)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(row.displayName).font(.callout.weight(.semibold))
                        Text(row.detail).font(.caption).foregroundStyle(.secondary)
                        if row.conversation == nil { Text("Conversation identity not available yet.").font(.caption).foregroundStyle(.secondary) }
                    }
                    Spacer()
                    Button("Focus") { model.focus(row.key) }.disabled(!model.feedLive || model.acting)
                }
            }
        }
    }

    private var conversationCards: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Recent outcomes and unfinished work").font(.headline)
            Text("All live conversations plus the ten most recent conversations, across profiles and agents, including automated sessions. Refresh reads saved recaps; Generate Recap uses your configured summarization profile.")
                .font(.caption).foregroundStyle(.secondary)
            if model.total > 10 {
                Text("Showing the ten most recent of \(model.total) matching conversations, plus any other live conversations")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if model.conversations.isEmpty, !model.loading {
                Text("No conversations found for this project.").foregroundStyle(.secondary)
            }
            ForEach(model.conversations, id: \.selector) { item in
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        VStack(alignment: .leading, spacing: 3) {
                            Text([item.harness, item.profile].filter { !$0.isEmpty }.joined(separator: " · ")).font(.headline)
                            Text(item.selector).font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
                            if let at = item.lastAt {
                                Text("Last message " + at.formatted(date: .abbreviated, time: .shortened)).font(.caption).foregroundStyle(.secondary)
                            }
                        }
                        Spacer()
                        Button("Read Conversation") { model.onConversation?(item.identity, nil) }.disabled(model.acting)
                        Button(model.liveRows(for: item).isEmpty ? "Resume" : "Focus Session") { model.continueConversation(item) }
                            .disabled(!model.feedLive || model.acting)
                    }
                    Button(WorkspaceHandoffModel.title) { model.onHandoff?(item) }
                        .disabled(!model.feedLive || model.acting || model.onHandoff == nil)
                    if let recap = model.recaps[item.selector] {
                        WorkspaceRecapView(model: recap, generationWaitMessage: model.feedLive
                            ? "Recap generation is available after the agent finishes its turn."
                            : "Reconnect the daemon to check whether this agent is still working.")
                    }
                }.padding(12).frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color.primary.opacity(0.025), in: RoundedRectangle(cornerRadius: 10))
            }
            Text("Each recap describes its own conversation. Older unfinished work may be outside this briefing; open Conversation History to explore further.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }
}

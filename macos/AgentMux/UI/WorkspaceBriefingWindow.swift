import AppKit
import SwiftUI

@MainActor
final class WorkspaceBriefingWindow: ReviewWindowController {
    let model: WorkspaceCatchUpModel
    init(model: WorkspaceCatchUpModel) {
        self.model = model
        super.init(title: "Briefing", autosaveName: "WorkspaceBriefing", size: NSSize(width: 1000, height: 900))
    }
    func show(project: String? = nil) { model.open(project: project); present() }
    func showFlight(_ session: FlightSession, kind: String) {
        guard !model.briefing.acting else { return }
        model.openFlight(session, kind: kind); present()
    }
    override func makeContentView() -> NSView { Self.host(WorkspaceBriefingView(model: model)) }
    override func willPresent() { model.refresh() }
    override var canClose: Bool { !model.briefing.acting }
    override func didClose() { model.close() }
}

struct WorkspaceBriefingView: View {
    @ObservedObject var model: WorkspaceCatchUpModel
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label("Briefing", systemImage: "text.book.closed").font(.title2)
                Picker("Scope", selection: Binding(get: { model.briefing.project }, set: { model.select($0) })) {
                    Text("All projects").tag("")
                    ForEach(model.briefing.projects, id: \.self) { Text($0).tag($0) }
                }.frame(maxWidth: 400).disabled(model.briefing.acting)
                Spacer()
                Button("Refresh") { model.refresh() }.disabled(model.briefing.acting)
            }
            if !model.briefing.feedLive {
                Label("Live status is unavailable. Session actions and recap generation will be available when the connection returns.", systemImage: "wifi.slash")
                    .font(.callout).foregroundStyle(.secondary)
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    waiting
                    WorkspaceActivityView(model: model.activity, title: "Since you last looked", grouped: true,
                        outcomes: model.summary.recaps, sessionRows: model.briefing.rows,
                        onRefresh: { model.refresh() }, onReview: { model.briefing.review($0) })
                        .frame(height: 460)
                    if !model.briefing.project.isEmpty {
                        WorkspaceBriefingConversationsView(model: model.briefing)
                    }
                }.padding(4)
            }
        }.padding(14)
            .accessibilityIdentifier("briefing")
    }

    private var waiting: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Waiting now · \(model.briefing.waiting.count)").font(.headline)
            if model.briefing.waiting.isEmpty {
                Text(model.briefing.feedLive ? "Nothing is waiting on you." : "Waiting status cannot be checked yet.")
                    .foregroundStyle(.secondary).font(.callout)
            }
            ForEach(model.briefing.waiting, id: \.key) { row in
                HStack(alignment: .top) {
                    Text(row.glyph)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(row.title).font(.callout.weight(.semibold))
                        Text(row.detail).font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    if row.answer != nil {
                        Button("Answer…") { model.briefing.answer(row.key) }.disabled(!model.briefing.feedLive || model.briefing.acting)
                    }
                    Button("Open session") { model.briefing.focus(row.key) }.disabled(!model.briefing.feedLive || model.briefing.acting)
                }.padding(10).background(Color.accentColor.opacity(0.07), in: RoundedRectangle(cornerRadius: 8))
            }
        }
    }
}

struct WorkspaceBriefingConversationsView: View {
    @ObservedObject var model: WorkspaceBriefingModel
    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            if model.loading { ProgressView("Refreshing conversations…") }
            if model.acting { ProgressView("Opening conversation…") }
            WorkspaceReadFailure(title: "Project conversations", error: model.error, retained: model.updatedAt != nil,
                                 loading: model.loading, retry: { model.refresh() })
            if let error = model.actionError { Text(error).foregroundStyle(.red).textSelection(.enabled) }
            conversationCards
            DisclosureGroup("Live sessions · \(model.rows.count)") { liveSessions }
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
                        if row.conversation == nil { Text("This session’s conversation is not available yet.").font(.caption).foregroundStyle(.secondary) }
                    }
                    Spacer()
                    Button("Focus") { model.focus(row.key) }.disabled(!model.feedLive || model.acting)
                }
            }
        }
    }

    private var conversationCards: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Recent conversations").font(.headline)
            DisclosureGroup("About Briefing") {
                Text("Includes conversations from live sessions, the ten most recent conversations, and their linked history. Outcomes use saved recaps; choose Generate recap to create one. Older unfinished work may be outside Briefing.")
                    .font(.caption).foregroundStyle(.secondary)
            }.font(.caption)
            if model.total > 10 {
                Text("Showing the ten most recent of \(model.total) matching conversations, plus any other live conversations and linked history")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if model.conversations.isEmpty, !model.loading {
                Text("No conversations found for this project.").foregroundStyle(.secondary)
            }
            if let warning = model.threadWarning {
                Text("Conversation links unavailable: " + warning).font(.caption).foregroundStyle(.secondary)
            }
            ForEach(model.threads) { thread in
                VStack(alignment: .leading, spacing: 8) {
                    if thread.conversations.count > 1 {
                        Label("Thread · \(thread.conversations.count) conversations", systemImage: "link").font(.subheadline.weight(.semibold))
                    }
                    ForEach(thread.conversations, id: \.selector) { item in conversationCard(item) }
                }.padding(thread.conversations.count > 1 ? 10 : 0)
                    .overlay(RoundedRectangle(cornerRadius: 12).stroke(Color.secondary.opacity(thread.conversations.count > 1 ? 0.25 : 0)))
            }
            Text("Each recap describes its own conversation. Older unfinished work may be outside Briefing; open Conversation History to explore further.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }
    private func conversationCard(_ item: RecentConversation) -> some View {
        VStack(alignment: .leading, spacing: 16) {
            VStack(alignment: .leading, spacing: 4) {
                Text(item.conversationTitle).font(.title3.weight(.semibold)).lineLimit(2)
                Text([item.harness, item.profile].filter { !$0.isEmpty }.joined(separator: " · "))
                    .font(.caption).foregroundStyle(.secondary)
            }
            if let recap = model.recaps[item.selector] {
                WorkspaceBriefingOutcome(model: recap, generationWaitMessage: model.feedLive
                    ? "Recap generation is available after the agent finishes its turn."
                    : "Reconnect to check the agent’s status.")
            }
            ViewThatFits(in: .horizontal) {
                HStack { conversationActions(item) }
                VStack(alignment: .leading) { conversationActions(item) }
            }
            DisclosureGroup("Conversation details") {
                VStack(alignment: .leading, spacing: 8) {
                    Text(item.selector).textSelection(.enabled)
                    if let at = item.lastAt {
                        Text("Last message " + at.formatted(date: .abbreviated, time: .shortened))
                    }
                    if !model.liveRows(for: item).isEmpty {
                        Text(model.feedLive ? "Live" : "Last known live")
                    }
                    ConversationLinksView(links: model.threadLinks, conversation: item.identity,
                                          onOpen: { model.onConversation?($0, nil) })
                    Button(WorkspaceHandoffModel.title) { model.onHandoff?(item) }
                        .disabled(!model.feedLive || model.acting || model.onHandoff == nil)
                }.font(.caption).foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading).padding(.top, 6)
            }.font(.caption)
        }.padding(18).frame(maxWidth: .infinity, alignment: .leading)
            .background(Color.primary.opacity(0.025), in: RoundedRectangle(cornerRadius: 12))
    }

    @ViewBuilder private func conversationActions(_ item: RecentConversation) -> some View {
        Button(model.liveRows(for: item).isEmpty ? "Resume" : "Focus session") { model.continueConversation(item) }
            .disabled(!model.feedLive || model.acting)
        Button("Fork") { model.fork(item) }.disabled(!model.feedLive || model.acting || model.onFork == nil)
        Button(WorkspaceHandoffModel.title) { model.onHandoff?(item) }
            .disabled(!model.feedLive || model.acting || model.onHandoff == nil)
        Button("Read conversation") { model.onConversation?(item.identity, nil) }.disabled(model.acting)
    }
}

/// The outcome leads; provenance and generation controls remain one disclosure away.
struct WorkspaceBriefingOutcome: View {
    @ObservedObject var model: WorkspaceRecapModel
    let generationWaitMessage: String
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            if let recap = model.report?.recap {
                Text(recap.latest.text.reveal()).font(.body).textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                if !recap.outstanding.text.reveal().isEmpty {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("Next").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                        Text(recap.outstanding.text.reveal()).font(.callout).textSelection(.enabled)
                    }
                }
                Text("Saved " + (WorkspaceActivityEntry.date(recap.generatedAt)?.formatted(date: .abbreviated, time: .shortened) ?? recap.generatedAt))
                    .font(.caption).foregroundStyle(.secondary)
                if model.report?.status != "ready" || model.error != nil || recap.partial {
                    Text("Saved context may be incomplete or omit newer messages.").font(.caption).foregroundStyle(.secondary)
                }
                DisclosureGroup("Goal, sources & recap controls") {
                    WorkspaceRecapView(model: model, generationWaitMessage: generationWaitMessage)
                }.font(.caption)
            } else {
                WorkspaceRecapView(model: model, generationWaitMessage: generationWaitMessage)
            }
        }
    }
}

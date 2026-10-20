import AppKit
import SwiftUI

struct WorkspaceRunsView: View {
    @ObservedObject var model: WorkspaceRunsModel
    @ObservedObject var plan: WorkspaceRunPlanModel
    private struct Confirmation { let method: String; let row: ASLRow }
    private enum Focus: Hashable { case list, details }
    @FocusState private var focus: Focus?
    @State private var evidenceExpanded = false
    @State private var confirmation: Confirmation?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Button(action: toggleRuns) { Image(systemName: "sidebar.left") }
                    .help("Show or hide runs (⌘⌥S)")
                    .accessibilityLabel(model.layout.collapsed ? "Show runs" : "Hide runs")
                Text("ASL Runs").font(.title2).fixedSize()
                Spacer(minLength: 0)
                WorkspaceReviewStatus(reads: [
                    .init(title: "Run list", loading: model.loading, retained: !model.rows.isEmpty, refreshedAt: model.listRefreshedAt,
                          feedback: runFeedback, retry: { model.refresh() }),
                    .init(title: "Plan progress", loading: model.reading, retained: model.detail != nil, refreshedAt: model.detailRefreshedAt,
                          feedback: model.detailError.map { WorkspaceFeedback(target: model.selected?.displayLabel ?? "Plan", message: $0) }, retry: { model.refresh() })
                ])
                WorkspaceStatus(loading: model.acting, title: "Checking operation…", feedback: model.actionFeedback,
                                recover: { if model.actionFeedback?.recovery == .receipt { model.retryReceipt() } else { model.refresh() } }, compactFeedback: true)
                    .frame(width: 24)
                WorkspaceFeedbackHistoryButton(history: model.feedbackHistory)
                Button("Refresh") { model.refresh() }.disabled(model.loading).help("Refresh runs (⌘R)")
                Button("Run a Plan…") { plan.isPresented = true }
            }
            WorkspaceReadFailure(title: "Runs", error: model.error, retained: !model.rows.isEmpty,
                                 loading: model.loading, retry: { model.refresh() })
            WorkspaceReviewSplit(layout: $model.layout, title: "Runs", minimum: 190, detailMinimum: 430) {
                List(selection: Binding(get: { model.selection }, set: { model.select($0) })) {
                    ForEach(model.rows, id: \.rowID) { row in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(row.displayLabel).font(.headline)
                            Text(WorkspaceWording.status(row.primaryState) + (row.sessionID.isEmpty ? "" : " · session \(row.sessionID)")).font(.caption)
                            if !row.pauseReason.isEmpty {
                                Label(WorkspaceWording.status(row.pauseReason), systemImage: "exclamationmark.circle")
                                    .font(.caption).foregroundStyle(WorkspaceReviewColors.warning)
                            }
                            if let question = row.question, question.unansweredCount > 0 {
                                Text("\(question.unansweredCount) unanswered").font(.caption)
                            }
                        }.tag(row.rowID)
                    }
                }.focused($focus, equals: .list)
            } detail: {
                VStack(alignment: .leading, spacing: 8) {
                    if let row = model.selected {
                        runHeader(row)
                        WorkspaceReadFailure(title: "Plan details", error: model.detailError, retained: model.detail != nil,
                                             loading: model.reading, retry: { model.refresh() })
                        Divider()
                        ScrollViewReader { proxy in
                        ScrollView {
                            VStack(alignment: .leading, spacing: 12) {
                                ForEach(row.warnings, id: \.self) { Text($0).foregroundStyle(WorkspaceReviewColors.warning) }
                                DisclosureGroup("Run details") {
                                    VStack(alignment: .leading, spacing: 8) {
                                        WorkspacePath(path: row.planDir)
                                        Text("Run \(row.runID)").textSelection(.enabled)
                                        Text("\(row.harness) · \(WorkspaceWording.status(row.phase)) · \(WorkspaceWording.status(row.activityLabel))")
                                        if !row.runStartedAt.isEmpty { WorkspaceTimestamp(title: "Started", value: row.runStartedAt) }
                                        if !row.runFinishedAt.isEmpty { WorkspaceTimestamp(title: "Finished", value: row.runFinishedAt) }
                                        if !row.lastActivityAt.isEmpty { WorkspaceTimestamp(title: "Last activity", value: row.lastActivityAt) }
                                        Button("Show Plan") { NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: row.planDir) }
                                    }.font(.callout).frame(maxWidth: .infinity, alignment: .leading).padding(.top, 6)
                                }
                                if let detail = model.detail {
                                    Text("Plan progress").font(.headline)
                                    ForEach(detail.sessions) { session in
                                        HStack {
                                            Text("Session \(session.id)")
                                            Text(model.verificationBlocked && session.id == row.sessionID && session.status == "pending" ? "Incomplete" : model.sessionStatus(session)).foregroundStyle(.secondary)
                                            Spacer()
                                            Button("Show in Finder") { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: session.path)]) }
                                        }
                                    }
                                    if let active = detail.runner.activeVerification {
                                        Text("Checking \(active.currentCheckName) · session \(active.sessionID) · \(active.checkCount) checks")
                                    }
                                    if let error = detail.verificationError { Text(error).foregroundStyle(WorkspaceReviewColors.failure) }
                                    DisclosureGroup("Recorded verification", isExpanded: $evidenceExpanded) {
                                        VStack(alignment: .leading, spacing: 8) {
                                            if let seconds = detail.runner.durationSeconds { Text("Recorded duration: \(seconds.formatted(.number.precision(.fractionLength(0)))) seconds") }
                                            if detail.verificationTruncated { Text("Showing bounded recent evidence; earlier results may be omitted.").foregroundStyle(WorkspaceReviewColors.warning) }
                                            Text("Recorded outcomes only. Open the session to investigate command output.").font(.caption).foregroundStyle(.secondary)
                                            if model.inspectedVerification?.event == "verification_finished" {
                                                Text("Only the attempt summary is available; individual failed checks were not retained.").font(.caption).foregroundStyle(.secondary)
                                            }
                                            if detail.verification.isEmpty { Text("No verification results recorded in the available evidence for this run.").foregroundStyle(.secondary) }
                                            ForEach(Array(detail.verification.enumerated()), id: \.offset) { index, record in
                                                VStack(alignment: .leading) {
                                                    Text(record.summary)
                                                    WorkspaceTimestamp(title: "Session \(record.sessionID)", value: record.at)
                                                    if let seconds = record.durationSeconds { Text("Duration: \(seconds.formatted(.number.precision(.fractionLength(1)))) seconds").font(.caption) }
                                                }
                                                .textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading).padding(8)
                                                .background(model.inspectedVerificationIndex == index ? WorkspaceReviewColors.warning.opacity(0.12) : Color.clear, in: RoundedRectangle(cornerRadius: 6))
                                                .overlay(alignment: .leading) {
                                                    if model.inspectedVerificationIndex == index { Rectangle().fill(WorkspaceReviewColors.warning).frame(width: 3) }
                                                }
                                                .accessibilityLabel((model.inspectedVerificationIndex == index ? "Selected failed check. " : "") + record.summary)
                                                .id(index)
                                            }
                                        }.frame(maxWidth: .infinity, alignment: .leading).padding(.top, 6)
                                    }
                                }
                            }.padding(4).frame(maxWidth: .infinity, alignment: .leading)
                        }.accessibilityIdentifier("runs-details").focusable().focused($focus, equals: .details)
                            .task(id: model.inspectionRequest) {
                                guard model.inspectionRequest != nil else { return }
                                evidenceExpanded = true
                                await Task.yield()
                                guard !Task.isCancelled else { return }
                                if let index = model.inspectedVerificationIndex { proxy.scrollTo(index, anchor: .center) }
                                focus = .details
                            }
                        }
                    } else if !model.loading && model.error == nil {
                        WorkspaceEmpty(title: model.rows.isEmpty ? "No active runs" : "Run progress",
                                       detail: model.rows.isEmpty ? "Start a plan to follow its progress here." : "Select a run to inspect its progress and controls.", symbol: "list.bullet.rectangle")
                    }
                }.padding(8).frame(minWidth: 430, maxWidth: .infinity, maxHeight: .infinity)

            }
        }.padding(12)
            .onAppear { focus = model.layout.collapsed ? .details : .list }
            .onChange(of: model.selection) { _, _ in evidenceExpanded = false }
            .background(WorkspaceReviewShortcuts(handle: handleShortcut).frame(width: 0, height: 0))
            .sheet(isPresented: $plan.isPresented) { WorkspaceRunPlanView(model: plan, onShowRuns: { plan.isPresented = false; model.refresh() }) }
            .alert(confirmation?.method == ASLMethod.runAbort ? "Abort this run?" : "Stop after the current session?",
                   isPresented: Binding(get: { confirmation != nil }, set: { if !$0 { confirmation = nil } })) {
                Button("Cancel", role: .cancel) { confirmation = nil }
                Button(confirmation?.method == ASLMethod.runAbort ? "Abort Run" : "Stop After Current Session", role: .destructive) {
                    if let confirmation { model.perform(confirmation.method, target: confirmation.row) }
                    confirmation = nil
                }
            } message: {
                Text("\(confirmation?.row.displayLabel ?? "Run") · \(confirmation?.row.runID ?? "")\n" + (confirmation?.method == ASLMethod.runAbort ? "Request that ASL stop the active run." : "ASL will finish the current session before stopping."))
            }
    }

    private func runHeader(_ row: ASLRow) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(row.displayLabel).font(.headline).lineLimit(1).help(row.displayLabel)
            Text(WorkspaceWording.status(row.primaryState) + (row.sessionID.isEmpty ? "" : " · Session " + row.sessionID))
                .font(.callout).lineLimit(1)
            if let detail = model.detail {
                Text(detail.progressSummary).font(.caption).lineLimit(2)
            }
            if model.hasQuestion {
                Label("Waiting for your answer", systemImage: "questionmark.circle").foregroundStyle(WorkspaceReviewColors.warning)
            } else if !row.pauseReason.isEmpty && !model.verificationBlocked {
                Label(WorkspaceWording.status(row.pauseReason), systemImage: "exclamationmark.circle").foregroundStyle(WorkspaceReviewColors.warning)
                    .lineLimit(2).help(WorkspaceWording.status(row.pauseReason))
            }
            if model.verificationBlocked, let detail = model.detail {
                let evidence = detail.blockingVerification(sessionID: row.sessionID)
                if let first = evidence.first {
                    Label(first.summary + (evidence.count > 1 ? " · \(evidence.count - 1) more failed checks" : ""), systemImage: "exclamationmark.circle.fill")
                        .foregroundStyle(WorkspaceReviewColors.warning).font(.callout.weight(.semibold)).lineLimit(2).textSelection(.enabled)
                        .help(evidence.map(\.summary).joined(separator: "\n"))
                    Text("Review the recorded failure before resuming.").font(.caption).foregroundStyle(.secondary)
                } else if !model.reading {
                    Text("No matching failed check is available in the recorded evidence.").font(.caption).foregroundStyle(.secondary)
                }
            }
            HStack {
                if model.hasQuestion {
                    Button("Answer…") { model.answer() }.buttonStyle(.borderedProminent).fontWeight(.semibold).disabled(model.stale || model.acting)
                }
                if model.nextAction == .inspect {
                    Button(model.inspectionTitle) { model.inspectFailedCheck() }
                        .buttonStyle(.borderedProminent).fontWeight(.semibold)
                        .disabled(model.inspectionUnavailable != nil)
                        .help(model.inspectionUnavailable ?? "Show the failed check in recorded verification (⌘⇧I)")
                    Button("Open Session") { model.onFocus?(row) }.buttonStyle(.link).help("Open session (⌘Return)")
                } else if model.nextAction == .focus {
                    Button("Focus Session") { model.onFocus?(row) }
                        .buttonStyle(.borderedProminent).fontWeight(.semibold).help("Focus session (⌘Return)")
                } else {
                    Button("Focus Session") { model.onFocus?(row) }.help("Focus session (⌘Return)")
                }
            }
            HStack(alignment: .top) {
                control("Resume", ASLMethod.runResume)
                control("Stop After Session…", ASLMethod.runStopAfterCurrent)
                control("Abort…", ASLMethod.runAbort)
            }
        }.accessibilityIdentifier("runs-controls")
    }

    private var runFeedback: WorkspaceFeedback? {
        if let error = model.error { return WorkspaceFeedback(target: "ASL runs", message: error,
            nextStep: "Previous runs remain visible. Refresh to recheck the connection; this does not repeat a control operation.") }
        let warnings = model.warnings + (model.truncated ? ["ASL truncated this run list. Some runs are not shown."] : [])
        return warnings.isEmpty ? nil : WorkspaceFeedback(target: "ASL runs", message: warnings.joined(separator: "\n"), kind: .warning)
    }
    private func handleShortcut(_ key: String, _ modifiers: NSEvent.ModifierFlags) -> Bool {
        guard !plan.isPresented && confirmation == nil else { return false }
        if modifiers == .command {
            switch key {
            case "r": if !model.loading { model.refresh() }
            case "\r": if let row = model.selected { model.onFocus?(row) }
            default: return false
            }
        } else if modifiers == [.command, .shift], key == "f" { model.layout.collapsed = false; focus = .list }
        else if modifiers == [.command, .shift], key == "i" { model.inspectFailedCheck() }
        else if modifiers == [.command, .option], key == "s" { toggleRuns() }
        else { return false }
        return true
    }

    private func toggleRuns() {
        model.layout.collapsed.toggle()
        focus = model.layout.collapsed ? .details : .list
    }

    private func control(_ title: String, _ method: String) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            if method == ASLMethod.runResume {
                if model.nextAction == .resume {
                    Button(title) { model.perform(method) }.buttonStyle(.borderedProminent)
                        .fontWeight(.semibold)
                        .disabled(model.disabledReason(method) != nil)
                } else {
                    Button(title) { model.perform(method) }.disabled(model.disabledReason(method) != nil)
                }
            } else {
                Button(title) { if let row = model.selected { confirmation = Confirmation(method: method, row: row) } }
                    .buttonStyle(.borderless).controlSize(.small)
                    .disabled(model.disabledReason(method) != nil)
            }
            if let reason = model.disabledReason(method) { Text(reason).font(.caption2).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true) }
        }.frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct WorkspaceRunPlanView: View {
    @ObservedObject var model: WorkspaceRunPlanModel
    var onShowRuns: (() -> Void)?
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                Text("Run a Plan").font(.title2)
                Text("Start a supervising agent in the workspace after ASL preflight passes.").foregroundStyle(.secondary)
                HStack {
                    TextField("Plan directory or configured plan name", text: $model.plan)
                    Button("Choose…") {
                        let picker = NSOpenPanel()
                        picker.canChooseFiles = false; picker.canChooseDirectories = true; picker.allowsMultipleSelection = false
                        if picker.runModal() == .OK, let url = picker.url { model.plan = url.path }
                    }
                }
                WorkspaceChoicePicker(title: "Project", selection: $model.project, label: model.projectLabel,
                                      defaultLabel: "Infer from plan workspace",
                                      choices: model.projects.map {
                    WorkspaceChoice(id: $0.profile + "/" + $0.title, label: $0.title + " · " + $0.profile,
                                    detail: $0.directory, problem: $0.directoryProblem)
                })
                if let problem = model.projectProblem { Text(problem).font(.caption).foregroundStyle(WorkspaceReviewColors.warning) }
                WorkspaceChoicePicker(title: "Supervisor", selection: $model.profile, label: model.profileLabel,
                                      defaultLabel: "Use project default",
                                      choices: model.profiles.map {
                    WorkspaceChoice(id: $0.name, label: $0.name, detail: $0.harness)
                })
                if let problem = model.profileProblem { Text(problem).font(.caption).foregroundStyle(WorkspaceReviewColors.warning) }
                Text(model.destinationSummary).font(.caption).foregroundStyle(.secondary)
                if model.loadingChoices { WorkspaceLoading(title: "Loading projects and profiles…") }
                if let error = model.choicesError {
                    WorkspaceNotice(message: error, kind: .error, actionTitle: "Reload Choices", action: { model.loadChoices() })
                }
                HStack {
                    Picker("Worker", selection: $model.harness) {
                        Text("ASL default").tag("")
                        ForEach(["codex", "claude", "copilot"], id: \.self) { Text($0).tag($0) }
                    }
                    Picker("Final review", selection: $model.reviewHarness) {
                        Text("Default / none").tag("")
                        ForEach(["codex", "claude", "copilot"], id: \.self) { Text($0).tag($0) }
                    }
                }
                Picker("Commit", selection: $model.commitMode) {
                    Text("ASL default").tag("")
                    Text("After each session").tag("per-session")
                    Text("At the end").tag("at-end")
                }
                HStack {
                    Button("Check Readiness") { model.check() }.disabled(model.checking || model.launching || model.selector.isEmpty || model.projectProblem != nil || model.profileProblem != nil)
                    Spacer()
                    Button("Start Supervised Run") { model.start() }.disabled(!model.canStart)
                }
                if model.optionsChanged { Text("Options changed. Check readiness again.").foregroundStyle(WorkspaceReviewColors.warning) }
                if model.submitted.contains(model.selector) { Text("A launch for this plan was submitted. Check Runs and the workspace for its supervising session.").foregroundStyle(.secondary) }
                WorkspaceStatus(loading: model.checking || model.launching,
                                title: model.launching ? "Starting supervised run…" : "Checking readiness…", showTarget: true, feedback: model.feedback,
                                recover: { if model.feedback?.recovery == .runs { onShowRuns?() } else { model.check() } }, history: model.feedbackHistory)
                if let preview = model.preview {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("\(WorkspaceWording.status(preview.readiness.state)) · \(preview.readiness.complete) complete · \(preview.readiness.pending) pending").font(.headline)
                        Text(preview.project.isEmpty ? "Choose a project above; the plan workspace does not identify one unambiguously." : "Project: \(preview.project) · supervisor: \(preview.profile)")
                        Text(preview.directory).font(.caption)
                        Text(preview.command).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                        ForEach(Array(preview.readiness.findings.enumerated()), id: \.offset) { _, finding in
                            Text("\(WorkspaceWording.status(finding.severity)) · \(WorkspaceWording.status(finding.category)): \(finding.message)\n\(finding.detail ?? "")")
                        }
                    }.frame(maxWidth: .infinity, alignment: .leading)
                }
                HStack { Spacer(); Button("Done") { model.isPresented = false }.disabled(model.launching) }
            }.padding(20)
        }.frame(minWidth: 680, idealWidth: 680, maxWidth: .infinity,
                minHeight: 480, idealHeight: 580, maxHeight: .infinity)
            .textFieldStyle(.roundedBorder)
            .onAppear { model.loadChoices() }
            .disabled(model.launching)
            .interactiveDismissDisabled(model.launching)
    }
}

@MainActor
final class WorkspaceRunsWindow: ReviewWindowController {
    let model: WorkspaceRunsModel
    let plan: WorkspaceRunPlanModel
    private var launching = false
    init(model: WorkspaceRunsModel, plan: WorkspaceRunPlanModel) {
        self.model = model; self.plan = plan
        super.init(title: "ASL Runs", autosaveName: "WorkspaceRuns", size: NSSize(width: 1100, height: 740))
        model.revealFeedback = { [weak self] in self?.show() }
        plan.revealFeedback = { [weak self] in self?.show() }
        plan.onFeedbackRuns = { [weak self] in self?.plan.isPresented = false; self?.show() }
    }

    func show(launch: Bool = false) {
        launching = launch
        present()
    }
    override func makeContentView() -> NSView { Self.host(WorkspaceRunsView(model: model, plan: plan)) }
    override func willPresent() {
        model.start()
        if launching { plan.isPresented = true }
        launching = false
    }
    /// Runs poll only while someone can see them.
    override func readingLiveChanged(_ live: Bool) { model.setAutomaticRefreshEnabled(live) }
    override var canClose: Bool { !model.acting && !plan.launching }
    override func didClose() { model.close() }
}

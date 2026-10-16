import AppKit
import SwiftUI

struct WorkspaceRunsView: View {
    @ObservedObject var model: WorkspaceRunsModel
    @ObservedObject var plan: WorkspaceRunPlanModel
    private struct Confirmation { let method: String; let row: ASLRow }
    @State private var confirmation: Confirmation?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("ASL Runs").font(.title2)
                Spacer()

                Button("Refresh") { model.refresh() }.disabled(model.loading)
                Button("Run a Plan…") { plan.isPresented = true }
            }
            if model.loading { WorkspaceLoading(title: "Refreshing runs…", retained: !model.rows.isEmpty) }
            if let error = model.error {
                WorkspaceNotice(message: error + (error.hasSuffix(".") ? "" : ".") + (model.rows.isEmpty ? "" : " Showing the previous run list."), kind: .error,
                                action: { model.refresh() })
            }
            if model.truncated { WorkspaceNotice(message: "ASL truncated this run list. Some runs are not shown.", kind: .warning) }
            ForEach(model.warnings, id: \.self) { WorkspaceNotice(message: $0, kind: .warning) }
            if let notice = model.actionNotice { Text(notice).textSelection(.enabled) }
            if model.pending != nil {
                Button("Retry Receipt") { model.retryReceipt() }.disabled(model.acting)
            }
            HSplitView {
                List(selection: Binding(get: { model.selection }, set: { model.select($0) })) {
                    ForEach(model.rows, id: \.rowID) { row in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(row.displayLabel).font(.headline)
                            Text(WorkspaceWording.status(row.primaryState) + (row.sessionID.isEmpty ? "" : " · session \(row.sessionID)")).font(.caption)
                            if !row.pauseReason.isEmpty { Text(WorkspaceWording.status(row.pauseReason)).font(.caption).foregroundStyle(.orange) }
                            if let question = row.question, question.unansweredCount > 0 {
                                Text("\(question.unansweredCount) unanswered").font(.caption)
                            }
                        }.tag(row.rowID)
                    }
                }.frame(minWidth: 190, idealWidth: 260, maxWidth: 300)
                ScrollView {
                    if let row = model.selected {
                        VStack(alignment: .leading, spacing: 12) {
                            Text(row.displayLabel).font(.title2)
                            Text(row.planDir).font(.caption).textSelection(.enabled)
                            Text("\(row.harness) · \(WorkspaceWording.status(row.phase)) · \(WorkspaceWording.status(row.activityLabel))")
                            Text("Run \(row.runID)").font(.caption).textSelection(.enabled)
                            if !row.sessionID.isEmpty { Text("Current session: \(row.sessionID)") }
                            if !row.runStartedAt.isEmpty { WorkspaceTimestamp(title: "Started", value: row.runStartedAt) }
                            if !row.runFinishedAt.isEmpty { WorkspaceTimestamp(title: "Finished", value: row.runFinishedAt) }
                            if !row.lastActivityAt.isEmpty { WorkspaceTimestamp(title: "Last activity", value: row.lastActivityAt) }
                            if !row.pauseReason.isEmpty { Text("Blocked: \(WorkspaceWording.status(row.pauseReason))").foregroundStyle(.orange) }
                            ForEach(row.warnings, id: \.self) { Text($0).foregroundStyle(.orange) }
                            HStack {
                                Button("Focus Session") { model.onFocus?(row) }
                                Button("Show Plan") { NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: row.planDir) }
                                if model.hasQuestion { Button("Answer…") { model.answer() }.disabled(model.stale || model.acting) }
                            }
                            HStack {
                                control("Resume", ASLMethod.runResume)
                                control("Stop After Current…", ASLMethod.runStopAfterCurrent)
                                control("Abort…", ASLMethod.runAbort)
                            }
                            if let error = model.detailError {
                                WorkspaceNotice(message: error + (error.hasSuffix(".") ? "" : ".") + (model.detail == nil ? "" : " Showing the previous progress."), kind: .error)
                            }
                            if model.reading { WorkspaceLoading(title: "Reading plan progress…", retained: model.detail != nil) }
                            if let detail = model.detail {
                                Divider()
                                Text("Plan progress").font(.headline)
                                Text("\(detail.counts["complete", default: 0]) complete · \(detail.counts["pending", default: 0]) pending · \(detail.counts["empty", default: 0]) empty · \(detail.counts["unknown", default: 0]) unknown")
                                if let seconds = detail.runner.durationSeconds { Text("Recorded duration: \(seconds.formatted(.number.precision(.fractionLength(0)))) seconds").font(.caption) }
                                ForEach(detail.sessions) { session in
                                    HStack {
                                        Text("Session \(session.id)")
                                        Text(WorkspaceWording.status(session.status)).foregroundStyle(.secondary)
                                        Spacer()
                                        Button("Reveal") { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: session.path)]) }
                                    }
                                }
                                Divider()
                                Text("Recorded verification").font(.headline)
                                if let active = detail.runner.activeVerification {
                                    Text("Running \(active.currentCheckName) · session \(active.sessionID) · \(active.checkCount) checks")
                                }
                                if let error = detail.verificationError { Text(error).foregroundStyle(.red) }
                                if detail.verificationTruncated { Text("Showing bounded recent evidence; earlier results may be omitted.").foregroundStyle(.orange) }
                                if detail.verification.isEmpty { Text("No verification results recorded in the available evidence for this run.").foregroundStyle(.secondary) }
                                ForEach(Array(detail.verification.enumerated()), id: \.offset) { _, record in
                                    VStack(alignment: .leading) {
                                        Text(record.summary)
                                        WorkspaceTimestamp(title: "Session \(record.sessionID)", value: record.at)
                                    }
                                }
                            }
                        }.padding(12).frame(maxWidth: .infinity, alignment: .leading)
                    } else if !model.loading && model.error == nil {
                        WorkspaceEmpty(title: model.rows.isEmpty ? "No active runs" : "Run progress",
                                       detail: model.rows.isEmpty ? "Start a plan to follow its progress here." : "Select a run to inspect its progress and controls.", symbol: "list.bullet.rectangle")
                    }
                }.frame(minWidth: 430, maxWidth: .infinity, maxHeight: .infinity)
            }
        }.padding(12)
            .sheet(isPresented: $plan.isPresented) { WorkspaceRunPlanView(model: plan) }
            .alert(confirmation?.method == ASLMethod.runAbort ? "Abort this run?" : "Stop after the current session?",
                   isPresented: Binding(get: { confirmation != nil }, set: { if !$0 { confirmation = nil } })) {
                Button("Cancel", role: .cancel) { confirmation = nil }
                Button("Confirm", role: .destructive) {
                    if let confirmation { model.perform(confirmation.method, target: confirmation.row) }
                    confirmation = nil
                }
            } message: {
                Text("\(confirmation?.row.displayLabel ?? "Run") · \(confirmation?.row.runID ?? "")\n" + (confirmation?.method == ASLMethod.runAbort ? "Request that ASL stop the active run." : "ASL will finish the current session before stopping."))
            }
    }

    private func control(_ title: String, _ method: String) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Button(title) {
                if method == ASLMethod.runResume { model.perform(method) }
                else if let row = model.selected { confirmation = Confirmation(method: method, row: row) }
            }.disabled(model.disabledReason(method) != nil)
            if let reason = model.disabledReason(method) { Text(reason).font(.caption2).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true) }
        }.frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct WorkspaceRunPlanView: View {
    @ObservedObject var model: WorkspaceRunPlanModel
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
                if let problem = model.projectProblem { Text(problem).font(.caption).foregroundStyle(.orange) }
                WorkspaceChoicePicker(title: "Supervisor", selection: $model.profile, label: model.profileLabel,
                                      defaultLabel: "Use project default",
                                      choices: model.profiles.map {
                    WorkspaceChoice(id: $0.name, label: $0.name, detail: $0.harness)
                })
                if let problem = model.profileProblem { Text(problem).font(.caption).foregroundStyle(.orange) }
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
                    if model.checking || model.launching { ProgressView().controlSize(.small) }
                    Spacer()
                    Button("Start Supervised Run") { model.start() }.disabled(!model.canStart)
                }
                if model.optionsChanged { Text("Options changed. Check readiness again.").foregroundStyle(.orange) }
                if model.submitted.contains(model.selector) { Text("A launch for this plan was submitted. Check Runs and the workspace for its supervising session.").foregroundStyle(.secondary) }
                if let notice = model.notice { Text(notice).textSelection(.enabled) }
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
final class WorkspaceRunsWindow: NSObject, NSWindowDelegate {
    let model: WorkspaceRunsModel
    let plan: WorkspaceRunPlanModel
    private(set) var window: NSWindow?
    var onVisibilityChanged: (Bool) -> Void = { _ in }
    init(model: WorkspaceRunsModel, plan: WorkspaceRunPlanModel) { self.model = model; self.plan = plan }

    func show(launch: Bool = false) {
        if window == nil {
            let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1100, height: 740),
                styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
            window.title = "ASL Runs"
            window.minSize = NSSize(width: 780, height: 500)
            window.isReleasedWhenClosed = false; window.delegate = self
            window.contentView = NSHostingView(rootView: WorkspaceRunsView(model: model, plan: plan))
            window.setFrameAutosaveName("WorkspaceRuns"); window.center()
            self.window = window
        }
        model.start()
        if launch { plan.isPresented = true }
        onVisibilityChanged(true)
        window?.makeKeyAndOrderFront(nil); NSApp.activate()
    }
    func windowShouldClose(_ sender: NSWindow) -> Bool { !model.acting && !plan.launching }
    func windowWillClose(_ notification: Notification) { model.close(); onVisibilityChanged(false) }
}

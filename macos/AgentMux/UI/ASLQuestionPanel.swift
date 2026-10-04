import AppKit
import os
import SwiftUI

// The ASL answer panel: a floating panel that answers one run's supervisor
// questions through the daemon, `asl_snapshot` (for the row and its token),
// then `asl_question` per body, then `asl_action` per submission. The rules
// live in `ASLResponseState`; `ASLQuestionFlow` runs the requests they ask
// for, and the controller owns the window.

/// The three daemon requests the panel makes. Blocking: called off the main
/// thread.
protocol ASLService {
    func snapshot() -> Result<ASLResult, ASLRequestError>
    func question(planDir: String, questionID: String) -> Result<ASLQuestionBody, ASLRequestError>
    func act(_ params: ASLRequestParams) -> Result<ASLOperation, ASLRequestError>
}

extension DaemonClient: ASLService {
    func snapshot() -> Result<ASLResult, ASLRequestError> { aslSnapshot() }
    func question(planDir: String, questionID: String) -> Result<ASLQuestionBody, ASLRequestError> {
        aslQuestion(planDir: planDir, questionID: questionID)
    }
    func act(_ params: ASLRequestParams) -> Result<ASLOperation, ASLRequestError> { aslAction(params) }
}

/// Runs blocking work somewhere and hands its result back on the main thread.
protocol ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void)
}

/// Work on a serial queue, results on main.
struct QueueWorkRunner: ASLWorkRunner {
    var queue = DispatchQueue(label: "io.github.sglavoie.agentmux.asl", qos: .userInitiated)

    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) {
        queue.async {
            let value = work()
            DispatchQueue.main.async { then(value) }
        }
    }
}

/// Drives one panel's requests. Main-thread confined; replies for a form that
/// has since closed, moved on, or been reopened are dropped by sequence.
@MainActor
final class ASLQuestionFlow: ObservableObject {
    private nonisolated static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "asl")

    enum Phase: Equatable {
        case idle
        /// Waiting for the snapshot that names the row and its token.
        case loading
        case open
        /// The form may not open; the fixed sentence says why.
        case refused(String)
    }

    @Published private(set) var phase = Phase.idle
    @Published private(set) var state = ASLResponseState()
    /// The run the panel was opened for.
    private(set) var answering: ASLAnswerTarget?

    /// The form closed itself: answered, or its run moved. The notice is set
    /// when the run moved.
    var onClose: (_ notice: String?) -> Void = { _ in }

    private let service: ASLService
    private let runner: ASLWorkRunner
    private let mint: () -> String
    private var openSeq = 0

    init(service: ASLService = DaemonClient(), runner: ASLWorkRunner = QueueWorkRunner(),
         mint: @escaping () -> String = ASLResponse.newOperationID) {
        self.service = service
        self.runner = runner
        self.mint = mint
    }

    /// Opens the form for `target`. Asking again for the run already open
    /// keeps it, drafts and all.
    func open(_ target: ASLAnswerTarget) {
        if answering == target, phase == .loading || phase == .open { return }
        reset()
        answering = target
        phase = .loading
        let seq = openSeq
        let service = self.service
        runner.run({ service.snapshot() }) { [weak self] result in
            MainActor.assumeIsolated { self?.snapshotLoaded(seq: seq, target: target, result) }
        }
    }

    /// Drops the form, its body, and every draft, without telling `onClose`:
    /// the operator closed the panel.
    func cancel() {
        reset()
    }

    func setDraft(_ text: String) {
        guard phase == .open else { return }
        state.setDraft(SecretText(text))
    }

    func submit(_ button: ASLResponseButton) {
        guard phase == .open else { return }
        if let effect = state.submit(button, mint: mint) { perform(effect) }
    }

    func move(by delta: Int) {
        guard phase == .open else { return }
        if let effect = state.move(by: delta) { perform(effect) }
    }

    private func reset() {
        openSeq += 1
        state = ASLResponseState()
        phase = .idle
        answering = nil
    }

    private func snapshotLoaded(seq: Int, target: ASLAnswerTarget, _ result: Result<ASLResult, ASLRequestError>) {
        guard seq == openSeq, phase == .loading else { return }
        switch result {
        case let .failure(error):
            Self.logger.notice("asl_snapshot for run \(target.runID, privacy: .public): \(error.description, privacy: .public)")
            switch error {
            case .notRunning, .unsupported: phase = .refused(ASLRefusal.text(error))
            case .refused, .transport: phase = .refused("actions are unavailable: agent-session-loop is not reachable")
            }
        case let .success(result):
            let rows = result.snapshot?.rows ?? []
            guard let row = target.row(in: rows) else {
                phase = .refused("this run is no longer waiting on an answer")
                return
            }
            let view = ASLView.of(status: result.status, meta: result.meta, hasRows: !rows.isEmpty)
            switch ASLResponseState.open(row: row, view: view, status: result.status) {
            case let .refused(reason):
                phase = .refused(reason)
            case let .opened(opened, effect):
                state = opened
                phase = .open
                perform(effect)
            }
        }
    }

    private func perform(_ effect: ASLPanelEffect) {
        let service = self.service
        switch effect {
        case let .fetch(seq, planDir, questionID):
            runner.run({ service.question(planDir: planDir, questionID: questionID) }) { [weak self] result in
                MainActor.assumeIsolated {
                    guard let self, self.phase == .open else { return }
                    if case let .failure(error) = result {
                        Self.logger.notice("asl_question \(questionID, privacy: .public): \(error.description, privacy: .public)")
                    }
                    if let next = self.state.questionLoaded(seq: seq, result) { self.perform(next) }
                }
            }
        case let .act(seq, params):
            // The description is body-free: method, run, question, operation.
            Self.logger.notice("asl_action \(params.description, privacy: .public)")
            runner.run({ service.act(params) }) { [weak self] result in
                MainActor.assumeIsolated {
                    switch result {
                    case let .success(operation):
                        Self.logger.notice("asl_action \(params.operationID, privacy: .public): \(operation.status, privacy: .public)")
                    case let .failure(error):
                        Self.logger.notice("asl_action \(params.operationID, privacy: .public): \(error.description, privacy: .public)")
                    }
                    guard let self, self.phase == .open else { return }
                    if let next = self.state.actionFinished(seq: seq, result) { self.perform(next) }
                }
            }
        case let .close(notice):
            reset()
            onClose(notice)
        }
    }
}

/// The panel's content.
struct ASLQuestionView: View {
    @ObservedObject var flow: ASLQuestionFlow
    var onClose: () -> Void
    var onEditorFocus: () -> Void
    @FocusState private var editorFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            switch flow.phase {
            case .idle, .loading:
                ProgressView("Loading the question…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            case let .refused(reason):
                Text("This question cannot be answered here.").font(.headline)
                Text(reason).foregroundStyle(.secondary).textSelection(.enabled)
                Spacer(minLength: 0)
            case .open:
                form
            }
            footer
        }
        .padding(16)
        .frame(minWidth: 460, minHeight: 360)
        .onChange(of: editorFocused) { _, focused in
            if focused { onEditorFocus() }
        }
    }

    private var state: ASLResponseState { flow.state }

    private var subtitle: String {
        var parts = [state.kind == .paused ? "Paused run" : "Waiting on its supervisor"]
        if state.questionIDs.count > 1 {
            parts.append("question \(state.questionIndex + 1) of \(state.questionIDs.count)")
        }
        if !state.askedAt.isEmpty { parts.append("asked \(state.askedAt)") }
        return parts.joined(separator: " · ")
    }

    @ViewBuilder private var form: some View {
        Text(state.target.label).font(.headline).lineLimit(1)
        Text(subtitle).font(.caption).foregroundStyle(.secondary)
        Group {
            if state.fetching {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    Text(state.body.reveal())
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(8)
                }
            }
        }
        .frame(minHeight: 120, maxHeight: .infinity)
        .background(Color(nsColor: .textBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 6))

        Text(state.kind == .paused ? "Your reply (sent, then the run resumes)" : "Your reply")
            .font(.caption).foregroundStyle(.secondary)
        TextEditor(text: Binding(get: { flow.state.draft.reveal() }, set: { flow.setDraft($0) }))
            .font(.body)
            .focused($editorFocused)
            .frame(minHeight: 80, maxHeight: 160)
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.secondary.opacity(0.4)))
            .disabled(state.inFlight)
        if !state.notice.isEmpty {
            Text(state.notice).font(.callout).foregroundStyle(.red)
        } else if state.inFlight {
            Text("Submitting…").font(.callout).foregroundStyle(.secondary)
        }
    }

    private var footer: some View {
        HStack {
            if flow.phase == .open, state.questionIDs.count > 1 {
                Button("◀") { flow.move(by: -1) }
                    .disabled(state.inFlight || state.questionIndex == 0)
                    .help("Previous question")
                Button("▶") { flow.move(by: 1) }
                    .disabled(state.inFlight || state.questionIndex >= state.questionIDs.count - 1)
                    .help("Next question")
            }
            Spacer()
            Button("Close", action: onClose).keyboardShortcut(.cancelAction)
            if flow.phase == .open {
                ForEach(Array(state.buttons.enumerated()), id: \.offset) { index, button in
                    submitButton(button, primary: index == 0)
                }
            }
        }
    }

    @ViewBuilder private func submitButton(_ button: ASLResponseButton, primary: Bool) -> some View {
        let base = Button(button.label) { flow.submit(button) }
            .disabled(!state.canSubmit(button))
        if primary {
            // ⌘↩ sends the answer, whichever button has focus.
            base.keyboardShortcut(.return, modifiers: .command).buttonStyle(.borderedProminent)
        } else {
            base
        }
    }
}

/// A panel that can take keyboard focus without the app being active first.
final class ASLPanel: NSPanel {
    override var canBecomeKey: Bool { true }
}

/// Owns the one answer panel. It floats above other windows and does not
/// activate the app until the reply editor takes focus.
@MainActor
final class ASLQuestionPanelController: NSObject, NSWindowDelegate {
    let flow: ASLQuestionFlow
    private var panel: ASLPanel?
    /// The form closed itself; `notice` is set when the run moved, and the
    /// menu should be refreshed either way.
    var onFinish: (_ notice: String?) -> Void = { _ in }

    init(flow: ASLQuestionFlow? = nil) {
        let flow = flow ?? ASLQuestionFlow()
        self.flow = flow
        super.init()
        flow.onClose = { [weak self] notice in self?.finish(notice) }
    }

    func show(_ target: ASLAnswerTarget) {
        flow.open(target)
        let panel = self.panel ?? makePanel()
        if !panel.isVisible { panel.center() }
        panel.orderFrontRegardless()
    }

    /// The operator closed the panel: the body and drafts go with it.
    func windowWillClose(_ notification: Notification) {
        flow.cancel()
    }

    private func finish(_ notice: String?) {
        // orderOut does not send windowWillClose; the flow has already reset.
        panel?.orderOut(nil)
        onFinish(notice)
    }

    private func makePanel() -> ASLPanel {
        let panel = ASLPanel(
            contentRect: NSRect(x: 0, y: 0, width: 520, height: 440),
            styleMask: [.titled, .closable, .resizable, .nonactivatingPanel, .utilityWindow],
            backing: .buffered,
            defer: true
        )
        panel.title = "Agent Mux — Supervisor question"
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.becomesKeyOnlyIfNeeded = true
        panel.isReleasedWhenClosed = false
        panel.collectionBehavior = [.moveToActiveSpace, .fullScreenAuxiliary]
        panel.delegate = self
        panel.contentView = NSHostingView(rootView: ASLQuestionView(
            flow: flow,
            onClose: { [weak panel] in panel?.close() },
            onEditorFocus: { [weak panel] in
                // Typing needs the app active; it becomes so only now.
                NSApp.activate()
                panel?.makeKey()
            }
        ))
        self.panel = panel
        return panel
    }
}

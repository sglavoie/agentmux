import AppKit
import os
import SwiftUI

// The prompt answer panel: a floating panel a permission banner's Answer…
// opens. It shows the tail of the session's screen and a strip of keys, and
// presses one through `amx send`. The rules live in `PromptAnswerState`;
// `PromptAnswerFlow` runs the two invocations they ask for, and the
// controller owns the window.

/// The two `amx send` invocations the panel makes. Blocking: called off the
/// main thread.
protocol PromptAnswerService {
    func look(runtimeSessionID: String) -> AMXCommandResult<PromptLook>
    func press(arguments: [String]) -> AMXCommandResult<PromptSent>
}

/// The service over the resolved `amx`. With none resolved, both invocations
/// fail with why.
struct AMXPromptAnswerService: PromptAnswerService {
    let command: () -> AMXCommand?
    let problem: () -> String

    func look(runtimeSessionID: String) -> AMXCommandResult<PromptLook> {
        guard let command = command() else { return .failure(.undecodable(status: 1, detail: problem())) }
        return command.json(PromptLook.self, PromptLook.arguments(runtimeSessionID: runtimeSessionID))
    }

    func press(arguments: [String]) -> AMXCommandResult<PromptSent> {
        guard let command = command() else { return .failure(.undecodable(status: 1, detail: problem())) }
        return command.json(PromptSent.self, arguments, timeout: AMXCommand.actionTimeout, mutates: true)
    }
}

/// Drives one panel's invocations. Main-thread confined; an answer for a
/// panel that has since closed or been reopened is dropped by sequence.
@MainActor
final class PromptAnswerFlow: ObservableObject {
    private nonisolated static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "prompt")

    /// How long a session that was just written to is given to redraw.
    static let settle: TimeInterval = 0.6

    @Published private(set) var state: PromptAnswerState?
    /// Counts the times the panel was opened, so its keys take the keyboard
    /// on each, even for the session already shown.
    @Published private(set) var shown = 0

    private let service: PromptAnswerService
    private let runner: ASLWorkRunner
    private let pause: (TimeInterval) -> Void
    /// Counts every state this flow has opened, so sequences never repeat
    /// across reopenings.
    private var lastSeq = 0

    init(service: PromptAnswerService, runner: ASLWorkRunner = QueueWorkRunner(),
         pause: @escaping (TimeInterval) -> Void = { Thread.sleep(forTimeInterval: $0) }) {
        self.service = service
        self.runner = runner
        self.pause = pause
    }

    /// Opens the panel on `target`, or looks again when it is already there.
    func open(_ target: PromptAnswerTarget) {
        shown += 1
        if var current = state, current.target == target, !current.isRefused {
            let effect = current.refresh()
            state = current
            if let effect { perform(effect) }
            return
        }
        let (opened, effect) = PromptAnswerState.open(target, after: lastSeq)
        state = opened
        perform(effect)
    }

    /// Drops the panel's state: the operator closed it.
    func cancel() {
        if let state { lastSeq = max(lastSeq, state.seq) }
        state = nil
    }

    func press(_ key: PromptKey) {
        guard var current = state, let effect = current.press(key) else { return }
        state = current
        perform(effect)
    }

    func refresh() {
        guard var current = state, let effect = current.refresh() else { return }
        state = current
        perform(effect)
    }

    private func perform(_ effect: PromptAnswerEffect) {
        guard let target = state?.target else { return }
        let service = self.service
        let pause = self.pause
        switch effect {
        case let .look(seq, settle):
            lastSeq = max(lastSeq, seq)
            runner.run({
                if settle { pause(Self.settle) }
                return service.look(runtimeSessionID: target.runtimeSessionID)
            }) { [weak self] result in
                MainActor.assumeIsolated {
                    guard let self, var current = self.state, current.target == target else { return }
                    if case let .failure(error) = result {
                        Self.logger.notice("look \(target.runtimeSessionID, privacy: .public): \(error.message, privacy: .public)")
                    }
                    current.looked(seq: seq, result)
                    self.state = current
                }
            }
        case let .press(seq, arguments):
            lastSeq = max(lastSeq, seq)
            // The arguments name a session and a key, never screen text.
            Self.logger.notice("press \(arguments.joined(separator: " "), privacy: .public)")
            runner.run({ service.press(arguments: arguments) }) { [weak self] result in
                MainActor.assumeIsolated {
                    guard let self, var current = self.state, current.target == target else { return }
                    if case let .failure(error) = result {
                        Self.logger.notice("press \(target.runtimeSessionID, privacy: .public): \(error.message, privacy: .public)")
                    }
                    let next = current.pressed(seq: seq, result)
                    self.state = current
                    if let next { self.perform(next) }
                }
            }
        }
    }
}

/// The panel's content.
struct PromptAnswerView: View {
    @ObservedObject var flow: PromptAnswerFlow
    var onClose: () -> Void
    @FocusState private var focused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let state = flow.state {
                switch state.phase {
                case .loading:
                    ProgressView("Reading the session's screen…")
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                case let .refused(reason):
                    Text("This prompt cannot be answered here.").font(.headline)
                    Text(reason).foregroundStyle(.secondary).textSelection(.enabled)
                    Spacer(minLength: 0)
                case .open:
                    form(state)
                }
            } else {
                Spacer(minLength: 0)
            }
            footer
        }
        .padding(16)
        .frame(minWidth: 560, minHeight: 420)
        .focusable()
        .focusEffectDisabled()
        .focused($focused)
        .defaultFocus($focused, true)
        .onAppear { focused = true }
        .onChange(of: flow.shown) { _, _ in focused = true }
        .onChange(of: flow.state?.phase) { _, phase in
            if phase == .open { focused = true }
        }
        .onKeyPress { press in
            guard let key = Self.key(press.characters, modifiers: press.modifiers),
                  flow.state?.canPress == true else { return .ignored }
            flow.press(key)
            return .handled
        }
    }

    /// The key typing `characters` presses. A key held with ⌘, ⌃, or ⌥ is a
    /// shortcut for the menus, never a key for the agent.
    static func key(_ characters: String, modifiers: EventModifiers) -> PromptKey? {
        guard modifiers.isDisjoint(with: [.command, .control, .option]),
              let character = characters.first else { return nil }
        return PromptKey.typed(character)
    }

    @ViewBuilder private func form(_ state: PromptAnswerState) -> some View {
        Text(state.title).font(.headline).lineLimit(1)
        Text(state.caption).font(.caption).foregroundStyle(.secondary)
        ScrollViewReader { proxy in
            ScrollView([.vertical, .horizontal]) {
                Text(state.look.lines.joined(separator: "\n"))
                    .font(.system(size: 11, design: .monospaced))
                    .textSelection(.enabled)
                    .fixedSize(horizontal: true, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(8)
                    .id("screen")
            }
            .onChange(of: state.look.lines, initial: true) { _, _ in proxy.scrollTo("screen", anchor: .bottomLeading) }
        }
        .frame(minHeight: 200, maxHeight: .infinity)
        .background(Color(nsColor: .textBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 6))

        HStack(spacing: 6) {
            keys(PromptKey.digits, state)
            Divider().frame(height: 18)
            keys(PromptKey.navigation, state)
            Divider().frame(height: 18)
            keys(PromptKey.answers, state)
        }
        if !state.notice.isEmpty {
            Text(state.notice).font(.callout).foregroundStyle(state.noticeKind.color)
        } else if state.inFlight {
            Text("Working…").font(.callout).foregroundStyle(.secondary)
        }
    }

    private func keys(_ keys: [PromptKey], _ state: PromptAnswerState) -> some View {
        ForEach(keys) { key in
            Button(key.label) { flow.press(key) }
                .help(key.help)
                .disabled(!state.canPress)
        }
    }

    private var footer: some View {
        HStack {
            Button("Look again") { flow.refresh() }
                .disabled(flow.state?.canLookAgain != true)
            Spacer()
            Button("Close", action: onClose).keyboardShortcut(.cancelAction)
        }
    }
}

/// Owns the one prompt answer panel. Like the ASL panel it floats above other
/// windows; it takes the keyboard when shown, since its keys are its purpose.
@MainActor
final class PromptAnswerPanelController: NSObject, NSWindowDelegate {
    let flow: PromptAnswerFlow
    private var panel: ASLPanel?

    init(flow: PromptAnswerFlow) {
        self.flow = flow
        super.init()
    }

    func show(_ target: PromptAnswerTarget) {
        flow.open(target)
        let panel = self.panel ?? makePanel()
        if !panel.isVisible { panel.center() }
        panel.orderFrontRegardless()
        // Typing a key needs the app active as well as the panel key.
        NSApp.activate()
        panel.makeKey()
    }

    /// The operator closed the panel: the screen it showed goes with it.
    func windowWillClose(_ notification: Notification) {
        flow.cancel()
    }

    private func makePanel() -> ASLPanel {
        let panel = ASLPanel(
            contentRect: NSRect(x: 0, y: 0, width: 640, height: 480),
            styleMask: [.titled, .closable, .resizable, .nonactivatingPanel, .utilityWindow],
            backing: .buffered,
            defer: true
        )
        panel.title = "Agent Mux — Answer a prompt"
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.isReleasedWhenClosed = false
        panel.collectionBehavior = [.moveToActiveSpace, .fullScreenAuxiliary]
        panel.delegate = self
        panel.contentView = NSHostingView(rootView: PromptAnswerView(
            flow: flow,
            onClose: { [weak panel] in panel?.close() }
        ))
        self.panel = panel
        return panel
    }
}

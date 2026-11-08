import AppKit
import Combine
import SwiftUI

/// A button that records the next shortcut typed. Esc cancels; a key
/// `HotKeyCombo.refusal` refuses shows why in the button instead.
struct HotKeyRecorderView: View {
    @Binding var combo: HotKeyCombo?
    @ObservedObject var model: PreferencesModel
    @ObservedObject var recorder: HotKeyRecorder
    /// The other shortcut's recorder, stopped when this one starts.
    let other: HotKeyRecorder
    /// The shortcut the app ships with, which Restore Default puts back.
    let defaultCombo: HotKeyCombo?
    var additionalOthers: [HotKeyRecorder] = []
    var scope: HotKeyCombo.Scope = .global
    var validate: (HotKeyCombo) -> String? = { _ in nil }
    /// Which of the rows sharing `recorder` this one is: only the row the
    /// recorder records for says so. Nil for a recorder of its own.
    var owner: AnyHashable?

    /// Whether `recorder` is recording for this row.
    private var isRecording: Bool { recorder.isRecording && recorder.owner == owner }

    var body: some View {
        VStack(alignment: .trailing, spacing: 4) {
            HStack {
                Button(action: toggleRecording) {
                    Text(label)
                        .frame(minWidth: 140)
                        .foregroundStyle(isRecording ? Color.accentColor : Color.primary)
                }
                if combo != nil, !isRecording {
                    Button("Clear") { combo = nil }
                }
                if combo != defaultCombo, !isRecording {
                    Button("Restore Default") { combo = defaultCombo }
                        .help(defaultCombo.map { "Use \($0.displayString) again" } ?? "Remove the custom shortcut")
                }
            }
            if isRecording, let hint = recorder.hint {
                Text(hint).font(.caption).foregroundStyle(WorkspaceReviewColors.warning).fixedSize(horizontal: false, vertical: true)
            }
        }
        .onDisappear { if isRecording { recorder.stop() } }
    }

    private var label: String {
        if isRecording {
            return "Type a shortcut…"
        }
        return combo?.displayString ?? "Record Shortcut"
    }

    private func toggleRecording() {
        if isRecording {
            recorder.stop()
            return
        }
        other.stop()
        additionalOthers.forEach { $0.stop() }
        let binding = $combo
        recorder.start(
            scope: scope, validate: validate, owner: owner,
            onStateChange: { [weak model] recording in model?.onRecordingChange?(recording) },
            onRecord: { binding.wrappedValue = $0 }
        )
    }
}

/// Local key monitors run on the main thread, which is where it is used.
final class HotKeyRecorder: ObservableObject {
    @Published private(set) var isRecording = false
    @Published private(set) var hint: String?
    /// The row recording, when several share this recorder.
    @Published private(set) var owner: AnyHashable?
    /// The Settings window: the only one whose keys are recorded.
    weak var window: NSWindow?
    private var monitor: Any?
    private var onStateChange: ((Bool) -> Void)?
    private var onRecord: ((HotKeyCombo) -> Void)?
    private var scope = HotKeyCombo.Scope.global
    private var validate: (HotKeyCombo) -> String? = { _ in nil }
    /// How the key monitor is installed and removed; tests capture the
    /// callback the recorder registers.
    var addMonitor: (@escaping (NSEvent) -> NSEvent?) -> Any? = {
        NSEvent.addLocalMonitorForEvents(matching: .keyDown, handler: $0)
    }
    var removeMonitor: (Any) -> Void = { NSEvent.removeMonitor($0) }

    func start(scope: HotKeyCombo.Scope = .global, validate: @escaping (HotKeyCombo) -> String? = { _ in nil },
               owner: AnyHashable? = nil,
               onStateChange: @escaping (Bool) -> Void, onRecord: @escaping (HotKeyCombo) -> Void) {
        stop()
        self.owner = owner
        self.scope = scope
        self.validate = validate
        self.onStateChange = onStateChange
        isRecording = true
        hint = nil
        onStateChange(true)
        self.onRecord = onRecord
        // A recorder that is gone passes the key on; a live one's nil
        // swallows it, so it must not fall back to the event.
        monitor = addMonitor { [weak self] event in
            guard let self else { return event }
            return self.handle(event)
        }
    }

    /// What the monitor does with a key: one typed in Settings is recorded
    /// (or refused, or Esc cancels) and swallowed; any other window's key
    /// goes on to it, since the app's shortcuts are unregistered meanwhile.
    func handle(_ event: NSEvent) -> NSEvent? {
        guard isRecording, let window, event.window === window else { return event }
        let chord = event.modifierFlags.intersection([.command, .control, .option, .shift])
        if Int(event.keyCode) == HotKeyRecorder.escapeKeyCode, chord.isEmpty {
            stop()
        } else if let combo = HotKeyCombo(event: event, scope: scope) {
            if let problem = validate(combo) { hint = problem; return nil }
            // Recorded before stopping, so the shortcuts the app
            // registers again when recording ends are the new ones.
            onRecord?(combo)
            stop()
        } else {
            hint = HotKeyCombo.refusal(event: event, scope: scope)
        }
        return nil
    }

    func stop() {
        if let monitor {
            removeMonitor(monitor)
            self.monitor = nil
        }
        guard isRecording else { return }
        isRecording = false
        hint = nil
        owner = nil
        onRecord = nil
        validate = { _ in nil }
        onStateChange?(false)
        onStateChange = nil
    }

    private static let escapeKeyCode = 0x35
}

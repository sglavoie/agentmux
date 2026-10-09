import AppKit
import GhosttyKit

// The Session 2 feasibility harness: one window, one embedded Ghostty surface,
// running whatever command AMX_HARNESS_COMMAND names (normally `tmux attach`
// to a private server). A driver steers it through a control FIFO, one command
// per line, and reads each result from the log; see HarnessController.

guard ghostty_init(UInt(CommandLine.argc), CommandLine.unsafeArgv) == GHOSTTY_SUCCESS else {
    FileHandle.standardError.write("ghostty_init failed\n".data(using: .utf8)!)
    exit(1)
}

let environment = ProcessInfo.processInfo.environment
guard let command = environment["AMX_HARNESS_COMMAND"], !command.isEmpty,
      let control = environment["AMX_HARNESS_CONTROL"], !control.isEmpty,
      let log = environment["AMX_HARNESS_LOG"], !log.isEmpty else {
    FileHandle.standardError.write(
        "AMX_HARNESS_COMMAND, AMX_HARNESS_CONTROL, and AMX_HARNESS_LOG are required\n".data(using: .utf8)!)
    exit(2)
}

let app = NSApplication.shared
app.setActivationPolicy(.regular)
let controller = HarnessController(
    command: command,
    controlPath: control,
    logPath: log,
    environment: ["TERM_PROGRAM": "agentmux-ghostty-harness"]
)
app.delegate = controller
app.run()

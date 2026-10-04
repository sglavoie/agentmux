import Foundation

/// A cobra `--help` text, read for what it declares: the subcommands under
/// "Available Commands:" and every `--flag` under "Flags:" or "Global Flags:".
/// Prose that merely mentions a flag is not a declaration.
struct AMXHelp: Equatable {
    let subcommands: Set<String>
    let flags: Set<String>

    init(subcommands: Set<String>, flags: Set<String>) {
        self.subcommands = subcommands
        self.flags = flags
    }

    init(_ text: String) {
        enum Section { case other, commands, flags }
        var section = Section.other
        var subcommands: Set<String> = []
        var flags: Set<String> = []
        for raw in text.split(separator: "\n", omittingEmptySubsequences: false) {
            let line = String(raw)
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if !line.hasPrefix(" ") {
                switch trimmed {
                case "Available Commands:", "Additional Commands:": section = .commands
                case "Flags:", "Global Flags:": section = .flags
                default: section = .other
                }
                continue
            }
            switch section {
            case .commands:
                if let name = trimmed.split(separator: " ").first {
                    subcommands.insert(String(name))
                }
            case .flags:
                // `  -j, --json   ...` or `      --window-id int   ...`
                for token in trimmed.split(separator: " ").prefix(2) {
                    let word = token.trimmingCharacters(in: CharacterSet(charactersIn: ","))
                    if word.hasPrefix("--") {
                        flags.insert(word)
                        break
                    }
                }
            case .other:
                break
            }
        }
        self.subcommands = subcommands
        self.flags = flags
    }
}

/// Which of the commands the app relies on the resolved amx offers. An older
/// amx refuses an unknown command or flag, so the app asks once per binary and
/// hides what is missing instead of failing at the moment it is used.
struct AMXCapabilities: Equatable {
    var project = false
    var openDashboard = false
    var notificationsSnooze = false
    var muteToggleJSON = false

    static let none = AMXCapabilities()

    init(project: Bool = false, openDashboard: Bool = false, notificationsSnooze: Bool = false, muteToggleJSON: Bool = false) {
        self.project = project
        self.openDashboard = openDashboard
        self.notificationsSnooze = notificationsSnooze
        self.muteToggleJSON = muteToggleJSON
    }

    /// From the help texts of the root, `notifications`, and `mute-toggle`.
    init(root: AMXHelp?, notifications: AMXHelp?, muteToggle: AMXHelp?) {
        project = root?.subcommands.contains("project") ?? false
        openDashboard = root?.subcommands.contains("open-dashboard") ?? false
        notificationsSnooze = notifications?.subcommands.contains("snooze") ?? false
        muteToggleJSON = muteToggle?.flags.contains("--json") ?? false
    }

    static func probe(_ command: AMXCommand) -> AMXCapabilities {
        let root = command.help()
        return AMXCapabilities(
            root: root,
            notifications: root?.subcommands.contains("notifications") == true ? command.help(["notifications"]) : nil,
            muteToggle: root?.subcommands.contains("mute-toggle") == true ? command.help(["mute-toggle"]) : nil
        )
    }
}

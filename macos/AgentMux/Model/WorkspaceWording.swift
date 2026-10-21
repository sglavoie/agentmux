import Foundation

/// Presentation only: protocol strings remain intact in models and decisions.
enum WorkspaceWording {
    /// Where a session's state came from, in the operator's words: who saw
    /// or said it, never the wire's name for the source.
    static func evidence(_ evidence: CompanionStatusEvidence?) -> String {
        guard let evidence else { return noEvidence }
        switch evidence.kind {
        case "screen": return "Read from the terminal screen"
        case "transcript": return "Read from the conversation"
        case "harness": return "Reported by the agent"
        case "subagent": return "Reported by a subagent"
        case "asl": return "Reported by the ASL run"
        case "probe": return "Seen by Agent Mux or set by you"
        case "legacy": return "Reported by an older hook"
        default: return "From a source this app does not know"
        }
    }

    static let noEvidence = "Nothing has reported it yet"

    static func status(_ value: String) -> String {
        let labels = [
            "fast_forward": "Ready to land", "nothing_to_land": "Nothing to land",
            "diverged_clean": "Branches diverged; merge required", "conflicts": "Merge conflicts",
            "unknown": "Not yet known", "not_ready": "Not ready", "ready": "Ready to run",
            "permission": "Needs permission", "key": "Needs permission",
            "command_approval": "Needs permission", "permission_grant": "Needs permission",
            "edit_approval": "Needs edit approval", "edit": "Needs edit approval", "trust": "Needs trust approval",
            "elicitation": "Waiting for your answer", "question": "Waiting for your answer",
            "request_user_input": "Waiting for your answer", "unanswered_confirmation": "Waiting for your answer",
            "done": "Turn complete", "stop": "Turn complete", "completed": "Turn complete",
            "running": "Running", "complete": "Complete", "paused": "Paused",
            "pending": "Pending", "empty": "Not started", "verification-failed": "Verification failed",
            "waiting_for_input": "Waiting for your answer", "waiting-for-input": "Waiting for your answer"
        ]
        if let label = labels[value] { return label }
        // Unknown protocol values stay visible, with word separators expanded.
        let words = value.replacingOccurrences(of: "_", with: " ").replacingOccurrences(of: "-", with: " ")
        guard let first = words.first else { return "Not recorded" }
        return first.uppercased() + words.dropFirst()
    }

    /// The shortest trailing path that distinguishes a file from its siblings.
    /// Full paths remain in tooltips, accessibility, and copyable details.
    static func uniqueSuffixes(_ paths: [String]) -> [String: String] {
        let unique = Set(paths)
        var counts: [String: Int] = [:]
        func suffixes(_ path: String) -> [String] {
            var suffix = ""
            return path.split(separator: "/").reversed().map { part in
                suffix = suffix.isEmpty ? String(part) : String(part) + "/" + suffix
                return suffix
            }
        }
        for path in unique {
            for suffix in suffixes(path) { counts[suffix, default: 0] += 1 }
        }
        return Dictionary(uniqueKeysWithValues: unique.map { path in
            (path, suffixes(path).first { counts[$0] == 1 } ?? path)
        })
    }

    static func relativeTime(_ raw: String, now: Date) -> String {
        guard let date = WorkspaceActivityEntry.date(raw) else { return "Time unavailable" }
        return AgeFormatter.string(since: date, now: now, style: .sentence)
    }
}

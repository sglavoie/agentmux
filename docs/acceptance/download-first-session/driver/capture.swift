import Foundation
import CoreGraphics
import AppKit

precondition(ProcessInfo.processInfo.environment["AMX_UI_REMOTE_EXECUTION"] == "1", "Run on the approved test Mac over SSH")
let root = URL(fileURLWithPath: "/tmp/amx-firstsession-1030")
let pid = Int(try String(contentsOf: root.appendingPathComponent("app.pid"), encoding: .utf8).trimmingCharacters(in: .whitespacesAndNewlines))!
precondition(NSRunningApplication(processIdentifier: pid_t(pid))?.bundleIdentifier == "io.github.sglavoie.agentmux.firstsession1030", "Not the isolated test app")
let label = CommandLine.arguments[1]
precondition(!label.isEmpty && label.allSatisfy { $0.isLetter || $0.isNumber || $0 == "-" })
let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
let own = windows.filter { ($0[kCGWindowOwnerPID as String] as? Int) == pid && ($0[kCGWindowLayer as String] as? Int) == 0 }
precondition(!own.isEmpty, "No test app window")
for (index, window) in own.enumerated() {
    let number = window[kCGWindowNumber as String] as! Int
    let output = root.appendingPathComponent("\(label)-\(index).png")
    let capture = Process()
    capture.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
    capture.arguments = ["-x", "-o", "-l", String(number), output.path]
    try capture.run()
    capture.waitUntilExit()
    precondition(capture.terminationStatus == 0)
}
let metadata = try JSONSerialization.data(withJSONObject: own, options: [.prettyPrinted, .sortedKeys])
try metadata.write(to: root.appendingPathComponent("\(label)-windows.json"))
print("Captured \(own.count) test app window(s)")

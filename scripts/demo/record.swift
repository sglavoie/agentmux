// On-device Accessibility walkthrough and window capture. Build with:
// sh scripts/demo/build-recorder.sh
import AppKit
import ApplicationServices
import ImageIO
import ScreenCaptureKit

enum DemoError: Error, CustomStringConvertible {
    case message(String)
    var description: String {
        switch self { case let .message(text): return text }
    }
}

@main
struct Recorder {
    @MainActor static func main() async {
        let args = CommandLine.arguments
        do { try await record() }
        catch {
            FileHandle.standardError.write(Data("demo-recorder: \(error)\n".utf8))
            if args.count == 3, args[1].hasPrefix("io.github.sglavoie.agentmux.demo.") {
                let errorURL = URL(fileURLWithPath: args[2] + ".error.txt")
                try? Data("\(error)\n".utf8).write(to: errorURL)
            }
            exit(1)
        }
    }

    static func pause(_ seconds: Double) async throws {
        try await Task.sleep(nanoseconds: UInt64(seconds * 1_000_000_000))
    }

    static func key(_ code: CGKeyCode, flags: CGEventFlags = [], pid: pid_t) throws {
        let source = CGEventSource(stateID: .privateState)
        for down in [true, false] {
            guard let event = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: down) else {
                throw DemoError.message("Could not create keyboard event")
            }
            event.flags = flags
            event.postToPid(pid)
        }
    }

    static func type(_ text: String, pid: pid_t) throws {
        let source = CGEventSource(stateID: .privateState)
        for unit in text.utf16 {
            var unit = unit
            for down in [true, false] {
                guard let event = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: down) else {
                    throw DemoError.message("Could not create text event")
                }
                event.keyboardSetUnicodeString(stringLength: 1, unicodeString: &unit)
                event.postToPid(pid)
            }
        }
    }

    static func selectProject(_ name: String, pid: pid_t) async throws {
        try pressMenu("sessionClicked:", descriptionPrefix: name + ",", pid: pid)
        try await pause(0.3)
    }

    static func find(_ element: AXUIElement, depth: Int = 0,
                     matching predicate: (AXUIElement) -> Bool) -> AXUIElement? {
        if predicate(element) { return element }
        guard depth < 10, let children = attribute(element, kAXChildrenAttribute) as? [AXUIElement] else { return nil }
        for child in children {
            if let found = find(child, depth: depth + 1, matching: predicate) { return found }
        }
        return nil
    }

    static func pressMenu(_ identifier: String, descriptionPrefix: String? = nil, pid: pid_t) throws {
        let root = AXUIElementCreateApplication(pid)
        guard let item = find(root, matching: { element in
            guard attribute(element, kAXRoleAttribute) as? String == kAXMenuItemRole,
                  attribute(element, kAXIdentifierAttribute) as? String == identifier else { return false }
            if let descriptionPrefix {
                return (attribute(element, kAXDescriptionAttribute) as? String)?.hasPrefix(descriptionPrefix) == true
            }
            return true
        }) else { throw DemoError.message("Demo menu item not found: \(identifier) \(descriptionPrefix ?? "")") }
        let result = AXUIElementPerformAction(item, kAXPressAction as CFString)
        guard result == .success else { throw DemoError.message("Could not press \(identifier): \(result.rawValue)") }
    }

    static func pressButton(_ description: String, pid: pid_t) throws {
        guard let button = find(AXUIElementCreateApplication(pid), matching: {
            attribute($0, kAXRoleAttribute) as? String == kAXButtonRole &&
                attribute($0, kAXDescriptionAttribute) as? String == description
        }), AXUIElementPerformAction(button, kAXPressAction as CFString) == .success else {
            throw DemoError.message("Could not press demo button: \(description)")
        }
    }

    static func attribute(_ element: AXUIElement, _ key: String) -> CFTypeRef? {
        var value: CFTypeRef?
        guard AXUIElementCopyAttributeValue(element, key as CFString, &value) == .success else { return nil }
        return value
    }

    static func describe(_ element: AXUIElement, depth: Int = 0) -> [String: Any] {
        var result: [String: Any] = [:]
        for key in [kAXRoleAttribute, kAXTitleAttribute, kAXDescriptionAttribute, kAXValueAttribute, kAXIdentifierAttribute] {
            if let value = attribute(element, key) as? String { result[key] = String(value.prefix(300)) }
        }
        if depth < 9, let children = attribute(element, kAXChildrenAttribute) as? [AXUIElement] {
            result["children"] = children.prefix(100).map { describe($0, depth: depth + 1) }
        }
        return result
    }

    @MainActor static func record() async throws {
        let env = ProcessInfo.processInfo.environment
        let guardFile = Bundle.main.bundleURL.deletingLastPathComponent()
            .appendingPathComponent(".demo-remote-only")
        guard !FileManager.default.fileExists(atPath: guardFile.path),
              env["AMX_UI_TEST_REMOTE_ONLY"] != "1" || env["AMX_UI_REMOTE_EXECUTION"] == "1" else {
            throw DemoError.message("UI is disabled on this Mac; use the remote test Mac")
        }
        let args = CommandLine.arguments
        if args.count == 4, args[1] == "--inspect", args[2].hasPrefix("io.github.sglavoie.agentmux.demo."),
           let app = NSRunningApplication.runningApplications(withBundleIdentifier: args[2]).first {
            let tree = describe(AXUIElementCreateApplication(app.processIdentifier))
            try JSONSerialization.data(withJSONObject: tree, options: [.prettyPrinted, .sortedKeys])
                .write(to: URL(fileURLWithPath: args[3]))
            return
        }
        if args.count == 3, ["--permissions", "--request-permissions"].contains(args[1]) {
            _ = NSApplication.shared
            if args[1] == "--request-permissions" {
                let options = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true]
                _ = AXIsProcessTrustedWithOptions(options as CFDictionary)
                _ = CGRequestScreenCaptureAccess()
            }
            let status = ["accessibility": AXIsProcessTrusted(),
                          "screen_recording": CGPreflightScreenCaptureAccess()]
            try JSONSerialization.data(withJSONObject: status, options: [.prettyPrinted, .sortedKeys])
                .write(to: URL(fileURLWithPath: args[2]))
            return
        }
        guard args.count == 3, args[1].hasPrefix("io.github.sglavoie.agentmux.demo.") else {
            throw DemoError.message("usage: demo-recorder DEMO_BUNDLE_ID NEW_FRAME_DIRECTORY")
        }
        guard AXIsProcessTrusted() else {
            throw DemoError.message("Accessibility access is required for this local recorder; no input was sent")
        }
        guard CGPreflightScreenCaptureAccess() else {
            throw DemoError.message("Screen Recording access is required for this local recorder; nothing was captured")
        }
        _ = NSApplication.shared
        var runningApp: NSRunningApplication?
        for _ in 0..<100 {
            runningApp = NSRunningApplication.runningApplications(withBundleIdentifier: args[1]).first
            if runningApp != nil { break }
            try await pause(0.1)
        }
        guard let app = runningApp else {
            throw DemoError.message("The isolated demo app is not running")
        }
        let out = URL(fileURLWithPath: args[2], isDirectory: true)
        guard !FileManager.default.fileExists(atPath: out.path) else {
            throw DemoError.message("Frame directory already exists; choose a new directory")
        }
        try FileManager.default.createDirectory(at: out, withIntermediateDirectories: true)
        app.activate(options: [])
        try await pause(1)

        let axApp = AXUIElementCreateApplication(app.processIdentifier)
        var value: CFTypeRef?
        guard AXUIElementCopyAttributeValue(axApp, kAXWindowsAttribute as CFString, &value) == .success,
              let windows = value as? [AXUIElement], let window = windows.first else {
            throw DemoError.message("The demo workspace has no accessible window")
        }
        var position = CGPoint(x: 60, y: 60)
        var size = CGSize(width: 1280, height: 800)
        if let axPosition = AXValueCreate(.cgPoint, &position), let axSize = AXValueCreate(.cgSize, &size) {
            AXUIElementSetAttributeValue(window, kAXPositionAttribute as CFString, axPosition)
            AXUIElementSetAttributeValue(window, kAXSizeAttribute as CFString, axSize)
        }
        try await pause(0.5)
        let content = try await SCShareableContent.excludingDesktopWindows(true, onScreenWindowsOnly: true)
        guard let target = content.windows
            .filter({ $0.owningApplication?.processID == app.processIdentifier && $0.frame.width > 500 })
            .max(by: { $0.frame.width * $0.frame.height < $1.frame.width * $1.frame.height }) else {
            throw DemoError.message("Could not find the demo workspace for capture")
        }
        let filter = SCContentFilter(desktopIndependentWindow: target)
        // Keep focus-follows-pointer window managers on the demo too. This
        // moves the pointer over its title bar without clicking anything.
        let pointer = CGPoint(x: target.frame.midX, y: target.frame.minY + 14)
        CGEvent(mouseEventSource: CGEventSource(stateID: .privateState), mouseType: .mouseMoved,
                mouseCursorPosition: pointer, mouseButton: .left)?.post(tap: .cghidEventTap)
        app.activate(options: [])
        AXUIElementPerformAction(window, kAXRaiseAction as CFString)
        try await pause(1)
        let config = SCStreamConfiguration()
        config.width = Int(target.frame.width)
        config.height = Int(target.frame.height)
        config.showsCursor = false
        config.captureResolution = .nominal

        try await selectProject("api-service", pid: app.processIdentifier)
        try await pause(1)

        // One image of the actual window every 200 ms. Actions use only the
        // demo process's menu equivalents; no input reaches another app.
        let fps = 5
        let seconds = 46
        var events: [[String: Any]] = []
        let begin = Date()
        for frame in 0..<(fps * seconds) {
            guard !app.isTerminated else {
                throw DemoError.message("Demo app exited; recording stopped")
            }
            var scene: String?
            var expectedTitle: String?
            switch frame {
            case 0:
                scene = "Completed change in api-service"
                expectedTitle = "api-service"
            case 5 * fps:
                try await selectProject("release-page", pid: app.processIdentifier)
                scene = "Switch to the working release-page session"
                expectedTitle = "release-page"
            case 10 * fps:
                try await selectProject("documentation", pid: app.processIdentifier)
                scene = "Documentation session waiting for approval"
                expectedTitle = "documentation"
            case 14 * fps:
                try type("approve", pid: app.processIdentifier)
                try key(36, pid: app.processIdentifier)
                scene = "Approve the scripted documentation check"
                expectedTitle = "documentation"
            case 19 * fps:
                try pressMenu("openDashboardFromMenu:", pid: app.processIdentifier)
                scene = "Terminal dashboard inside the desktop app"
                expectedTitle = "Dashboard"
            case 24 * fps:
                try pressMenu("openOverviewFromMenu:", pid: app.processIdentifier)
                scene = "Live overview"
                expectedTitle = "Overview"
            case 29 * fps:
                try await selectProject("api-service", pid: app.processIdentifier)
                scene = "Return to the completed change"
                expectedTitle = "api-service"
            case 33 * fps:
                try pressButton("Toggle Inspector", pid: app.processIdentifier)
                scene = "Session inspector and attention list"
                expectedTitle = "api-service"
            case 38 * fps:
                try pressButton("Toggle Inspector", pid: app.processIdentifier)
                try pressMenu("splitWorkspaceChanges:", pid: app.processIdentifier)
                scene = "Review the health endpoint patch beside the agent"
                expectedTitle = "Checkout Changes"
            default: break
            }
            if let scene {
                app.activate(options: [])
                AXUIElementPerformAction(window, kAXRaiseAction as CFString)
                try await pause(0.35)
                let title = attribute(window, kAXTitleAttribute) as? String ?? ""
                guard app.isActive, let expectedTitle, title.hasPrefix(expectedTitle) else {
                    throw DemoError.message("Scene did not reach the expected foreground view: \(scene), title=\(title)")
                }
                events.append(["frame": frame, "scene": scene,
                               "window_title": title,
                               "foreground": app.isActive])
                print(scene)
            }
            let image = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)
            let path = out.appendingPathComponent(String(format: "%04d.png", frame))
            guard let destination = CGImageDestinationCreateWithURL(path as CFURL, "public.png" as CFString, 1, nil) else {
                throw DemoError.message("Could not write frame \(frame)")
            }
            CGImageDestinationAddImage(destination, image, nil)
            guard CGImageDestinationFinalize(destination) else {
                throw DemoError.message("Could not finish frame \(frame)")
            }
            let remaining = Double(frame + 1) / Double(fps) - Date().timeIntervalSince(begin)
            if remaining > 0 { try await pause(remaining) }
        }
        let metadata: [String: Any] = ["fps": fps, "frames": fps * seconds,
            "scripted_agent_output": true, "events": events,
            "elapsed_seconds": Date().timeIntervalSince(begin)]
        try JSONSerialization.data(withJSONObject: metadata, options: [.prettyPrinted, .sortedKeys])
            .write(to: out.appendingPathComponent("recording.json"))
        print("Captured \(fps * seconds) frames of the isolated demo window")
    }
}

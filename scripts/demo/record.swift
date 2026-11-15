// On-device Accessibility walkthrough and window capture. Build with:
// sh scripts/demo/build-recorder.sh
import AppKit
import ApplicationServices
import ImageIO
import ScreenCaptureKit
import AVFoundation

enum DemoError: Error, CustomStringConvertible {
    case message(String)
    var description: String {
        switch self { case let .message(text): return text }
    }
}

// ScreenCaptureKit delivers actual display updates at up to 30 fps. Encoding
// runs off the UI driver queue, so menu actions never stall frame collection.
final class MovieOutput: NSObject, SCStreamOutput {
    let queue = DispatchQueue(label: "demo.movie")
    let writer: AVAssetWriter
    let input: AVAssetWriterInput
    var started = false
    var frames = 0
    var failure: String?

    init(url: URL, width: Int, height: Int) throws {
        writer = try AVAssetWriter(outputURL: url, fileType: .mov)
        input = AVAssetWriterInput(mediaType: .video, outputSettings: [
            AVVideoCodecKey: AVVideoCodecType.h264,
            AVVideoWidthKey: width, AVVideoHeightKey: height,
            AVVideoCompressionPropertiesKey: [AVVideoAverageBitRateKey: 8_000_000]])
        input.expectsMediaDataInRealTime = true
        super.init()
        writer.add(input)
        guard writer.startWriting() else { throw DemoError.message("Could not start movie writer") }
    }

    func stream(_ stream: SCStream, didOutputSampleBuffer sample: CMSampleBuffer,
                of type: SCStreamOutputType) {
        guard type == .screen, sample.isValid,
              let attachments = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: false)
                as? [[SCStreamFrameInfo: Any]],
              let status = attachments.first?[.status] as? Int,
              status == SCFrameStatus.complete.rawValue else { return }
        if !started {
            writer.startSession(atSourceTime: sample.presentationTimeStamp)
            started = true
        }
        guard input.isReadyForMoreMediaData else {
            failure = "Movie encoder could not keep up with capture"
            return
        }
        if input.append(sample) { frames += 1 }
        else { failure = writer.error?.localizedDescription ?? "Could not append frame" }
    }

    func finish() async throws {
        queue.sync { input.markAsFinished() }
        await writer.finishWriting()
        if let failure { throw DemoError.message(failure) }
        guard writer.status == .completed, frames > 0 else {
            throw DemoError.message("Movie did not finish: \(String(describing: writer.error))")
        }
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
                attribute($0, kAXEnabledAttribute) as? Bool == true &&
                (attribute($0, kAXDescriptionAttribute) as? String == description ||
                 attribute($0, kAXTitleAttribute) as? String == description)
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

        try await selectProject("api-service", pid: app.processIdentifier)
        let axApp = AXUIElementCreateApplication(app.processIdentifier)
        var value: CFTypeRef?
        guard AXUIElementCopyAttributeValue(axApp, kAXWindowsAttribute as CFString, &value) == .success,
              let windows = value as? [AXUIElement], let window = windows.first(where: {
                  (attribute($0, kAXTitleAttribute) as? String ?? "").contains("—")
              }) else {
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
        guard let display = content.displays.first(where: { $0.frame.contains(target.frame) }),
              let capturedApp = content.applications.first(where: { $0.processID == app.processIdentifier }) else {
            throw DemoError.message("Demo window must fit on one display")
        }
        // Include the demo's answer panels and Briefing, excluding every other
        // application. Crop to the workspace, never the operator's desktop.
        let filter = SCContentFilter(display: display, including: [capturedApp], exceptingWindows: [])
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
        config.sourceRect = target.frame.offsetBy(dx: -display.frame.minX, dy: -display.frame.minY)
        config.minimumFrameInterval = CMTime(value: 1, timescale: 30)
        config.queueDepth = 6

        try await selectProject("api-service", pid: app.processIdentifier)
        try await pause(1)
        let movie = try MovieOutput(url: out.appendingPathComponent("capture.mov"),
                                   width: config.width, height: config.height)
        let stream = SCStream(filter: filter, configuration: config, delegate: nil)
        try stream.addStreamOutput(movie, type: .screen, sampleHandlerQueue: movie.queue)
        try await stream.startCapture()
        var events: [[String: Any]] = []
        let begin = Date()
        do {
            for second in [0, 4, 8, 12, 16, 20, 26] {
                let delay = Double(second) - Date().timeIntervalSince(begin)
                if delay > 0 { try await pause(delay) }
                guard !app.isTerminated else { throw DemoError.message("Demo app exited") }
                let scene: String
                switch second {
                case 0:
                    scene = "Working sidebar row with live tool and elapsed time"
                case 4:
                    try type("r", pid: app.processIdentifier)
                    scene = "API service moves to Needs attention"
                case 8:
                    try pressButton("Answer…", pid: app.processIdentifier)
                    scene = "Answer queue: api-service prompt"
                case 12:
                    try pressButton("y", pid: app.processIdentifier)
                    scene = "Answer sent; queue advances to documentation"
                case 16:
                    try pressButton("y", pid: app.processIdentifier)
                    try await pause(1.5)
                    try pressButton("Toggle Inspector", pid: app.processIdentifier)
                    scene = "Second answer sent; all caught up"
                case 20:
                    try pressMenu("showBriefing:", pid: app.processIdentifier)
                    scene = "Briefing: retained activity across projects"
                default:
                    try key(13, flags: .maskCommand, pid: app.processIdentifier)
                    try pressButton("Toggle Inspector", pid: app.processIdentifier)
                    try await selectProject("api-service", pid: app.processIdentifier)
                    try pressMenu("splitWorkspaceChanges:", pid: app.processIdentifier)
                    try await pause(0.5)
                    try pressButton("Hide changed files", pid: app.processIdentifier)
                    scene = "Review checkout changes beside the agent"
                }
                try await pause(0.8)
                // Keep standalone views inside the fixed app-only crop.
                if let current = attribute(axApp, kAXFocusedWindowAttribute) as! AXUIElement? {
                    var origin = position
                    if second == 20 {
                        var viewSize = size
                        if let value = AXValueCreate(.cgSize, &viewSize) {
                            AXUIElementSetAttributeValue(current, kAXSizeAttribute as CFString, value)
                        }
                    } else if second == 8 || second == 12 {
                        origin = CGPoint(x: position.x + 310, y: position.y + 100)
                    }
                    if let value = AXValueCreate(.cgPoint, &origin) {
                        AXUIElementSetAttributeValue(current, kAXPositionAttribute as CFString, value)
                    }
                }
                guard app.isActive else { throw DemoError.message("Demo lost foreground: \(scene)") }
                try await pause(0.3)
                let expectedTitle: String
                switch second {
                case 0, 4, 8: expectedTitle = "api-service"
                case 12, 16: expectedTitle = "documentation"
                case 20: expectedTitle = "Briefing"
                default: expectedTitle = "Checkout Changes"
                }
                let sceneWindow = second == 20
                    ? find(axApp, matching: { attribute($0, kAXRoleAttribute) as? String == kAXWindowRole &&
                        attribute($0, kAXTitleAttribute) as? String == "Briefing" }) : window
                let title = sceneWindow.flatMap { attribute($0, kAXTitleAttribute) as? String } ?? ""
                guard title.hasPrefix(expectedTitle) else {
                    throw DemoError.message("Wrong scene after \(scene): \(title)")
                }
                let panel = find(axApp, matching: { attribute($0, kAXRoleAttribute) as? String == kAXWindowRole &&
                    attribute($0, kAXTitleAttribute) as? String == "Agent Mux — Answer a Prompt" })
                if second == 8 || second == 12 {
                    guard let panel,
                          find(panel, matching: { attribute($0, kAXDescriptionAttribute) as? String == "y" &&
                              attribute($0, kAXEnabledAttribute) as? Bool == true }) != nil else {
                        throw DemoError.message("Expected an authorized answer panel for \(expectedTitle)")
                    }
                } else if second >= 16, panel != nil {
                    throw DemoError.message("Answer queue did not close")
                }
                let tree = describe(axApp)
                try JSONSerialization.data(withJSONObject: tree, options: [.prettyPrinted, .sortedKeys])
                    .write(to: out.appendingPathComponent("scene-\(second).json"))
                let image = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)
                let path = out.appendingPathComponent("scene-\(second).png")
                guard let destination = CGImageDestinationCreateWithURL(path as CFURL, "public.png" as CFString, 1, nil) else {
                    throw DemoError.message("Could not write scene frame")
                }
                CGImageDestinationAddImage(destination, image, nil)
                guard CGImageDestinationFinalize(destination) else { throw DemoError.message("Could not finish scene frame") }
                events.append(["second": second, "scene": scene, "foreground": app.isActive, "window_title": title])
                print(scene)
            }
            let delay = 32 - Date().timeIntervalSince(begin)
            if delay > 0 { try await pause(delay) }
        } catch {
            try? await stream.stopCapture()
            try? await movie.finish()
            throw error
        }
        try await stream.stopCapture()
        try await movie.finish()
        let metadata: [String: Any] = ["capture_fps_limit": 30, "captured_frames": movie.frames,
            "tour": "native-desktop-v3", "scripted_agent_output": true, "events": events,
            "elapsed_seconds": Date().timeIntervalSince(begin)]
        try JSONSerialization.data(withJSONObject: metadata, options: [.prettyPrinted, .sortedKeys])
            .write(to: out.appendingPathComponent("recording.json"))
        print("Captured \(movie.frames) display updates of the isolated demo app")
    }
}

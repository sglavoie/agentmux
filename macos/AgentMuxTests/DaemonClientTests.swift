import Darwin
import XCTest
@testable import AgentMux

/// A one-connection Unix socket server: it accepts, reads one line, and
/// answers with `reply` (or never answers when `reply` is nil).
private final class FakeDaemon {
    let path: String
    private let fd: Int32
    private(set) var received: String?
    private let done = DispatchSemaphore(value: 0)

    init(directory: URL, reply: String?) throws {
        path = directory.appendingPathComponent("d.sock").path
        fd = socket(AF_UNIX, SOCK_STREAM, 0)
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8)
        withUnsafeMutableBytes(of: &address.sun_path) { raw in
            raw.copyBytes(from: bytes)
            raw[bytes.count] = 0
        }
        let bound = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.bind(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) }
        }
        guard bound == 0, listen(fd, 1) == 0 else { throw LineSocketError.system("bind", errno) }
        let listener = fd
        DispatchQueue.global().async { [self] in
            let client = accept(listener, nil, nil)
            guard client >= 0 else { done.signal(); return }
            var line = Data()
            var byte: UInt8 = 0
            while read(client, &byte, 1) == 1, byte != 0x0A { line.append(byte) }
            received = String(decoding: line, as: UTF8.self)
            if let reply {
                _ = (reply + "\n").withCString { write(client, $0, strlen($0)) }
                Darwin.close(client)
            } else {
                // Hold the connection open, silent, until the test ends.
                Thread.sleep(forTimeInterval: 2)
                Darwin.close(client)
            }
            done.signal()
        }
    }

    func finish() {
        _ = done.wait(timeout: .now() + 5)
        Darwin.close(fd)
    }
}

final class DaemonClientTests: XCTestCase {
    private var directory: URL!

    override func setUpWithError() throws {
        // Short, under /tmp: sun_path holds barely 103 bytes.
        directory = URL(fileURLWithPath: "/tmp/amx-\(UUID().uuidString.prefix(8))")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: directory)
    }

    // MARK: Socket path

    func testSocketPathMatchesTheDaemon() {
        XCTAssertEqual(SocketPath.daemon(environment: ["HOME": "/Users/me"]), "/Users/me/.local/share/agent-mux/daemon.sock")
        XCTAssertEqual(SocketPath.daemon(environment: ["HOME": "", "TMPDIR": "/var/folders/x/T/"]), "/var/folders/x/T/agent-mux/daemon.sock")
        XCTAssertEqual(SocketPath.daemon(environment: [:]), "/tmp/agent-mux/daemon.sock")
        XCTAssertEqual(SocketPath.maxLength, 103)
    }

    func testLiveDefaultUsesThisProcessHome() {
        let home = ProcessInfo.processInfo.environment["HOME"] ?? ""
        XCTAssertEqual(DaemonClient().socketPath, home + "/.local/share/agent-mux/daemon.sock")
    }

    // MARK: Health replies

    func testReadyReply() {
        let reply = #"{"ok":true,"pid":9,"ready":true,"build":{"version":"0.8.0","revision":"52189e529adfe3f24cb1","dirty":true,"binary_hash":"cc"},"metrics":{"x":1}}"#
        let build = DaemonBuild(version: "0.8.0", revision: "52189e529adfe3f24cb1", dirty: true, binaryHash: "cc")
        XCTAssertEqual(DaemonClient.status(fromHealthReply: Data(reply.utf8)), .ready(build))
        XCTAssertEqual(DaemonStatusLine.text(for: .ready(build)), "ready · 0.8.0 (52189e529adf-dirty)")
    }

    func testDegradedReply() {
        let reply = #"{"ok":true,"ready":false,"degraded_reason":"restore timed out","build":{"version":"0.8.0"}}"#
        let status = DaemonClient.status(fromHealthReply: Data(reply.utf8))
        XCTAssertEqual(status, .degraded(reason: "restore timed out", build: DaemonBuild(version: "0.8.0")))
        XCTAssertEqual(DaemonStatusLine.text(for: status), "degraded · restore timed out")
    }

    func testRefusedHealthIsDegraded() {
        let status = DaemonClient.status(fromHealthReply: Data(#"{"ok":false,"error":"shutting down","ready":false}"#.utf8))
        XCTAssertEqual(status, .degraded(reason: "shutting down", build: nil))
    }

    func testGarbageReplyIsUnresponsive() {
        XCTAssertEqual(DaemonClient.status(fromHealthReply: Data("not json".utf8)), .unresponsive("unreadable health reply"))
    }

    func testStatusLineWording() {
        XCTAssertEqual(DaemonStatusLine.text(for: nil), "checking…")
        XCTAssertEqual(DaemonStatusLine.text(for: .notRunning), "not running")
        XCTAssertEqual(DaemonStatusLine.text(for: .ready(nil)), "ready")
        XCTAssertEqual(DaemonStatusLine.text(for: .degraded(reason: " ", build: nil)), "degraded")
        XCTAssertEqual(DaemonStatusLine.text(for: .unresponsive("no reply in time")), "unresponsive · no reply in time")
        XCTAssertEqual(DaemonStatusLine.title(for: .notRunning), "Daemon: not running")
        XCTAssertEqual(DaemonStatusLine.text(for: .ready(DaemonBuild(version: "0.8.0"))), "ready · 0.8.0 (unknown)")
    }

    // MARK: Over a real socket

    func testHealthRoundTrip() throws {
        let server = try FakeDaemon(directory: directory, reply: #"{"ok":true,"ready":true,"build":{"version":"0.8.0","revision":"abc"}}"#)
        let status = DaemonClient(socketPath: server.path).health()
        server.finish()
        XCTAssertEqual(status, .ready(DaemonBuild(version: "0.8.0", revision: "abc")))
        XCTAssertEqual(server.received, #"{"type":"health"}"#)
    }

    func testMissingSocketIsNotRunning() {
        XCTAssertEqual(DaemonClient(socketPath: directory.appendingPathComponent("none.sock").path).health(), .notRunning)
    }

    func testStaleSocketFileIsNotRunning() throws {
        // A socket file nobody listens on: a daemon that died without cleanup.
        let path = directory.appendingPathComponent("stale.sock").path
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8)
        withUnsafeMutableBytes(of: &address.sun_path) { raw in
            raw.copyBytes(from: bytes)
            raw[bytes.count] = 0
        }
        _ = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.bind(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) }
        }
        Darwin.close(fd)
        XCTAssertTrue(FileManager.default.fileExists(atPath: path))
        XCTAssertEqual(DaemonClient(socketPath: path).health(), .notRunning)
    }

    func testSilentDaemonTimesOut() throws {
        let server = try FakeDaemon(directory: directory, reply: nil)
        let start = Date()
        let status = DaemonClient(socketPath: server.path).health(timeout: 0.3)
        XCTAssertEqual(status, .unresponsive("no reply in time"))
        XCTAssertLessThan(Date().timeIntervalSince(start), 1.5)
        server.finish()
    }

    func testOverlongPathIsRefusedBeforeConnecting() {
        let path = "/tmp/" + String(repeating: "x", count: 120)
        XCTAssertThrowsError(try LineSocket.roundTrip(path: path, request: Data("{}".utf8), timeout: 1)) { error in
            XCTAssertEqual(error as? LineSocketError, .pathTooLong(path))
        }
    }

    func testReadLineSplitsBufferedLines() throws {
        let server = try FakeDaemon(directory: directory, reply: "first\nsecond")
        let socket = LineSocket()
        try socket.connect(path: server.path)
        let deadline = Date().addingTimeInterval(2)
        try socket.writeLine(Data("hi".utf8), deadline: deadline)
        XCTAssertEqual(String(decoding: try socket.readLine(deadline: deadline), as: UTF8.self), "first")
        XCTAssertEqual(String(decoding: try socket.readLine(deadline: deadline), as: UTF8.self), "second")
        XCTAssertThrowsError(try socket.readLine(deadline: deadline)) { error in
            XCTAssertEqual(error as? LineSocketError, .closed)
        }
        socket.close()
        server.finish()
        XCTAssertEqual(server.received, "hi")
    }

    func testOversizeLineIsRefused() throws {
        let server = try FakeDaemon(directory: directory, reply: String(repeating: "x", count: 4096))
        let socket = LineSocket(maxLineBytes: 1024)
        try socket.connect(path: server.path)
        let deadline = Date().addingTimeInterval(2)
        try socket.writeLine(Data("hi".utf8), deadline: deadline)
        XCTAssertThrowsError(try socket.readLine(deadline: deadline)) { error in
            XCTAssertEqual(error as? LineSocketError, .lineTooLong)
        }
        socket.close()
        server.finish()
    }
}

import Darwin
import Foundation
@testable import AgentMux

/// One accepted connection on a `FakeStreamServer`.
final class FakeConnection {
    let fd: Int32
    private var buffer = Data()

    init(fd: Int32) { self.fd = fd }

    /// The next line the client wrote; nil at EOF.
    func readLine() -> String? {
        var byte: UInt8 = 0
        var line = Data()
        while read(fd, &byte, 1) == 1 {
            if byte == 0x0A { return String(decoding: line, as: UTF8.self) }
            line.append(byte)
        }
        return nil
    }

    func write(_ line: String) {
        _ = (line + "\n").withCString { Darwin.write(fd, $0, strlen($0)) }
    }

    func close() { Darwin.close(fd) }
}

/// A Unix-socket server that hands each accepted connection to the next
/// script in order; connections past the last script are closed at once.
final class FakeStreamServer {
    let path: String
    private let fd: Int32
    private let lock = NSLock()
    private var scripts: [(FakeConnection) -> Void]
    private var acceptedCount = 0
    private var requestLines: [String] = []

    init(directory: URL, scripts: [(FakeConnection) -> Void]) throws {
        path = directory.appendingPathComponent("s.sock").path
        self.scripts = scripts
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
        guard bound == 0, Darwin.listen(fd, 8) == 0 else { throw LineSocketError.system("bind", errno) }
        let listener = fd
        Thread.detachNewThread { [weak self] in
            while true {
                let client = accept(listener, nil, nil)
                guard client >= 0 else { return }
                guard let self else { Darwin.close(client); return }
                let script = self.nextScript()
                DispatchQueue.global().async {
                    let connection = FakeConnection(fd: client)
                    guard let script else { connection.close(); return }
                    script(connection)
                }
            }
        }
    }

    private func nextScript() -> ((FakeConnection) -> Void)? {
        lock.lock(); defer { lock.unlock() }
        acceptedCount += 1
        return scripts.isEmpty ? nil : scripts.removeFirst()
    }

    var accepted: Int {
        lock.lock(); defer { lock.unlock() }
        return acceptedCount
    }

    func shutdown() {
        Darwin.shutdown(fd, SHUT_RDWR)
        Darwin.close(fd)
    }
}

/// Frames as the daemon writes them.
enum WireFixture {
    static let ack = #"{"ok":true,"subscription":{"protocol":1,"server_protocol_max":1,"subscriber_id":"sub-1","heartbeat_ms":15000,"features":["snapshot","heartbeat"]},"ready":true}"#

    static func ack(heartbeatMS: Int) -> String {
        #"{"ok":true,"subscription":{"protocol":1,"server_protocol_max":1,"subscriber_id":"sub-1","heartbeat_ms":\#(heartbeatMS)},"ready":true}"#
    }

    static func snapshot(seq: Int, key: Int = 1) -> String {
        #"{"v":1,"seq":\#(seq),"type":"snapshot","data":{"v":1,"daemon":{"pid":7,"ready":true},"kitty":{"ready":true,"socket":"unix:/tmp/kitty-1"},"notification":{},"dashboard":{"open":false},"attention":{"total":\#(key),"key":\#(key)},"asl":{},"sessions":[{"window_id":3,"icon_state":"key","attention":true,"project":"api","harness":"claude","reason":"permission"}]}}"#
    }

    static func heartbeat(seq: Int) -> String { #"{"v":1,"seq":\#(seq),"type":"heartbeat"}"# }

    static func bye(seq: Int, reason: String = "shutdown") -> String {
        #"{"v":1,"seq":\#(seq),"type":"bye","data":{"reason":"\#(reason)"}}"#
    }
}

/// Collects a client's events and lets a test wait for one.
final class EventRecorder {
    private let lock = NSLock()
    private var recorded: [SubscriptionClient.Event] = []

    func record(_ event: SubscriptionClient.Event) {
        lock.lock(); recorded.append(event); lock.unlock()
    }

    var events: [SubscriptionClient.Event] {
        lock.lock(); defer { lock.unlock() }
        return recorded
    }

    /// Waits until `predicate` holds for the events so far.
    @discardableResult
    func wait(timeout: TimeInterval = 5, until predicate: ([SubscriptionClient.Event]) -> Bool) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if predicate(events) { return true }
            Thread.sleep(forTimeInterval: 0.01)
        }
        return predicate(events)
    }
}

extension Array where Element == SubscriptionClient.Event {
    var subscribedCount: Int {
        filter { if case .subscribed = $0 { return true } else { return false } }.count
    }

    var snapshotSeqs: [UInt64] {
        compactMap { if case let .frame(.snapshot(seq, _)) = $0 { return seq } else { return nil } }
    }

    var disconnects: [SubscriptionClient.Disconnect] {
        compactMap { if case let .disconnected(reason, _) = $0 { return reason } else { return nil } }
    }
}

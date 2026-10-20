import Foundation

/// Watches one file, `config.toml`, and says when it may have changed, so the
/// project list is read again then rather than on a timer.
///
/// Editors save in place or by writing a new file and renaming it over the
/// old one, so the file's directory is watched as well as the file: a rename
/// or a delete re-opens the file (which may not exist yet), and every change
/// is reported once things settle (`debounce`). A directory that does not
/// exist is not watched; the next `watch` of the path tries again.
@MainActor
final class ConfigWatcher {
    private(set) var path: String?
    private let debounce: TimeInterval
    private let onChange: @MainActor () -> Void
    private var directorySource: DispatchSourceFileSystemObject?
    private var fileSource: DispatchSourceFileSystemObject?
    private var pending: DispatchWorkItem?

    init(debounce: TimeInterval = 0.5, onChange: @escaping @MainActor () -> Void) {
        self.debounce = debounce
        self.onChange = onChange
    }

    /// Watches `path`, replacing any earlier one; the same path again keeps
    /// what is watched, unless the directory could not be opened before.
    func watch(_ path: String) {
        guard path != self.path || directorySource == nil else { return }
        stop()
        self.path = path
        let directory = (path as NSString).deletingLastPathComponent
        directorySource = source(directory, events: [.write, .rename, .delete]) { [weak self] in
            self?.reopenFile()
            self?.changed()
        }
        reopenFile()
    }

    func stop() {
        pending?.cancel()
        pending = nil
        directorySource?.cancel()
        directorySource = nil
        fileSource?.cancel()
        fileSource = nil
        path = nil
    }

    private func reopenFile() {
        fileSource?.cancel()
        fileSource = nil
        guard let path else { return }
        fileSource = source(path, events: [.write, .extend, .rename, .delete]) { [weak self] in
            self?.reopenFile()
            self?.changed()
        }
    }

    private func changed() {
        pending?.cancel()
        let item = DispatchWorkItem { [weak self] in
            MainActor.assumeIsolated { self?.onChange() }
        }
        pending = item
        DispatchQueue.main.asyncAfter(deadline: .now() + debounce, execute: item)
    }

    private func source(_ path: String, events: DispatchSource.FileSystemEvent,
                        handler: @escaping @MainActor () -> Void) -> DispatchSourceFileSystemObject? {
        let descriptor = open(path, O_EVTONLY)
        guard descriptor >= 0 else { return nil }
        let source = DispatchSource.makeFileSystemObjectSource(fileDescriptor: descriptor, eventMask: events, queue: .main)
        source.setEventHandler { MainActor.assumeIsolated { handler() } }
        source.setCancelHandler { close(descriptor) }
        source.resume()
        return source
    }
}

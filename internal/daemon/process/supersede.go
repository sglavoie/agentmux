package process

import "os"

// SocketOlderThanBinary reports whether a running daemon predates the amx
// binary a client is about to talk to, which is the signal to restart it
// rather than speak to a stale build. An unknown binary or an unreadable
// socket is never treated as staleness: the client keeps talking to whatever
// is already listening.
func SocketOlderThanBinary(socketPath, amxBin string) bool {
	if amxBin == "" {
		return false
	}
	socketInfo, err := os.Stat(socketPath)
	if err != nil {
		return false
	}
	binInfo, err := os.Stat(amxBin)
	if err != nil {
		return false
	}
	return binInfo.ModTime().After(socketInfo.ModTime())
}

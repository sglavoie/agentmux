package agentloop

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

func pathSHA256(path string) string {
	canonical, err := CanonicalizePath(path)
	if err != nil {
		canonical = filepath.Clean(path)
	}
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

func isHexSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

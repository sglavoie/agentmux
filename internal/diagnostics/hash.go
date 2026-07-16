package diagnostics

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const hmacKeyBytes = 32

func loadOrCreateHMACKey(path string) ([]byte, error) {
	return loadOrCreateHMACKeyContext(context.Background(), path)
}

func loadOrCreateHMACKeyContext(ctx context.Context, path string) ([]byte, error) {
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	lock, err := acquireFileLock(ctx, path+".lock", time.Second)
	if err != nil {
		return nil, fmt.Errorf("diagnostics: locking HMAC key: %w", err)
	}
	defer releaseFileLock(lock)
	return loadOrCreateHMACKeyLocked(path)
}

func loadOrCreateHMACKeyLocked(path string) ([]byte, error) {
	if err := validateRegularTarget(path); err != nil {
		return nil, err
	}
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != hmacKeyBytes {
			return nil, fmt.Errorf("diagnostics: HMAC key has %d bytes, want %d", len(key), hmacKeyBytes)
		}
		if err := os.Chmod(path, fileMode); err != nil {
			return nil, err
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("diagnostics: reading HMAC key: %w", err)
	}
	key = make([]byte, hmacKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("diagnostics: generating HMAC key: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return nil, fmt.Errorf("diagnostics: creating HMAC key: %w", err)
	}
	if _, err := file.Write(key); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	err = errors.Join(file.Sync(), file.Close())
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return key, nil
}

// PayloadHMAC returns a non-linkable local digest suitable for trace records.
func PayloadHMAC(key, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
}

// HashLocalIdentifier applies the private diagnostic HMAC key to an operator
// identifier before it leaves the machine in a shareable bundle.
func HashLocalIdentifier(paths Paths, value string) (string, error) {
	key, err := loadOrCreateHMACKey(paths.HMACKey)
	if err != nil {
		return "", err
	}
	return localIdentifierHash(key, value), nil
}

// LocalIdentifierHasher returns a reusable private HMAC function so archive
// generation does not reopen and relock the diagnostic key for every field.
func LocalIdentifierHasher(paths Paths) (func(string) string, error) {
	key, err := loadOrCreateHMACKey(paths.HMACKey)
	if err != nil {
		return nil, err
	}
	return func(value string) string { return localIdentifierHash(key, value) }, nil
}

func localIdentifierHash(key []byte, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return PayloadHMAC(key, []byte(value))
}

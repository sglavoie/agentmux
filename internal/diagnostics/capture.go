package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	CaptureSchemaVersion = 1
	CaptureDefaultTTL    = 15 * time.Minute
	CaptureMaximumTTL    = 60 * time.Minute
	CaptureMaximumBytes  = int64(64 << 20)
	CapturePayloadKeep   = 24 * time.Hour
)

// CaptureScope limits a temporary raw-payload capture. At least one field is
// required so an operator cannot accidentally enable a machine-wide capture.
//
// RuntimeSessionID scopes a capture to one runtime session, which is how a
// session with no Kitty window is named: its hooks carry no window ID.
type CaptureScope struct {
	WindowID         int    `json:"window_id,omitempty"`
	RuntimeSessionID string `json:"runtime_session_id,omitempty"`
	Harness          string `json:"harness,omitempty"`
}

// named reports whether the scope limits the capture to anything.
func (s CaptureScope) named() bool {
	return s.WindowID != 0 || strings.TrimSpace(s.RuntimeSessionID) != "" || strings.TrimSpace(s.Harness) != ""
}

// CaptureDescriptor is the restart-safe control plane consumed independently
// by hook clients and the daemon. Raw payloads live below captures/<id>/ and
// are deliberately not represented in the diagnostic journal.
type CaptureDescriptor struct {
	SchemaVersion       int          `json:"schema_version"`
	ID                  string       `json:"id"`
	Status              string       `json:"status"`
	Scope               CaptureScope `json:"scope"`
	IncludeHookPayloads bool         `json:"include_hook_payloads"`
	StartedAt           time.Time    `json:"started_at"`
	ExpiresAt           time.Time    `json:"expires_at"`
	EndedAt             *time.Time   `json:"ended_at,omitempty"`
	QuotaBytes          int64        `json:"quota_bytes"`
	CapturedBytes       int64        `json:"captured_bytes"`
	CapturedPayloads    int          `json:"captured_payloads"`
}

func (d CaptureDescriptor) Active() bool { return d.Status == "active" }

type CaptureOptions struct {
	Now         func() time.Time
	LockTimeout time.Duration
}

type CaptureStartRequest struct {
	Scope               CaptureScope
	Duration            time.Duration
	IncludeHookPayloads bool
}

type CapturePayloadRequest struct {
	WindowID int
	// RuntimeSessionID is the runtime session the hook ran in, when it is a
	// managed one.
	RuntimeSessionID string
	Harness          string
	Event            string
	TraceID          string
	RequestID        string
	Payload          []byte
	Truncated        bool
}

type CaptureWriteResult struct {
	Captured  bool   `json:"captured"`
	Duplicate bool   `json:"duplicate,omitempty"`
	Reason    string `json:"reason"`
	CaptureID string `json:"capture_id,omitempty"`
	Path      string `json:"-"`
}

type CaptureStatus struct {
	Descriptor      *CaptureDescriptor `json:"descriptor,omitempty"`
	PurgedPayloads  int                `json:"purged_payloads,omitempty"`
	PurgedBytes     int64              `json:"purged_bytes,omitempty"`
	QuarantinedFile string             `json:"quarantined_file,omitempty"`
}

type CaptureStore struct {
	paths      Paths
	descriptor string
	lock       string
	now        func() time.Time
	lockWait   time.Duration
}

func NewCaptureStore(paths Paths, options CaptureOptions) (*CaptureStore, error) {
	if paths.Root == "" || paths.CaptureDir == "" || !withinRoot(paths.Root, paths.CaptureDir) {
		return nil, errors.New("diagnostics: invalid capture paths")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.LockTimeout <= 0 {
		options.LockTimeout = 100 * time.Millisecond
	}
	descriptor := filepath.Join(paths.CaptureDir, "capture.json")
	return &CaptureStore{
		paths: paths, descriptor: descriptor, lock: descriptor + ".lock",
		now: options.Now, lockWait: options.LockTimeout,
	}, nil
}

func (s *CaptureStore) Start(ctx context.Context, request CaptureStartRequest) (CaptureDescriptor, error) {
	if s == nil {
		return CaptureDescriptor{}, errors.New("diagnostics: nil capture store")
	}
	request.Scope.Harness = strings.ToLower(strings.TrimSpace(request.Scope.Harness))
	if request.Scope.WindowID < 0 {
		return CaptureDescriptor{}, errors.New("diagnostics: capture window ID must not be negative")
	}
	request.Scope.RuntimeSessionID = strings.TrimSpace(request.Scope.RuntimeSessionID)
	if request.Scope.WindowID != 0 && request.Scope.RuntimeSessionID != "" {
		return CaptureDescriptor{}, errors.New("diagnostics: capture scope names a window or a runtime session, not both")
	}
	if !request.Scope.named() {
		return CaptureDescriptor{}, errors.New("diagnostics: capture requires --window, --runtime-session, and/or --harness scope")
	}
	if !request.IncludeHookPayloads {
		return CaptureDescriptor{}, errors.New("diagnostics: raw capture requires explicit hook-payload inclusion")
	}
	if request.Duration == 0 {
		request.Duration = CaptureDefaultTTL
	}
	if request.Duration < time.Second || request.Duration > CaptureMaximumTTL {
		return CaptureDescriptor{}, fmt.Errorf("diagnostics: capture duration must be between 1s and %s", CaptureMaximumTTL)
	}
	if err := s.paths.Ensure(); err != nil {
		return CaptureDescriptor{}, err
	}
	var started CaptureDescriptor
	err := s.withLock(ctx, func() error {
		_, _ = s.maintainLocked()
		current, err := s.loadLocked(false)
		if err != nil {
			return err
		}
		if current != nil && current.Active() {
			return fmt.Errorf("diagnostics: capture %s is already active until %s", current.ID, current.ExpiresAt.Format(time.RFC3339))
		}
		id, err := NewLaunchID()
		if err != nil {
			return err
		}
		now := s.now().UTC()
		started = CaptureDescriptor{
			SchemaVersion: CaptureSchemaVersion, ID: id, Status: "active", Scope: request.Scope,
			IncludeHookPayloads: true, StartedAt: now, ExpiresAt: now.Add(request.Duration),
			QuotaBytes: CaptureMaximumBytes,
		}
		if err := ensurePrivateDirectory(s.captureDirectory(id)); err != nil {
			return err
		}
		return s.writeLocked(started)
	})
	return started, err
}

func (s *CaptureStore) Stop(ctx context.Context) (CaptureDescriptor, error) {
	if s == nil {
		return CaptureDescriptor{}, errors.New("diagnostics: nil capture store")
	}
	if _, err := os.Lstat(s.descriptor); errors.Is(err, os.ErrNotExist) {
		return CaptureDescriptor{}, errors.New("diagnostics: no capture has been started")
	}
	var stopped CaptureDescriptor
	err := s.withLock(ctx, func() error {
		_, _ = s.maintainLocked()
		current, err := s.loadLocked(false)
		if err != nil {
			return err
		}
		if current == nil {
			return errors.New("diagnostics: no capture has been started")
		}
		stopped = *current
		if stopped.Active() {
			now := s.now().UTC()
			stopped.Status = "stopped"
			stopped.EndedAt = &now
			if err := s.writeLocked(stopped); err != nil {
				return err
			}
		}
		return nil
	})
	return stopped, err
}

// Status performs bounded maintenance before returning the durable descriptor.
// This is what makes expiration and quota state restart-safe without requiring
// a long-lived timer in every hook process.
func (s *CaptureStore) Status(ctx context.Context) (CaptureStatus, error) {
	if s == nil {
		return CaptureStatus{}, errors.New("diagnostics: nil capture store")
	}
	if _, err := os.Lstat(s.descriptor); errors.Is(err, os.ErrNotExist) {
		return CaptureStatus{}, nil
	}
	var status CaptureStatus
	err := s.withLock(ctx, func() error {
		maintained, err := s.maintainLocked()
		status = maintained
		if err != nil {
			return err
		}
		current, err := s.loadLocked(false)
		if err != nil {
			return err
		}
		status.Descriptor = current
		return nil
	})
	return status, err
}

func (s *CaptureStore) Maintain(ctx context.Context) (CaptureStatus, error) {
	if s == nil {
		return CaptureStatus{}, errors.New("diagnostics: nil capture store")
	}
	if _, err := os.Lstat(s.paths.CaptureDir); errors.Is(err, os.ErrNotExist) {
		return CaptureStatus{}, nil
	}
	var status CaptureStatus
	err := s.withLock(ctx, func() error {
		var err error
		status, err = s.maintainLocked()
		return err
	})
	return status, err
}

func (s *CaptureStore) Record(ctx context.Context, request CapturePayloadRequest) (CaptureWriteResult, error) {
	result := CaptureWriteResult{Reason: "inactive"}
	if s == nil {
		return result, errors.New("diagnostics: nil capture store")
	}
	// Missing state is the hot-path norm. Avoid creating directories or lock
	// files for users who never opted into raw capture.
	if _, err := os.Lstat(s.descriptor); errors.Is(err, os.ErrNotExist) {
		return result, nil
	} else if err != nil {
		return result, err
	}
	err := s.withLock(ctx, func() error {
		if _, err := s.maintainLocked(); err != nil {
			return err
		}
		descriptor, err := s.loadLocked(false)
		if err != nil {
			return err
		}
		if descriptor == nil || !descriptor.Active() || !descriptor.IncludeHookPayloads {
			return nil
		}
		result.CaptureID = descriptor.ID
		if descriptor.Scope.WindowID != 0 && descriptor.Scope.WindowID != request.WindowID ||
			descriptor.Scope.RuntimeSessionID != "" && descriptor.Scope.RuntimeSessionID != strings.TrimSpace(request.RuntimeSessionID) ||
			descriptor.Scope.Harness != "" && descriptor.Scope.Harness != strings.ToLower(strings.TrimSpace(request.Harness)) {
			result.Reason = "scope_mismatch"
			return nil
		}
		entry := struct {
			SchemaVersion  int       `json:"schema_version"`
			CapturedAt     time.Time `json:"captured_at"`
			WindowID       int       `json:"window_id,omitempty"`
			RuntimeSession string    `json:"runtime_session_id,omitempty"`
			Harness        string    `json:"harness,omitempty"`
			Event          string    `json:"event,omitempty"`
			TraceID        string    `json:"trace_id,omitempty"`
			RequestID      string    `json:"request_id,omitempty"`
			Payload        string    `json:"payload"`
			Truncated      bool      `json:"truncated,omitempty"`
		}{
			SchemaVersion: CaptureSchemaVersion, CapturedAt: s.now().UTC(), WindowID: request.WindowID,
			RuntimeSession: strings.TrimSpace(request.RuntimeSessionID),
			Harness:        strings.TrimSpace(request.Harness), Event: strings.TrimSpace(request.Event),
			TraceID: strings.TrimSpace(request.TraceID), RequestID: strings.TrimSpace(request.RequestID),
			Payload: string(request.Payload), Truncated: request.Truncated,
		}
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		data = append(data, '\n')
		hmacKey, err := loadOrCreateHMACKeyContext(ctx, s.paths.HMACKey)
		if err != nil {
			return fmt.Errorf("diagnostics: loading capture HMAC key: %w", err)
		}
		name := strings.TrimPrefix(PayloadHMAC(hmacKey, captureDedupeMaterial(request, entry.CapturedAt)), "hmac-sha256:") + ".raw.json"
		path := filepath.Join(s.captureDirectory(descriptor.ID), name)
		if !withinRoot(s.paths.CaptureDir, path) {
			return errors.New("diagnostics: capture payload path escaped root")
		}
		if info, statErr := os.Lstat(path); statErr == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("diagnostics: capture payload target %s is not regular", path)
			}
			result.Captured, result.Duplicate, result.Reason, result.Path = true, true, "already_captured", path
			return nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if descriptor.CapturedBytes+int64(len(data)) > descriptor.QuotaBytes {
			now := s.now().UTC()
			descriptor.Status = "quota_reached"
			descriptor.EndedAt = &now
			result.Reason = "quota_reached"
			return s.writeLocked(*descriptor)
		}
		if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
			return err
		}
		if err := atomicWritePrivate(path, data); err != nil {
			return err
		}
		descriptor.CapturedBytes += int64(len(data))
		descriptor.CapturedPayloads++
		if err := s.writeLocked(*descriptor); err != nil {
			_ = os.Remove(path)
			return err
		}
		result.Captured, result.Reason, result.Path = true, "captured", path
		return nil
	})
	return result, err
}

func captureDedupeMaterial(request CapturePayloadRequest, capturedAt time.Time) []byte {
	material := append([]byte("agentmux.capture.v1\x00event\x00"), strings.TrimSpace(request.Event)...)
	if requestID := strings.TrimSpace(request.RequestID); requestID != "" {
		material = append(material, "\x00request_id\x00"...)
		return append(material, requestID...)
	}
	if traceID := strings.TrimSpace(request.TraceID); traceID != "" {
		material = append(material, "\x00trace_id\x00"...)
		return append(material, traceID...)
	}
	material = append(material, "\x00captured_at\x00"...)
	material = append(material, capturedAt.UTC().Format(time.RFC3339Nano)...)
	material = append(material, "\x00payload\x00"...)
	return append(material, request.Payload...)
}

func (s *CaptureStore) withLock(ctx context.Context, fn func() error) error {
	if err := ensurePrivateDirectory(s.paths.CaptureDir); err != nil {
		return err
	}
	lockContext := ctx
	if lockContext == nil {
		lockContext = context.Background()
	}
	if _, hasDeadline := lockContext.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		lockContext, cancel = context.WithTimeout(lockContext, s.lockWait)
		defer cancel()
	}
	lock, err := acquireFileLock(lockContext, s.lock, s.lockWait)
	if err != nil {
		return err
	}
	defer releaseFileLock(lock)
	return fn()
}

func (s *CaptureStore) maintainLocked() (CaptureStatus, error) {
	var status CaptureStatus
	descriptor, err := s.loadLocked(true)
	if err != nil {
		return status, err
	}
	now := s.now().UTC()
	descriptorChanged := false
	if descriptor != nil && descriptor.Active() && !now.Before(descriptor.ExpiresAt) {
		descriptor.Status = "expired"
		descriptor.EndedAt = &now
		descriptorChanged = true
	}
	cutoff := now.Add(-CapturePayloadKeep)
	entries, err := os.ReadDir(s.paths.CaptureDir)
	if err != nil {
		return status, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if !entry.IsDir() || !validLaunchID(entry.Name()) {
			continue
		}
		dir := filepath.Join(s.paths.CaptureDir, entry.Name())
		files, readErr := os.ReadDir(dir)
		if readErr != nil {
			continue
		}
		for _, file := range files {
			path := filepath.Join(dir, file.Name())
			info, infoErr := os.Lstat(path)
			if infoErr != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
				continue
			}
			if removeErr := os.Remove(path); removeErr == nil {
				status.PurgedPayloads++
				status.PurgedBytes += info.Size()
			}
		}
		remaining, _ := os.ReadDir(dir)
		if descriptor != nil && descriptor.ID == entry.Name() {
			var bytes int64
			var payloads int
			for _, file := range remaining {
				info, infoErr := file.Info()
				if infoErr == nil && info.Mode().IsRegular() && strings.HasSuffix(file.Name(), ".raw.json") {
					bytes += info.Size()
					payloads++
				}
			}
			if descriptor.CapturedBytes != bytes || descriptor.CapturedPayloads != payloads {
				descriptor.CapturedBytes = bytes
				descriptor.CapturedPayloads = payloads
				descriptorChanged = true
			}
		}
		if len(remaining) == 0 {
			_ = os.Remove(dir)
		}
	}
	if descriptor != nil && descriptor.Active() && descriptor.QuotaBytes > 0 && descriptor.CapturedBytes >= descriptor.QuotaBytes {
		descriptor.Status = "quota_reached"
		descriptor.EndedAt = &now
		descriptorChanged = true
	}
	if descriptor != nil && descriptorChanged {
		if err := s.writeLocked(*descriptor); err != nil {
			return status, err
		}
	}
	status.Descriptor = descriptor
	return status, nil
}

func (s *CaptureStore) loadLocked(quarantine bool) (*CaptureDescriptor, error) {
	if err := validateRegularTarget(s.descriptor); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.descriptor)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var descriptor CaptureDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil || !validCaptureDescriptor(descriptor) {
		if quarantine {
			stamp := s.now().UTC().Format("20060102T150405.000000000Z")
			quarantined := s.descriptor + ".corrupt-" + stamp
			if renameErr := os.Rename(s.descriptor, quarantined); renameErr != nil {
				return nil, fmt.Errorf("diagnostics: invalid capture descriptor and quarantine failed: %w", renameErr)
			}
			return nil, nil
		}
		if err == nil {
			err = errors.New("invalid descriptor fields")
		}
		return nil, fmt.Errorf("diagnostics: decoding capture descriptor: %w", err)
	}
	return &descriptor, nil
}

func validCaptureDescriptor(descriptor CaptureDescriptor) bool {
	if descriptor.SchemaVersion != CaptureSchemaVersion || !validLaunchID(descriptor.ID) || descriptor.Scope.WindowID < 0 ||
		!descriptor.Scope.named() || !descriptor.IncludeHookPayloads ||
		descriptor.StartedAt.IsZero() || descriptor.ExpiresAt.Before(descriptor.StartedAt) ||
		descriptor.ExpiresAt.Sub(descriptor.StartedAt) > CaptureMaximumTTL || descriptor.QuotaBytes <= 0 ||
		descriptor.QuotaBytes > CaptureMaximumBytes || descriptor.CapturedBytes < 0 || descriptor.CapturedPayloads < 0 {
		return false
	}
	switch descriptor.Status {
	case "active", "stopped", "expired", "quota_reached":
		return true
	default:
		return false
	}
}

func (s *CaptureStore) writeLocked(descriptor CaptureDescriptor) error {
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := validateRegularTarget(s.descriptor); err != nil {
		return err
	}
	return atomicWritePrivate(s.descriptor, data)
}

func (s *CaptureStore) captureDirectory(id string) string {
	return filepath.Join(s.paths.CaptureDir, id)
}

package flightlog

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/atomicfile"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"golang.org/x/sys/unix"
)

type Store struct{ Path string }

type header struct {
	SchemaVersion int       `json:"schema_version"`
	TrackingSince time.Time `json:"tracking_since"`
	Settings      Settings  `json:"settings"`
}

func DefaultStore() (Store, error) {
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		return Store{}, err
	}
	return Store{Path: filepath.Join(paths.Root, "flight-log.jsonl")}, nil
}

// withLock uses the same stable-sidecar convention as the runtime registry,
// but honors a caller budget so statistics cannot stall launching or hooks.
func (s Store) withLock(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(s.Path) {
		return errors.New("flight log: path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }()
	return fn()
}

func defaultHeader(now time.Time) header {
	return header{SchemaVersion: SchemaVersion, TrackingSince: now.UTC(), Settings: Settings{Enabled: true, Celebrations: true}}
}

func (s Store) readHeader() (header, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return header{}, err
	}
	defer func() { _ = f.Close() }()
	return readHeader(bufio.NewReader(f))
}

func readHeader(reader *bufio.Reader) (header, error) {
	var h header
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return h, fmt.Errorf("flight log: incomplete header: %w", err)
	}
	if err := json.Unmarshal(line, &h); err != nil {
		return h, fmt.Errorf("flight log: invalid header: %w", err)
	}
	if h.SchemaVersion != SchemaVersion || h.TrackingSince.IsZero() {
		return h, errors.New("flight log: unsupported or invalid header; data preserved")
	}
	return h, nil
}

func (s Store) initialize(now time.Time) (header, error) {
	h, err := s.readHeader()
	if !errors.Is(err, os.ErrNotExist) {
		return h, err
	}
	h = defaultHeader(now)
	data, err := json.Marshal(h)
	if err != nil {
		return h, err
	}
	return h, atomicfile.Write(s.Path, append(data, '\n'), 0o600)
}

func (s Store) Initialize(ctx context.Context, now time.Time) error {
	return s.withLock(ctx, func() error { _, err := s.initialize(now); return err })
}

// Append persists only compact events. Duplicate IDs are harmless: projections
// deduplicate after sorting, including late delivery and explicit imports.
func (s Store) Append(ctx context.Context, events ...Event) error {
	if len(events) == 0 {
		return nil
	}
	return s.withLock(ctx, func() error {
		started := time.Now()
		for _, event := range events {
			if !event.Partial && !event.At.IsZero() && event.At.Before(started) {
				started = event.At
			}
		}
		h, err := s.initialize(started)
		if err != nil || !h.Settings.Enabled {
			return err
		}
		f, err := os.OpenFile(s.Path, os.O_RDWR|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		// A partial last write stays visible as a corrupt line. Start the next
		// record on a new line instead of destroying either record.
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if info.Size() > 0 {
			var last [1]byte
			if _, err := f.ReadAt(last[:], info.Size()-1); err != nil {
				return err
			}
			if last[0] != '\n' {
				if _, err := f.Write([]byte{'\n'}); err != nil {
					return err
				}
			}
		}
		encoder := json.NewEncoder(f)
		for _, event := range events {
			if event.ID == "" || event.At.IsZero() {
				return errors.New("flight log: event identity and time required")
			}
			if event.At.Before(h.TrackingSince) && !strings.HasPrefix(event.ID, "import:") {
				continue
			} // reset fences queued writes
			if err := encoder.Encode(event); err != nil {
				return err
			}
		}
		return f.Sync()
	})
}

func (s Store) Load(ctx context.Context) (snapshot Snapshot, err error) {
	// Pin a complete prefix under the writer lock, then parse off the lock.
	// Appends cannot change the prefix, and atomic replacements leave this
	// descriptor on its original inode. Large reports never block recording.
	var file *os.File
	var size int64
	err = s.withLock(ctx, func() error {
		var openErr error
		file, openErr = os.Open(s.Path)
		if errors.Is(openErr, os.ErrNotExist) {
			return nil
		}
		if openErr != nil {
			return openErr
		}
		info, statErr := file.Stat()
		if statErr != nil {
			return statErr
		}
		size = info.Size()
		return nil
	})
	if file != nil {
		defer func() { _ = file.Close() }()
	}
	if err != nil {
		return snapshot, err
	}
	if file == nil {
		return emptySnapshot(), nil
	}
	return decodeSnapshot(ctx, io.NewSectionReader(file, 0, size))
}

func emptySnapshot() Snapshot {
	return Snapshot{SchemaVersion: SchemaVersion, Settings: defaultHeader(time.Now()).Settings, Events: []Event{}}
}

func (s Store) load(ctx context.Context) (Snapshot, error) {
	file, err := os.Open(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return emptySnapshot(), nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = file.Close() }()
	return decodeSnapshot(ctx, file)
}

func decodeSnapshot(ctx context.Context, source io.Reader) (Snapshot, error) {
	reader := bufio.NewReader(source)
	h, err := readHeader(reader)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{SchemaVersion: h.SchemaVersion, TrackingSince: h.TrackingSince, Settings: h.Settings, Events: []Event{}}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return snapshot, err
		}
		var event Event
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.ID == "" || event.At.IsZero() {
			snapshot.CorruptLines++
			continue
		}
		snapshot.Events = append(snapshot.Events, event)
	}
	return snapshot, scanner.Err()
}

// Configure and Reset replace the ledger atomically while retaining the stable
// lock. Reset is explicit; corrupt authoritative data is never auto-rebuilt.
func (s Store) Configure(ctx context.Context, enabled, celebrations *bool) error {
	return s.withLock(ctx, func() error {
		h, err := s.initialize(time.Now())
		if err != nil {
			return err
		}
		changedTracking := enabled != nil && h.Settings.Enabled != *enabled
		if enabled != nil {
			h.Settings.Enabled = *enabled
		}
		if celebrations != nil {
			h.Settings.Celebrations = *celebrations
		}
		f, err := os.Open(s.Path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		r := bufio.NewReader(f)
		if _, err = r.ReadBytes('\n'); err != nil {
			return err
		}
		data, err := json.Marshal(h)
		if err != nil {
			return err
		}
		rest, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		if changedTracking {
			if len(rest) > 0 && rest[len(rest)-1] != '\n' {
				rest = append(rest, '\n')
			}
			event, _ := json.Marshal(Event{ID: diagnostics.NewTraceID(), At: time.Now().UTC(), Kind: "gap", Name: "Tracking preference changed"})
			rest = append(rest, append(event, '\n')...)
		}
		data = append(append(data, '\n'), rest...)
		return atomicfile.Write(s.Path, data, 0o600)
	})
}

func (s Store) Reset(ctx context.Context, now time.Time) error {
	return s.withLock(ctx, func() error {
		h, err := s.readHeader()
		var pathError *os.PathError
		if errors.As(err, &pathError) && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		next := defaultHeader(now)
		if err == nil {
			next.Settings = h.Settings
		}
		data, err := json.Marshal(next)
		if err != nil {
			return err
		}
		return atomicfile.Write(s.Path, append(data, '\n'), 0o600)
	})
}

package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/timeutil"
)

type DaemonLogOptions struct {
	KeepDays     int
	MaxBytes     int64
	SegmentBytes int64
	Now          func() time.Time
}

// DaemonLogWriter is a process-safe rotating writer used by slog's JSON
// handler. It never calls slog itself, so failures cannot recurse.
type DaemonLogWriter struct {
	paths   Paths
	opts    DaemonLogOptions
	current string
	lock    string
	mu      sync.Mutex
}

func NewDaemonLogWriter(paths Paths, options DaemonLogOptions) (*DaemonLogWriter, error) {
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	if options.KeepDays == 0 {
		options.KeepDays = 14
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = 256 << 20
	}
	if options.SegmentBytes <= 0 {
		options.SegmentBytes = MaxSegmentBytes
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	writer := &DaemonLogWriter{
		paths: paths, opts: options,
		current: paths.DaemonLogFile,
		lock:    filepath.Join(paths.DaemonDir, "daemon.lock"),
	}
	if err := writer.maintain(); err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *DaemonLogWriter) Write(record []byte) (int, error) {
	if w == nil {
		return 0, errors.New("diagnostics: nil daemon log writer")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	lock, err := acquireFileLock(context.Background(), w.lock, time.Second)
	if err != nil {
		return 0, err
	}
	defer releaseFileLock(lock)
	if err := validateRegularTarget(w.current); err != nil {
		return 0, err
	}
	if info, err := os.Stat(w.current); err == nil && info.Size() > 0 && info.Size()+int64(len(record)) > w.opts.SegmentBytes {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
		if err := w.retentionLocked(); err != nil {
			return 0, err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	file, err := os.OpenFile(w.current, os.O_WRONLY|os.O_CREATE|os.O_APPEND, fileMode)
	if err != nil {
		return 0, err
	}
	if err := file.Chmod(fileMode); err != nil {
		_ = file.Close()
		return 0, err
	}
	// One slog record per call, so no fsync here: see appendLineLocked.
	n, writeErr := file.Write(record)
	err = errors.Join(writeErr, file.Close())
	if err != nil {
		return n, err
	}
	if n != len(record) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

func (w *DaemonLogWriter) Close() error { return nil }

func (w *DaemonLogWriter) maintain() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	lock, err := acquireFileLock(context.Background(), w.lock, time.Second)
	if err != nil {
		return err
	}
	defer releaseFileLock(lock)
	if info, err := os.Stat(w.current); err == nil && info.Size() > w.opts.SegmentBytes {
		if err := w.rotateLocked(); err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return w.retentionLocked()
}

func (w *DaemonLogWriter) rotateLocked() error {
	if _, err := os.Stat(w.current); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	destination := filepath.Join(w.paths.DaemonDir, fmt.Sprintf(
		"daemon-%s-%d-%06d.jsonl", timestamp(w.opts.Now()), os.Getpid(), rotationSequence.Add(1),
	))
	return os.Rename(w.current, destination)
}

func (w *DaemonLogWriter) retentionLocked() error {
	entries, err := os.ReadDir(w.paths.DaemonDir)
	if err != nil {
		return err
	}
	var files []retainedFile
	var total int64
	for _, entry := range entries {
		name := entry.Name()
		isLog := strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".log")
		if entry.IsDir() || name == filepath.Base(w.lock) || !isLog {
			continue
		}
		path := filepath.Join(w.paths.DaemonDir, name)
		if err := validateRegularTarget(path); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := os.Chmod(path, fileMode); err != nil {
			return err
		}
		protected := false
		if strings.HasPrefix(name, "bootstrap-") && strings.HasSuffix(name, ".log") {
			protected, err = bootstrapLogActive(path)
			if err != nil {
				return err
			}
		}
		files = append(files, retainedFile{path: path, name: name, size: info.Size(), modTime: info.ModTime(), protected: protected})
		total += info.Size()
	}
	sort.Slice(files, func(i, k int) bool {
		if files[i].modTime.Equal(files[k].modTime) {
			return files[i].name < files[k].name
		}
		return files[i].modTime.Before(files[k].modTime)
	})
	cutoff := time.Time{}
	if w.opts.KeepDays > 0 {
		cutoff = timeutil.DaysAgo(w.opts.Now(), w.opts.KeepDays)
	}
	for _, file := range files {
		if file.path == w.current || file.protected {
			continue
		}
		old := !cutoff.IsZero() && file.modTime.Before(cutoff)
		over := w.opts.MaxBytes > 0 && total > w.opts.MaxBytes
		if !old && !over {
			continue
		}
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= file.size
	}
	return nil
}

// OpenBootstrapLog creates the inherited stdout/stderr target for a detached
// daemon. This catches config and logger-initialization failures that occur
// before structured daemon logging is ready.
func OpenBootstrapLog(paths Paths, launchID string) (*os.File, string, error) {
	if err := paths.Ensure(); err != nil {
		return nil, "", err
	}
	lock, err := acquireFileLock(context.Background(), filepath.Join(paths.DaemonDir, "daemon.lock"), time.Second)
	if err != nil {
		return nil, "", err
	}
	defer releaseFileLock(lock)
	launchID = safeFilenamePart(launchID)
	if launchID == "" {
		launchID = newID()
	}
	path := filepath.Join(paths.DaemonDir, "bootstrap-"+launchID+".log")
	if err := validateRegularTarget(path); err != nil {
		return nil, "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, fileMode)
	if err != nil {
		return nil, "", err
	}
	if err := file.Chmod(fileMode); err != nil {
		_ = file.Close()
		return nil, "", err
	}
	// The descriptor lock is inherited across fork/exec with stdout and stderr.
	// Retention probes it to avoid unlinking evidence from a live daemon.
	if err := acquireOpenFileLock(context.Background(), file, time.Second); err != nil {
		_ = file.Close()
		return nil, "", err
	}
	header, _ := json.Marshal(map[string]any{
		"time":      time.Now().UTC(),
		"level":     "INFO",
		"msg":       "daemon bootstrap started",
		"launch_id": launchID,
	})
	header = append(header, '\n')
	if _, err := file.Write(header); err != nil {
		_ = file.Close()
		return nil, "", err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, "", err
	}
	return file, path, nil
}

func bootstrapLogActive(path string) (bool, error) {
	file, err := os.OpenFile(path, os.O_RDWR, fileMode)
	if err != nil {
		return false, err
	}
	if err := acquireOpenFileLock(context.Background(), file, 0); err != nil {
		_ = file.Close()
		if errors.Is(err, ErrLockTimeout) {
			return true, nil
		}
		return false, err
	}
	releaseFileLock(file)
	return false, nil
}

func safeFilenamePart(input string) string {
	var out strings.Builder
	for _, r := range input {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			out.WriteRune(r)
		}
		if out.Len() >= 80 {
			break
		}
	}
	return out.String()
}

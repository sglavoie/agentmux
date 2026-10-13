package debugger

import (
	"context"
	"errors"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

const CaptureSchemaVersion = 1

type CaptureStartOptions struct {
	WindowID            int
	RuntimeSessionID    string
	Harness             string
	Duration            time.Duration
	IncludeHookPayloads bool
	Authorized          bool
}

type CaptureReport struct {
	SchemaVersion  int                            `json:"schema_version"`
	GeneratedAt    time.Time                      `json:"generated_at"`
	Descriptor     *diagnostics.CaptureDescriptor `json:"descriptor,omitempty"`
	PurgedPayloads int                            `json:"purged_payloads,omitempty"`
	PurgedBytes    int64                          `json:"purged_bytes,omitempty"`
}

type CaptureService struct {
	store *diagnostics.CaptureStore
	now   func() time.Time
}

func NewCaptureService(paths diagnostics.Paths, now func() time.Time) (*CaptureService, error) {
	if now == nil {
		now = time.Now
	}
	store, err := diagnostics.NewCaptureStore(paths, diagnostics.CaptureOptions{Now: now})
	if err != nil {
		return nil, err
	}
	return &CaptureService{store: store, now: now}, nil
}

func (s *CaptureService) Start(ctx context.Context, options CaptureStartOptions) (CaptureReport, error) {
	if s == nil || s.store == nil {
		return CaptureReport{}, errors.New("debugger: capture service unavailable")
	}
	if !options.IncludeHookPayloads {
		return CaptureReport{}, errors.New("raw hook capture requires --include-hook-payloads")
	}
	if !options.Authorized {
		return CaptureReport{}, errors.New("raw hook capture requires explicit confirmation")
	}
	descriptor, err := s.store.Start(ctx, diagnostics.CaptureStartRequest{
		Scope:    diagnostics.CaptureScope{WindowID: options.WindowID, RuntimeSessionID: options.RuntimeSessionID, Harness: options.Harness},
		Duration: options.Duration, IncludeHookPayloads: true,
	})
	if err != nil {
		return CaptureReport{}, err
	}
	return CaptureReport{SchemaVersion: CaptureSchemaVersion, GeneratedAt: s.now().UTC(), Descriptor: &descriptor}, nil
}

func (s *CaptureService) Stop(ctx context.Context) (CaptureReport, error) {
	if s == nil || s.store == nil {
		return CaptureReport{}, errors.New("debugger: capture service unavailable")
	}
	descriptor, err := s.store.Stop(ctx)
	if err != nil {
		return CaptureReport{}, err
	}
	return CaptureReport{SchemaVersion: CaptureSchemaVersion, GeneratedAt: s.now().UTC(), Descriptor: &descriptor}, nil
}

func (s *CaptureService) Status(ctx context.Context) (CaptureReport, error) {
	if s == nil || s.store == nil {
		return CaptureReport{}, errors.New("debugger: capture service unavailable")
	}
	status, err := s.store.Status(ctx)
	if err != nil {
		return CaptureReport{}, err
	}
	return CaptureReport{
		SchemaVersion: CaptureSchemaVersion, GeneratedAt: s.now().UTC(), Descriptor: status.Descriptor,
		PurgedPayloads: status.PurgedPayloads, PurgedBytes: status.PurgedBytes,
	}, nil
}

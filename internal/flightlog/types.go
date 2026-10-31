// Package flightlog owns the local, durable Flight Log. Its compact event
// ledger is independent of diagnostic retention and contains no prompts,
// terminal output, command lines, or environment values. Reports are pure
// projections; only Store writes data, under a stable process-shared lock.
package flightlog

import "time"

const SchemaVersion = 1

// ObservationLimit bounds interpolation between observations. A daemon
// restart, sleep, failed sample, or missed heartbeat never becomes work time.
const ObservationLimit = 45 * time.Second

type Event struct {
	ID               string     `json:"id"`
	At               time.Time  `json:"at"`
	Kind             string     `json:"kind"` // launch, state, sample, run, exit, gap
	Key              string     `json:"key,omitempty"`
	RuntimeSessionID string     `json:"runtime_session_id,omitempty"`
	LaunchID         string     `json:"launch_id,omitempty"`
	ConversationID   string     `json:"conversation_id,omitempty"`
	Harness          string     `json:"harness,omitempty"`
	Profile          string     `json:"profile,omitempty"`
	Project          string     `json:"project,omitempty"`
	Operation        string     `json:"operation,omitempty"`
	Icon             string     `json:"icon,omitempty"`
	BeforeIcon       string     `json:"before_icon,omitempty"`
	Looping          bool       `json:"looping,omitempty"`
	BeforeLooping    bool       `json:"before_looping,omitempty"`
	Epoch            string     `json:"epoch,omitempty"`
	RunID            string     `json:"run_id,omitempty"`
	TraceID          string     `json:"trace_id,omitempty"`
	Name             string     `json:"name,omitempty"`
	Partial          bool       `json:"partial,omitempty"`
	Fleet            []string   `json:"fleet,omitempty"`
	From             *time.Time `json:"from,omitempty"`
	Observed         bool       `json:"observed,omitempty"`
}

type Settings struct {
	Enabled      bool `json:"enabled"`
	Celebrations bool `json:"celebrations"`
}

type Snapshot struct {
	SchemaVersion int       `json:"schema_version"`
	TrackingSince time.Time `json:"tracking_since"`
	Settings      Settings  `json:"settings"`
	Events        []Event   `json:"events"`
	CorruptLines  int       `json:"corrupt_lines"`
}

type Metrics struct {
	Launches        int     `json:"launches"`
	Fresh           int     `json:"fresh"`
	Resumes         int     `json:"resumes"`
	Forks           int     `json:"forks"`
	Restarts        int     `json:"restarts"`
	Conversations   int     `json:"conversations"`
	Projects        int     `json:"projects"`
	StateChanges    int     `json:"state_changes"`
	TurnsFinished   int     `json:"turns_finished"`
	Questions       int     `json:"questions"`
	Permissions     int     `json:"permissions"`
	Errors          int     `json:"errors"`
	RunsFinished    int     `json:"runs_finished"`
	WorkingSeconds  float64 `json:"working_seconds"`
	WaitingSeconds  float64 `json:"waiting_seconds"`
	ObservedSeconds float64 `json:"observed_seconds"`
	PeakWorking     int     `json:"peak_working"`
}

type Segment struct {
	ConversationID string    `json:"conversation_id,omitempty"`
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	Icon           string    `json:"icon"`
	Looping        bool      `json:"looping"`
	Observed       bool      `json:"observed"`
}

type Session struct {
	Key              string         `json:"key"`
	RuntimeSessionID string         `json:"runtime_session_id"`
	LaunchID         string         `json:"launch_id"`
	ConversationID   string         `json:"conversation_id"`
	Harness          string         `json:"harness"`
	Profile          string         `json:"profile"`
	Project          string         `json:"project"`
	Operation        string         `json:"operation"`
	FirstAt          time.Time      `json:"first_at"`
	LastAt           time.Time      `json:"last_at"`
	Metrics          Metrics        `json:"metrics"`
	StateCounts      map[string]int `json:"state_counts"`
	Timeline         []Segment      `json:"timeline"`
	Events           []Event        `json:"events"`
	Partial          bool           `json:"partial"`
}

type Day struct {
	Date    string  `json:"date"`
	Metrics Metrics `json:"metrics"`
}

type Achievement struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Progress    int        `json:"progress"`
	Target      int        `json:"target"`
	UnlockedAt  *time.Time `json:"unlocked_at,omitempty"`
	SessionKey  string     `json:"session_key"`
}

type Filters struct {
	Period  string `json:"period"` // today, week, all
	Project string `json:"project"`
	Harness string `json:"harness"`
	Profile string `json:"profile"`
	Session string `json:"session"`
}

type Report struct {
	SchemaVersion int           `json:"schema_version"`
	TrackingSince time.Time     `json:"tracking_since"`
	GeneratedAt   time.Time     `json:"generated_at"`
	Since         time.Time     `json:"since"`
	Timezone      string        `json:"timezone"`
	Settings      Settings      `json:"settings"`
	Filters       Filters       `json:"filters"`
	Totals        Metrics       `json:"totals"`
	Lifetime      Metrics       `json:"lifetime"`
	Days          []Day         `json:"days"`
	Sessions      []Session     `json:"sessions"`
	Achievements  []Achievement `json:"achievements"`
	Recap         []string      `json:"recap"`
	Warnings      []string      `json:"warnings"`
	Projects      []string      `json:"projects"`
	Harnesses     []string      `json:"harnesses"`
	Profiles      []string      `json:"profiles"`
}

func working(icon string) bool { return icon == "bolt" || icon == "subagent" || icon == "compact" }
func waiting(icon string) bool { return icon == "key" || icon == "question" }

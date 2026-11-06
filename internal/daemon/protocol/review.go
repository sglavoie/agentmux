package protocol

// TypeSessionReview edits operator annotations without changing agent state.
const TypeSessionReview MessageType = "session_review"

// SessionReview is shared by the dashboard and desktop. Epoch changes only
// when new work needs review; it is also a precondition on acknowledgements.
type SessionReview struct {
	Epoch       string `json:"epoch,omitempty"`
	Accounted   bool   `json:"accounted,omitempty"`
	Reviewed    bool   `json:"reviewed,omitempty"`
	Note        string `json:"note,omitempty"`
	ProjectNote string `json:"project_note,omitempty"`
}

type SessionReviewRequest struct {
	// Targets is the exact set the operator saw, with each observed epoch.
	Targets   map[string]string `json:"targets,omitempty"`
	Project   string            `json:"project,omitempty"`
	Accounted *bool             `json:"accounted,omitempty"`
	Note      *string           `json:"note,omitempty"`
	UndoToken string            `json:"undo_token,omitempty"`
}

type SessionReviewResult struct {
	UndoToken string `json:"undo_token,omitempty"`
	Count     int    `json:"count"`
}

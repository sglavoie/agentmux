package actions

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Registry provides deterministic lookup and execution of action definitions.
type Registry struct {
	byID map[ID]Definition
	ids  []ID
}

// New constructs a registry and rejects invalid or duplicate definitions.
func New(definitions ...Definition) (*Registry, error) {
	r := &Registry{byID: make(map[ID]Definition, len(definitions))}
	for _, definition := range definitions {
		if definition.ID == "" {
			return nil, errors.New("actions: action ID is required")
		}
		if definition.execute == nil {
			return nil, fmt.Errorf("actions: action %q has no handler", definition.ID)
		}
		if _, exists := r.byID[definition.ID]; exists {
			return nil, fmt.Errorf("actions: duplicate action ID %q", definition.ID)
		}
		definition.Requires = append([]EntityRequirement(nil), definition.Requires...)
		definition.Parameters = append([]ParameterSchema(nil), definition.Parameters...)
		r.byID[definition.ID] = definition
		r.ids = append(r.ids, definition.ID)
	}
	sort.Slice(r.ids, func(i, j int) bool { return r.ids[i] < r.ids[j] })
	return r, nil
}

// DefaultRegistry returns the standard actions.  Construction is pure and
// inexpensive; callers may create one per command or model without state
// leakage between runs.
func DefaultRegistry() *Registry {
	r, err := New(defaultDefinitions()...)
	if err != nil {
		panic(err)
	}
	return r
}

// Definitions returns definitions in stable ID order.
func (r *Registry) Definitions() []Definition {
	if r == nil {
		return nil
	}
	out := make([]Definition, 0, len(r.ids))
	for _, id := range r.ids {
		out = append(out, r.byID[id])
	}
	return out
}

// Get returns one definition by stable ID.
func (r *Registry) Get(id ID) (Definition, bool) {
	if r == nil {
		return Definition{}, false
	}
	definition, ok := r.byID[id]
	return definition, ok
}

// Availability evaluates a definition's entity and state requirements.
func (r *Registry) Availability(deps Dependencies, id ID, request Request) Availability {
	definition, ok := r.Get(id)
	if !ok {
		return Availability{Reason: fmt.Sprintf("unknown action %q", id)}
	}
	if err := validateEntities(definition, request); err != nil {
		return Availability{Reason: err.Error()}
	}
	if definition.available == nil {
		return Availability{Available: true}
	}
	return definition.available(deps, request)
}

// Execute validates a request, enforces non-interactive confirmation, and
// invokes the action handler.  It never asks a question or writes output.
func (r *Registry) Execute(ctx context.Context, deps Dependencies, id ID, request Request) (Result, error) {
	definition, ok := r.Get(id)
	if !ok {
		return Result{}, &Error{Code: "unknown_action", Action: id, Message: fmt.Sprintf("unknown action %q", id)}
	}
	if err := validateEntities(definition, request); err != nil {
		return Result{}, err
	}
	if err := validateParameters(definition, request); err != nil {
		return Result{}, err
	}
	if definition.prepare != nil {
		var err error
		request, err = definition.prepare(ctx, deps, request)
		if err != nil {
			return Result{}, err
		}
	}
	availability := r.Availability(deps, id, request)
	if !availability.Available {
		message := availability.Reason
		if message == "" {
			message = fmt.Sprintf("action %q is unavailable", id)
		}
		return Result{}, &Error{Code: "unavailable", Action: id, Message: message}
	}
	if definition.Confirmation.Required && !request.Confirmed {
		message := definition.Confirmation.Prompt
		if message == "" {
			message = fmt.Sprintf("action %q requires confirmation", id)
		}
		return Result{}, &Error{
			Code: "confirmation_required", Action: id, Message: message,
			MissingParameters: []ParameterSchema{{Name: "yes", Type: "boolean", Description: "explicitly confirm this destructive action", Required: true}},
		}
	}
	result, err := definition.execute(ctx, deps, request)
	result.Action = id
	if err != nil {
		return result, err
	}
	return result, nil
}

func validateEntities(definition Definition, request Request) error {
	for _, requirement := range definition.Requires {
		switch requirement.Kind {
		case EntityRunningSession:
			if request.Session == nil {
				return &Error{Code: "missing_entity", Action: definition.ID, Message: "a running session is required"}
			}
		case EntityProject:
			if request.Project == nil {
				return &Error{Code: "missing_entity", Action: definition.ID, Message: "a configured project is required"}
			}
		case EntityConversation:
			if request.Conversation == nil {
				return &Error{Code: "missing_entity", Action: definition.ID, Message: "an indexed conversation is required"}
			}
		case EntityWorktree:
			if request.Worktree == nil && strings.TrimSpace(request.Selector) == "" {
				return &Error{Code: "missing_entity", Action: definition.ID, Message: "a worktree selector is required"}
			}
		case EntityPlan:
			if request.Plan == nil {
				return &Error{Code: "missing_entity", Action: definition.ID, Message: "a plan is required"}
			}
		default:
			return &Error{Code: "invalid_action", Action: definition.ID, Message: fmt.Sprintf("unknown entity requirement %q", requirement.Kind)}
		}
	}
	return nil
}

func validateParameters(definition Definition, request Request) error {
	missing := make([]ParameterSchema, 0)
	for _, parameter := range definition.Parameters {
		if parameter.Required && strings.TrimSpace(request.Parameters[parameter.Name]) == "" {
			missing = append(missing, parameter)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &Error{
		Code:              "missing_parameters",
		Action:            definition.ID,
		Message:           fmt.Sprintf("action %q requires additional parameters", definition.ID),
		MissingParameters: missing,
	}
}

package tui

// This file is the package-level entry point that turns a set of options plus a
// list of actions into a sequence of observations. It is the seam Session 4's
// scenario parser and Session 6's CLI plug into: prepare config and a client,
// describe the initial size and the steps, and get back one normalized
// observation per step. The runner owns no CLI, filesystem, or Kitty concerns —
// those are supplied by the caller through cfg and client.

import (
	"errors"
	"fmt"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// RunUITest builds a driver from cfg and client at opts.Size, then applies each
// action in order. It returns one observation per step: index 0 is the initial
// render before any action, and index i+1 is the screen after actions[i]. The
// returned slice therefore always has len(actions)+1 entries on success. On the
// first action error the observations captured so far are returned alongside the
// error so callers can still inspect how far the run got.
func RunUITest(cfg *config.Config, client kitty.KittyClient, opts UITestOptions, actions []UITestAction) ([]UITestObservation, error) {
	return runUITestActions(NewUITestDriver(cfg, client, opts.Size), actions)
}

// RunUITestFixture builds a driver from the named built-in fixture and applies
// each action, returning one observation per step (index 0 is the initial
// render before any action). opts.Size overrides the fixture's default size
// when non-zero. An unknown fixture name returns a structured
// *UITestFixtureError before any driver is constructed.
func RunUITestFixture(name string, opts UITestOptions, actions []UITestAction) ([]UITestObservation, error) {
	fx, err := UITestFixtureByName(name)
	if err != nil {
		return nil, err
	}
	d, _ := NewUITestFixtureDriver(fx, opts.Size)
	return runUITestActions(d, actions)
}

// RunUITestModel is RunUITest for callers that have already prepared a Model
// (for example, one with sessions preloaded). opts.Size sets the initial
// viewport; the model's prior width/height are overwritten by the driver's
// initial resize.
func RunUITestModel(m Model, opts UITestOptions, actions []UITestAction) ([]UITestObservation, error) {
	return runUITestActions(NewUITestDriverModel(m, opts.Size), actions)
}

// runUITestActions captures the initial observation, then applies each action
// and captures an observation after it. Shared by both public entry points.
func runUITestActions(d *UITestDriver, actions []UITestAction) (obs []UITestObservation, retErr error) {
	defer func() { retErr = errors.Join(retErr, d.Close()) }()
	if err := d.Err(); err != nil {
		return nil, err
	}
	obs = make([]UITestObservation, 0, len(actions)+1)
	obs = append(obs, d.Observe())
	for i, a := range actions {
		if err := d.Apply(a); err != nil {
			return obs, fmt.Errorf("uitest: action %d (%s): %w", i, a.Type, err)
		}
		obs = append(obs, d.Observe())
	}
	return obs, nil
}

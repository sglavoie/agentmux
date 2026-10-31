package commands

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/flightlog"
)

type FlightLogMsg struct {
	Report   flightlog.Report
	Err      error
	Sequence int
}

func ReadFlightLog(filters flightlog.Filters, harnesses []string, sequence int) tea.Cmd {
	return func() tea.Msg {
		msg := FlightLogMsg{Sequence: sequence}
		store, err := flightlog.DefaultStore()
		if err != nil {
			msg.Err = err
			return msg
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		snapshot, err := store.Load(ctx)
		if err != nil {
			msg.Err = err
			return msg
		}
		msg.Report = flightlog.Build(snapshot, filters, time.Now(), harnesses)
		return msg
	}
}

package plans

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/planrun"
)

func TestPlanStatusSchemaAndNoLaunch(t *testing.T) {
	world := newPlanWorld(t)
	world.asl.status = `{"ok":true,"plan_dir":"/plan","plan_title":"Fix things","counts":{"complete":1,"pending":2},"sessions":[{"id":"1","status":"complete","path":"/plan/session-1.md"}],"runner_state":{"run_id":"r","paused":true,"reason":"verification-failed"}}`
	cmd := newStatusCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.ParseFlags([]string{"--json", "--", world.first}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, cmd.Flags().Args()); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Schema int               `json:"schema_version"`
		Data   planrun.RunStatus `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Schema != 1 || envelope.Data.Counts["complete"] != 1 || envelope.Data.Runner.RunID != "r" {
		t.Fatalf("%s", out.Bytes())
	}
	if len(world.kitty.launched) != 0 || strings.Join(world.asl.asked, ",") != "status" {
		t.Fatal("status must not execute a run or verification")
	}
}

func TestDisabledPlanStatusDoesNotStartASL(t *testing.T) {
	world := newPlanWorld(t)
	disabled := false
	world.cfg.Defaults.ASL.Enabled = &disabled
	cmd := newStatusCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.ParseFlags([]string{"--json", world.first}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, cmd.Flags().Args()); err == nil {
		t.Fatal("accepted a disabled integration")
	}
	if len(world.asl.asked) != 0 || !strings.Contains(out.String(), "asl_disabled") {
		t.Fatalf("%s %+v", out.String(), world.asl.asked)
	}
}

package agent

import (
	"testing"

	"github.com/lucasnevespereira/nevinho/config"
)

func TestApprovalTurnIncludesDetachedDetails(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(&bashThenTalkProvider{}, cfg, "test", "", ModeLocal)
	turn, err := a.Chat("u1", "run echo", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Kind != TurnApproval || turn.Approval == nil {
		t.Fatal("approval turn must include structured details")
	}
	if turn.Approval.Kind != "code" || turn.Approval.Detail != "echo hi" {
		t.Fatalf("unexpected approval details: %+v", turn.Approval)
	}
	if turn.Text != approvalMessage(a.tools.PendingApproval("u1")) {
		t.Error("existing transport approval text must not change")
	}

	// Presentation data must not let a transport change the held command.
	turn.Approval.Detail = "different command"
	if a.tools.PendingApproval("u1").Detail != "echo hi" {
		t.Fatal("presentation details share mutable approval state")
	}
	turn, err = a.Resolve("u1", Denied)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Approval != nil {
		t.Error("ordinary replies must not retain approval details")
	}
}

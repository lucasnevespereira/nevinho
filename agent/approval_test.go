package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/llm"
)

// bashThenTalkProvider asks for one bash call, then replies in text. It
// records the messages it was given on the second call, which is where the
// resolved tool result shows up.
type bashThenTalkProvider struct {
	calls    int
	lastSeen []llm.Message
}

func (p *bashThenTalkProvider) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	p.calls++
	p.lastSeen = req.Messages
	if p.calls == 1 {
		calls := []llm.ToolCall{{ID: "call-1", Name: "bash", Input: json.RawMessage(`{"command":"echo hi"}`)}}
		return &llm.Response{
			Assistant:  llm.Message{Role: llm.RoleAssistant, ToolCalls: calls},
			ToolCalls:  calls,
			StopReason: llm.StopToolUse,
		}, nil
	}
	return &llm.Response{Text: "done", Assistant: llm.Message{Role: llm.RoleAssistant, Text: "done"}, StopReason: llm.StopEndTurn}, nil
}

func (p *bashThenTalkProvider) Model() string { return "fake" }

func pausedAgent(t *testing.T) (*Agent, *bashThenTalkProvider) {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &bashThenTalkProvider{}
	// Local mode gates every command, so the first turn always pauses.
	a := New(p, cfg, "test", "", ModeLocal)
	if _, err := a.Chat("u1", "run echo", false, nil); err != nil {
		t.Fatal(err)
	}
	if !a.HasPendingApproval("u1") {
		t.Fatal("expected the turn to pause for approval")
	}
	return a, p
}

func historyText(msgs []llm.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(m.Text)
		for _, r := range m.ToolResults {
			sb.WriteString(r.Output)
		}
	}
	return sb.String()
}

func TestResolveDeniedClearsPendingAndPatchesHistory(t *testing.T) {
	a, p := pausedAgent(t)

	if _, err := a.Resolve("u1", Denied); err != nil {
		t.Fatal(err)
	}

	if a.HasPendingApproval("u1") {
		t.Error("denial should clear the pending call")
	}
	seen := historyText(p.lastSeen)
	if strings.Contains(seen, "NEEDS_APPROVAL") {
		t.Error("the model still sees the unresolved placeholder after a denial")
	}
	if !strings.Contains(seen, "denied by user") {
		t.Error("the model should see the call resolved as denied")
	}
}

func TestResolveApprovedRunsTheHeldCall(t *testing.T) {
	a, p := pausedAgent(t)

	if _, err := a.Resolve("u1", Approved); err != nil {
		t.Fatal(err)
	}

	if a.HasPendingApproval("u1") {
		t.Error("approval should clear the pending call")
	}
	if seen := historyText(p.lastSeen); strings.Contains(seen, "NEEDS_APPROVAL") {
		t.Error("the model still sees the unresolved placeholder after an approval")
	}
}

// A typed answer and a button press must leave the same state behind.
func TestTypedDenialMatchesResolve(t *testing.T) {
	a, p := pausedAgent(t)

	if _, err := a.Chat("u1", "no", false, nil); err != nil {
		t.Fatal(err)
	}

	if a.HasPendingApproval("u1") {
		t.Error("typed denial should clear the pending call")
	}
	if !strings.Contains(historyText(p.lastSeen), "denied by user") {
		t.Error("typed denial should resolve the held call the same way")
	}
}

func TestResolveWithNothingPending(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(&bashThenTalkProvider{}, cfg, "test", "", ModeLocal)

	if _, err := a.Resolve("u1", Approved); err == nil {
		t.Error("expected an error when nothing is waiting")
	}
}

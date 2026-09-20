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
	lastSeen []json.RawMessage
}

func (p *bashThenTalkProvider) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	p.calls++
	p.lastSeen = req.Messages
	if p.calls == 1 {
		msg, _ := json.Marshal(map[string]any{"role": "assistant", "content": "running"})
		return &llm.Response{
			AssistantMessage: msg,
			ToolCalls:        []llm.ToolCall{{ID: "call-1", Name: "bash", Input: json.RawMessage(`{"command":"echo hi"}`)}},
			StopReason:       llm.StopToolUse,
		}, nil
	}
	msg, _ := json.Marshal(map[string]any{"role": "assistant", "content": "done"})
	return &llm.Response{Text: "done", AssistantMessage: msg, StopReason: llm.StopEndTurn}, nil
}

func (p *bashThenTalkProvider) FormatUserMessage(text string, images []llm.Image) json.RawMessage {
	msg, _ := json.Marshal(map[string]any{"role": "user", "content": text})
	return msg
}

func (p *bashThenTalkProvider) FormatToolResults(results []llm.ToolResult) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(results))
	for _, r := range results {
		msg, _ := json.Marshal(map[string]any{"role": "tool", "tool_call_id": r.ID, "content": r.Output})
		out = append(out, msg)
	}
	return out
}

func (p *bashThenTalkProvider) ReplaceToolResult(history []json.RawMessage, toolUseID, newOutput string) []json.RawMessage {
	for i, m := range history {
		var peek struct {
			ID string `json:"tool_call_id"`
		}
		if json.Unmarshal(m, &peek) == nil && peek.ID == toolUseID {
			history[i], _ = json.Marshal(map[string]any{"role": "tool", "tool_call_id": toolUseID, "content": newOutput})
		}
	}
	return history
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

func historyText(msgs []json.RawMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		sb.Write(m)
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

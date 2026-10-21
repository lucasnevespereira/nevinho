package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/llm"
)

type approvalBatchProvider struct {
	calls    int
	lastSeen []llm.Message
}

func (p *approvalBatchProvider) Model() string { return "fake" }

func (p *approvalBatchProvider) Complete(_ context.Context, req *llm.Request) (*llm.Response, error) {
	p.calls++
	p.lastSeen = req.Messages
	if p.calls == 1 {
		calls := []llm.ToolCall{
			{ID: "before", Name: "file_list", Input: json.RawMessage(`{"path":"."}`)},
			{ID: "held", Name: "bash", Input: json.RawMessage(`{"command":"printf first"}`)},
			{ID: "later-read", Name: "file_list", Input: json.RawMessage(`{"path":"."}`)},
			{ID: "later-command", Name: "bash", Input: json.RawMessage(`{"command":"printf second"}`)},
		}
		return &llm.Response{
			Assistant:  llm.Message{Role: llm.RoleAssistant, ToolCalls: calls},
			ToolCalls:  calls,
			StopReason: llm.StopToolUse,
		}, nil
	}
	return &llm.Response{
		Text:       "done",
		Assistant:  llm.Message{Role: llm.RoleAssistant, Text: "done"},
		StopReason: llm.StopEndTurn,
	}, nil
}

func TestToolBatchStopsAtFirstApproval(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer Answer
		output string
	}{
		{name: "approved", answer: Approved, output: "first"},
		{name: "denied", answer: Denied, output: "denied by user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			p := &approvalBatchProvider{}
			a := New(p, cfg, "test", "", ModeLocal)
			var started []string
			a.SetToolCallback("u1", func(event ToolEvent) {
				if event.Phase == ToolStart {
					started = append(started, event.Name)
				}
			})

			turn, err := a.Chat("u1", "run the batch", false, nil)
			if err != nil {
				t.Fatal(err)
			}
			if turn.Kind != TurnApproval || p.calls != 1 {
				t.Fatalf("expected pause after one model response, got kind=%s calls=%d", turn.Kind, p.calls)
			}
			if len(started) != 2 || started[0] != "file_list" || started[1] != "bash" {
				t.Errorf("tools started past the approval boundary: %v", started)
			}
			pending := a.tools.PendingApproval("u1")
			if pending == nil || pending.Detail != "printf first" {
				t.Fatalf("first approval was replaced: %+v", pending)
			}
			if a.pendingToolID["u1"] != "held" {
				t.Fatalf("wrong held call: %q", a.pendingToolID["u1"])
			}

			assertResults := func(messages []llm.Message, heldOutput string) {
				t.Helper()
				var results []llm.ToolResult
				for _, message := range messages {
					results = append(results, message.ToolResults...)
				}
				if len(results) != 4 {
					t.Fatalf("expected one result per tool call, got %d", len(results))
				}
				for i, id := range []string{"before", "held", "later-read", "later-command"} {
					if results[i].ID != id {
						t.Errorf("result %d: want ID %s, got %s", i, id, results[i].ID)
					}
				}
				if !strings.Contains(results[1].Output, heldOutput) {
					t.Errorf("held result: want %q, got %q", heldOutput, results[1].Output)
				}
				for _, result := range results[2:] {
					if !result.IsError || !strings.Contains(result.Output, "not executed") {
						t.Errorf("skipped call must have an explicit error result: %+v", result)
					}
				}
			}
			assertResults(a.messages("u1"), "NEEDS_APPROVAL")

			if _, err := a.Resolve("u1", tc.answer); err != nil {
				t.Fatal(err)
			}
			if a.HasPendingApproval("u1") {
				t.Error("resolved call is still pending")
			}
			assertResults(p.lastSeen, tc.output)
			if strings.Contains(historyText(p.lastSeen), "NEEDS_APPROVAL") {
				t.Error("unresolved approval placeholder remains in history")
			}
			if _, err := a.Resolve("u1", Approved); err == nil {
				t.Error("a resolved approval must not execute again")
			}
		})
	}
}

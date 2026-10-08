package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/llm"
)

// loopingProvider asks for a tool on every call until it has been called
// stopAfter times, then answers in text.
type loopingProvider struct {
	calls     int
	stopAfter int
}

func (p *loopingProvider) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	p.calls++
	if p.calls > p.stopAfter {
		done := llm.Message{Role: llm.RoleAssistant, Text: "finished"}
		return &llm.Response{Text: "finished", Assistant: done, StopReason: llm.StopEndTurn}, nil
	}
	calls := []llm.ToolCall{{ID: "c", Name: "no_such_tool", Input: []byte(`{}`)}}
	return &llm.Response{
		ToolCalls:  calls,
		Assistant:  llm.Message{Role: llm.RoleAssistant, ToolCalls: calls},
		StopReason: llm.StopToolUse,
	}, nil
}

func (p *loopingProvider) Model() string { return "fake" }

// The step limit depends on where nevinho runs, and hitting it pauses the
// task: the next message picks up where the turn stopped.
func TestTurnLimitPausesAndContinues(t *testing.T) {
	for name, tc := range map[string]struct {
		mode RunMode
		want int
	}{
		"terminal": {ModeLocal, 100},
		"discord":  {ModeDaemon, 25},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			p := &loopingProvider{stopAfter: tc.want + 3}
			a := New(p, cfg, "test", "", tc.mode)

			turn, err := a.Chat("u", "do a long task", false, nil)
			if err != nil {
				t.Fatal(err)
			}
			if p.calls != tc.want || !strings.Contains(turn.Text, "Say continue") {
				t.Fatalf("calls=%d text=%q, want %d calls and a pause", p.calls, turn.Text, tc.want)
			}

			turn, err = a.Chat("u", "continue", false, nil)
			if err != nil {
				t.Fatal(err)
			}
			if turn.Text != "finished" {
				t.Fatalf("after continue got %q, want finished", turn.Text)
			}
		})
	}
}

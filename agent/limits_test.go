package agent

import (
	"context"
	"errors"
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

func TestBudgetFollowsTheModel(t *testing.T) {
	for model, wantLarge := range map[string]bool{
		"claude-haiku-4-5":                true,
		"claude-opus-5-5":                 true,
		"gpt-5-mini":                      true,
		"gpt-6-luna":                      true,
		"gemini-3.8-flash":                true,
		"gpt-4-turbo":                     false, // 4096 output tokens at most
		"gpt-4o-mini":                     false,
		"groq:llama-3.3-70b-versatile":    false,
		"openrouter:deepseek/deepseek-v4": false,
		"llama3":                          false,
	} {
		history, output := budgetFor(model)
		if large := history == largeHistoryTokens && output == largeOutputTokens; large != wantLarge {
			t.Errorf("%s: history=%d output=%d, want large=%v", model, history, output, wantLarge)
		}
	}
}

// Cached input is part of what was sent and part of the bill. Counting
// only the uncached remainder made a long cached session look free.
func TestCostCountsCachedInput(t *testing.T) {
	const million = 1_000_000
	// claude-sonnet-4-6 input is $3 per million.
	for name, tc := range map[string]struct {
		usage llm.Usage
		want  float64
	}{
		"uncached":    {llm.Usage{In: million}, 3.00},
		"cache read":  {llm.Usage{CacheRead: million}, 0.30},
		"cache write": {llm.Usage{CacheWrite: million}, 3.75},
	} {
		if got := estimateCost("claude-sonnet-4-6", tc.usage); got < tc.want-0.001 || got > tc.want+0.001 {
			t.Errorf("%s: cost = %.3f, want %.2f", name, got, tc.want)
		}
	}

	a := &Agent{llm: &fallbackProvider{}}
	a.addUsage(llm.Usage{In: 10, CacheRead: 900, CacheWrite: 90, Out: 5})
	if in, out, _ := a.Usage(); in != 1000 || out != 5 {
		t.Errorf("usage = %d in, %d out, want 1000 in, 5 out", in, out)
	}
}

// failingProvider returns an error on the calls listed in failOn and
// otherwise behaves like loopingProvider.
type failingProvider struct {
	loopingProvider
	failOn map[int]bool
}

func (p *failingProvider) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if p.failOn[p.calls+1] {
		p.calls++
		return nil, errors.New("API 529: overloaded")
	}
	return p.loopingProvider.Complete(ctx, req)
}

func TestFailedTurnLeavesHistoryUsable(t *testing.T) {
	newAgent := func(p llm.Provider) *Agent {
		cfg, err := config.Load(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return New(p, cfg, "test", "", ModeDaemon)
	}

	t.Run("no reply at all: the message is taken back", func(t *testing.T) {
		a := newAgent(&failingProvider{failOn: map[int]bool{1: true}})
		if _, err := a.Chat("u", "hello", false, nil); err == nil {
			t.Fatal("want an error")
		}
		if got := len(a.messages("u")); got != 0 {
			t.Fatalf("history has %d messages, want 0", got)
		}
	})

	t.Run("failure mid task: finished steps stay", func(t *testing.T) {
		// Call 1 asks for a tool, call 2 fails, call 3 answers.
		p := &failingProvider{loopingProvider: loopingProvider{stopAfter: 2}, failOn: map[int]bool{2: true}}
		a := newAgent(p)
		if _, err := a.Chat("u", "do the task", false, nil); err == nil {
			t.Fatal("want an error")
		}
		if got := len(a.messages("u")); got != 3 {
			t.Fatalf("history has %d messages, want the question, the tool call and its result", got)
		}
		turn, err := a.Chat("u", "continue", false, nil)
		if err != nil || turn.Text != "finished" {
			t.Fatalf("continue gave %q, %v", turn.Text, err)
		}
	})
}

// emptyProvider returns an empty reply for its first `empties` calls.
type emptyProvider struct {
	calls    int
	empties  int
	sawNudge bool
}

func (p *emptyProvider) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	p.calls++
	for _, m := range req.Messages {
		if strings.Contains(m.Text, "Your last turn was empty") {
			p.sawNudge = true
		}
	}
	if p.calls <= p.empties {
		return &llm.Response{Assistant: llm.Message{Role: llm.RoleAssistant}, StopReason: llm.StopEndTurn}, nil
	}
	return &llm.Response{Text: "answer", Assistant: llm.Message{Role: llm.RoleAssistant, Text: "answer"}, StopReason: llm.StopEndTurn}, nil
}

func (p *emptyProvider) Model() string { return "fake" }

// An empty first reply is asked again as is. It is never turned into
// "summarize what you did", because nothing was done.
func TestEmptyReplyBeforeAnyToolIsRetriedNotNudged(t *testing.T) {
	for name, tc := range map[string]struct {
		empties  int
		wantText string
	}{
		"empty once":  {1, "answer"},
		"empty twice": {2, "empty reply twice"},
	} {
		cfg, err := config.Load(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		p := &emptyProvider{empties: tc.empties}
		turn, err := New(p, cfg, "test", "", ModeLocal).Chat("u", "read the file", false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if p.calls != 2 || p.sawNudge || !strings.Contains(turn.Text, tc.wantText) {
			t.Errorf("%s: calls=%d nudged=%v text=%q", name, p.calls, p.sawNudge, turn.Text)
		}
	}
}

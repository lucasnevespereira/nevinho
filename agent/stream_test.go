package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/llm"
)

type fallbackProvider struct{ called bool }

func (p *fallbackProvider) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	p.called = true
	msg, _ := json.Marshal(map[string]any{"role": "assistant", "content": "fallback ok"})
	return &llm.Response{Text: "fallback ok", AssistantMessage: msg, StopReason: llm.StopEndTurn}, nil
}
func (p *fallbackProvider) FormatUserMessage(text string, images []llm.Image) json.RawMessage {
	msg, _ := json.Marshal(map[string]any{"role": "user", "content": text})
	return msg
}
func (p *fallbackProvider) FormatToolResults(results []llm.ToolResult) []json.RawMessage { return nil }
func (p *fallbackProvider) ReplaceToolResult(history []json.RawMessage, toolUseID, newOutput string) []json.RawMessage {
	return history
}
func (p *fallbackProvider) Model() string { return "fake" }

func TestChatStreamFallsBackToComplete(t *testing.T) {
	p := &fallbackProvider{}
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(p, cfg, "test", "", ModeLocal)
	var streamed bool
	got, err := a.ChatStream("u", "hello", false, nil, func(delta string) { streamed = true })
	if err != nil {
		t.Fatal(err)
	}
	if got != "fallback ok" {
		t.Fatalf("got %q, want fallback ok", got)
	}
	if !p.called {
		t.Fatal("Complete was not called")
	}
	if streamed {
		t.Fatal("stream callback should not run for fallback provider")
	}
}

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

type optInStreamingProvider struct {
	completeCalls int
	streamCalls   int
}

func (p *optInStreamingProvider) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	p.completeCalls++
	msg, _ := json.Marshal(map[string]any{"role": "assistant", "content": "complete ok"})
	return &llm.Response{Text: "complete ok", AssistantMessage: msg, StopReason: llm.StopEndTurn}, nil
}

func (p *optInStreamingProvider) StreamComplete(ctx context.Context, req *llm.Request, cb llm.StreamCallback) (*llm.Response, error) {
	p.streamCalls++
	if cb != nil {
		cb("stream")
		cb(" ok")
	}
	msg, _ := json.Marshal(map[string]any{"role": "assistant", "content": "stream ok"})
	return &llm.Response{Text: "stream ok", AssistantMessage: msg, StopReason: llm.StopEndTurn}, nil
}

func (p *optInStreamingProvider) FormatUserMessage(text string, images []llm.Image) json.RawMessage {
	msg, _ := json.Marshal(map[string]any{"role": "user", "content": text})
	return msg
}
func (p *optInStreamingProvider) FormatToolResults(results []llm.ToolResult) []json.RawMessage {
	return nil
}
func (p *optInStreamingProvider) ReplaceToolResult(history []json.RawMessage, toolUseID, newOutput string) []json.RawMessage {
	return history
}
func (p *optInStreamingProvider) Model() string { return "fake" }

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

func TestChatDoesNotStreamUnlessCallerOptsIn(t *testing.T) {
	p := &optInStreamingProvider{}
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(p, cfg, "test", "", ModeLocal)

	got, err := a.Chat("u", "hello", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "complete ok" {
		t.Fatalf("got %q, want complete ok", got)
	}
	if p.completeCalls != 1 || p.streamCalls != 0 {
		t.Fatalf("complete=%d stream=%d, want complete only", p.completeCalls, p.streamCalls)
	}
}

func TestChatStreamUsesStreamingProvider(t *testing.T) {
	p := &optInStreamingProvider{}
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(p, cfg, "test", "", ModeLocal)

	var gotDelta string
	got, err := a.ChatStream("u", "hello", false, nil, func(delta string) { gotDelta += delta })
	if err != nil {
		t.Fatal(err)
	}
	if got != "stream ok" || gotDelta != "stream ok" {
		t.Fatalf("got=%q delta=%q, want stream ok", got, gotDelta)
	}
	if p.completeCalls != 0 || p.streamCalls != 1 {
		t.Fatalf("complete=%d stream=%d, want stream only", p.completeCalls, p.streamCalls)
	}
}

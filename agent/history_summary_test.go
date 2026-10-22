package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/llm"
)

func TestFlattenMessagesPreservesToolEvidence(t *testing.T) {
	msgs := []llm.Message{
		llm.UserMessage("fix agent/history.go", nil),
		{Role: llm.RoleAssistant, Text: "Run checks", ToolCalls: []llm.ToolCall{{ID: "check", Name: "bash", Input: json.RawMessage(`{"command":"go test ./agent"}`)}}},
		llm.ToolResultMessage([]llm.ToolResult{{ID: "check", Output: "FAIL agent/history_test.go:42", IsError: true}}),
		llm.ToolResultMessage([]llm.ToolResult{{ID: "edit", Output: "edited agent/history.go", IsError: false}}),
	}
	got := flattenMessages(msgs)
	for _, want := range []string{"fix agent/history.go", "Run checks", `tool call "check" "bash"`, "go test ./agent", `tool result "check" (error=true)`, "FAIL agent/history_test.go:42", `tool result "edit" (error=false)`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in summary evidence: %s", want, got)
		}
	}
}

func TestSummaryExcerptBoundsUnicodeAndKeepsOutcomes(t *testing.T) {
	short := "exact path: src/日本語.go"
	if got := summaryExcerpt(short); got != short {
		t.Fatalf("short excerpt changed: %q", got)
	}
	long := "START " + strings.Repeat("界", 2000) + " FAIL: check did not pass"
	got := summaryExcerpt(long)
	if !utf8.ValidString(got) || len([]rune(got)) > 650 {
		t.Fatalf("invalid or oversized excerpt: %d runes", len([]rune(got)))
	}
	if !strings.HasPrefix(got, "START ") || !strings.HasSuffix(got, "FAIL: check did not pass") || !strings.Contains(got, "[excerpt truncated]") {
		t.Fatalf("lost evidence boundaries: %q", got)
	}
}

func TestFlattenMessagesDoesNotExposeImageOrWireBytes(t *testing.T) {
	got := flattenMessages([]llm.Message{
		llm.UserMessage("inspect", []llm.Image{{Data: []byte("private image bytes")}}),
		{Role: llm.RoleAssistant, Text: "visible reply", Wire: json.RawMessage(`{"private":"reasoning"}`)},
	})
	if !strings.Contains(got, "image contents unavailable") || !strings.Contains(got, "visible reply") {
		t.Fatalf("missing visible evidence: %s", got)
	}
	if strings.Contains(got, "private") || strings.Contains(got, "reasoning") {
		t.Fatalf("raw private data in evidence: %s", got)
	}
	if got := flattenMessages(nil); got != "" {
		t.Fatalf("empty history produced %q", got)
	}
}

type summaryEvidenceProvider struct {
	requests []*llm.Request
}

func (p *summaryEvidenceProvider) Model() string { return "test" }
func (p *summaryEvidenceProvider) Complete(_ context.Context, req *llm.Request) (*llm.Response, error) {
	p.requests = append(p.requests, req)
	return &llm.Response{Text: "Checks and outcomes: go test ./agent failed."}, nil
}

func TestHandoffSummaryUsedForCompactionAndPersistence(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := &summaryEvidenceProvider{}
	a := &Agent{llm: provider, cfg: cfg, history: map[string][]llm.Message{"u": {llm.UserMessage("continue", nil)}}}
	evidence := []llm.Message{
		llm.UserMessage("fix tests", nil),
		llm.ToolResultMessage([]llm.ToolResult{{ID: "check", Output: "go test ./agent failed", IsError: true}}),
	}
	a.summarizeAndPrepend("u", evidence)
	if got := a.messages("u"); len(got) != 2 || !strings.Contains(got[0].Text, "Checks and outcomes:") || got[1].Text != "continue" {
		t.Fatalf("compaction lost summary or kept history: %+v", got)
	}
	a.setMessages("u", evidence)
	a.persistUser(context.Background(), "u")
	if got := loadSummary(cfg.Dir(), "u"); !strings.Contains(got, "go test ./agent failed") {
		t.Fatalf("missing persisted summary: %q", got)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(provider.requests))
	}
	for _, req := range provider.requests {
		if req.SystemPrompt != handoffSummaryPrompt || req.MaxTokens != 600 {
			t.Errorf("unexpected summary config: %+v", req)
		}
		if len(req.Messages) != 1 || !strings.Contains(req.Messages[0].Text, "go test ./agent failed") {
			t.Errorf("missing tool evidence: %+v", req.Messages)
		}
	}
}

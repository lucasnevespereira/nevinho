package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const thinkingReply = `{"content":[` +
	`{"type":"thinking","thinking":"","signature":"sig-1"},` +
	`{"type":"text","text":"checking"},` +
	`{"type":"thinking","thinking":"","signature":"sig-2"},` +
	`{"type":"tool_use","id":"c1","name":"bash","input":{"command":"ls"}}],` +
	`"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`

// A turn with thinking blocks goes back to the API with every block in
// its original order, signatures included.
func TestAnthropicReplaysThinkingBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(thinkingReply))
	}))
	defer srv.Close()

	resp, err := NewAnthropic("key", srv.URL, "claude-opus-5-5").Complete(context.Background(), &Request{MaxTokens: 10})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "checking" || len(resp.ToolCalls) != 1 {
		t.Fatalf("text=%q calls=%d", resp.Text, len(resp.ToolCalls))
	}
	sent := string(anthropicEncode([]Message{resp.Assistant})[0])
	last := -1
	for _, want := range []string{"sig-1", "checking", "sig-2", "tool_use"} {
		at := strings.Index(sent, want)
		if at <= last {
			t.Fatalf("%q missing or out of order in %s", want, sent)
		}
		last = at
	}
}

func TestAnthropicTurnWithoutThinkingHasNoWire(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	resp, err := NewAnthropic("key", srv.URL, "claude-haiku-4-5").Complete(context.Background(), &Request{MaxTokens: 10})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Assistant.Wire != nil {
		t.Fatalf("wire = %s, want none", resp.Assistant.Wire)
	}
}

func TestAnthropicStreamKeepsThinkingBlocks(t *testing.T) {
	srv := sseServer(t, "/v1/messages", strings.Join([]string{
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hm"}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-"}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"1"}}`,
		``,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		``,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"done"}}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		``,
	}, "\n"))
	defer srv.Close()

	resp, err := NewAnthropic("key", srv.URL, "claude-opus-5-5").StreamComplete(context.Background(), &Request{MaxTokens: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire := string(resp.Assistant.Wire)
	for _, want := range []string{`"signature":"sig-1"`, `"thinking":"hm"`, `"text":"done"`} {
		if !strings.Contains(wire, want) {
			t.Fatalf("wire lacks %s: %s", want, wire)
		}
	}
}

// When the API refuses stale thinking blocks, the request is resent once
// without them and the caller is told.
func TestAnthropicRetriesWithoutRejectedThinking(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		if strings.Contains(string(body), "sig-1") {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("{\"error\":{\"message\":\"messages.1.content.0: Invalid `signature` in `thinking` block. The block is bound to a different conversation.\"}}"))
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	history := []Message{
		UserMessage("hi", nil),
		{Role: RoleAssistant, Text: "checking", Wire: []byte(`[{"type":"thinking","thinking":"","signature":"sig-1"},{"type":"text","text":"checking"}]`)},
		UserMessage("and?", nil),
	}
	resp, err := NewAnthropic("key", srv.URL, "claude-opus-5-5").Complete(context.Background(), &Request{Messages: history, MaxTokens: 10})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "ok" || !resp.ThinkingRejected {
		t.Fatalf("text=%q rejected=%v", resp.Text, resp.ThinkingRejected)
	}
	if len(bodies) != 2 || !strings.Contains(bodies[1], "checking") || strings.Contains(bodies[1], "sig-1") {
		t.Fatalf("retry should keep the text and drop the thinking, got %v", bodies)
	}
	if history[1].Wire == nil {
		t.Fatal("the caller's history must not be changed by the provider")
	}
}

func TestAnthropicMaxTokensLeavesRoomForThinking(t *testing.T) {
	for model, want := range map[string]int{
		"claude-haiku-4-5":  200,
		"claude-opus-4-7":   200,
		"claude-haiku-5-5":  200 + thinkingRoom,
		"claude-sonnet-5-5": 200 + thinkingRoom,
		"claude-opus-5-5":   200 + thinkingRoom,
		"claude-fable-5-1":  200 + thinkingRoom,
	} {
		if got := NewAnthropic("key", "", model).maxTokens(200); got != want {
			t.Errorf("%s: max_tokens = %d, want %d", model, got, want)
		}
	}
}

// The conversation is cached, not only the system prompt and tools.
func TestAnthropicCachesTheConversation(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn",` +
			`"usage":{"input_tokens":3,"cache_read_input_tokens":900,"cache_creation_input_tokens":97,"output_tokens":5}}`))
	}))
	defer srv.Close()

	resp, err := NewAnthropic("key", srv.URL, "claude-opus-5-5").Complete(context.Background(),
		&Request{Messages: []Message{UserMessage("hi", nil)}, MaxTokens: 10})
	if err != nil {
		t.Fatal(err)
	}
	if string(body["cache_control"]) != `{"type":"ephemeral"}` {
		t.Fatalf("top-level cache_control = %s", body["cache_control"])
	}
	if resp.Usage.Input() != 1000 {
		t.Fatalf("input = %d, want 1000 with cached tokens counted", resp.Usage.Input())
	}
}

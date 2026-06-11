package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sseServer(t *testing.T, wantPath string, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.String(), wantPath) {
			t.Fatalf("path = %s, want prefix %s", r.URL.String(), wantPath)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
}

func TestAnthropicStreamComplete(t *testing.T) {
	srv := sseServer(t, "/v1/messages", strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":7}}}`,
		``,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hel"}}`,
		``,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		``,
	}, "\n"))
	defer srv.Close()

	p := NewAnthropic("key", srv.URL, "claude-test")
	var gotDelta string
	resp, err := p.StreamComplete(context.Background(), &Request{SystemPrompt: "s", MaxTokens: 10}, func(d string) { gotDelta += d })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello" || gotDelta != "hello" {
		t.Fatalf("text=%q delta=%q, want hello", resp.Text, gotDelta)
	}
	if resp.StopReason != StopEndTurn {
		t.Fatalf("stop=%q", resp.StopReason)
	}
}

func TestGeminiStreamComplete(t *testing.T) {
	srv := sseServer(t, "/v1beta/models/gemini-test:streamGenerateContent", strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]}}]}`,
		``,
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":" there"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}}`,
		``,
	}, "\n"))
	defer srv.Close()

	p := NewGemini("key", srv.URL, "gemini-test")
	var gotDelta string
	resp, err := p.StreamComplete(context.Background(), &Request{SystemPrompt: "s", MaxTokens: 10}, func(d string) { gotDelta += d })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hi there" || gotDelta != "hi there" {
		t.Fatalf("text=%q delta=%q, want hi there", resp.Text, gotDelta)
	}
	if resp.Usage.In != 3 || resp.Usage.Out != 2 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
}

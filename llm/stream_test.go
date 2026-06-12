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
	if resp.Usage.In != 7 || resp.Usage.Out != 2 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
}

func TestOpenAIStreamCompleteText(t *testing.T) {
	srv := sseServer(t, "/v1/chat/completions", strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"hel"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
		``,
		`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))
	defer srv.Close()

	p := NewOpenAI("key", srv.URL, "gpt-test")
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
	if resp.Usage.In != 5 || resp.Usage.Out != 2 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
	var msg struct {
		Role    string  `json:"role"`
		Content *string `json:"content"`
	}
	if err := json.Unmarshal(resp.AssistantMessage, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Role != "assistant" || msg.Content == nil || *msg.Content != "hello" {
		t.Fatalf("assistant message=%s", resp.AssistantMessage)
	}
}

func TestOpenAIStreamCompleteToolCall(t *testing.T) {
	srv := sseServer(t, "/v1/chat/completions", strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"file_","arguments":"{\"path\""}}]}}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"read","arguments":":\"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`,
		``,
		`data: {"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4}}`,
		``,
	}, "\n"))
	defer srv.Close()

	p := NewOpenAI("key", srv.URL, "gpt-test")
	var gotDelta string
	resp, err := p.StreamComplete(context.Background(), &Request{SystemPrompt: "s", MaxTokens: 10}, func(d string) { gotDelta += d })
	if err != nil {
		t.Fatal(err)
	}
	if gotDelta != "" || resp.Text != "" {
		t.Fatalf("text=%q delta=%q, want no user-visible stream", resp.Text, gotDelta)
	}
	if resp.StopReason != StopToolUse {
		t.Fatalf("stop=%q", resp.StopReason)
	}
	if resp.Usage.In != 9 || resp.Usage.Out != 4 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls=%+v", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "file_read" || string(tc.Input) != `{"path":"README.md"}` {
		t.Fatalf("tool call=%+v input=%s", tc, tc.Input)
	}
}

func TestOpenAIStreamCompleteIncludesUsageOption(t *testing.T) {
	body := openAIStreamBody(t, NewOpenAI("key", "", "gpt-test"))
	options, ok := body["stream_options"].(map[string]interface{})
	if !ok {
		t.Fatalf("stream_options missing or wrong type: %#v", body["stream_options"])
	}
	if options["include_usage"] != true {
		t.Fatalf("include_usage=%#v, want true", options["include_usage"])
	}
}

func TestOpenAICompatibleStreamCompleteOmitsUsageOption(t *testing.T) {
	body := openAIStreamBody(t, NewOpenAICompatible("key", "", "gpt-test"))
	if _, ok := body["stream_options"]; ok {
		t.Fatalf("compatible provider should omit stream_options: %#v", body["stream_options"])
	}
}

func openAIStreamBody(t *testing.T, p *OpenAI) map[string]interface{} {
	t.Helper()
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\n"))
	}))
	defer srv.Close()
	p.baseURL = srv.URL

	if _, err := p.StreamComplete(context.Background(), &Request{SystemPrompt: "s", MaxTokens: 10}, nil); err != nil {
		t.Fatal(err)
	}
	return got
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

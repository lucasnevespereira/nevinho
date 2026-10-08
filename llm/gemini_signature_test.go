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

const geminiToolTurn = `{"candidates":[{"content":{"role":"model","parts":[` +
	`{"functionCall":{"name":"file_read","args":{"path":"go.mod"}},"thoughtSignature":"SIG-1"},` +
	`{"functionCall":{"name":"file_read","args":{"path":"README.md"}}}]},"finishReason":"STOP"}],` +
	`"usageMetadata":{"promptTokenCount":1000,"cachedContentTokenCount":900,"candidatesTokenCount":20,"thoughtsTokenCount":300}}`

// The failure seen on gemini-3.8-flash: a function call sent back without
// the thought signature it came with is rejected. The model's parts now
// go back as they arrived.
func TestGeminiReplaysThoughtSignature(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		_, _ = w.Write([]byte(geminiToolTurn))
	}))
	defer srv.Close()

	p := NewGemini("key", srv.URL, "gemini-3.8-flash")
	history := []Message{UserMessage("read both", nil)}
	resp, err := p.Complete(context.Background(), &Request{Messages: history, MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 2 || resp.StopReason != StopToolUse {
		t.Fatalf("calls=%d stop=%q", len(resp.ToolCalls), resp.StopReason)
	}
	// 100 uncached, 900 cached, and thinking counted as output.
	if resp.Usage.In != 100 || resp.Usage.CacheRead != 900 || resp.Usage.Out != 320 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	var first struct {
		GenerationConfig struct {
			MaxOutputTokens int `json:"maxOutputTokens"`
		} `json:"generationConfig"`
	}
	_ = json.Unmarshal([]byte(bodies[0]), &first)
	if first.GenerationConfig.MaxOutputTokens != 100+thinkingRoom {
		t.Fatalf("maxOutputTokens = %d", first.GenerationConfig.MaxOutputTokens)
	}

	history = append(history, resp.Assistant, ToolResultMessage([]ToolResult{{ID: "file_read", Output: "go 1.25"}}))
	if _, err := p.Complete(context.Background(), &Request{Messages: history, MaxTokens: 100}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bodies[1], `"thoughtSignature":"SIG-1"`) || !strings.Contains(bodies[1], "README.md") {
		t.Fatalf("second request lost the signature or a call: %s", bodies[1])
	}
}

func TestGeminiStream(t *testing.T) {
	call := `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"bash","args":{}},"thoughtSignature":"SIG-1"}]}}]}`
	for name, tc := range map[string]struct {
		lines   []string
		wantErr bool
	}{
		"signature kept":     {[]string{call, ``, `data: {"candidates":[{"content":{"parts":[{"text":""}]},"finishReason":"STOP"}]}`, ``}, false},
		"connection closed":  {[]string{call, ``}, true},
		"malformed function": {[]string{`data: {"candidates":[{"content":{},"finishReason":"MALFORMED_FUNCTION_CALL"}]}`, ``}, true},
	} {
		srv := sseServer(t, "/v1beta/models/gemini-3.8-flash:streamGenerateContent", strings.Join(tc.lines, "\n"))
		resp, err := NewGemini("key", srv.URL, "gemini-3.8-flash").StreamComplete(context.Background(), &Request{}, nil)
		srv.Close()
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, want error = %v", name, err, tc.wantErr)
			continue
		}
		if err == nil && !strings.Contains(string(resp.Assistant.Wire), "SIG-1") {
			t.Errorf("%s: wire = %s", name, resp.Assistant.Wire)
		}
		if name == "malformed function" && !strings.Contains(err.Error(), "MALFORMED_FUNCTION_CALL") {
			t.Errorf("error should name the finish reason: %v", err)
		}
	}
}

func TestGeminiTurnWithoutSignatureHasNoWire(t *testing.T) {
	resp, err := geminiResponse([]json.RawMessage{json.RawMessage(`{"text":"hi"}`)}, "STOP")
	if err != nil || resp.Text != "hi" || resp.Assistant.Wire != nil {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

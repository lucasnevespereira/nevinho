package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lucasnevespereira/nevinho/config"
)

const responsesToolTurn = `{"status":"completed","output":[` +
	`{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENC"},` +
	`{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"checking","annotations":[]}]},` +
	`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"bash","arguments":"{\"command\":\"ls\"}","status":"completed"}],` +
	`"usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":900},"output_tokens":20}}`

func TestResolveRoutesReasoningModelsToResponses(t *testing.T) {
	pc := config.ProviderConfig{OpenAIKey: "key"}
	for model, wantResponses := range map[string]bool{
		"gpt-5":       true,
		"gpt-5-mini":  true,
		"gpt-6-luna":  true,
		"gpt-6.1-sol": true,
		"gpt-6-astra": true,
		"o4-mini":     true,
		"gpt-4o":      false,
		"gpt-4o-mini": false,
		"gpt-4-turbo": false,
	} {
		p, err := Resolve(model, pc)
		if err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		if _, got := p.(*OpenAIResponses); got != wantResponses {
			t.Errorf("%s: responses provider = %v, want %v", model, got, wantResponses)
		}
	}
}

// One tool step end to end: the request is stateless, the reply is parsed,
// and the next request sends the model's items back unchanged, followed
// by the tool output.
func TestResponsesToolLoop(t *testing.T) {
	var bodies []map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]json.RawMessage
		_ = json.Unmarshal(raw, &body)
		bodies = append(bodies, body)
		_, _ = w.Write([]byte(responsesToolTurn))
	}))
	defer srv.Close()

	p := NewOpenAIResponses("key", srv.URL, "gpt-5")
	tools := []ToolDef{{Name: "bash", Description: "run", Schema: `{"type":"object"}`}}
	history := []Message{UserMessage("list files", nil)}
	resp, err := p.Complete(context.Background(), &Request{SystemPrompt: "be brief", Messages: history, Tools: tools, MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}

	if resp.Text != "checking" || resp.StopReason != StopToolUse {
		t.Fatalf("text=%q stop=%q", resp.Text, resp.StopReason)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_1" || string(resp.ToolCalls[0].Input) != `{"command":"ls"}` {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.Usage.In != 100 || resp.Usage.CacheRead != 900 || resp.Usage.Input() != 1000 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	first := bodies[0]
	if string(first["store"]) != "false" || string(first["instructions"]) != `"be brief"` || string(first["max_output_tokens"]) != "8292" {
		t.Fatalf("store=%s instructions=%s max=%s", first["store"], first["instructions"], first["max_output_tokens"])
	}
	if !strings.Contains(string(first["tools"]), `"strict":false`) {
		t.Fatalf("tools = %s", first["tools"])
	}

	history = append(history, resp.Assistant, ToolResultMessage([]ToolResult{{ID: "call_1", Output: "a.txt"}}))
	if _, err := p.Complete(context.Background(), &Request{Messages: history, Tools: tools, MaxTokens: 100}); err != nil {
		t.Fatal(err)
	}
	input := string(bodies[1]["input"])
	last := -1
	for _, want := range []string{"list files", `"encrypted_content":"ENC"`, `"id":"msg_1"`, `"id":"fc_1"`, `"output":"a.txt"`, `"type":"function_call_output"`} {
		at := strings.Index(input, want)
		if at <= last {
			t.Fatalf("%s missing or out of order in %s", want, input)
		}
		last = at
	}
}

// A turn that lost its stored items (history was trimmed) is rebuilt from
// its text and tool calls.
func TestResponsesEncodeWithoutWire(t *testing.T) {
	msgs := []Message{
		{Role: RoleAssistant, Text: "checking", ToolCalls: []ToolCall{{ID: "call_1", Name: "bash", Input: []byte(`{"command":"ls"}`)}}},
		ToolResultMessage([]ToolResult{{ID: "call_1", Output: "a.txt"}}),
	}
	var joined string
	for _, item := range responsesEncode(msgs) {
		joined += string(item)
	}
	for _, want := range []string{`"role":"assistant"`, `"type":"function_call"`, `"call_id":"call_1"`, `"arguments":"{\"command\":\"ls\"}"`, `"type":"function_call_output"`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("%s missing in %s", want, joined)
		}
	}
}

func TestResponsesStream(t *testing.T) {
	deltas := []string{
		`data: {"type":"response.output_text.delta","delta":"check"}`,
		``,
		`data: {"type":"response.output_text.delta","delta":"ing"}`,
		``,
	}
	for name, tc := range map[string]struct {
		tail    []string
		wantErr bool
	}{
		"completed":         {[]string{`data: {"type":"response.completed","response":` + responsesToolTurn + `}`, ``}, false},
		"failed":            {[]string{`data: {"type":"response.failed","response":{"status":"failed","error":{"message":"server error"}}}`, ``}, true},
		"error event":       {[]string{`data: {"type":"error","message":"overloaded"}`, ``}, true},
		"connection closed": {nil, true},
	} {
		srv := sseServer(t, "/v1/responses", strings.Join(append(append([]string{}, deltas...), tc.tail...), "\n"))
		var live string
		resp, err := NewOpenAIResponses("key", srv.URL, "gpt-5").StreamComplete(context.Background(), &Request{MaxTokens: 10}, func(d string) { live += d })
		srv.Close()
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, want error = %v", name, err, tc.wantErr)
			continue
		}
		if err == nil && (live != "checking" || resp.Text != "checking" || len(resp.ToolCalls) != 1 || resp.Assistant.Wire == nil) {
			t.Errorf("%s: live=%q text=%q calls=%d", name, live, resp.Text, len(resp.ToolCalls))
		}
	}
}

func TestResponsesHitsOutputCap(t *testing.T) {
	resp, err := parseResponsesResult([]byte(`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`))
	if err != nil || resp.StopReason != StopMaxTokens {
		t.Fatalf("stop=%v err=%v", resp, err)
	}
}

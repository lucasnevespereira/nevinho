package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func userMsg(text string) json.RawMessage {
	msg, _ := json.Marshal(map[string]any{"role": "user", "content": text})
	return msg
}

func assistantMsg(text string) json.RawMessage {
	msg, _ := json.Marshal(map[string]any{"role": "assistant", "content": text})
	return msg
}

func toolMsg() json.RawMessage {
	msg, _ := json.Marshal(map[string]any{"role": "tool", "tool_call_id": "1", "content": "ok"})
	return msg
}

func toolUseUserMsg() json.RawMessage {
	content := []map[string]any{{"type": "tool_result", "tool_use_id": "1", "content": "ok"}}
	msg, _ := json.Marshal(map[string]any{"role": "user", "content": content})
	return msg
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name string
		msgs []json.RawMessage
		want int
	}{
		{
			name: "empty history",
			msgs: nil,
			want: 0,
		},
		{
			name: "single short message",
			msgs: []json.RawMessage{userMsg("hello")},
			want: len(userMsg("hello")) / 4,
		},
		{
			name: "multiple messages sum correctly",
			msgs: []json.RawMessage{userMsg("hello"), assistantMsg("hi there")},
			want: (len(userMsg("hello")) + len(assistantMsg("hi there"))) / 4,
		},
		{
			name: "large message produces proportionally large count",
			msgs: []json.RawMessage{userMsg(strings.Repeat("a", 4000))},
			want: len(userMsg(strings.Repeat("a", 4000))) / 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateTokens(tt.msgs)
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTrimHistoryByTokens(t *testing.T) {
	small := userMsg("hi")
	big := userMsg(strings.Repeat("x", 4000))

	tests := []struct {
		name      string
		msgs      []json.RawMessage
		maxTokens int
		wantCount int
	}{
		{
			name:      "under maxTokens keeps everything",
			msgs:      []json.RawMessage{small, assistantMsg("hey")},
			maxTokens: 10000,
			wantCount: 2,
		},
		{
			name:      "over maxTokens trims oldest messages",
			msgs:      []json.RawMessage{big, big, big, small},
			maxTokens: estimateTokens([]json.RawMessage{big, small}),
			wantCount: 2,
		},
		{
			name:      "skips orphaned tool results to find clean boundary",
			msgs:      []json.RawMessage{big, toolMsg(), small, assistantMsg("ok")},
			maxTokens: estimateTokens([]json.RawMessage{small, assistantMsg("ok")}),
			wantCount: 2,
		},
		{
			name:      "skips orphaned assistant messages",
			msgs:      []json.RawMessage{big, assistantMsg("old"), small, assistantMsg("new")},
			maxTokens: estimateTokens([]json.RawMessage{small, assistantMsg("new")}),
			wantCount: 2,
		},
		{
			name:      "skips tool_use array user messages",
			msgs:      []json.RawMessage{big, toolUseUserMsg(), small},
			maxTokens: estimateTokens([]json.RawMessage{small}),
			wantCount: 1,
		},
		{
			name:      "keeps at least the last message when everything exceeds maxTokens",
			msgs:      []json.RawMessage{big, big, big},
			maxTokens: 1,
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimHistoryByTokens(tt.msgs, tt.maxTokens)
			if len(got) != tt.wantCount {
				t.Errorf("got %d messages, want %d", len(got), tt.wantCount)
			}
		})
	}
}

func TestTrimHistoryByTokens_LandsOnUserMessage(t *testing.T) {
	big := userMsg(strings.Repeat("x", 4000))
	msgs := []json.RawMessage{
		big,
		assistantMsg("reply"),
		toolMsg(),
		userMsg("second turn"),
		assistantMsg("reply2"),
	}

	maxTokens := estimateTokens(msgs[3:])
	got := trimHistoryByTokens(msgs, maxTokens)

	var first struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	json.Unmarshal(got[0], &first)

	if first.Role != "user" {
		t.Errorf("first message role = %q, want \"user\"", first.Role)
	}
	if first.Content != "second turn" {
		t.Errorf("first message content = %q, want \"second turn\"", first.Content)
	}
}

func TestFlattenMessages(t *testing.T) {
	tests := []struct {
		name     string
		msgs     []json.RawMessage
		contains []string
	}{
		{
			name:     "extracts role and text content",
			msgs:     []json.RawMessage{userMsg("hello"), assistantMsg("hi")},
			contains: []string{"user: hello", "assistant: hi"},
		},
		{
			name:     "truncates long content at 200 runes",
			msgs:     []json.RawMessage{userMsg(strings.Repeat("a", 300))},
			contains: []string{strings.Repeat("a", 200) + "..."},
		},
		{
			name:     "non-string content shows tool interaction",
			msgs:     []json.RawMessage{toolUseUserMsg()},
			contains: []string{"[tool interaction]"},
		},
		{
			name:     "empty input returns empty string",
			msgs:     nil,
			contains: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flattenMessages(tt.msgs)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\ngot: %s", want, got)
				}
			}
			if tt.contains == nil && got != "" {
				t.Errorf("expected empty string, got %q", got)
			}
		})
	}
}

func TestEstimateCost(t *testing.T) {
	tests := []struct {
		name  string
		model string
		in    int
		out   int
		want  float64
	}{
		{
			name:  "haiku pricing",
			model: "claude-haiku-4-5",
			in:    1_000_000,
			out:   1_000_000,
			want:  0.80 + 4.00,
		},
		{
			name:  "sonnet pricing",
			model: "claude-sonnet-4-6",
			in:    1_000_000,
			out:   1_000_000,
			want:  3.00 + 15.00,
		},
		{
			name:  "gpt-4o-mini matched before gpt-4o",
			model: "gpt-4o-mini",
			in:    1_000_000,
			out:   1_000_000,
			want:  0.15 + 0.60,
		},
		{
			name:  "unknown model returns zero",
			model: "llama3",
			in:    1_000_000,
			out:   1_000_000,
			want:  0,
		},
		{
			name:  "zero tokens returns zero cost",
			model: "claude-haiku-4-5",
			in:    0,
			out:   0,
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateCost(tt.model, tt.in, tt.out)
			if got != tt.want {
				t.Errorf("got %.4f, want %.4f", got, tt.want)
			}
		})
	}
}

func TestLooksLikeApproval(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"yes", true},
		{"Yes", true},
		{"  YES  ", true},
		{"oui", true},
		{"go ahead", true},
		{"no", false},
		{"yes please", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := looksLikeApproval(tt.input); got != tt.want {
				t.Errorf("looksLikeApproval(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestAppendHistory_EvictsWhenOverLimit(t *testing.T) {
	a := &Agent{
		history: make(map[string][]json.RawMessage),
	}

	big := userMsg(strings.Repeat("x", maxHistoryTokens*4))
	a.history["u1"] = []json.RawMessage{big}

	evicted := a.appendHistory("u1", userMsg("new"))

	if len(evicted) == 0 {
		t.Fatal("expected eviction but got none")
	}

	if estimateTokens(a.history["u1"]) > maxHistoryTokens {
		t.Errorf("history still over maxTokens after trim: %d tokens", estimateTokens(a.history["u1"]))
	}
}

func TestAppendHistory_NoEvictionUnderLimit(t *testing.T) {
	a := &Agent{
		history: make(map[string][]json.RawMessage),
	}

	evicted := a.appendHistory("u1", userMsg("hello"))

	if len(evicted) != 0 {
		t.Errorf("expected no eviction, got %d messages evicted", len(evicted))
	}
	if len(a.history["u1"]) != 1 {
		t.Errorf("expected 1 message in history, got %d", len(a.history["u1"]))
	}
}

package agent

import (
	"strings"
	"testing"

	"github.com/lucasnevespereira/nevinho/llm"
)

func userMsg(text string) llm.Message {
	return llm.UserMessage(text, nil)
}

func assistantMsg(text string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Text: text}
}

func toolMsg() llm.Message {
	return llm.ToolResultMessage([]llm.ToolResult{{ID: "1", Output: "ok"}})
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name string
		msgs []llm.Message
		want int
	}{
		{
			name: "empty history",
			msgs: nil,
			want: 0,
		},
		{
			name: "single short message",
			msgs: []llm.Message{userMsg("hello")},
			want: userMsg("hello").Size() / 4,
		},
		{
			name: "multiple messages sum correctly",
			msgs: []llm.Message{userMsg("hello"), assistantMsg("hi there")},
			want: (userMsg("hello").Size() + assistantMsg("hi there").Size()) / 4,
		},
		{
			name: "large message produces proportionally large count",
			msgs: []llm.Message{userMsg(strings.Repeat("a", 4000))},
			want: userMsg(strings.Repeat("a", 4000)).Size() / 4,
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
		msgs      []llm.Message
		maxTokens int
		wantCount int
	}{
		{
			name:      "under maxTokens keeps everything",
			msgs:      []llm.Message{small, assistantMsg("hey")},
			maxTokens: 10000,
			wantCount: 2,
		},
		{
			name:      "over maxTokens trims oldest messages",
			msgs:      []llm.Message{big, big, big, small},
			maxTokens: estimateTokens([]llm.Message{big, small}),
			wantCount: 2,
		},
		{
			name:      "skips orphaned tool results to find clean boundary",
			msgs:      []llm.Message{big, toolMsg(), small, assistantMsg("ok")},
			maxTokens: estimateTokens([]llm.Message{small, assistantMsg("ok")}),
			wantCount: 2,
		},
		{
			name:      "skips orphaned assistant messages",
			msgs:      []llm.Message{big, assistantMsg("old"), small, assistantMsg("new")},
			maxTokens: estimateTokens([]llm.Message{small, assistantMsg("new")}),
			wantCount: 2,
		},
		{
			name:      "keeps at least the last message when everything exceeds maxTokens",
			msgs:      []llm.Message{big, big, big},
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
	msgs := []llm.Message{
		big,
		assistantMsg("reply"),
		toolMsg(),
		userMsg("second turn"),
		assistantMsg("reply2"),
	}

	maxTokens := estimateTokens(msgs[3:])
	got := trimHistoryByTokens(msgs, maxTokens)

	if got[0].Role != llm.RoleUser {
		t.Errorf("first message role = %q, want user", got[0].Role)
	}
	if got[0].Text != "second turn" {
		t.Errorf("first message text = %q, want \"second turn\"", got[0].Text)
	}
}

func TestFlattenMessages(t *testing.T) {
	tests := []struct {
		name     string
		msgs     []llm.Message
		contains []string
	}{
		{
			name:     "extracts role and text content",
			msgs:     []llm.Message{userMsg("hello"), assistantMsg("hi")},
			contains: []string{"user: hello", "assistant: hi"},
		},
		{
			name:     "truncates long content at 200 runes",
			msgs:     []llm.Message{userMsg(strings.Repeat("a", 300))},
			contains: []string{strings.Repeat("a", 200) + "..."},
		},
		{
			name:     "non-string content shows tool interaction",
			msgs:     []llm.Message{toolMsg()},
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
			ans := answerIn(tt.input)
			got := ans != nil && *ans == Approved
			if got != tt.want {
				t.Errorf("answerIn(%q) approved = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestAppendHistory_EvictsWhenOverLimit(t *testing.T) {
	a := &Agent{
		history: make(map[string][]llm.Message),
	}

	big := userMsg(strings.Repeat("x", maxHistoryTokens*8))
	a.history["u1"] = []llm.Message{big}

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
		history: make(map[string][]llm.Message),
	}

	evicted := a.appendHistory("u1", userMsg("hello"))

	if len(evicted) != 0 {
		t.Errorf("expected no eviction, got %d messages evicted", len(evicted))
	}
	if len(a.history["u1"]) != 1 {
		t.Errorf("expected 1 message in history, got %d", len(a.history["u1"]))
	}
}

package agent

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/lucasnevespereira/nevinho/config"
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
			want:  1.00 + 5.00,
		},
		{
			name:  "sonnet pricing",
			model: "claude-sonnet-4-6",
			in:    1_000_000,
			out:   1_000_000,
			want:  3.00 + 15.00,
		},
		{
			name:  "haiku 5.5 matched before haiku",
			model: "claude-haiku-5-5",
			in:    1_000_000,
			out:   1_000_000,
			want:  0.10 + 0.50,
		},
		{
			name:  "opus 5.5 matched before opus",
			model: "claude-opus-5-5",
			in:    1_000_000,
			out:   1_000_000,
			want:  4.00 + 20.00,
		},
		{
			name:  "openrouter opus uses a dotted version",
			model: "openrouter:anthropic/claude-opus-4.7",
			in:    1_000_000,
			out:   1_000_000,
			want:  5.00 + 25.00,
		},
		{
			name:  "gpt-5-mini matched before gpt-5",
			model: "gpt-5-mini",
			in:    1_000_000,
			out:   1_000_000,
			want:  0.25 + 2.00,
		},
		{
			name:  "gpt-6.1-sol falls onto the gpt-6 row",
			model: "gpt-6.1-sol",
			in:    1_000_000,
			out:   1_000_000,
			want:  2.00 + 10.00,
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
			got := estimateCost(tt.model, llm.Usage{In: tt.in, Out: tt.out})
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
		llm:     &fallbackProvider{},
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
		llm:     &fallbackProvider{},
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

// Every paid model the picker offers needs a price row, otherwise the
// status bar reports $0.00 for it.
func TestCatalogModelsHavePrices(t *testing.T) {
	for _, provider := range []string{"anthropic", "openai", "gemini"} {
		for _, name := range config.KnownModels[provider] {
			if in, out := priceFor(name); in == 0 || out == 0 {
				t.Errorf("%s has no price row", name)
			}
		}
	}
}

// Trimming evicts whole old turns and leaves the kept ones as they are.
// Gemini 3 rejects a tool call that comes back without its signature, so
// the agent must not strip what a provider stored on a surviving turn.
func TestTrimLeavesKeptTurnsIntact(t *testing.T) {
	a := &Agent{llm: &fallbackProvider{}, history: map[string][]llm.Message{}}
	half := strings.Repeat("x", maxHistoryTokens*2)
	a.appendHistory("u", userMsg(half), assistantMsg("old"))
	a.appendHistory("u", userMsg("next"), llm.Message{Role: llm.RoleAssistant, Text: "kept", Wire: []byte(`[]`)})

	if evicted := a.appendHistory("u", userMsg(half)); len(evicted) != 2 {
		t.Fatalf("evicted %d messages, want the first turn", len(evicted))
	}
	hist := a.history["u"]
	if hist[0].Text != "next" || hist[1].Wire == nil {
		t.Fatalf("kept turn lost its stored content: %+v", hist[1])
	}
}

func TestRejectedThinkingIsDroppedFromHistory(t *testing.T) {
	a := &Agent{llm: &fallbackProvider{}, history: map[string][]llm.Message{
		"u": {userMsg("hi"), {Role: llm.RoleAssistant, Text: "a", Wire: []byte(`[]`)}, userMsg("again")},
	}}
	a.appendReply("u", &llm.Response{ThinkingRejected: true, Assistant: assistantMsg("b")})
	if len(a.history["u"]) != 4 || a.history["u"][1].Wire != nil {
		t.Fatalf("history = %+v", a.history["u"])
	}
}

// Two users (or a user and a scheduled run) take turns at the same time.
// Run with -race: unguarded, this dies with "concurrent map writes".
func TestHistoryIsSafeAcrossUsers(t *testing.T) {
	a := &Agent{llm: &fallbackProvider{}, history: map[string][]llm.Message{}, userLock: map[string]*sync.Mutex{}}
	var wg sync.WaitGroup
	for _, userID := range []string{"alice", "scheduler:1"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock := a.getUserLock(userID)
			for range 2000 {
				lock.Lock()
				a.appendHistory(userID, userMsg("hi"))
				_ = a.messages(userID)
				lock.Unlock()
			}
		}()
	}
	wg.Wait()
	if got := len(a.messages("alice")); got != 2000 {
		t.Fatalf("alice has %d messages, want 2000", got)
	}
}

// A single turn that outgrows the budget is kept whole. Cutting inside it
// would leave a tool result without the call that produced it.
func TestTrimKeepsOversizedTurnWhole(t *testing.T) {
	big := []byte(strings.Repeat("x", maxHistoryTokens*3))
	call := func(id string) llm.Message {
		return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "file_write", Input: big}}}
	}
	result := func(id string) llm.Message {
		return llm.ToolResultMessage([]llm.ToolResult{{ID: id, Output: "ok"}})
	}
	msgs := []llm.Message{
		userMsg("earlier turn"), assistantMsg("done"),
		userMsg("write two big files"), call("1"), result("1"), call("2"), result("2"),
	}

	got := trimHistoryByTokens(msgs, maxHistoryTokens)
	if len(got) != 5 || got[0].Text != "write two big files" {
		t.Fatalf("kept %d messages starting with %q, want the 5 of the current turn", len(got), got[0].Text)
	}
}

// An image costs a fixed amount, so sending one does not evict the
// conversation that came before it.
func TestImageDoesNotEvictHistory(t *testing.T) {
	a := &Agent{llm: &fallbackProvider{}, history: map[string][]llm.Message{}}
	a.appendHistory("u", userMsg("my name is Lucas"), assistantMsg("noted"))
	photo := []llm.Image{{MediaType: "image/png", Data: make([]byte, 2_000_000)}}
	if evicted := a.appendHistory("u", llm.UserMessage("what is this?", photo)); len(evicted) != 0 {
		t.Fatalf("evicted %d messages", len(evicted))
	}
}

func TestCapToolResult(t *testing.T) {
	short := "all good"
	if got := capToolResult(short); got != short {
		t.Fatalf("short output changed: %q", got)
	}

	long := "START" + strings.Repeat("é", maxToolResult) + "exit status 1"
	got := capToolResult(long)
	if !strings.HasPrefix(got, "START") || !strings.HasSuffix(got, "exit status 1") {
		t.Fatal("lost the start or the end")
	}
	if len(got) > maxToolResult+100 || !utf8.ValidString(got) {
		t.Fatalf("len=%d valid=%v", len(got), utf8.ValidString(got))
	}
}

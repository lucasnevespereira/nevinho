package llm

import "testing"

func TestAnthropicStopReason(t *testing.T) {
	cases := map[string]StopReason{
		"tool_use":      StopToolUse,
		"max_tokens":    StopMaxTokens,
		"end_turn":      StopEndTurn,
		"stop_sequence": StopEndTurn,
		"refusal":       StopOther,
		"":              StopOther,
	}
	for in, want := range cases {
		if got := anthropicStopReason(in); got != want {
			t.Errorf("anthropicStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenAIStopReason(t *testing.T) {
	cases := map[string]StopReason{
		"tool_calls":     StopToolUse,
		"function_call":  StopToolUse,
		"length":         StopMaxTokens,
		"stop":           StopEndTurn,
		"content_filter": StopOther,
		"":               StopOther,
	}
	for in, want := range cases {
		if got := openAIStopReason(in); got != want {
			t.Errorf("openAIStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGeminiStopReason(t *testing.T) {
	// Gemini reports STOP even when the turn carries function calls, so a
	// tool call must win over the raw reason.
	if got := geminiStopReason("STOP", true); got != StopToolUse {
		t.Errorf("geminiStopReason(STOP, hasToolCalls) = %q, want %q", got, StopToolUse)
	}
	cases := map[string]StopReason{
		"STOP":       StopEndTurn,
		"MAX_TOKENS": StopMaxTokens,
		"SAFETY":     StopOther,
		"":           StopOther,
	}
	for in, want := range cases {
		if got := geminiStopReason(in, false); got != want {
			t.Errorf("geminiStopReason(%q, false) = %q, want %q", in, got, want)
		}
	}
}

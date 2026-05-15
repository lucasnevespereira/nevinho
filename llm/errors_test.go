package llm

import (
	"fmt"
	"strings"
	"testing"
)

func TestFriendlyError(t *testing.T) {
	tests := []struct {
		name     string
		err      string
		contains string
	}{
		{"401 shows key error", "API 401: unauthorized", "invalid or expired"},
		{"429 shows rate limit", "API 429: too many requests", "Rate limited"},
		{"500 shows server error", "API 500: internal error", "server error"},
		{"503 shows overloaded", "API 503: service unavailable", "overloaded"},
		{"529 shows overloaded", "API 529: overloaded", "overloaded"},
		{"connection refused", "connection refused", "reach the API"},
		{"insufficient_quota shows billing", `{"code":"insufficient_quota"}`, "quota exhausted"},
		{"exceeded current quota shows billing", "exceeded your current quota", "quota exhausted"},
		{"unknown error passes through", "something weird", "something weird"},
		{"long unknown error is truncated", strings.Repeat("x", 500), "…"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FriendlyError(fmt.Errorf("%s", tt.err))
			if !strings.Contains(got, tt.contains) {
				t.Errorf("got %q, want containing %q", got, tt.contains)
			}
		})
	}
}

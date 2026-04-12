package discord

import (
	"fmt"
	"strings"
	"testing"
)

func TestSplitMessage(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantChunks int
		maxLen     int
	}{
		{
			name:       "short message stays as one chunk",
			input:      "hello",
			wantChunks: 1,
			maxLen:     maxMessageLen,
		},
		{
			name:       "exactly 2000 chars stays as one chunk",
			input:      strings.Repeat("a", maxMessageLen),
			wantChunks: 1,
			maxLen:     maxMessageLen,
		},
		{
			name:       "2001 chars splits into two chunks",
			input:      strings.Repeat("a", maxMessageLen+1),
			wantChunks: 2,
			maxLen:     maxMessageLen,
		},
		{
			name:       "4000 chars splits into two chunks",
			input:      strings.Repeat("a", 4000),
			wantChunks: 2,
			maxLen:     maxMessageLen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := splitMessage(tt.input)
			if len(chunks) != tt.wantChunks {
				t.Errorf("got %d chunks, want %d", len(chunks), tt.wantChunks)
			}
			for i, c := range chunks {
				if len(c) > tt.maxLen {
					t.Errorf("chunk %d is %d chars, exceeds %d", i, len(c), tt.maxLen)
				}
			}
		})
	}
}

func TestSplitMessage_PreservesAllContent(t *testing.T) {
	input := strings.Repeat("x", 5000)
	chunks := splitMessage(input)
	reassembled := strings.Join(chunks, "")
	if reassembled != input {
		t.Errorf("reassembled length = %d, want %d", len(reassembled), len(input))
	}
}

func TestSplitMessage_PrefersNewlineBoundary(t *testing.T) {
	line := strings.Repeat("a", 1500)
	input := line + "\n" + strings.Repeat("b", 1000)
	chunks := splitMessage(input)

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if !strings.HasSuffix(chunks[0], "\n") {
		t.Error("first chunk should end at the newline boundary")
	}
}

func TestCleanForDiscord(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "three newlines collapse to two",
			input: "a\n\n\nb",
			want:  "a\n\nb",
		},
		{
			name:  "five newlines collapse to two",
			input: "a\n\n\n\n\nb",
			want:  "a\n\nb",
		},
		{
			name:  "two newlines stay as two",
			input: "a\n\nb",
			want:  "a\n\nb",
		},
		{
			name:  "strips HTML tags",
			input: `<p align="center">hello</p>`,
			want:  "hello",
		},
		{
			name:  "strips img tags",
			input: `<img src="logo.png" width="200" />`,
			want:  "",
		},
		{
			name:  "strips image badge markdown",
			input: `![badge](https://img.shields.io/badge/go-1.21-blue)`,
			want:  "",
		},
		{
			name:  "strips linked badge",
			input: `[![Go](https://img.shields.io/badge/go-1.21-blue)](https://go.dev)`,
			want:  "",
		},
		{
			name:  "keeps regular markdown",
			input: "**bold** and `code` and [link](https://example.com)",
			want:  "**bold** and `code` and [link](https://example.com)",
		},
		{
			name:  "strips horizontal rule",
			input: "before\n\n---\n\nafter",
			want:  "before\n\nafter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanForDiscord(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

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
		{"unknown error passes through", "something weird", "something weird"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := friendlyError(fmt.Errorf("%s", tt.err))
			if !strings.Contains(got, tt.contains) {
				t.Errorf("got %q, want containing %q", got, tt.contains)
			}
		})
	}
}

package discord

import (
	"strings"
	"testing"
)

func TestFormatIndicator(t *testing.T) {
	tests := []struct {
		name    string
		toolN   string
		detail  string
		want    string
		include string
	}{
		{
			name:   "no detail",
			toolN:  "bash",
			detail: "",
			want:   "-# running bash...",
		},
		{
			name:   "with detail wraps in code span",
			toolN:  "bash",
			detail: "ls -la",
			want:   "-# running bash: `ls -la`",
		},
		{
			name:   "whitespace-only detail is treated as empty",
			toolN:  "bash",
			detail: "   ",
			want:   "-# running bash...",
		},
		{
			name:    "truncates long detail",
			toolN:   "web_search",
			detail:  strings.Repeat("x", 200),
			include: "...`",
		},
		{
			name:   "escapes backticks in detail",
			toolN:  "bash",
			detail: "echo `whoami`",
			want:   "-# running bash: `echo 'whoami'`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIndicator(tt.toolN, tt.detail)
			if tt.want != "" && got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if tt.include != "" && !strings.Contains(got, tt.include) {
				t.Errorf("got %q, want to contain %q", got, tt.include)
			}
		})
	}
}

func TestFormatIndicator_RespectsMaxDetailLength(t *testing.T) {
	detail := strings.Repeat("a", indicatorMaxDetail+50)
	got := formatIndicator("bash", detail)
	// Body between backticks must not exceed indicatorMaxDetail + 3 for "...".
	openIdx := strings.Index(got, "`")
	closeIdx := strings.LastIndex(got, "`")
	if openIdx == -1 || closeIdx == -1 || openIdx == closeIdx {
		t.Fatalf("expected backticks, got %q", got)
	}
	body := got[openIdx+1 : closeIdx]
	if len(body) > indicatorMaxDetail+3 {
		t.Errorf("body length %d exceeds max+3 (%d): %q", len(body), indicatorMaxDetail+3, body)
	}
}

func TestFormatChain(t *testing.T) {
	tests := []struct {
		name   string
		chain  []string
		detail string
		want   string
	}{
		{
			name:  "empty chain",
			chain: []string{},
			want:  "",
		},
		{
			name:   "single tool no detail",
			chain:  []string{"bash"},
			detail: "",
			want:   "-# running bash...",
		},
		{
			name:   "two tools last has detail",
			chain:  []string{"web_search", "web_read"},
			detail: "https://example.com",
			want:   "-# web_search → web_read: `https://example.com`",
		},
		{
			name:   "three tools no detail",
			chain:  []string{"web_search", "web_read", "file_write"},
			detail: "",
			want:   "-# web_search → web_read → file_write...",
		},
		{
			name:   "prefix tools never show detail",
			chain:  []string{"bash", "web_search"},
			detail: "nevinho",
			want:   "-# bash → web_search: `nevinho`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatChain(tt.chain, tt.detail)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

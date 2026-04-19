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
	e := func(name string, count int) chainEntry { return chainEntry{name: name, count: count} }
	tests := []struct {
		name   string
		chain  []chainEntry
		detail string
		want   string
	}{
		{
			name:  "empty chain",
			chain: []chainEntry{},
			want:  "",
		},
		{
			name:   "single tool no detail",
			chain:  []chainEntry{e("bash", 1)},
			detail: "",
			want:   "-# running bash...",
		},
		{
			name:   "two tools last has detail",
			chain:  []chainEntry{e("web_search", 1), e("web_read", 1)},
			detail: "https://example.com",
			want:   "-# web_search → web_read: `https://example.com`",
		},
		{
			name:   "three tools no detail",
			chain:  []chainEntry{e("web_search", 1), e("web_read", 1), e("file_write", 1)},
			detail: "",
			want:   "-# web_search → web_read → file_write...",
		},
		{
			name:   "prefix tools never show detail",
			chain:  []chainEntry{e("bash", 1), e("web_search", 1)},
			detail: "nevinho",
			want:   "-# bash → web_search: `nevinho`",
		},
		{
			name:   "repeated tool shows count",
			chain:  []chainEntry{e("web_search", 3)},
			detail: "query",
			want:   "-# running web_search ×3: `query`",
		},
		{
			name:   "count only on repeated entry",
			chain:  []chainEntry{e("web_search", 3), e("web_read", 1)},
			detail: "https://example.com",
			want:   "-# web_search ×3 → web_read: `https://example.com`",
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

func TestIndicatorCollapsesDuplicates(t *testing.T) {
	a := &activityIndicator{}
	// Simulate three consecutive web_search calls followed by one web_read
	// without touching Discord. We can't call onEvent because it hits the
	// network, but the collapse logic lives purely in the chain update.
	a.chain = append(a.chain, chainEntry{name: "web_search", count: 1})
	for range 2 {
		if n := len(a.chain); n > 0 && a.chain[n-1].name == "web_search" {
			a.chain[n-1].count++
		}
	}
	a.chain = append(a.chain, chainEntry{name: "web_read", count: 1})

	got := formatChain(a.chain, "https://example.com")
	want := "-# web_search ×3 → web_read: `https://example.com`"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

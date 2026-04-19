package tools

import (
	"strings"
	"testing"
)

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{"https is allowed", "https://example.com", ""},
		{"http is allowed", "http://example.com", ""},
		{"ftp is blocked", "ftp://example.com", "only http/https"},
		{"file scheme is blocked", "file:///etc/passwd", "only http/https"},
		{"javascript scheme is blocked", "javascript:alert(1)", "only http/https"},
		{"empty string fails", "", "only http/https"},
		{"localhost is blocked", "http://localhost", "internal"},
		{"127.0.0.1 is blocked", "http://127.0.0.1", "internal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateURL(tt.url)

			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestTimeRangeToDays(t *testing.T) {
	tests := map[string]int{
		"d":  1,
		"w":  7,
		"m":  30,
		"y":  365,
		"":   0,
		"xx": 0,
		"D":  1,
	}
	for in, want := range tests {
		if got := timeRangeToDays(in); got != want {
			t.Errorf("timeRangeToDays(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseDuckDuckGoLite(t *testing.T) {
	lite := `<html><body><table>
<tr><td>1.</td><td><a class="result-link" href="https://example.com/a">Title A</a></td></tr>
<tr><td></td><td class="result-snippet">Snippet for A</td></tr>
<tr><td>2.</td><td><a class="result-link" href="/l/?uddg=https%3A%2F%2Fexample.com%2Fb">Title B</a></td></tr>
<tr><td></td><td class="result-snippet">Snippet for B</td></tr>
</table></body></html>`

	got := parseDuckDuckGoLite(lite)
	for _, want := range []string{
		"Title A", "Title B",
		"https://example.com/a",
		"https://example.com/b",
		"Snippet for A", "Snippet for B",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\ngot: %s", want, got)
		}
	}
}

func TestParseDuckDuckGoLite_NoResults(t *testing.T) {
	if got := parseDuckDuckGoLite("<html><body>empty</body></html>"); got != "no results found" {
		t.Errorf("expected no-results sentinel, got %q", got)
	}
}

func TestHtmlToMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		contains []string
		excludes []string
	}{
		{
			name:     "extracts paragraph text",
			html:     "<html><body><p>Hello world</p></body></html>",
			contains: []string{"Hello world"},
		},
		{
			name:     "strips script tags",
			html:     "<html><body><script>alert(1)</script><p>Safe</p></body></html>",
			contains: []string{"Safe"},
			excludes: []string{"alert"},
		},
		{
			name:     "strips style tags",
			html:     "<html><body><style>.x{color:red}</style><p>Visible</p></body></html>",
			contains: []string{"Visible"},
			excludes: []string{"color:red"},
		},
		{
			name:     "prefers main tag when present",
			html:     "<html><body><nav>Nav stuff</nav><main><p>Content</p></main></body></html>",
			contains: []string{"Content"},
			excludes: []string{"Nav stuff"},
		},
		{
			name:     "preserves link as markdown",
			html:     `<html><body><p>See <a href="https://example.com/x">the docs</a>.</p></body></html>`,
			contains: []string{"[the docs](https://example.com/x)"},
		},
		{
			name:     "preserves list structure",
			html:     "<html><body><ul><li>one</li><li>two</li></ul></body></html>",
			contains: []string{"- one", "- two"},
		},
		{
			name:     "preserves heading",
			html:     "<html><body><main><h2>Title</h2><p>body</p></main></body></html>",
			contains: []string{"## Title", "body"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := htmlToMarkdown(tt.html)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\ngot: %s", want, got)
				}
			}
			for _, bad := range tt.excludes {
				if strings.Contains(got, bad) {
					t.Errorf("output should not contain %q\ngot: %s", bad, got)
				}
			}
		})
	}
}

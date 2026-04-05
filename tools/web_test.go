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

func TestExtractText(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		contains string
		excludes string
	}{
		{
			name:     "extracts paragraph text",
			html:     "<html><body><p>Hello world</p></body></html>",
			contains: "Hello world",
		},
		{
			name:     "strips script tags",
			html:     "<html><body><script>alert(1)</script><p>Safe</p></body></html>",
			contains: "Safe",
			excludes: "alert",
		},
		{
			name:     "strips style tags",
			html:     "<html><body><style>.x{color:red}</style><p>Visible</p></body></html>",
			contains: "Visible",
			excludes: "color:red",
		},
		{
			name:     "prefers main tag when present",
			html:     "<html><body><nav>Nav stuff</nav><main><p>Content</p></main></body></html>",
			contains: "Content",
			excludes: "Nav stuff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractText(tt.html)
			if !strings.Contains(got, tt.contains) {
				t.Errorf("output missing %q\ngot: %s", tt.contains, got)
			}
			if tt.excludes != "" && strings.Contains(got, tt.excludes) {
				t.Errorf("output should not contain %q\ngot: %s", tt.excludes, got)
			}
		})
	}
}

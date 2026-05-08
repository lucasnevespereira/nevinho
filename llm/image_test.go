package llm

import "testing"

func TestModelSupportsVision(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"claude-haiku-4-5-20251001", true},
		{"claude-sonnet-4-6", true},
		{"claude-opus-4-7", true},
		{"gpt-4o", true},
		{"gpt-4o-mini", true},
		{"gpt-4-turbo", true},
		{"gpt-3.5-turbo", false},
		{"o3-mini", false},
		{"llava:7b", true},
		{"llama3.2-vision", true},
		{"qwen2-vl:7b", true},
		{"bakllava", true},
		{"moondream", true},
		{"llama3", false},
		{"codellama", false},
		{"", false},
	}
	for _, c := range cases {
		if got := ModelSupportsVision(c.name); got != c.want {
			t.Errorf("ModelSupportsVision(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

package config

import "testing"

func TestModelLabelDistinguishesOpenAISources(t *testing.T) {
	if got := ModelLabel("chatgpt:gpt-5"); got != "gpt-5 [OpenAI · ChatGPT plan]" {
		t.Fatal(got)
	}
	if got := ModelLabel("gpt-5"); got != "gpt-5 [OpenAI API key]" {
		t.Fatal(got)
	}
	if got := ModelLabel("openrouter:anthropic/claude-opus"); got != "anthropic/claude-opus [OpenRouter]" {
		t.Fatal(got)
	}
}

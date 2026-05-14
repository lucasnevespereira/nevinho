package llm

import (
	"strings"
	"testing"

	"github.com/lucasnevespereira/nevinho/config"
)

func TestIsKnownModel(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"claude-haiku-4-5", true},
		{"claude-sonnet-4-6", true},
		{"claude-haiku-4-5-20251001", true}, // dated variant
		{"claude-sonnet-4-6-20250514", true},
		{"gpt-4o-mini", true},
		{"gpt-4o", true},
		{"o4-mini", true},
		{"gemini-2.5-flash", true},
		{"gemini-2.5-pro", true},
		{"gpt-5.4-mini", false},
		{"claude-haiku-9000", false},
		{"gemini-3.0-ultra", false},
		{"llama3", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsKnownModel(c.name); got != c.want {
			t.Errorf("IsKnownModel(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestResolveRejectsBogusGemini(t *testing.T) {
	_, err := Resolve("gemini-3.0-ultra", config.ProviderConfig{GeminiKey: "k"})
	if err == nil {
		t.Fatal("expected error for unknown gemini model, got nil")
	}
	if !strings.Contains(err.Error(), "unknown Gemini model") {
		t.Errorf("error mismatch: %v", err)
	}
}

func TestResolveGeminiRequiresKey(t *testing.T) {
	_, err := Resolve("gemini-2.5-flash", config.ProviderConfig{})
	if err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Errorf("expected GEMINI_API_KEY error, got: %v", err)
	}
}

func TestResolveAllowsKnownGemini(t *testing.T) {
	p, err := Resolve("gemini-2.5-flash", config.ProviderConfig{GeminiKey: "k"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if p == nil {
		t.Fatal("expected provider, got nil")
	}
}

func TestResolveRejectsBogusOpenAI(t *testing.T) {
	_, err := Resolve("gpt-5.4-mini", config.ProviderConfig{OpenAIKey: "k"})
	if err == nil {
		t.Fatal("expected error for unknown gpt model, got nil")
	}
	if !strings.Contains(err.Error(), "unknown OpenAI model") {
		t.Errorf("error mismatch: %v", err)
	}
}

func TestResolveRejectsBogusAnthropic(t *testing.T) {
	_, err := Resolve("claude-imaginary", config.ProviderConfig{AnthropicKey: "k"})
	if err == nil {
		t.Fatal("expected error for unknown claude model, got nil")
	}
	if !strings.Contains(err.Error(), "unknown Anthropic model") {
		t.Errorf("error mismatch: %v", err)
	}
}

func TestResolveAllowsKnownOpenAI(t *testing.T) {
	p, err := Resolve("gpt-4o-mini", config.ProviderConfig{OpenAIKey: "k"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if p == nil {
		t.Fatal("expected provider, got nil")
	}
}

func TestResolveAllowsAnyOllamaModel(t *testing.T) {
	p, err := Resolve("custom-local-model", config.ProviderConfig{OllamaURL: "http://localhost:11434"})
	if err != nil {
		t.Fatalf("expected ollama to accept any name, got: %v", err)
	}
	if p == nil {
		t.Fatal("expected provider, got nil")
	}
}

func TestResolveGroqRoutesAnyModelAfterPrefix(t *testing.T) {
	p, err := Resolve("groq:llama-3.3-70b-versatile", config.ProviderConfig{GroqKey: "k"})
	if err != nil {
		t.Fatalf("expected groq route, got: %v", err)
	}
	if p == nil {
		t.Fatal("expected provider, got nil")
	}

	// Any name after the prefix should resolve. The Groq catalog is large.
	if _, err := Resolve("groq:something-new-tomorrow", config.ProviderConfig{GroqKey: "k"}); err != nil {
		t.Errorf("groq prefix should accept any name, got: %v", err)
	}
}

func TestResolveGroqRequiresKey(t *testing.T) {
	_, err := Resolve("groq:llama-3.3-70b-versatile", config.ProviderConfig{})
	if err == nil || !strings.Contains(err.Error(), "GROQ_API_KEY") {
		t.Errorf("expected GROQ_API_KEY error, got: %v", err)
	}
}

func TestResolveOpenRouterRoutesAnyModelAfterPrefix(t *testing.T) {
	p, err := Resolve("openrouter:meta-llama/llama-3.3-70b-instruct:free", config.ProviderConfig{OpenRouterKey: "k"})
	if err != nil {
		t.Fatalf("expected openrouter route, got: %v", err)
	}
	if p == nil {
		t.Fatal("expected provider, got nil")
	}
}

func TestResolveOpenRouterRequiresKey(t *testing.T) {
	_, err := Resolve("openrouter:meta-llama/llama-3.3-70b-instruct:free", config.ProviderConfig{})
	if err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Errorf("expected OPENROUTER_API_KEY error, got: %v", err)
	}
}

func TestIsFreeModel(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"groq:llama-3.3-70b-versatile", true},
		{"openrouter:meta-llama/llama-3.3-70b-instruct:free", true},
		{"openrouter:anthropic/claude-3.5-sonnet", false},
		{"claude-haiku-4-5", false},
		{"gpt-4o-mini", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsFreeModel(c.name); got != c.want {
			t.Errorf("IsFreeModel(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

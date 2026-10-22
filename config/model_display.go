package config

import "strings"

// ModelSource names the account or provider a model request will use.
// It is display text; the full model ID remains the selector value.
func ModelSource(name string) string {
	switch {
	case strings.HasPrefix(name, "chatgpt:"):
		return "OpenAI · ChatGPT plan"
	case strings.HasPrefix(name, "openrouter:"):
		return "OpenRouter"
	case strings.HasPrefix(name, "groq:"):
		return "Groq"
	case strings.HasPrefix(name, "claude-"):
		return "Anthropic API key"
	case strings.HasPrefix(name, "gpt-"), strings.HasPrefix(name, "o1-"), strings.HasPrefix(name, "o3-"), strings.HasPrefix(name, "o4-"):
		return "OpenAI API key"
	case strings.HasPrefix(name, "gemini-"):
		return "Gemini API key"
	default:
		return "Ollama"
	}
}

// ModelLabel keeps the provider visible while leaving the underlying model
// identifier unchanged for /model commands and persisted preferences.
func ModelLabel(name string) string {
	display := name
	for _, prefix := range []string{"chatgpt:", "openrouter:", "groq:"} {
		display = strings.TrimPrefix(display, prefix)
	}
	return display + " [" + ModelSource(name) + "]"
}

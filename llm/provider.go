package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lucasnevespereira/nevinho/config"
)

type Provider interface {
	Complete(ctx context.Context, req *Request) (*Response, error)
	// FormatUserMessage emits a user message carrying optional inline images.
	// images may be nil or empty for plain text turns.
	FormatUserMessage(text string, images []Image) json.RawMessage
	FormatToolResults(results []ToolResult) []json.RawMessage
	// ReplaceToolResult swaps the stored output of a prior tool_result for the
	// given tool use id. Used after deferred approval so the LLM sees the
	// actual executed output instead of the stale NEEDS_APPROVAL placeholder.
	ReplaceToolResult(history []json.RawMessage, toolUseID, newOutput string) []json.RawMessage
	Model() string
}

type Request struct {
	SystemPrompt string
	Messages     []json.RawMessage
	Tools        []ToolDef
	MaxTokens    int
}

type Response struct {
	Text             string
	ToolCalls        []ToolCall
	Usage            Usage
	AssistantMessage json.RawMessage
	StopReason       StopReason
}

// StopReason is the normalized reason a provider stopped generating. Each
// provider maps its own native reason onto these so the agent loop can read
// the model's intent instead of guessing it from the shape of the output.
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"   // finished a complete reply
	StopToolUse   StopReason = "tool_use"   // wants tool results before continuing
	StopMaxTokens StopReason = "max_tokens" // hit the output token cap, truncated
	StopOther     StopReason = "other"      // safety filter, unknown, or unset
)

type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type ToolResult struct {
	ID      string
	Output  string
	IsError bool
}

type Usage struct {
	In         int
	Out        int
	CacheRead  int
	CacheWrite int
}

type ToolDef struct {
	Name        string
	Description string
	Schema      string
}

// isDatedVariant reports whether name looks like a friendly model with a
// trailing date suffix (e.g. claude-haiku-4-5-20251001). Such names route
// to the same provider as their friendly counterpart.
func isDatedVariant(name string) bool {
	if len(name) < 9 {
		return false
	}
	tail := name[len(name)-8:]
	for _, c := range tail {
		if c < '0' || c > '9' {
			return false
		}
	}
	return name[len(name)-9] == '-'
}

// IsKnownModel reports whether name appears in any provider's KnownModels
// list, or is a dated variant of one of those names.
func IsKnownModel(name string) bool {
	for _, models := range config.KnownModels {
		for _, m := range models {
			if m == name {
				return true
			}
			if isDatedVariant(name) && strings.HasPrefix(name, m+"-") {
				return true
			}
		}
	}
	return false
}

// Resolve maps a model name to the correct provider using the given config.
// For Anthropic and OpenAI, the model must be in KnownModels or Resolve errors.
// Groq and OpenRouter accept any model name after the prefix since their
// catalogs are large and change frequently. Ollama accepts any local name.
func Resolve(name string, pc config.ProviderConfig) (Provider, error) {
	switch {
	case strings.HasPrefix(name, "groq:"):
		if pc.GroqKey == "" {
			return nil, fmt.Errorf("GROQ_API_KEY not configured")
		}
		return NewOpenAI(pc.GroqKey, "https://api.groq.com/openai", strings.TrimPrefix(name, "groq:")), nil
	case strings.HasPrefix(name, "openrouter:"):
		if pc.OpenRouterKey == "" {
			return nil, fmt.Errorf("OPENROUTER_API_KEY not configured")
		}
		return NewOpenAI(pc.OpenRouterKey, "https://openrouter.ai/api", strings.TrimPrefix(name, "openrouter:")), nil
	case strings.HasPrefix(name, "gpt-") || strings.HasPrefix(name, "o1-") || strings.HasPrefix(name, "o3-") || strings.HasPrefix(name, "o4-"):
		if pc.OpenAIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY not configured")
		}
		if !IsKnownModel(name) {
			return nil, fmt.Errorf("unknown OpenAI model %q (not in catalog)", name)
		}
		return NewOpenAI(pc.OpenAIKey, "", name), nil
	case strings.HasPrefix(name, "claude-"):
		if pc.AnthropicKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY not configured")
		}
		if !IsKnownModel(name) {
			return nil, fmt.Errorf("unknown Anthropic model %q (not in catalog)", name)
		}
		return NewAnthropic(pc.AnthropicKey, "", name), nil
	case strings.HasPrefix(name, "gemini-"):
		if pc.GeminiKey == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY not configured")
		}
		if !IsKnownModel(name) {
			return nil, fmt.Errorf("unknown Gemini model %q (not in catalog)", name)
		}
		return NewGemini(pc.GeminiKey, "", name), nil
	default:
		if pc.OllamaURL != "" {
			return NewOpenAI("", pc.OllamaURL, name), nil
		}
		if pc.OpenAIKey != "" {
			return nil, fmt.Errorf("unknown model %q (no Ollama URL, model does not look like a known cloud model)", name)
		}
		return nil, fmt.Errorf("unknown model: %s", name)
	}
}

// IsFreeModel reports whether running the given model is free at time of
// writing. Conservative: only marks names known to map to free quotas.
// Used by display layers to tag dropdown entries.
func IsFreeModel(name string) bool {
	if strings.HasPrefix(name, "groq:") {
		return true
	}
	if strings.HasPrefix(name, "openrouter:") && strings.HasSuffix(name, ":free") {
		return true
	}
	return false
}

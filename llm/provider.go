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
}

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

// KnownModels is the canonical list of friendly model names per provider
// that nevinho recognizes. Names outside this list resolve only when the
// provider is Ollama (any local model name allowed) so a typo or stale
// saved name fails loudly instead of producing 400s at request time.
//
// Dated variants like claude-haiku-4-5-20251001 are accepted by the upstream
// API and used internally as defaults, but are not listed here to keep the
// user facing menu compact.
var KnownModels = map[string][]string{
	"anthropic": {
		"claude-haiku-4-5",
		"claude-sonnet-4-6",
		"claude-sonnet-4-7",
		"claude-opus-4-6",
		"claude-opus-4-7",
	},
	"openai": {
		"gpt-5-nano",
		"gpt-5-mini",
		"gpt-5",
		"gpt-4o-mini",
		"gpt-4o",
		"gpt-4-turbo",
		"o1-mini",
		"o3-mini",
		"o4-mini",
	},
	// Groq's free tier covers all listed models within rate limits
	// (~14k req/day at writing). Names are passed through to Groq's
	// OpenAI compatible endpoint with the "groq:" prefix stripped.
	"groq": {
		"groq:llama-3.3-70b-versatile",
		"groq:llama-3.1-8b-instant",
		"groq:mixtral-8x7b-32768",
		"groq:gemma2-9b-it",
		"groq:llama-3.2-90b-vision-preview",
	},
	// OpenRouter is a router across many providers. Routes ending in
	// ":free" run on free tier quotas (~200 req/day at writing). Routes
	// without ":free" are paid per token. Curated list mixes both. Users
	// can also pass any other openrouter:<route> name.
	"openrouter": {
		"openrouter:nvidia/nemotron-3-super-120b-a12b:free",
		"openrouter:openai/gpt-oss-120b:free",
		"openrouter:openai/gpt-oss-20b:free",
		"openrouter:inclusionai/ring-2.6-1t:free",
		"openrouter:z-ai/glm-4.5-air:free",
		"openrouter:minimax/minimax-m2.5:free",
		"openrouter:google/gemma-4-31b-it:free",
		"openrouter:moonshotai/kimi-k2",
	},
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
	for _, models := range KnownModels {
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
		return NewOpenAI(pc.GroqKey, "https://api.groq.com/openai/v1", strings.TrimPrefix(name, "groq:")), nil
	case strings.HasPrefix(name, "openrouter:"):
		if pc.OpenRouterKey == "" {
			return nil, fmt.Errorf("OPENROUTER_API_KEY not configured")
		}
		return NewOpenAI(pc.OpenRouterKey, "https://openrouter.ai/api/v1", strings.TrimPrefix(name, "openrouter:")), nil
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

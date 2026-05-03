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
	FormatUserMessage(text string) json.RawMessage
	FormatToolResults(results []ToolResult) []json.RawMessage
	// ReplaceToolResult swaps the stored output of a prior tool_result for the
	// given tool-use id. Used after deferred approval so the LLM sees the
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

// Resolve maps a model name to the correct provider using the given config.
func Resolve(name string, pc config.ProviderConfig) (Provider, error) {
	if name == "" {
		return nil, fmt.Errorf("model name required")
	}
	if strings.HasPrefix(name, "openrouter:") {
		if pc.OpenRouterKey == "" {
			return nil, fmt.Errorf("OPENROUTER_API_KEY not configured")
		}
		return NewOpenRouter(pc.OpenRouterKey, "", strings.TrimPrefix(name, "openrouter:")), nil
	}
	if strings.HasPrefix(name, "openai:") {
		if pc.OpenAIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY not configured")
		}
		return NewOpenAI(pc.OpenAIKey, "", strings.TrimPrefix(name, "openai:")), nil
	}
	if strings.HasPrefix(name, "anthropic:") {
		if pc.AnthropicKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY not configured")
		}
		return NewAnthropic(pc.AnthropicKey, "", strings.TrimPrefix(name, "anthropic:")), nil
	}
	switch {
	case strings.HasPrefix(name, "gpt-") || strings.HasPrefix(name, "o1-") || strings.HasPrefix(name, "o3-") || strings.HasPrefix(name, "o4-"):
		if pc.OpenAIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY not configured")
		}
		return NewOpenAI(pc.OpenAIKey, "", name), nil
	case strings.HasPrefix(name, "claude-"):
		if pc.AnthropicKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY not configured")
		}
		return NewAnthropic(pc.AnthropicKey, "", name), nil
	case strings.HasPrefix(name, "kimi-") || strings.HasPrefix(name, "moonshot-"):
		if pc.OpenRouterKey == "" {
			return nil, fmt.Errorf("OPENROUTER_API_KEY not configured")
		}
		return NewOpenRouter(pc.OpenRouterKey, "", name), nil
	default:
		if pc.OllamaURL != "" {
			return NewOpenAI("", pc.OllamaURL, name), nil
		}
		return nil, fmt.Errorf("unknown model: %s", name)
	}
}

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
	ID     string
	Output string
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
	default:
		if pc.OllamaURL != "" {
			return NewOpenAI("", pc.OllamaURL, name), nil
		}
		if pc.OpenAIKey != "" {
			return NewOpenAI(pc.OpenAIKey, "", name), nil
		}
		return nil, fmt.Errorf("unknown model: %s", name)
	}
}

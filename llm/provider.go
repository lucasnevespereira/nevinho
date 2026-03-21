package llm

import "encoding/json"

// Provider abstracts LLM API differences (Anthropic, OpenAI, Ollama).
type Provider interface {
	Complete(req *Request) (*Response, error)
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
	In  int
	Out int
}

type ToolDef struct {
	Name        string
	Description string
	Schema      string
}

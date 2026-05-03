package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type OpenRouter struct {
	apiKey  string
	baseURL string
	model   string
}

func NewOpenRouter(apiKey, baseURL, model string) *OpenRouter {
	if baseURL == "" {
		baseURL = "https://openrouter.ai"
	}
	if model == "" {
		model = "moonshotai/kimi-k2"
	}
	return &OpenRouter{apiKey: apiKey, baseURL: baseURL, model: model}
}

func (o *OpenRouter) Model() string { return o.model }

func (o *OpenRouter) Complete(ctx context.Context, req *Request) (*Response, error) {
	sysMsg, _ := json.Marshal(map[string]interface{}{
		"role": "system", "content": req.SystemPrompt,
	})
	messages := append([]json.RawMessage{sysMsg}, ensureSlice(req.Messages)...)

	body := map[string]interface{}{
		"model":    o.model,
		"messages": messages,
		"tools":    o.formatTools(req.Tools),
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}

	data, err := doHTTP(ctx, o.baseURL+"/api/v1/chat/completions", body, map[string]string{
		"Authorization": "Bearer " + o.apiKey,
		"Content-Type":  "application/json",
		"HTTP-Referer":   "https://github.com/lucasnevespereira/nevinho",
		"X-Title":        "nevinho",
	})
	if err != nil {
		return nil, err
	}

	var raw struct {
		Choices []struct {
			Message struct {
				Role      string          `json:"role"`
				Content   *string         `json:"content"`
				ToolCalls []openAIToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if len(raw.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := raw.Choices[0]
	resp := &Response{Usage: Usage{In: raw.Usage.PromptTokens, Out: raw.Usage.CompletionTokens}}
	assistantMsg, _ := json.Marshal(choice.Message)
	resp.AssistantMessage = assistantMsg
	if choice.Message.Content != nil {
		resp.Text = *choice.Message.Content
	}
	for _, tc := range choice.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(tc.Function.Arguments)})
	}
	return resp, nil
}

func (o *OpenRouter) FormatUserMessage(text string) json.RawMessage {
	msg, _ := json.Marshal(map[string]interface{}{"role": "user", "content": text})
	return msg
}

func (o *OpenRouter) ReplaceToolResult(history []json.RawMessage, toolUseID, newOutput string) []json.RawMessage {
	return (&OpenAI{}).ReplaceToolResult(history, toolUseID, newOutput)
}

func (o *OpenRouter) FormatToolResults(results []ToolResult) []json.RawMessage {
	return (&OpenAI{}).FormatToolResults(results)
}

func (o *OpenRouter) formatTools(defs []ToolDef) []map[string]interface{} {
	var out []map[string]interface{}
	for _, d := range defs {
		out = append(out, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        d.Name,
				"description": d.Description,
				"parameters":  json.RawMessage(d.Schema),
			},
		})
	}
	return out
}

var _ = strings.Contains

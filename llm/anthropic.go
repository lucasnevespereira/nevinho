package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Anthropic struct {
	apiKey  string
	baseURL string
	model   string
}

func NewAnthropic(apiKey, baseURL, model string) *Anthropic {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	return &Anthropic{apiKey: apiKey, baseURL: baseURL, model: model}
}

func (a *Anthropic) Model() string { return a.model }

func (a *Anthropic) Complete(req *Request) (*Response, error) {
	body := map[string]interface{}{
		"model":      a.model,
		"max_tokens": req.MaxTokens,
		"system":     req.SystemPrompt,
		"messages":   ensureSlice(req.Messages),
		"tools":      a.formatTools(req.Tools),
	}

	data, err := doHTTP(a.baseURL+"/v1/messages", body, map[string]string{
		"x-api-key":         a.apiKey,
		"anthropic-version": "2023-06-01",
		"content-type":      "application/json",
	})
	if err != nil {
		return nil, err
	}

	var raw struct {
		Content    []json.RawMessage `json:"content"`
		StopReason string            `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	resp := &Response{
		Usage: Usage{In: raw.Usage.InputTokens, Out: raw.Usage.OutputTokens},
	}

	assistantMsg, _ := json.Marshal(map[string]interface{}{
		"role": "assistant", "content": raw.Content,
	})
	resp.AssistantMessage = assistantMsg

	var textParts []string
	for _, block := range raw.Content {
		var b struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		json.Unmarshal(block, &b)
		if b.Type == "text" && b.Text != "" {
			textParts = append(textParts, b.Text)
		}
		if b.Type == "tool_use" {
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID: b.ID, Name: b.Name, Input: b.Input,
			})
		}
	}
	resp.Text = strings.Join(textParts, "\n")

	return resp, nil
}

func (a *Anthropic) FormatUserMessage(text string) json.RawMessage {
	msg, _ := json.Marshal(map[string]interface{}{"role": "user", "content": text})
	return msg
}

func (a *Anthropic) FormatToolResults(results []ToolResult) []json.RawMessage {
	var content []interface{}
	for _, r := range results {
		content = append(content, map[string]interface{}{
			"type": "tool_result", "tool_use_id": r.ID, "content": r.Output,
		})
	}
	msg, _ := json.Marshal(map[string]interface{}{"role": "user", "content": content})
	return []json.RawMessage{msg}
}

func (a *Anthropic) formatTools(defs []ToolDef) []map[string]interface{} {
	var out []map[string]interface{}
	for _, d := range defs {
		out = append(out, map[string]interface{}{
			"name":         d.Name,
			"description":  d.Description,
			"input_schema": json.RawMessage(d.Schema),
		})
	}
	return out
}

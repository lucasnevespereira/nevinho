package llm

import (
	"context"
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

func (a *Anthropic) Complete(ctx context.Context, req *Request) (*Response, error) {
	tools := a.formatTools(req.Tools)
	// Mark last tool with cache_control so the entire prefix (system + tools) is cached
	if len(tools) > 0 {
		tools[len(tools)-1]["cache_control"] = map[string]string{"type": "ephemeral"}
	}

	body := map[string]interface{}{
		"model":      a.model,
		"max_tokens": req.MaxTokens,
		"system": []map[string]interface{}{
			{
				"type":          "text",
				"text":          req.SystemPrompt,
				"cache_control": map[string]string{"type": "ephemeral"},
			},
		},
		"messages": ensureSlice(req.Messages),
		"tools":    tools,
	}

	data, err := doHTTP(ctx, a.baseURL+"/v1/messages", body, map[string]string{
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
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	resp := &Response{
		Usage: Usage{
			In:         raw.Usage.InputTokens,
			Out:        raw.Usage.OutputTokens,
			CacheRead:  raw.Usage.CacheReadInputTokens,
			CacheWrite: raw.Usage.CacheCreationInputTokens,
		},
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

func (a *Anthropic) ReplaceToolResult(history []json.RawMessage, toolUseID, newOutput string) []json.RawMessage {
	for i := len(history) - 1; i >= 0; i-- {
		var msg struct {
			Role    string                   `json:"role"`
			Content []map[string]interface{} `json:"content"`
		}
		if err := json.Unmarshal(history[i], &msg); err != nil {
			continue
		}
		if msg.Role != "user" {
			continue
		}
		changed := false
		for j, block := range msg.Content {
			if block["type"] != "tool_result" {
				continue
			}
			if id, _ := block["tool_use_id"].(string); id == toolUseID {
				msg.Content[j]["content"] = newOutput
				delete(msg.Content[j], "is_error")
				changed = true
			}
		}
		if changed {
			if rebuilt, err := json.Marshal(msg); err == nil {
				history[i] = rebuilt
			}
			return history
		}
	}
	return history
}

func (a *Anthropic) FormatToolResults(results []ToolResult) []json.RawMessage {
	var content []interface{}
	for _, r := range results {
		entry := map[string]interface{}{
			"type": "tool_result", "tool_use_id": r.ID, "content": r.Output,
		}
		if r.IsError {
			entry["is_error"] = true
		}
		content = append(content, entry)
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

package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type OpenAI struct {
	apiKey  string
	baseURL string
	model   string
}

func NewOpenAI(apiKey, baseURL, model string) *OpenAI {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &OpenAI{apiKey: apiKey, baseURL: baseURL, model: model}
}

func (o *OpenAI) Model() string { return o.model }

func (o *OpenAI) Complete(ctx context.Context, req *Request) (*Response, error) {
	sysMsg, _ := json.Marshal(map[string]interface{}{
		"role": "system", "content": req.SystemPrompt,
	})
	messages := append([]json.RawMessage{sysMsg}, ensureSlice(req.Messages)...)

	body := map[string]interface{}{
		"model":      o.model,
		"max_completion_tokens": req.MaxTokens,
		"messages":   messages,
		"tools":      o.formatTools(req.Tools),
	}

	data, err := doHTTP(ctx, o.baseURL+"/v1/chat/completions", body, map[string]string{
		"Authorization": "Bearer " + o.apiKey,
		"Content-Type":  "application/json",
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
			FinishReason string `json:"finish_reason"`
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
	resp := &Response{
		Usage: Usage{In: raw.Usage.PromptTokens, Out: raw.Usage.CompletionTokens},
	}

	assistantMsg, _ := json.Marshal(choice.Message)
	resp.AssistantMessage = assistantMsg

	if choice.Message.Content != nil {
		resp.Text = *choice.Message.Content
	}

	for _, tc := range choice.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(tc.Function.Arguments),
		})
	}

	return resp, nil
}

func (o *OpenAI) FormatUserMessage(text string) json.RawMessage {
	msg, _ := json.Marshal(map[string]interface{}{"role": "user", "content": text})
	return msg
}

func (o *OpenAI) FormatUserMessageWithImages(text string, images []Image) json.RawMessage {
	if len(images) == 0 {
		return o.FormatUserMessage(text)
	}
	var content []map[string]interface{}
	if text != "" {
		content = append(content, map[string]interface{}{"type": "text", "text": text})
	}
	for _, img := range images {
		dataURL := "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
		content = append(content, map[string]interface{}{
			"type":      "image_url",
			"image_url": map[string]string{"url": dataURL},
		})
	}
	msg, _ := json.Marshal(map[string]interface{}{"role": "user", "content": content})
	return msg
}

func (o *OpenAI) ReplaceToolResult(history []json.RawMessage, toolUseID, newOutput string) []json.RawMessage {
	for i := len(history) - 1; i >= 0; i-- {
		var msg map[string]interface{}
		if err := json.Unmarshal(history[i], &msg); err != nil {
			continue
		}
		if role, _ := msg["role"].(string); role != "tool" {
			continue
		}
		if id, _ := msg["tool_call_id"].(string); id != toolUseID {
			continue
		}
		msg["content"] = newOutput
		if rebuilt, err := json.Marshal(msg); err == nil {
			history[i] = rebuilt
		}
		return history
	}
	return history
}

func (o *OpenAI) FormatToolResults(results []ToolResult) []json.RawMessage {
	var msgs []json.RawMessage
	for _, r := range results {
		msg, _ := json.Marshal(map[string]interface{}{
			"role": "tool", "tool_call_id": r.ID, "content": r.Output,
		})
		msgs = append(msgs, msg)
	}
	return msgs
}

func (o *OpenAI) formatTools(defs []ToolDef) []map[string]interface{} {
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

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

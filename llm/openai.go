package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

type OpenAI struct {
	apiKey             string
	baseURL            string
	model              string
	streamIncludeUsage bool
}

func NewOpenAI(apiKey, baseURL, model string) *OpenAI {
	return newOpenAI(apiKey, baseURL, model, true)
}

func NewOpenAICompatible(apiKey, baseURL, model string) *OpenAI {
	return newOpenAI(apiKey, baseURL, model, false)
}

func newOpenAI(apiKey, baseURL, model string, streamIncludeUsage bool) *OpenAI {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &OpenAI{
		apiKey:             apiKey,
		baseURL:            baseURL,
		model:              model,
		streamIncludeUsage: streamIncludeUsage,
	}
}

func (o *OpenAI) Model() string { return o.model }

func (o *OpenAI) Complete(ctx context.Context, req *Request) (*Response, error) {
	sysMsg, _ := json.Marshal(map[string]interface{}{
		"role": "system", "content": req.SystemPrompt,
	})
	messages := append([]json.RawMessage{sysMsg}, ensureSlice(req.Messages)...)

	body := map[string]interface{}{
		"model":                 o.model,
		"max_completion_tokens": req.MaxTokens,
		"messages":              messages,
		"tools":                 o.formatTools(req.Tools),
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
				Role      string           `json:"role"`
				Content   *string          `json:"content"`
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
		Usage:      Usage{In: raw.Usage.PromptTokens, Out: raw.Usage.CompletionTokens},
		StopReason: openAIStopReason(choice.FinishReason),
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

func (o *OpenAI) StreamComplete(ctx context.Context, req *Request, cb StreamCallback) (*Response, error) {
	sysMsg, _ := json.Marshal(map[string]interface{}{
		"role": "system", "content": req.SystemPrompt,
	})
	messages := append([]json.RawMessage{sysMsg}, ensureSlice(req.Messages)...)

	body := map[string]interface{}{
		"model":                 o.model,
		"max_completion_tokens": req.MaxTokens,
		"messages":              messages,
		"tools":                 o.formatTools(req.Tools),
		"stream":                true,
	}
	if o.streamIncludeUsage {
		body["stream_options"] = map[string]bool{"include_usage": true}
	}

	resp := &Response{}
	var text strings.Builder
	var finish string
	toolParts := map[int]*openAIToolCall{}
	err := doSSE(ctx, o.baseURL+"/v1/chat/completions", body, map[string]string{
		"Authorization": "Bearer " + o.apiKey,
		"Content-Type":  "application/json",
	}, func(data []byte) error {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Role      string `json:"role"`
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(data, &chunk); err != nil {
			return fmt.Errorf("parse stream chunk: %w", err)
		}
		if chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 {
			resp.Usage = Usage{In: chunk.Usage.PromptTokens, Out: chunk.Usage.CompletionTokens}
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				text.WriteString(ch.Delta.Content)
				if cb != nil {
					cb(ch.Delta.Content)
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				part := toolParts[tc.Index]
				if part == nil {
					part = &openAIToolCall{}
					toolParts[tc.Index] = part
				}
				if tc.ID != "" {
					part.ID = tc.ID
				}
				if tc.Type != "" {
					part.Type = tc.Type
				}
				if tc.Function.Name != "" {
					part.Function.Name += tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					part.Function.Arguments += tc.Function.Arguments
				}
			}
			if ch.FinishReason != "" {
				finish = ch.FinishReason
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	resp.Text = text.String()
	resp.StopReason = openAIStopReason(finish)
	msg := struct {
		Role      string           `json:"role"`
		Content   *string          `json:"content"`
		ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
	}{Role: "assistant"}
	if resp.Text != "" {
		msg.Content = &resp.Text
	}
	for i := 0; i < len(toolParts); i++ {
		if tc := toolParts[i]; tc != nil {
			msg.ToolCalls = append(msg.ToolCalls, *tc)
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(tc.Function.Arguments)})
		}
	}
	assistantMsg, _ := json.Marshal(msg)
	resp.AssistantMessage = assistantMsg
	return resp, nil
}

// openAIStopReason maps the OpenAI-style finish_reason onto the normalized
// set. Groq, OpenRouter, and Ollama all speak this same dialect.
func openAIStopReason(s string) StopReason {
	switch s {
	case "tool_calls", "function_call":
		return StopToolUse
	case "length":
		return StopMaxTokens
	case "stop":
		return StopEndTurn
	default:
		return StopOther
	}
}

func (o *OpenAI) FormatUserMessage(text string, images []Image) json.RawMessage {
	if len(images) == 0 {
		msg, _ := json.Marshal(map[string]interface{}{"role": "user", "content": text})
		return msg
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

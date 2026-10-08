package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// OpenAIResponses talks to OpenAI's Responses API. It is used for the
// reasoning models (gpt-5, gpt-6, the o series). On this API the model's
// reasoning comes back as items that can be sent again with the tool
// results, so it does not start planning from scratch on every tool call.
// gpt-6-astra and gpt-6.1-sol only support function calling here.
//
// Requests are stateless (store is false). Each one carries the whole
// conversation, like every other provider in this package.
type OpenAIResponses struct {
	apiKey  string
	baseURL string
	model   string
}

func NewOpenAIResponses(apiKey, baseURL, model string) *OpenAIResponses {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &OpenAIResponses{apiKey: apiKey, baseURL: baseURL, model: model}
}

func (o *OpenAIResponses) Model() string { return o.model }

// usesResponsesAPI reports whether an OpenAI model should go through the
// Responses API. Older chat models stay on Chat Completions.
func usesResponsesAPI(model string) bool {
	for _, family := range []string{"gpt-5", "gpt-6", "o1", "o3", "o4"} {
		if strings.HasPrefix(model, family) {
			return true
		}
	}
	return false
}

func (o *OpenAIResponses) body(req *Request) map[string]interface{} {
	body := map[string]interface{}{
		"model":        o.model,
		"instructions": req.SystemPrompt,
		"input":        responsesEncode(req.Messages),
		// Reasoning is billed against this cap, so leave it room.
		"max_output_tokens": req.MaxTokens + thinkingRoom,
		"store":             false,
		// Ask for reasoning in a form that can be sent back, since
		// nothing is stored on OpenAI's side.
		"include": []string{"reasoning.encrypted_content"},
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]interface{}, 0, len(req.Tools))
		for _, d := range req.Tools {
			tools = append(tools, map[string]interface{}{
				"type":        "function",
				"name":        d.Name,
				"description": d.Description,
				"parameters":  json.RawMessage(d.Schema),
				// Strict mode is the default here and demands schemas
				// that list every property as required.
				"strict": false,
			})
		}
		body["tools"] = tools
	}
	return body
}

func (o *OpenAIResponses) headers() map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + o.apiKey,
		"Content-Type":  "application/json",
	}
}

func (o *OpenAIResponses) Complete(ctx context.Context, req *Request) (*Response, error) {
	data, err := doHTTP(ctx, o.baseURL+"/v1/responses", o.body(req), o.headers())
	if err != nil {
		return nil, err
	}
	return parseResponsesResult(data)
}

func (o *OpenAIResponses) StreamComplete(ctx context.Context, req *Request, cb StreamCallback) (*Response, error) {
	body := o.body(req)
	body["stream"] = true

	// Only two things are read from the stream: text deltas for the live
	// view, and the closing event, which carries the complete response in
	// the same shape the non-streaming call returns.
	var final json.RawMessage
	err := doSSE(ctx, o.baseURL+"/v1/responses", body, o.headers(), func(data []byte) error {
		var ev struct {
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			Message  string          `json:"message"`
			Response json.RawMessage `json:"response"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return fmt.Errorf("parse stream chunk: %w", err)
		}
		switch ev.Type {
		case "response.output_text.delta":
			if cb != nil && ev.Delta != "" {
				cb(ev.Delta)
			}
		case "response.completed", "response.incomplete", "response.failed":
			final = ev.Response
		case "error":
			return fmt.Errorf("API stream error: %s", data)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if final == nil {
		return nil, fmt.Errorf("API stream ended before the reply was complete")
	}
	return parseResponsesResult(final)
}

// parseResponsesResult reads a Response object, from either a plain call
// or the closing event of a stream.
func parseResponsesResult(data []byte) (*Response, error) {
	var raw struct {
		Status            string `json:"status"`
		IncompleteDetails struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Output []json.RawMessage `json:"output"`
		Usage  struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if raw.Status == "failed" {
		msg := "no error message"
		if raw.Error != nil {
			msg = raw.Error.Message
		}
		return nil, fmt.Errorf("API response failed: %s", msg)
	}

	cached := raw.Usage.InputTokensDetails.CachedTokens
	resp := &Response{Usage: Usage{
		// input_tokens includes the cached part. Split it out so cost
		// prices it at the cached rate.
		In:        raw.Usage.InputTokens - cached,
		CacheRead: cached,
		Out:       raw.Usage.OutputTokens,
	}}

	var texts []string
	for _, item := range raw.Output {
		var it struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		}
		json.Unmarshal(item, &it)
		switch it.Type {
		case "message":
			for _, c := range it.Content {
				if c.Text != "" {
					texts = append(texts, c.Text)
				}
				if c.Refusal != "" {
					texts = append(texts, c.Refusal)
				}
			}
		case "function_call":
			args := it.Arguments
			if args == "" {
				args = "{}"
			}
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: it.CallID, Name: it.Name, Input: json.RawMessage(args)})
		}
	}
	resp.Text = strings.Join(texts, "\n")

	switch {
	case len(resp.ToolCalls) > 0:
		resp.StopReason = StopToolUse
	case raw.Status == "incomplete" && raw.IncompleteDetails.Reason == "max_output_tokens":
		resp.StopReason = StopMaxTokens
	case raw.Status == "completed":
		resp.StopReason = StopEndTurn
	default:
		resp.StopReason = StopOther
	}

	resp.Assistant = Message{Role: RoleAssistant, Text: resp.Text, ToolCalls: resp.ToolCalls}
	if len(raw.Output) > 0 {
		// The output items go back as they came. They include the
		// encrypted reasoning that ties each tool call to its plan.
		resp.Assistant.Wire, _ = json.Marshal(raw.Output)
	}
	return resp, nil
}

// responsesEncode turns history into Responses API input items. The API
// takes a flat list: messages, function calls and their outputs are all
// items side by side, not nested inside turns.
func responsesEncode(msgs []Message) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(msgs))
	add := func(item map[string]interface{}) {
		raw, _ := json.Marshal(item)
		out = append(out, raw)
	}
	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			add(responsesUserMessage(m.Text, m.Images))
		case RoleTool:
			for _, r := range m.ToolResults {
				add(map[string]interface{}{"type": "function_call_output", "call_id": r.ID, "output": r.Output})
			}
		case RoleAssistant:
			var items []json.RawMessage
			if m.Wire != nil && json.Unmarshal(m.Wire, &items) == nil {
				out = append(out, items...)
				continue
			}
			// No stored items (the turn was trimmed down to its text and
			// tool calls). Rebuild the visible parts.
			if m.Text != "" {
				add(map[string]interface{}{"role": "assistant", "content": m.Text})
			}
			for _, c := range m.ToolCalls {
				add(map[string]interface{}{
					"type": "function_call", "call_id": c.ID, "name": c.Name, "arguments": string(c.Input),
				})
			}
		}
	}
	return out
}

func responsesUserMessage(text string, images []Image) map[string]interface{} {
	if len(images) == 0 {
		return map[string]interface{}{"role": "user", "content": text}
	}
	var content []map[string]interface{}
	if text != "" {
		content = append(content, map[string]interface{}{"type": "input_text", "text": text})
	}
	for _, img := range images {
		content = append(content, map[string]interface{}{
			"type":      "input_image",
			"image_url": "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data),
		})
	}
	return map[string]interface{}{"role": "user", "content": content}
}

package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
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
	apiKey       string
	baseURL      string
	model        string
	subscription bool
	tokenSource  func(context.Context) (string, error)
}

func NewOpenAIResponses(apiKey, baseURL, model string) *OpenAIResponses {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &OpenAIResponses{apiKey: apiKey, baseURL: baseURL, model: model}
}

// NewChatGPTResponses uses an explicitly authorized ChatGPT plan. The
// subscription endpoint requires streamed, stateless requests and namespaced
// functions, so it cannot use the ordinary API-key request shape unchanged.
func NewChatGPTResponses(model string, tokenSource func(context.Context) (string, error)) *OpenAIResponses {
	return &OpenAIResponses{baseURL: "https://api.openai.com", model: model, subscription: true, tokenSource: tokenSource}
}

func (o *OpenAIResponses) Model() string {
	if o.subscription {
		return "chatgpt:" + o.model
	}
	return o.model
}

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
		"store":        false,
		// Ask for reasoning in a form that can be sent back, since
		// nothing is stored on OpenAI's side.
		"include": []string{"reasoning.encrypted_content"},
	}
	if !o.subscription {
		// Reasoning is billed against this cap, so leave it room.
		body["max_output_tokens"] = req.MaxTokens + thinkingRoom
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
		if o.subscription {
			body["tools"] = []map[string]interface{}{{
				"type": "namespace", "name": "nevinho",
				"description": "Tools available to the Nevinho agent",
				"tools":       tools,
			}}
		} else {
			body["tools"] = tools
		}
	}
	return body
}

func (o *OpenAIResponses) headers() map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + o.apiKey,
		"Content-Type":  "application/json",
	}
}

func (o *OpenAIResponses) requestHeaders(ctx context.Context) (map[string]string, error) {
	if !o.subscription {
		return o.headers(), nil
	}
	token, err := o.tokenSource(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json"}, nil
}

func (o *OpenAIResponses) Complete(ctx context.Context, req *Request) (*Response, error) {
	if o.subscription {
		return o.StreamComplete(ctx, req, nil)
	}
	headers, err := o.requestHeaders(ctx)
	if err != nil {
		return nil, err
	}
	data, err := doHTTP(ctx, o.baseURL+"/v1/responses", o.body(req), headers)
	if err != nil {
		return nil, err
	}
	return parseResponsesResult(data)
}

func (o *OpenAIResponses) StreamComplete(ctx context.Context, req *Request, cb StreamCallback) (*Response, error) {
	body := o.body(req)
	body["stream"] = true
	headers, err := o.requestHeaders(ctx)
	if err != nil {
		return nil, err
	}

	// Text deltas update the live view. Completed output items also matter for
	// ChatGPT plan streams, whose closing event may omit some or all output.
	var final json.RawMessage
	var finalEvent string
	completedItems := make(map[int]json.RawMessage)
	err = doSSE(ctx, o.baseURL+"/v1/responses", body, headers, func(data []byte) error {
		var ev struct {
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			Message  string          `json:"message"`
			Response json.RawMessage `json:"response"`
			Item     json.RawMessage `json:"item"`
			Index    *int            `json:"output_index"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return fmt.Errorf("parse stream chunk: %w", err)
		}
		switch ev.Type {
		case "response.output_item.done":
			if ev.Index != nil && *ev.Index >= 0 && len(ev.Item) > 0 && json.Valid(ev.Item) {
				completedItems[*ev.Index] = ev.Item
			}
		case "response.output_text.delta":
			if cb != nil && ev.Delta != "" {
				cb(ev.Delta)
			}
		case "response.completed", "response.incomplete", "response.failed":
			final = ev.Response
			finalEvent = ev.Type
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
	if o.subscription && finalEvent != "response.completed" {
		return nil, fmt.Errorf("ChatGPT stream ended with %s: %s", finalEvent, final)
	}
	if o.subscription && len(completedItems) > 0 {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(final, &raw); err != nil {
			return nil, fmt.Errorf("parse ChatGPT final response: %w", err)
		}
		var output []json.RawMessage
		if len(raw["output"]) > 0 {
			if err := json.Unmarshal(raw["output"], &output); err != nil {
				return nil, fmt.Errorf("parse ChatGPT final output: %w", err)
			}
		}
		for index := range output {
			if item, ok := completedItems[index]; ok {
				output[index] = item
			}
		}
		indices := make([]int, 0, len(completedItems))
		for index := range completedItems {
			if index >= len(output) {
				indices = append(indices, index)
			}
		}
		sort.Ints(indices)
		for _, index := range indices {
			output = append(output, completedItems[index])
		}
		raw["output"], err = json.Marshal(output)
		if err != nil {
			return nil, err
		}
		final, err = json.Marshal(raw)
		if err != nil {
			return nil, err
		}
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

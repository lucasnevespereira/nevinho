package llm

import (
	"context"
	"encoding/base64"
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

// thinkingRoom is the extra output budget given to models that think on
// every request. Thinking is billed against max_tokens, so without it a
// short cap can be spent before the model writes a word of the reply.
const thinkingRoom = 8192

// maxTokens widens the caller's cap for models that always think, so the
// cap keeps meaning "length of the visible reply".
func (a *Anthropic) maxTokens(limit int) int {
	for _, family := range []string{"fable", "opus-5", "sonnet-5", "haiku-5"} {
		if strings.Contains(a.model, family) {
			return limit + thinkingRoom
		}
	}
	return limit
}

// withThinkingRetry runs call with thinking blocks replayed. The API
// rejects those blocks when anything before them in the conversation has
// changed (a trimmed history, a new system prompt). The documented
// recovery is to resend the same history without them.
func withThinkingRetry(req *Request, call func(*Request) (*Response, error)) (*Response, error) {
	resp, err := call(req)
	if err == nil || !strings.Contains(err.Error(), "API 400") || !strings.Contains(err.Error(), "in `thinking` block") {
		return resp, err
	}
	bare := *req
	bare.Messages = make([]Message, len(req.Messages))
	for i, m := range req.Messages {
		m.Wire = nil
		bare.Messages[i] = m
	}
	resp, err = call(&bare)
	if resp != nil {
		resp.ThinkingRejected = true
	}
	return resp, err
}

// isThinking reports whether a content block type is model reasoning.
func isThinking(blockType string) bool {
	return blockType == "thinking" || blockType == "redacted_thinking"
}

func (a *Anthropic) Complete(ctx context.Context, req *Request) (*Response, error) {
	return withThinkingRetry(req, func(req *Request) (*Response, error) { return a.complete(ctx, req) })
}

func (a *Anthropic) complete(ctx context.Context, req *Request) (*Response, error) {
	tools := a.formatTools(req.Tools)
	// Mark last tool with cache_control so the entire prefix (system + tools) is cached
	if len(tools) > 0 {
		tools[len(tools)-1]["cache_control"] = map[string]string{"type": "ephemeral"}
	}

	body := map[string]interface{}{
		"model":      a.model,
		"max_tokens": a.maxTokens(req.MaxTokens),
		"system": []map[string]interface{}{
			{
				"type":          "text",
				"text":          req.SystemPrompt,
				"cache_control": map[string]string{"type": "ephemeral"},
			},
		},
		"messages": anthropicEncode(req.Messages),
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
		StopReason: anthropicStopReason(raw.StopReason),
	}

	var textParts []string
	var wire []json.RawMessage
	thought := false
	for _, block := range raw.Content {
		var b struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		json.Unmarshal(block, &b)
		thought = thought || isThinking(b.Type)
		// The API rejects an empty text block when it is sent back.
		if b.Type != "text" || b.Text != "" {
			wire = append(wire, block)
		}
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
	resp.Assistant = Message{Role: RoleAssistant, Text: resp.Text, ToolCalls: resp.ToolCalls}
	if thought {
		resp.Assistant.Wire, _ = json.Marshal(wire)
	}

	return resp, nil
}

func (a *Anthropic) StreamComplete(ctx context.Context, req *Request, cb StreamCallback) (*Response, error) {
	return withThinkingRetry(req, func(req *Request) (*Response, error) { return a.streamComplete(ctx, req, cb) })
}

func (a *Anthropic) streamComplete(ctx context.Context, req *Request, cb StreamCallback) (*Response, error) {
	tools := a.formatTools(req.Tools)
	if len(tools) > 0 {
		tools[len(tools)-1]["cache_control"] = map[string]string{"type": "ephemeral"}
	}
	body := map[string]interface{}{
		"model":      a.model,
		"max_tokens": a.maxTokens(req.MaxTokens),
		"system": []map[string]interface{}{{
			"type":          "text",
			"text":          req.SystemPrompt,
			"cache_control": map[string]string{"type": "ephemeral"},
		}},
		"messages": anthropicEncode(req.Messages),
		"tools":    tools,
		"stream":   true,
	}

	resp := &Response{}
	blocks := map[int]map[string]interface{}{}
	var order []int
	err := doSSE(ctx, a.baseURL+"/v1/messages", body, map[string]string{
		"x-api-key":         a.apiKey,
		"anthropic-version": "2023-06-01",
		"content-type":      "application/json",
	}, func(data []byte) error {
		var ev struct {
			Type         string                 `json:"type"`
			Index        int                    `json:"index"`
			ContentBlock map[string]interface{} `json:"content_block"`
			Message      struct {
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					OutputTokens             int `json:"output_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				InputTokens              int `json:"input_tokens"`
				OutputTokens             int `json:"output_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			return fmt.Errorf("parse stream chunk: %w", err)
		}
		applyAnthropicUsage(&resp.Usage, ev.Message.Usage.InputTokens, ev.Message.Usage.OutputTokens,
			ev.Message.Usage.CacheCreationInputTokens, ev.Message.Usage.CacheReadInputTokens)
		applyAnthropicUsage(&resp.Usage, ev.Usage.InputTokens, ev.Usage.OutputTokens,
			ev.Usage.CacheCreationInputTokens, ev.Usage.CacheReadInputTokens)
		switch ev.Type {
		case "content_block_start":
			blocks[ev.Index] = ev.ContentBlock
			order = append(order, ev.Index)
		case "content_block_delta":
			b := blocks[ev.Index]
			if b == nil {
				b = map[string]interface{}{}
				blocks[ev.Index] = b
				order = append(order, ev.Index)
			}
			if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				prev, _ := b["text"].(string)
				b["text"] = prev + ev.Delta.Text
				resp.Text += ev.Delta.Text
				if cb != nil {
					cb(ev.Delta.Text)
				}
			}
			if ev.Delta.Type == "thinking_delta" {
				prev, _ := b["thinking"].(string)
				b["thinking"] = prev + ev.Delta.Thinking
			}
			if ev.Delta.Type == "signature_delta" {
				prev, _ := b["signature"].(string)
				b["signature"] = prev + ev.Delta.Signature
			}
			if ev.Delta.Type == "input_json_delta" && ev.Delta.PartialJSON != "" {
				prev, _ := b["_partial_json"].(string)
				b["_partial_json"] = prev + ev.Delta.PartialJSON
			}
		case "message_delta":
			resp.StopReason = anthropicStopReason(ev.Delta.StopReason)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var wire []map[string]interface{}
	thought := false
	for _, idx := range order {
		b := blocks[idx]
		if b == nil {
			continue
		}
		if partial, _ := b["_partial_json"].(string); partial != "" {
			b["input"] = json.RawMessage(partial)
			delete(b, "_partial_json")
		}
		blockType, _ := b["type"].(string)
		thought = thought || isThinking(blockType)
		// The API rejects an empty text block when it is sent back.
		if blockType != "text" || b["text"] != "" {
			wire = append(wire, b)
		}
		if b["type"] == "tool_use" {
			input, _ := json.Marshal(b["input"])
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: fmt.Sprint(b["id"]), Name: fmt.Sprint(b["name"]), Input: input})
		}
	}
	if resp.StopReason == "" {
		resp.StopReason = StopEndTurn
	}
	resp.Assistant = Message{Role: RoleAssistant, Text: resp.Text, ToolCalls: resp.ToolCalls}
	if thought {
		// A marshal error means a tool input was cut off mid JSON. The
		// turn is then rebuilt from Text and ToolCalls like any other.
		resp.Assistant.Wire, _ = json.Marshal(wire)
	}
	return resp, nil
}

func applyAnthropicUsage(u *Usage, in, out, cacheWrite, cacheRead int) {
	if in != 0 {
		u.In = in
	}
	if out != 0 {
		u.Out = out
	}
	if cacheWrite != 0 {
		u.CacheWrite = cacheWrite
	}
	if cacheRead != 0 {
		u.CacheRead = cacheRead
	}
}

// anthropicStopReason maps Anthropic's stop_reason onto the normalized set.
func anthropicStopReason(s string) StopReason {
	switch s {
	case "tool_use":
		return StopToolUse
	case "max_tokens":
		return StopMaxTokens
	case "end_turn", "stop_sequence":
		return StopEndTurn
	default:
		return StopOther
	}
}

func anthropicUserMessage(text string, images []Image) json.RawMessage {
	if len(images) == 0 {
		msg, _ := json.Marshal(map[string]interface{}{"role": "user", "content": text})
		return msg
	}
	var content []map[string]interface{}
	if text != "" {
		content = append(content, map[string]interface{}{"type": "text", "text": text})
	}
	for _, img := range images {
		content = append(content, map[string]interface{}{
			"type": "image",
			"source": map[string]string{
				"type":       "base64",
				"media_type": img.MediaType,
				"data":       base64.StdEncoding.EncodeToString(img.Data),
			},
		})
	}
	msg, _ := json.Marshal(map[string]interface{}{"role": "user", "content": content})
	return msg
}

func anthropicToolResults(results []ToolResult) json.RawMessage {
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
	return msg
}

// encode turns history into Anthropic's wire shape. Tool results ride in a
// user message, which is what the API expects.
func anthropicEncode(msgs []Message) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			out = append(out, anthropicUserMessage(m.Text, m.Images))
		case RoleTool:
			out = append(out, anthropicToolResults(m.ToolResults))
		case RoleAssistant:
			if m.Wire != nil {
				msg, _ := json.Marshal(map[string]interface{}{"role": "assistant", "content": m.Wire})
				out = append(out, msg)
				continue
			}
			var content []map[string]interface{}
			if m.Text != "" {
				content = append(content, map[string]interface{}{"type": "text", "text": m.Text})
			}
			for _, c := range m.ToolCalls {
				content = append(content, map[string]interface{}{
					"type": "tool_use", "id": c.ID, "name": c.Name, "input": json.RawMessage(c.Input),
				})
			}
			if len(content) == 0 {
				continue // the API rejects an empty assistant turn
			}
			msg, _ := json.Marshal(map[string]interface{}{"role": "assistant", "content": content})
			out = append(out, msg)
		}
	}
	return out
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

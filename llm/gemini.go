package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type Gemini struct {
	apiKey  string
	baseURL string
	model   string
}

func NewGemini(apiKey, baseURL, model string) *Gemini {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	if model == "" {
		model = "gemini-2.5-flash"
	}
	return &Gemini{apiKey: apiKey, baseURL: baseURL, model: model}
}

func (g *Gemini) Model() string { return g.model }

func (g *Gemini) Complete(ctx context.Context, req *Request) (*Response, error) {
	contents := ensureSlice(req.Messages)

	// Gemini doesn't have a separate "system" message in the history.
	// We can use system_instruction in the request body.
	body := map[string]interface{}{
		"contents": contents,
	}

	if req.SystemPrompt != "" {
		body["system_instruction"] = map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": req.SystemPrompt},
			},
		}
	}

	if len(req.Tools) > 0 {
		body["tools"] = []map[string]interface{}{
			{"function_declarations": g.formatTools(req.Tools)},
		}
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", g.baseURL, g.model, g.apiKey)
	data, err := doHTTP(ctx, url, body, map[string]string{
		"Content-Type": "application/json",
	})
	if err != nil {
		return nil, err
	}

	var raw struct {
		Candidates []struct {
			Content struct {
				Role  string            `json:"role"`
				Parts []json.RawMessage `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(raw.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates in response")
	}

	cand := raw.Candidates[0]
	resp := &Response{
		Usage: Usage{
			In:  raw.UsageMetadata.PromptTokenCount,
			Out: raw.UsageMetadata.CandidatesTokenCount,
		},
	}

	assistantMsg, _ := json.Marshal(cand.Content)
	resp.AssistantMessage = assistantMsg

	for _, part := range cand.Content.Parts {
		var p struct {
			Text         string `json:"text"`
			FunctionCall *struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"functionCall"`
		}
		json.Unmarshal(part, &p)
		if p.Text != "" {
			if resp.Text != "" {
				resp.Text += "\n"
			}
			resp.Text += p.Text
		}
		if p.FunctionCall != nil {
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:    p.FunctionCall.Name, // Gemini function calls don't have IDs, using name as ID
				Name:  p.FunctionCall.Name,
				Input: p.FunctionCall.Args,
			})
		}
	}

	resp.StopReason = geminiStopReason(cand.FinishReason, len(resp.ToolCalls) > 0)
	return resp, nil
}

func (g *Gemini) StreamComplete(ctx context.Context, req *Request, cb StreamCallback) (*Response, error) {
	body := geminiBody(req)
	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s", g.baseURL, g.model, g.apiKey)
	resp := &Response{}
	var parts []json.RawMessage
	err := doSSE(ctx, url, body, map[string]string{"Content-Type": "application/json"}, func(data []byte) error {
		var raw struct {
			Candidates []struct {
				Content struct {
					Role  string            `json:"role"`
					Parts []json.RawMessage `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			UsageMetadata struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
			} `json:"usageMetadata"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse stream chunk: %w", err)
		}
		if raw.UsageMetadata.PromptTokenCount != 0 {
			resp.Usage.In = raw.UsageMetadata.PromptTokenCount
		}
		if raw.UsageMetadata.CandidatesTokenCount != 0 {
			resp.Usage.Out = raw.UsageMetadata.CandidatesTokenCount
		}
		if len(raw.Candidates) == 0 {
			return nil
		}
		cand := raw.Candidates[0]
		if cand.FinishReason != "" {
			resp.StopReason = geminiStopReason(cand.FinishReason, len(resp.ToolCalls) > 0)
		}
		for _, part := range cand.Content.Parts {
			parts = append(parts, part)
			var p struct {
				Text         string `json:"text"`
				FunctionCall *struct {
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"functionCall"`
			}
			json.Unmarshal(part, &p)
			if p.Text != "" {
				resp.Text += p.Text
				if cb != nil {
					cb(p.Text)
				}
			}
			if p.FunctionCall != nil {
				resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: p.FunctionCall.Name, Name: p.FunctionCall.Name, Input: p.FunctionCall.Args})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if resp.StopReason == "" {
		resp.StopReason = geminiStopReason("STOP", len(resp.ToolCalls) > 0)
	}
	assistantMsg, _ := json.Marshal(map[string]interface{}{"role": "model", "parts": parts})
	resp.AssistantMessage = assistantMsg
	return resp, nil
}

func geminiBody(req *Request) map[string]interface{} {
	body := map[string]interface{}{"contents": ensureSlice(req.Messages)}
	if req.SystemPrompt != "" {
		body["system_instruction"] = map[string]interface{}{"parts": []map[string]interface{}{{"text": req.SystemPrompt}}}
	}
	if len(req.Tools) > 0 {
		g := &Gemini{}
		body["tools"] = []map[string]interface{}{{"function_declarations": g.formatTools(req.Tools)}}
	}
	return body
}

// geminiStopReason maps Gemini's finishReason onto the normalized set.
// Gemini keeps finishReason as "STOP" even when the turn carries function
// calls, so tool use is detected from the parsed parts, not the reason.
func geminiStopReason(s string, hasToolCalls bool) StopReason {
	if hasToolCalls {
		return StopToolUse
	}
	switch s {
	case "STOP":
		return StopEndTurn
	case "MAX_TOKENS":
		return StopMaxTokens
	default:
		return StopOther
	}
}

func (g *Gemini) FormatUserMessage(text string, images []Image) json.RawMessage {
	var parts []map[string]interface{}
	if text != "" {
		parts = append(parts, map[string]interface{}{"text": text})
	}
	for _, img := range images {
		parts = append(parts, map[string]interface{}{
			"inline_data": map[string]string{
				"mime_type": img.MediaType,
				"data":      base64.StdEncoding.EncodeToString(img.Data),
			},
		})
	}
	msg, _ := json.Marshal(map[string]interface{}{
		"role":  "user",
		"parts": parts,
	})
	return msg
}

func (g *Gemini) ReplaceToolResult(history []json.RawMessage, toolUseID, newOutput string) []json.RawMessage {
	for i := len(history) - 1; i >= 0; i-- {
		var msg struct {
			Role  string                   `json:"role"`
			Parts []map[string]interface{} `json:"parts"`
		}
		if err := json.Unmarshal(history[i], &msg); err != nil {
			continue
		}
		// Tool results live in user-role turns; the functionResponse part
		// check below skips ordinary user messages.
		if msg.Role != "user" {
			continue
		}
		changed := false
		for j, part := range msg.Parts {
			fnRes, ok := part["functionResponse"].(map[string]interface{})
			if !ok {
				continue
			}
			if name, _ := fnRes["name"].(string); name == toolUseID {
				msg.Parts[j]["functionResponse"] = map[string]interface{}{
					"name": name,
					"response": map[string]interface{}{
						"output": newOutput,
					},
				}
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

func (g *Gemini) FormatToolResults(results []ToolResult) []json.RawMessage {
	var parts []interface{}
	for _, r := range results {
		parts = append(parts, map[string]interface{}{
			"functionResponse": map[string]interface{}{
				"name": r.ID,
				"response": map[string]interface{}{
					"output": r.Output,
				},
			},
		})
	}
	// Gemini's REST API only accepts "user" and "model" roles; a function
	// response is a part inside a user-role turn, not its own role. Sending
	// "role": "function" corrupts the history and breaks multi-turn tools.
	msg, _ := json.Marshal(map[string]interface{}{
		"role":  "user",
		"parts": parts,
	})
	return []json.RawMessage{msg}
}

func (g *Gemini) formatTools(defs []ToolDef) []map[string]interface{} {
	var out []map[string]interface{}
	for _, d := range defs {
		var params map[string]interface{}
		json.Unmarshal([]byte(d.Schema), &params)

		out = append(out, map[string]interface{}{
			"name":        d.Name,
			"description": d.Description,
			"parameters":  params,
		})
	}
	return out
}

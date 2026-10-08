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

// geminiChunk is one generateContent response, or one event of a stream.
type geminiChunk struct {
	Candidates []struct {
		Content struct {
			Parts []json.RawMessage `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount        int `json:"promptTokenCount"`
		CachedContentTokenCount int `json:"cachedContentTokenCount"`
		CandidatesTokenCount    int `json:"candidatesTokenCount"`
		ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
}

// applyUsage copies token counts when the chunk carries them. Streams
// repeat usage on several events, so later values replace earlier ones.
func (c geminiChunk) applyUsage(u *Usage) {
	m := c.UsageMetadata
	if m.PromptTokenCount == 0 && m.CandidatesTokenCount == 0 {
		return
	}
	// The prompt count includes the cached part. Thinking is billed as
	// output but reported apart from the visible reply.
	u.In = m.PromptTokenCount - m.CachedContentTokenCount
	u.CacheRead = m.CachedContentTokenCount
	u.Out = m.CandidatesTokenCount + m.ThoughtsTokenCount
}

func (g *Gemini) Complete(ctx context.Context, req *Request) (*Response, error) {
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", g.baseURL, g.model, g.apiKey)
	data, err := doHTTP(ctx, url, geminiBody(req), map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return nil, err
	}
	var chunk geminiChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if len(chunk.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates in response")
	}
	resp, err := geminiResponse(chunk.Candidates[0].Content.Parts, chunk.Candidates[0].FinishReason)
	if err != nil {
		return nil, err
	}
	chunk.applyUsage(&resp.Usage)
	return resp, nil
}

func (g *Gemini) StreamComplete(ctx context.Context, req *Request, cb StreamCallback) (*Response, error) {
	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s", g.baseURL, g.model, g.apiKey)
	var usage Usage
	var parts []json.RawMessage
	finish := ""
	err := doSSE(ctx, url, geminiBody(req), map[string]string{"Content-Type": "application/json"}, func(data []byte) error {
		var chunk geminiChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return fmt.Errorf("parse stream chunk: %w", err)
		}
		chunk.applyUsage(&usage)
		if len(chunk.Candidates) == 0 {
			return nil
		}
		cand := chunk.Candidates[0]
		if cand.FinishReason != "" {
			finish = cand.FinishReason
		}
		for _, part := range cand.Content.Parts {
			parts = append(parts, part)
			var p struct {
				Text    string `json:"text"`
				Thought bool   `json:"thought"`
			}
			json.Unmarshal(part, &p)
			if cb != nil && p.Text != "" && !p.Thought {
				cb(p.Text)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Every complete reply ends with a finish reason. Without one the
	// connection closed early, and what arrived is a fragment.
	if finish == "" {
		return nil, fmt.Errorf("API stream ended before the reply was complete")
	}
	resp, err := geminiResponse(parts, finish)
	if err != nil {
		return nil, err
	}
	resp.Usage = usage
	return resp, nil
}

// geminiResponse builds a Response from the parts of a model turn, whether
// they arrived in one piece or across a stream.
func geminiResponse(parts []json.RawMessage, finishReason string) (*Response, error) {
	resp := &Response{}
	signed := false
	for _, part := range parts {
		var p struct {
			Text             string `json:"text"`
			Thought          bool   `json:"thought"`
			ThoughtSignature string `json:"thoughtSignature"`
			SnakeSignature   string `json:"thought_signature"` // the API accepts both spellings
			FunctionCall     *struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"functionCall"`
		}
		json.Unmarshal(part, &p)
		signed = signed || p.ThoughtSignature != "" || p.SnakeSignature != ""
		if p.Text != "" && !p.Thought {
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
	// Nothing came back and the model did not stop normally (a malformed
	// function call, a safety block). Say why. Passing it on as an empty
	// reply hides the reason.
	if resp.Text == "" && len(resp.ToolCalls) == 0 && finishReason != "STOP" && finishReason != "MAX_TOKENS" {
		return nil, fmt.Errorf("gemini returned no content (finish reason %s)", finishReason)
	}
	resp.StopReason = geminiStopReason(finishReason, len(resp.ToolCalls) > 0)
	resp.Assistant = Message{Role: RoleAssistant, Text: resp.Text, ToolCalls: resp.ToolCalls}
	if signed {
		// Gemini 3 models reject a function call that comes back without
		// the thought signature it was issued with. The signature can sit
		// on any part, so the parts are kept as they came.
		resp.Assistant.Wire, _ = json.Marshal(parts)
	}
	return resp, nil
}

func geminiBody(req *Request) map[string]interface{} {
	body := map[string]interface{}{"contents": geminiEncode(req.Messages)}
	if req.MaxTokens > 0 {
		// Thinking counts against this cap, so leave it room. Without a
		// cap at all, a reply that falls into a loop runs to the model's
		// own limit of tens of thousands of tokens.
		body["generationConfig"] = map[string]interface{}{"maxOutputTokens": req.MaxTokens + thinkingRoom}
	}
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

func geminiUserMessage(text string, images []Image) json.RawMessage {
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

func geminiToolResults(results []ToolResult) json.RawMessage {
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
	return msg
}

// encode turns history into Gemini's wire shape. The assistant is "model"
// here, and tool results ride inside user turns.
func geminiEncode(msgs []Message) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			out = append(out, geminiUserMessage(m.Text, m.Images))
		case RoleTool:
			out = append(out, geminiToolResults(m.ToolResults))
		case RoleAssistant:
			if m.Wire != nil {
				msg, _ := json.Marshal(map[string]interface{}{"role": "model", "parts": m.Wire})
				out = append(out, msg)
				continue
			}
			var parts []map[string]interface{}
			if m.Text != "" {
				parts = append(parts, map[string]interface{}{"text": m.Text})
			}
			for _, c := range m.ToolCalls {
				parts = append(parts, map[string]interface{}{
					"functionCall": map[string]interface{}{
						"name": c.Name,
						"args": json.RawMessage(c.Input),
					},
				})
			}
			if len(parts) == 0 {
				continue // the API rejects an empty model turn
			}
			msg, _ := json.Marshal(map[string]interface{}{"role": "model", "parts": parts})
			out = append(out, msg)
		}
	}
	return out
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

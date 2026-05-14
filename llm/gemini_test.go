package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGemini_Complete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "test-key" {
			t.Errorf("expected API key test-key, got %s", r.URL.Query().Get("key"))
		}
		
		resp := map[string]interface{}{
			"candidates": []map[string]interface{}{
				{
					"content": map[string]interface{}{
						"role": "model",
						"parts": []map[string]interface{}{
							{"text": "Hello from Gemini!"},
						},
					},
					"finishReason": "STOP",
				},
			},
			"usageMetadata": map[string]interface{}{
				"promptTokenCount":     10,
				"candidatesTokenCount": 5,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	g := NewGemini("test-key", server.URL, "gemini-2.5-flash")
	req := &Request{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []json.RawMessage{json.RawMessage(`{"role":"user","parts":[{"text":"Hi"}]}`)},
		MaxTokens:    100,
	}

	resp, err := g.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	if resp.Text != "Hello from Gemini!" {
		t.Errorf("expected text 'Hello from Gemini!', got %q", resp.Text)
	}
	if resp.Usage.In != 10 || resp.Usage.Out != 5 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

func TestGemini_FormatUserMessage(t *testing.T) {
	g := NewGemini("test-key", "", "gemini-2.5-flash")
	msg := g.FormatUserMessage("Hello", []Image{
		{Data: []byte("fake-image"), MediaType: "image/png"},
	})

	var parsed struct {
		Role  string `json:"role"`
		Parts []struct {
			Text       string `json:"text"`
			InlineData *struct {
				MimeType string `json:"mime_type"`
				Data     string `json:"data"`
			} `json:"inline_data"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(msg, &parsed); err != nil {
		t.Fatalf("failed to unmarshal formatted message: %v", err)
	}

	if parsed.Role != "user" {
		t.Errorf("expected role user, got %s", parsed.Role)
	}
	if len(parsed.Parts) != 2 {
		t.Errorf("expected 2 parts, got %d", len(parsed.Parts))
	}
	if parsed.Parts[0].Text != "Hello" {
		t.Errorf("expected text Hello, got %s", parsed.Parts[0].Text)
	}
	if parsed.Parts[1].InlineData == nil || parsed.Parts[1].InlineData.MimeType != "image/png" {
		t.Errorf("missing or invalid inline data: %+v", parsed.Parts[1].InlineData)
	}
	if !strings.HasPrefix(parsed.Parts[1].InlineData.Data, "ZmFrZS1pbWFnZQ==") { // base64 of "fake-image"
		t.Errorf("unexpected data: %s", parsed.Parts[1].InlineData.Data)
	}
}

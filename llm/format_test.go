package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnthropicFormatUserMessage(t *testing.T) {
	a := NewAnthropic("k", "", "claude-haiku-4-5")
	img := Image{MediaType: "image/png", Data: []byte("PNG\x00bytes")}
	raw := a.FormatUserMessage("hello", []Image{img})

	var msg struct {
		Role    string                   `json:"role"`
		Content []map[string]interface{} `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Role != "user" {
		t.Errorf("role = %q, want user", msg.Role)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("got %d blocks, want 2", len(msg.Content))
	}
	if msg.Content[0]["type"] != "text" || msg.Content[0]["text"] != "hello" {
		t.Errorf("first block bad: %+v", msg.Content[0])
	}
	if msg.Content[1]["type"] != "image" {
		t.Errorf("second block type = %v, want image", msg.Content[1]["type"])
	}
	src, ok := msg.Content[1]["source"].(map[string]interface{})
	if !ok {
		t.Fatalf("source not a map: %T", msg.Content[1]["source"])
	}
	if src["type"] != "base64" || src["media_type"] != "image/png" {
		t.Errorf("source bad: %+v", src)
	}
	data, _ := src["data"].(string)
	if data == "" || strings.Contains(data, "\x00") {
		t.Errorf("data not base64 encoded: %q", data)
	}
}

func TestAnthropicFormatUserMessageEmptyText(t *testing.T) {
	a := NewAnthropic("k", "", "claude-haiku-4-5")
	img := Image{MediaType: "image/jpeg", Data: []byte("JPG")}
	raw := a.FormatUserMessage("", []Image{img})

	var msg struct {
		Content []map[string]interface{} `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("got %d blocks, want 1 (image only)", len(msg.Content))
	}
	if msg.Content[0]["type"] != "image" {
		t.Errorf("only block type = %v, want image", msg.Content[0]["type"])
	}
}

func TestAnthropicFormatUserMessageNoImagesIsString(t *testing.T) {
	a := NewAnthropic("k", "", "claude-haiku-4-5")
	raw := a.FormatUserMessage("plain", nil)

	var msg struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var s string
	if err := json.Unmarshal(msg.Content, &s); err != nil {
		t.Fatalf("content not a string when images empty: %s", msg.Content)
	}
	if s != "plain" {
		t.Errorf("content = %q, want plain", s)
	}
}

func TestOpenAIFormatUserMessage(t *testing.T) {
	o := NewOpenAI("k", "", "gpt-4o-mini")
	img := Image{MediaType: "image/png", Data: []byte("PNG\x00bytes")}
	raw := o.FormatUserMessage("look", []Image{img})

	var msg struct {
		Role    string                   `json:"role"`
		Content []map[string]interface{} `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Role != "user" {
		t.Errorf("role = %q, want user", msg.Role)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("got %d blocks, want 2", len(msg.Content))
	}
	if msg.Content[0]["type"] != "text" || msg.Content[0]["text"] != "look" {
		t.Errorf("first block bad: %+v", msg.Content[0])
	}
	if msg.Content[1]["type"] != "image_url" {
		t.Errorf("second block type = %v, want image_url", msg.Content[1]["type"])
	}
	imgURL, ok := msg.Content[1]["image_url"].(map[string]interface{})
	if !ok {
		t.Fatalf("image_url not a map: %T", msg.Content[1]["image_url"])
	}
	url, _ := imgURL["url"].(string)
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Errorf("url = %q, want data: prefix", url)
	}
}

func TestOpenAIFormatUserMessageNoImagesIsString(t *testing.T) {
	o := NewOpenAI("k", "", "gpt-4o-mini")
	raw := o.FormatUserMessage("plain", nil)

	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var s string
	if err := json.Unmarshal(msg.Content, &s); err != nil {
		t.Fatalf("content not a string when images empty: %s", msg.Content)
	}
	if s != "plain" {
		t.Errorf("content = %q, want plain", s)
	}
}

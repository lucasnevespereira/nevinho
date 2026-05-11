package llm

import "strings"

// Image is a single image attachment routed to a vision capable model.
// Data holds raw bytes. Providers encode as needed for their wire format.
type Image struct {
	MediaType string
	Data      []byte
}

// ModelSupportsVision reports whether the given model accepts image input.
// Updated as new vision capable model families ship. When in doubt, prefer
// false so the bot rejects the upload with a clear message rather than
// silently dropping the image and producing a confusing model reply.
func ModelSupportsVision(name string) bool {
	switch {
	case strings.HasPrefix(name, "claude"):
		return true
	case strings.HasPrefix(name, "gpt-5"):
		return true
	case strings.HasPrefix(name, "gpt-4o"):
		return true
	case strings.HasPrefix(name, "gpt-4-turbo"), strings.HasPrefix(name, "gpt-4-vision"):
		return true
	}
	lower := strings.ToLower(name)
	for _, marker := range []string{"llava", "vision", "qwen2-vl", "bakllava", "moondream", "gemini"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

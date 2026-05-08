package llm

import "strings"

// Image is a single image attachment routed to a vision capable model.
// Data holds raw bytes. Providers encode as needed for their wire format.
type Image struct {
	MediaType string
	Data      []byte
}

// ModelSupportsVision reports whether the given model accepts image input.
// Conservative. Returns true only for known good model families.
func ModelSupportsVision(name string) bool {
	switch {
	case strings.HasPrefix(name, "claude"):
		return true
	case strings.HasPrefix(name, "gpt-4o"):
		return true
	case strings.HasPrefix(name, "gpt-4-turbo"), strings.HasPrefix(name, "gpt-4-vision"):
		return true
	}
	lower := strings.ToLower(name)
	for _, marker := range []string{"llava", "vision", "qwen2-vl", "bakllava", "moondream"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

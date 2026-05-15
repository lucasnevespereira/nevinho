package llm

import "strings"

// FriendlyError translates an LLM or network error into a one line message
// the user can act on without seeing the raw provider response. Both the
// Discord and terminal transports render it.
func FriendlyError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "insufficient_quota"),
		strings.Contains(msg, "exceeded your current quota"):
		return "Provider quota exhausted. Check your billing dashboard and add credit."
	case strings.Contains(msg, "API 401"):
		return "API key is invalid or expired. Check your config."
	case strings.Contains(msg, "API 402"),
		strings.Contains(msg, "credit balance"):
		return "Insufficient funds on your API account."
	case strings.Contains(msg, "API 429"):
		return "Rate limited by the API. Wait a moment and try again."
	case strings.Contains(msg, "API 529"), strings.Contains(msg, "API 503"):
		return "API is overloaded. Try again shortly."
	case strings.Contains(msg, "API 500"):
		return "API returned a server error. Try again."
	case strings.Contains(msg, "no such host"),
		strings.Contains(msg, "connection refused"):
		return "Can't reach the API. Check your network."
	default:
		if len(msg) > 200 {
			msg = msg[:199] + "…"
		}
		return "Something went wrong: " + msg
	}
}

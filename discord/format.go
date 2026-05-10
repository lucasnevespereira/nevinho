package discord

import (
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

func keepTyping(s *discordgo.Session, channelID string) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.ChannelTyping(channelID)
			}
		}
	}()
	return func() { close(stop) }
}

var (
	htmlTagRe    = regexp.MustCompile(`<[^>]+>`)
	imgBadgeRe   = regexp.MustCompile(`\[?!\[[^\]]*\]\([^)]+\)\]?(\([^)]+\))?`)
	hrRuleRe     = regexp.MustCompile(`(?m)^\s*(---+|\*\*\*+|___+)\s*$`)
	mdLinkRe     = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	emptyLinesRe = regexp.MustCompile(`\n{3,}`)
)

// cleanForDiscord strips markdown Discord cannot render (HTML, badges,
// horizontal rules) and converts relative links to plain text.
func cleanForDiscord(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = imgBadgeRe.ReplaceAllString(s, "")
	s = hrRuleRe.ReplaceAllString(s, "")
	s = mdLinkRe.ReplaceAllStringFunc(s, func(m string) string {
		parts := mdLinkRe.FindStringSubmatch(m)
		text, url := parts[1], parts[2]
		if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
			return m
		}
		return text
	})
	s = emptyLinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// splitMessage breaks text into chunks under maxMessageLen, preserving code
// fences across chunk boundaries by closing and reopening them.
func splitMessage(text string) []string {
	if len(text) <= maxMessageLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxMessageLen {
			chunks = append(chunks, text)
			break
		}

		cutAt := maxMessageLen
		if idx := strings.LastIndex(text[:cutAt], "\n"); idx > cutAt/2 {
			cutAt = idx + 1
		}

		chunk := text[:cutAt]
		text = text[cutAt:]

		if lang, open := unclosedFence(chunk); open {
			chunk += "\n```"
			text = "```" + lang + "\n" + text
		}

		chunks = append(chunks, chunk)
	}

	return chunks
}

// unclosedFence returns the language tag and true if the chunk has an
// unclosed code fence.
func unclosedFence(text string) (string, bool) {
	open := false
	lang := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if open {
				open = false
				lang = ""
			} else {
				open = true
				lang = strings.TrimPrefix(trimmed, "```")
			}
		}
	}
	return lang, open
}

// FriendlyError translates an LLM or network error into a one line message
// the user can act on without seeing the raw provider response.
func FriendlyError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "insufficient_quota"),
		strings.Contains(msg, "exceeded your current quota"):
		return "Provider quota exhausted. Check your billing dashboard and add credit."
	case strings.Contains(msg, "API 401"):
		return "API key is invalid or expired. Check `/config`."
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

func helpMessage() string {
	return `**nevinho**

**Tools:** bash · grep · find · file read · file edit · file write · web search · web read

**Commands:**
` + "`/cancel`" + ` cancel current operation
` + "`/forget`" + ` wipe this conversation
` + "`/memory`" + ` show what nevinho remembers about you
` + "`/summary`" + ` show the saved conversation summary
` + "`/model`" + ` show or switch model
` + "`/status`" + ` uptime, tokens, cost
` + "`/paths`" + ` manage approved write paths
` + "`/schedules`" + ` list, pause, resume, or delete scheduled tasks
` + "`/config`" + ` view or update configuration
` + "`/help`" + ` this message

Just type what you need.`
}

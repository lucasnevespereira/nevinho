package discord

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/lucasnevespereira/nevinho/agent"
)

const (
	indicatorMinInterval = 600 * time.Millisecond
	indicatorMaxDetail   = 80
	indicatorChainMax    = 3
)

// activityIndicator maintains a single Discord message that reflects the
// chain of tool calls in the current turn. It creates the message lazily on
// the first event, edits it on subsequent events, and deletes it on Close.
//
// The chain shows up to indicatorChainMax most recent tool names joined with
// an arrow, followed by the current tool's detail. Older tools fall off the
// left as new ones arrive so the message stays compact on mobile.
//
// Discord rate-limits edits to roughly 5 per 5s per channel. We throttle to
// one edit per ~600ms which keeps us well under that ceiling while staying
// responsive for the user.
type activityIndicator struct {
	session   *discordgo.Session
	channelID string

	mu        sync.Mutex
	messageID string
	lastEdit  time.Time
	closed    bool
	chain     []string
}

func newActivityIndicator(s *discordgo.Session, channelID string) *activityIndicator {
	return &activityIndicator{session: s, channelID: channelID}
}

// onEvent is the agent.ToolCallback. It runs on the agent's goroutine; all
// Discord API calls happen inline so the agent sees back-pressure if Discord
// is slow.
func (a *activityIndicator) onEvent(ev agent.ToolEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return
	}

	a.chain = append(a.chain, ev.Name)
	if len(a.chain) > indicatorChainMax {
		a.chain = a.chain[len(a.chain)-indicatorChainMax:]
	}

	content := formatChain(a.chain, ev.Detail)

	if a.messageID == "" {
		msg, err := a.session.ChannelMessageSendComplex(a.channelID, &discordgo.MessageSend{
			Content: content,
			// Keep the indicator silent on mobile. It is metadata, not a reply.
			Flags: discordgo.MessageFlagsSuppressNotifications,
		})
		if err != nil {
			return
		}
		a.messageID = msg.ID
		a.lastEdit = time.Now()
		return
	}

	if time.Since(a.lastEdit) < indicatorMinInterval {
		return
	}
	if _, err := a.session.ChannelMessageEdit(a.channelID, a.messageID, content); err == nil {
		a.lastEdit = time.Now()
	}
}

// Close deletes the indicator message if one was posted. Safe to call more
// than once.
func (a *activityIndicator) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	if a.messageID == "" {
		return
	}
	a.session.ChannelMessageDelete(a.channelID, a.messageID)
	a.messageID = ""
}

// formatChain renders the tool chain as subtext. Only the last (current) tool
// carries its detail; earlier tools are shown by name only to keep the line
// short on mobile.
func formatChain(chain []string, detail string) string {
	if len(chain) == 0 {
		return ""
	}
	detail = strings.TrimSpace(detail)

	prefix := strings.Join(chain[:len(chain)-1], " → ")
	current := chain[len(chain)-1]

	var head string
	if prefix == "" {
		head = fmt.Sprintf("running %s", current)
	} else {
		head = fmt.Sprintf("%s → %s", prefix, current)
	}

	if detail == "" {
		return fmt.Sprintf("-# %s...", head)
	}
	if len(detail) > indicatorMaxDetail {
		detail = detail[:indicatorMaxDetail] + "..."
	}
	return fmt.Sprintf("-# %s: `%s`", head, escapeBackticks(detail))
}

// formatIndicator is kept for backwards compatibility with existing tests and
// single-tool callers. It renders a chain of length one.
func formatIndicator(name, detail string) string {
	return formatChain([]string{name}, detail)
}

// escapeBackticks prevents a detail that contains backticks from breaking out
// of the inline code span in the indicator message.
func escapeBackticks(s string) string {
	return strings.ReplaceAll(s, "`", "'")
}

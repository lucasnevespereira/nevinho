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
)

// activityIndicator maintains a single Discord message that reflects the
// currently running tool call. It creates the message lazily on the first
// event, edits it on subsequent events, and deletes it on Close.
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

	content := formatIndicator(ev.Name, ev.Detail)

	if a.messageID == "" {
		msg, err := a.session.ChannelMessageSend(a.channelID, content)
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

func formatIndicator(name, detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return fmt.Sprintf("_running %s..._", name)
	}
	if len(detail) > indicatorMaxDetail {
		detail = detail[:indicatorMaxDetail] + "..."
	}
	return fmt.Sprintf("_running %s:_ `%s`", name, escapeBackticks(detail))
}

// escapeBackticks prevents a detail that contains backticks from breaking out
// of the inline code span in the indicator message.
func escapeBackticks(s string) string {
	return strings.ReplaceAll(s, "`", "'")
}

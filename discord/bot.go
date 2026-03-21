package discord

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/lucasnevespereira/nevinho/agent"
)

const maxMessageLen = 2000

type Bot struct {
	session *discordgo.Session
	ownerID string
	agent   *agent.Agent
}

func New(token, ownerID string, a *agent.Agent) (*Bot, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent

	bot := &Bot{
		session: session,
		ownerID: ownerID,
		agent:   a,
	}

	session.AddHandler(bot.onMessage)

	return bot, nil
}

func (b *Bot) Start() error {
	return b.session.Open()
}

func (b *Bot) Stop() {
	b.session.Close()
}

func (b *Bot) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore own messages
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Only respond to the owner (MVP: just you)
	if m.Author.ID != b.ownerID {
		return
	}

	// Only respond in DMs
	channel, err := s.Channel(m.ChannelID)
	if err != nil || channel.Type != discordgo.ChannelTypeDM {
		return
	}

	text := strings.TrimSpace(m.Content)
	if text == "" {
		return
	}

	// Handle commands
	switch strings.ToLower(text) {
	case "/new":
		b.agent.ClearHistory(m.Author.ID)
		s.ChannelMessageSend(m.ChannelID, "Fresh conversation started.")
		return
	case "/forget":
		b.agent.ClearHistory(m.Author.ID)
		s.ChannelMessageSend(m.ChannelID, "All history cleared.")
		return
	case "/help":
		s.ChannelMessageSend(m.ChannelID, helpMessage())
		return
	case "/model":
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Current model: `%s`", b.agent.Model()))
		return
	}

	// Show typing indicator
	s.ChannelTyping(m.ChannelID)

	// Process with agent
	response, err := b.agent.Chat(m.Author.ID, text)
	if err != nil {
		log.Printf("agent error for user %s: %v", m.Author.ID, err)
		s.ChannelMessageSend(m.ChannelID, "Something went wrong. Try again.")
		return
	}

	if response == "" {
		response = "Done. (no text response)"
	}

	// Split long messages (Discord has a 2000 char limit)
	for _, chunk := range splitMessage(response) {
		s.ChannelMessageSend(m.ChannelID, chunk)
	}
}

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

		// Try to split at a newline
		cutAt := maxMessageLen
		if idx := strings.LastIndex(text[:cutAt], "\n"); idx > cutAt/2 {
			cutAt = idx + 1
		}

		chunks = append(chunks, text[:cutAt])
		text = text[cutAt:]
	}

	return chunks
}

func helpMessage() string {
	return `**nevinho** — your AI assistant

**What I can do:**
• Browse the web and read articles
• Search the web for information
• Run code (Python, JavaScript, bash)
• Save and read files (notes, code, etc.)

**Commands:**
• /new — start a fresh conversation
• /forget — delete all history
• /model — show current LLM model
• /help — show this message

Just type what you need. I'll figure it out.`
}

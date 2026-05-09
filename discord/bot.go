// Package discord wires the agent to a Discord bot session.
package discord

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/config"
)

const (
	maxMessageLen       = 2000
	maxImageBytes       = 5 * 1024 * 1024
	maxImagesPerMessage = 4
)

type Bot struct {
	session *discordgo.Session
	ownerID string
	agent   *agent.Agent
	cfg     *config.Config
	cmds    []*discordgo.ApplicationCommand
}

func New(token, ownerID string, a *agent.Agent, cfg *config.Config) (*Bot, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	// DMs include message content with just the DirectMessages intent.
	// We deliberately do not request the privileged MESSAGE_CONTENT intent
	// since nevinho only operates in DMs, and requesting it adds a setup
	// step (toggle in the Developer Portal) that DM-only bots do not need.
	session.Identify.Intents = discordgo.IntentsDirectMessages

	// Disable state caching. nevinho does not read guild or member state,
	// and discordgo's state goroutine has occasionally panicked on nil
	// dereferences without recover, killing the listen goroutine.
	session.StateEnabled = false

	bot := &Bot{
		session: session,
		ownerID: ownerID,
		agent:   a,
		cfg:     cfg,
	}

	session.AddHandler(bot.onMessage)
	session.AddHandler(bot.onInteraction)

	return bot, nil
}

func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return err
	}
	b.registerCommands()
	return nil
}

func (b *Bot) Stop() {
	b.removeCommands()
	b.session.Close()
}

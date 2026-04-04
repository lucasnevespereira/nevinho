package discord

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/config"
)

const maxMessageLen = 2000

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

	session.Identify.Intents = discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent

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

var slashCommands = []*discordgo.ApplicationCommand{
	{
		Name:        "new",
		Description: "Start a fresh conversation",
	},
	{
		Name:        "model",
		Description: "Show or switch the current LLM model",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "name",
				Description: "Model to switch to (e.g. gpt-4o, claude-sonnet-4-6)",
				Required:    false,
			},
		},
	},
	{
		Name:        "status",
		Description: "Show bot status: uptime, tokens, model",
	},
	{
		Name:        "paths",
		Description: "Manage approved file write paths",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "action",
				Description: "Action to perform",
				Required:    false,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "clear", Value: "clear"},
				},
			},
		},
	},
	{
		Name:        "help",
		Description: "Show available commands and capabilities",
	},
	{
		Name:        "config",
		Description: "View or update configuration",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "key",
				Description: "Config key to set (e.g. ANTHROPIC_API_KEY)",
				Required:    false,
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "value",
				Description: "New value (message is ephemeral, only you see it)",
				Required:    false,
			},
		},
	},
}

func (b *Bot) registerCommands() {
	appID := b.session.State.User.ID
	for _, cmd := range slashCommands {
		registered, err := b.session.ApplicationCommandCreate(appID, "", cmd)
		if err != nil {
			log.Printf("failed to register /%s: %v", cmd.Name, err)
			continue
		}
		b.cmds = append(b.cmds, registered)
	}
}

func (b *Bot) removeCommands() {
	appID := b.session.State.User.ID
	for _, cmd := range b.cmds {
		if err := b.session.ApplicationCommandDelete(appID, "", cmd.ID); err != nil {
			log.Printf("failed to remove /%s: %v", cmd.Name, err)
		}
	}
}

func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	userID := ""
	if i.Member != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}
	if userID != b.ownerID {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "Not authorized."},
		})
		return
	}

	data := i.ApplicationCommandData()
	var reply string

	switch data.Name {
	case "new":
		b.agent.ClearHistory(userID)
		reply = "Fresh conversation started."

	case "help":
		reply = helpMessage()

	case "model":
		if len(data.Options) > 0 {
			name := data.Options[0].StringValue()
			if err := b.agent.SwitchModel(name); err != nil {
				reply = fmt.Sprintf("Failed to switch: %v", err)
			} else {
				b.agent.ClearHistory(userID)
				reply = fmt.Sprintf("Switched to `%s`. History cleared.", name)
			}
		} else {
			reply = b.modelStatus()
		}

	case "status":
		reply = b.agent.Status()

	case "paths":
		if len(data.Options) > 0 && data.Options[0].StringValue() == "clear" {
			b.agent.ClearApprovedPaths()
			reply = "All path permissions cleared."
		} else {
			paths := b.agent.ApprovedPaths()
			if len(paths) == 0 {
				reply = "No approved paths."
			} else {
				var sb strings.Builder
				sb.WriteString("**Approved paths:**\n")
				for _, p := range paths {
					fmt.Fprintf(&sb, "• `%s`\n", p)
				}
				sb.WriteString("\nUse `/paths clear` to revoke all.")
				reply = sb.String()
			}
		}

	case "config":
		b.handleConfigSlash(s, i, data)
		return
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: reply},
	})
}

func (b *Bot) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	if m.Author.ID != b.ownerID {
		return
	}

	channel, err := s.Channel(m.ChannelID)
	if err != nil || channel.Type != discordgo.ChannelTypeDM {
		return
	}

	text := strings.TrimSpace(m.Content)
	if text == "" {
		return
	}

	lower := strings.ToLower(text)
	switch {
	case lower == "/new":
		b.agent.ClearHistory(m.Author.ID)
		s.ChannelMessageSend(m.ChannelID, "Fresh conversation started.")
		return
	case lower == "/help":
		s.ChannelMessageSend(m.ChannelID, helpMessage())
		return
	case lower == "/model":
		s.ChannelMessageSend(m.ChannelID, b.modelStatus())
		return
	case lower == "/status":
		s.ChannelMessageSend(m.ChannelID, b.agent.Status())
		return
	case lower == "/paths":
		paths := b.agent.ApprovedPaths()
		if len(paths) == 0 {
			s.ChannelMessageSend(m.ChannelID, "No approved paths.")
		} else {
			var sb strings.Builder
			sb.WriteString("**Approved paths:**\n")
			for _, p := range paths {
				fmt.Fprintf(&sb, "• `%s`\n", p)
			}
			sb.WriteString("\nUse `/paths clear` to revoke all.")
			s.ChannelMessageSend(m.ChannelID, sb.String())
		}
		return
	case lower == "/paths clear":
		b.agent.ClearApprovedPaths()
		s.ChannelMessageSend(m.ChannelID, "All path permissions cleared.")
		return
	case strings.HasPrefix(lower, "/model "):
		name := strings.TrimSpace(text[7:])
		if err := b.agent.SwitchModel(name); err != nil {
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Failed to switch: %v", err))
		} else {
			b.agent.ClearHistory(m.Author.ID)
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Switched to `%s`. History cleared.", name))
		}
		return
	case lower == "/config":
		s.ChannelMessageSend(m.ChannelID, b.configStatus())
		return
	case strings.HasPrefix(lower, "/config "):
		// Delete the message immediately since it may contain a secret
		s.ChannelMessageDelete(m.ChannelID, m.ID)
		parts := strings.Fields(text[8:])
		if len(parts) == 1 {
			s.ChannelMessageSend(m.ChannelID, b.configDelete(parts[0]))
		} else if len(parts) >= 2 {
			s.ChannelMessageSend(m.ChannelID, b.configSet(parts[0], strings.Join(parts[1:], " ")))
		}
		return
	}

	s.ChannelTyping(m.ChannelID)
	stopTyping := keepTyping(s, m.ChannelID)

	response, err := b.agent.Chat(m.Author.ID, text)
	stopTyping()
	if err != nil {
		log.Printf("agent error for user %s: %v", m.Author.ID, err)
		s.ChannelMessageSend(m.ChannelID, friendlyError(err))
		return
	}

	if response == "" {
		response = "Done. (no text response)"
	}

	for _, chunk := range splitMessage(response) {
		s.ChannelMessageSend(m.ChannelID, chunk)
	}
}

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

		chunks = append(chunks, text[:cutAt])
		text = text[cutAt:]
	}

	return chunks
}

func (b *Bot) handleConfigSlash(s *discordgo.Session, i *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	var key, value string
	for _, opt := range data.Options {
		switch opt.Name {
		case "key":
			key = opt.StringValue()
		case "value":
			value = opt.StringValue()
		}
	}

	// No args: show current config
	if key == "" {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: b.configStatus(),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Key + value: update
	if value != "" {
		reply := b.configSet(key, value)
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: reply,
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Key only: delete
	reply := b.configDelete(key)
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: reply,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func (b *Bot) configStatus() string {
	keys := b.cfg.Keys()
	var sb strings.Builder
	sb.WriteString("**Configuration**\n")
	for _, k := range keys {
		if k.Set {
			fmt.Fprintf(&sb, "• `%s`: set (%s)\n", k.Name, k.Source)
		} else {
			fmt.Fprintf(&sb, "• `%s`: not set\n", k.Name)
		}
	}
	sb.WriteString("\nUpdate: `/config key:KEY value:VALUE`")
	sb.WriteString("\nClear: `/config key:KEY`")
	return sb.String()
}

func (b *Bot) configSet(key, value string) string {
	if err := b.cfg.Set(key, value); err != nil {
		return fmt.Sprintf("Failed: %v", err)
	}

	// Reload provider if an LLM key changed
	if isLLMKey(key) {
		b.reloadProvider()
	}

	return fmt.Sprintf("Updated `%s`.", key)
}

func (b *Bot) configDelete(key string) string {
	if err := b.cfg.Delete(key); err != nil {
		return fmt.Sprintf("Failed: %v", err)
	}
	if isLLMKey(key) {
		b.reloadProvider()
	}
	return fmt.Sprintf("Cleared `%s`.", key)
}

func (b *Bot) reloadProvider() {
	b.agent.SwitchModel(b.agent.Model())
}

func isLLMKey(key string) bool {
	return key == "ANTHROPIC_API_KEY" || key == "OPENAI_API_KEY" || key == "OLLAMA_MODEL"
}

func (b *Bot) modelStatus() string {
	current := b.agent.Model()
	pc := b.cfg.ProviderConfig()

	var sb strings.Builder
	fmt.Fprintf(&sb, "**Current model:** `%s`\n\n", current)
	sb.WriteString("**Available:**\n")

	if pc.AnthropicKey != "" {
		sb.WriteString("• `claude-haiku-4-5`\n")
		sb.WriteString("• `claude-sonnet-4-6`\n")
		sb.WriteString("• `claude-opus-4-6`\n")
	}
	if pc.OpenAIKey != "" {
		sb.WriteString("• `gpt-4o-mini`\n")
		sb.WriteString("• `gpt-4o`\n")
		sb.WriteString("• `o4-mini`\n")
	}
	if pc.OllamaURL != "" {
		sb.WriteString("• any Ollama model name\n")
	}

	sb.WriteString("\nSwitch: `/model <name>`")
	return sb.String()
}

func friendlyError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "API 401"):
		return "API key is invalid or expired. Check `/config`."
	case strings.Contains(msg, "API 402") || strings.Contains(msg, "insufficient"):
		return "Insufficient funds on your API account."
	case strings.Contains(msg, "API 429"):
		return "Rate limited by the API. Wait a moment and try again."
	case strings.Contains(msg, "API 529") || strings.Contains(msg, "API 503"):
		return "API is overloaded. Try again shortly."
	case strings.Contains(msg, "API 500"):
		return "API returned a server error. Try again."
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "connection refused"):
		return "Can't reach the API. Check your network."
	default:
		return "Something went wrong: " + msg
	}
}

func helpMessage() string {
	return `**nevinho**

**What I can do:**
• Run bash commands
• Browse and read the web
• Read and write files

**Commands:**
• /new start a fresh conversation
• /model show or switch model
• /status uptime, tokens, model info
• /paths manage approved write paths
• /config view or update configuration
• /help show this message

Just type what you need.`
}

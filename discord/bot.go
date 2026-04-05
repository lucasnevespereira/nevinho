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
		Name:        "cancel",
		Description: "Cancel the current operation",
	},
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

	if i.Type == discordgo.InteractionMessageComponent {
		b.onComponent(s, i, userID)
		return
	}
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()
	var reply string

	switch data.Name {
	case "cancel":
		if b.agent.Cancel(userID) {
			reply = "Cancelled."
		} else {
			reply = "Nothing running."
		}

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
			b.respondModelSelector(s, i)
			return
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
	case lower == "/cancel":
		if b.agent.Cancel(m.Author.ID) {
			s.ChannelMessageSend(m.ChannelID, "Cancelled.")
		} else {
			s.ChannelMessageSend(m.ChannelID, "Nothing running.")
		}
		return
	case lower == "/new":
		b.agent.ClearHistory(m.Author.ID)
		s.ChannelMessageSend(m.ChannelID, "Fresh conversation started.")
		return
	case lower == "/help":
		s.ChannelMessageSend(m.ChannelID, helpMessage())
		return
	case lower == "/model":
		content := fmt.Sprintf("Current: `%s`", b.agent.Model())
		if components := b.modelComponents(); len(components) > 0 {
			s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
				Content:    content,
				Components: components,
			})
		} else {
			s.ChannelMessageSend(m.ChannelID, b.modelStatus())
		}
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

	response = collapseNewlines(response)
	for _, chunk := range splitMessage(response) {
		s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
			Content: chunk,
			Flags:   discordgo.MessageFlagsSuppressEmbeds,
		})
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

// collapseNewlines replaces 3+ consecutive newlines with 2, reducing big gaps in Discord.
func collapseNewlines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
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
	fmt.Fprintf(&sb, "Current: `%s`\n\n", current)

	if pc.AnthropicKey != "" {
		sb.WriteString("**Anthropic**\n")
		sb.WriteString("• `claude-haiku-4-5`\n• `claude-sonnet-4-6`\n• `claude-opus-4-6`\n\n")
	}
	if pc.OpenAIKey != "" {
		sb.WriteString("**OpenAI**\n")
		sb.WriteString("• `gpt-4o-mini`\n• `gpt-4o`\n• `o4-mini`\n\n")
	}
	if pc.OllamaURL != "" {
		sb.WriteString("**Ollama**\n")
		sb.WriteString("Any local model name\n\n")
	}

	sb.WriteString("Switch: `/model <name>`")
	return sb.String()
}

func friendlyError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "API 401"):
		return "API key is invalid or expired. Check `/config`."
	case strings.Contains(msg, "API 402") || strings.Contains(msg, "insufficient") || strings.Contains(msg, "credit balance"):
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

func (b *Bot) onComponent(s *discordgo.Session, i *discordgo.InteractionCreate, userID string) {
	data := i.MessageComponentData()
	if data.CustomID != "model_select" || len(data.Values) == 0 {
		return
	}
	name := data.Values[0]
	if err := b.agent.SwitchModel(name); err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    fmt.Sprintf("Failed: %v", err),
				Components: []discordgo.MessageComponent{},
			},
		})
		return
	}
	b.agent.ClearHistory(userID)
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("Switched to `%s`. History cleared.", name),
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (b *Bot) respondModelSelector(s *discordgo.Session, i *discordgo.InteractionCreate) {
	content := fmt.Sprintf("Current: `%s`", b.agent.Model())
	components := b.modelComponents()
	if len(components) == 0 {
		content = b.modelStatus()
	}
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    content,
			Components: components,
		},
	})
}

func (b *Bot) modelComponents() []discordgo.MessageComponent {
	options := b.modelOptions()
	if len(options) == 0 {
		return nil
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.SelectMenu{
					CustomID:    "model_select",
					Placeholder: "Switch model",
					Options:     options,
				},
			},
		},
	}
}

func (b *Bot) modelOptions() []discordgo.SelectMenuOption {
	current := b.agent.Model()
	pc := b.cfg.ProviderConfig()
	var options []discordgo.SelectMenuOption

	if pc.AnthropicKey != "" {
		for _, m := range [][2]string{
			{"claude-haiku-4-5", "Fast, affordable"},
			{"claude-sonnet-4-6", "Balanced"},
			{"claude-opus-4-6", "Most capable"},
		} {
			options = append(options, discordgo.SelectMenuOption{
				Label:       m[0],
				Value:       m[0],
				Description: m[1],
				Default:     m[0] == current,
			})
		}
	}

	if pc.OpenAIKey != "" {
		for _, m := range [][2]string{
			{"gpt-4o-mini", "Fast, affordable"},
			{"gpt-4o", "Balanced"},
			{"o4-mini", "Reasoning"},
		} {
			options = append(options, discordgo.SelectMenuOption{
				Label:       m[0],
				Value:       m[0],
				Description: m[1],
				Default:     m[0] == current,
			})
		}
	}

	return options
}

func helpMessage() string {
	return `**nevinho**

**Tools:** bash · web search · web read · file read · file write

**Commands:**
` + "`/cancel`" + ` cancel current operation
` + "`/new`" + ` fresh conversation
` + "`/model`" + ` show or switch model
` + "`/status`" + ` uptime, tokens, cost
` + "`/paths`" + ` manage approved write paths
` + "`/config`" + ` view or update configuration
` + "`/help`" + ` this message

Just type what you need.`
}

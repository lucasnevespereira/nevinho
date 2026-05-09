package discord

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/lucasnevespereira/nevinho/llm"
)

var slashCommands = []*discordgo.ApplicationCommand{
	{Name: "cancel", Description: "Cancel the current operation"},
	{Name: "forget", Description: "Wipe this conversation and any persisted summary"},
	{Name: "memory", Description: "Show what nevinho remembers about you"},
	{Name: "summary", Description: "Show the saved summary of the current conversation"},
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
	{Name: "status", Description: "Show bot status: uptime, tokens, model"},
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
	{Name: "help", Description: "Show available commands and capabilities"},
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

	case "forget":
		b.agent.ClearHistory(userID)
		reply = "Forgotten. Fresh thread."

	case "memory":
		reply = b.agent.MemoryView()

	case "summary":
		reply = b.agent.SummaryView(userID)

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

func (b *Bot) onComponent(s *discordgo.Session, i *discordgo.InteractionCreate, userID string) {
	data := i.MessageComponentData()

	switch data.CustomID {
	case "model_select":
		if len(data.Values) == 0 {
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

	case "approve":
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    i.Message.Content + "\n\n**Approved.**",
				Components: []discordgo.MessageComponent{},
			},
		})
		go b.runApproval(s, i.ChannelID, userID)

	case "deny":
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    i.Message.Content + "\n\n**Denied.**",
				Components: []discordgo.MessageComponent{},
			},
		})
		b.agent.ClearPending(userID)
	}
}

func (b *Bot) runApproval(s *discordgo.Session, channelID, userID string) {
	s.ChannelTyping(channelID)
	stopTyping := keepTyping(s, channelID)

	indicator := newActivityIndicator(s, channelID)
	b.agent.SetToolCallback(userID, indicator.onEvent)

	response, err := b.agent.Chat(userID, "yes", false, nil)
	b.agent.SetToolCallback(userID, nil)
	indicator.Close()
	stopTyping()
	if err != nil {
		s.ChannelMessageSend(channelID, friendlyError(err))
		return
	}
	if response == "" {
		response = "Done. (no text response)"
	}
	response = cleanForDiscord(response)
	for _, chunk := range splitMessage(response) {
		s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
			Content: chunk,
			Flags:   discordgo.MessageFlagsSuppressEmbeds,
		})
	}
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
		for _, name := range llm.KnownModels["anthropic"] {
			options = append(options, discordgo.SelectMenuOption{
				Label:   name,
				Value:   name,
				Default: name == current,
			})
		}
	}
	if pc.OpenAIKey != "" {
		for _, name := range llm.KnownModels["openai"] {
			options = append(options, discordgo.SelectMenuOption{
				Label:   name,
				Value:   name,
				Default: name == current,
			})
		}
	}
	return options
}

func (b *Bot) modelStatus() string {
	current := b.agent.Model()
	pc := b.cfg.ProviderConfig()

	var sb strings.Builder
	fmt.Fprintf(&sb, "Current: `%s`\n\n", current)

	if pc.AnthropicKey != "" {
		sb.WriteString("**Anthropic**\n")
		for _, m := range llm.KnownModels["anthropic"] {
			fmt.Fprintf(&sb, "• `%s`\n", m)
		}
		sb.WriteString("\n")
	}
	if pc.OpenAIKey != "" {
		sb.WriteString("**OpenAI**\n")
		for _, m := range llm.KnownModels["openai"] {
			fmt.Fprintf(&sb, "• `%s`\n", m)
		}
		sb.WriteString("\n")
	}
	if pc.OllamaURL != "" {
		sb.WriteString("**Ollama**\n")
		sb.WriteString("Any local model name\n\n")
	}

	sb.WriteString("Switch: `/model <name>`")
	return sb.String()
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
		if !k.Set {
			fmt.Fprintf(&sb, "• `%s`: not set\n", k.Name)
			continue
		}
		if isSecretKey(k.Name) {
			fmt.Fprintf(&sb, "• `%s`: set (%s)\n", k.Name, k.Source)
			continue
		}
		val, _ := b.cfg.Get(k.Name)
		fmt.Fprintf(&sb, "• `%s`: %s (%s)\n", k.Name, val, k.Source)
	}
	sb.WriteString("\nUpdate: `/config key:KEY value:VALUE`")
	sb.WriteString("\nClear: `/config key:KEY`")
	return sb.String()
}

func isSecretKey(key string) bool {
	switch key {
	case "DISCORD_BOT_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "TAVILY_API_KEY":
		return true
	}
	return false
}

func (b *Bot) configSet(key, value string) string {
	if err := b.cfg.Set(key, value); err != nil {
		return fmt.Sprintf("Failed: %v", err)
	}
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

// reloadProvider re-resolves the saved model so a freshly added or removed
// API key takes effect without restarting the process.
func (b *Bot) reloadProvider() {
	b.agent.SwitchModel(b.agent.Model())
}

func isLLMKey(key string) bool {
	return key == "ANTHROPIC_API_KEY" || key == "OPENAI_API_KEY" || key == "OLLAMA_MODEL"
}

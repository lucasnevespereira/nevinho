package discord

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/schedule"
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
		Name:        "schedules",
		Description: "List scheduled tasks, view their run history, or manage them",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "action",
				Description: "Action: pause, resume, delete, or logs. Omit to list.",
				Required:    false,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "pause", Value: "pause"},
					{Name: "resume", Value: "resume"},
					{Name: "delete", Value: "delete"},
					{Name: "logs", Value: "logs"},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "name",
				Description: "Schedule name. Required for pause, resume, delete, logs.",
				Required:    false,
			},
		},
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

	case "schedules":
		var action, name string
		for _, opt := range data.Options {
			switch opt.Name {
			case "action":
				action = opt.StringValue()
			case "name":
				name = opt.StringValue()
			}
		}
		reply = b.scheduleAction(action, name)
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
		s.ChannelMessageSend(channelID, FriendlyError(err))
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
			options = append(options, modelOption(name, current))
		}
	}
	if pc.OpenAIKey != "" {
		for _, name := range llm.KnownModels["openai"] {
			options = append(options, modelOption(name, current))
		}
	}
	if pc.GeminiKey != "" {
		for _, name := range llm.KnownModels["gemini"] {
			options = append(options, modelOption(name, current))
		}
	}
	if pc.GroqKey != "" {
		for _, name := range llm.KnownModels["groq"] {
			options = append(options, modelOption(name, current))
		}
	}
	if pc.OpenRouterKey != "" {
		for _, name := range llm.KnownModels["openrouter"] {
			options = append(options, modelOption(name, current))
		}
	}
	return options
}

// modelOption builds a Discord select menu option, tagging vision capable
// models so the operator can see at selection time which ones accept images.
func modelOption(name, current string) discordgo.SelectMenuOption {
	desc := ""
	if llm.ModelSupportsVision(name) {
		desc = "vision"
	}
	return discordgo.SelectMenuOption{
		Label:       name,
		Value:       name,
		Description: desc,
		Default:     name == current,
	}
}

// visionTag returns " (vision)" when the model supports image input, or
// empty string otherwise. Used in plain-text model listings.
func visionTag(name string) string {
	if llm.ModelSupportsVision(name) {
		return " (vision)"
	}
	return ""
}

func (b *Bot) modelStatus() string {
	current := b.agent.Model()
	pc := b.cfg.ProviderConfig()

	var sb strings.Builder
	fmt.Fprintf(&sb, "Current: `%s`\n\n", current)

	if pc.AnthropicKey != "" {
		sb.WriteString("**Anthropic**\n")
		for _, m := range llm.KnownModels["anthropic"] {
			fmt.Fprintf(&sb, "• `%s`%s\n", m, visionTag(m))
		}
		sb.WriteString("\n")
	}
	if pc.OpenAIKey != "" {
		sb.WriteString("**OpenAI**\n")
		for _, m := range llm.KnownModels["openai"] {
			fmt.Fprintf(&sb, "• `%s`%s\n", m, visionTag(m))
		}
		sb.WriteString("\n")
	}
	if pc.GeminiKey != "" {
		sb.WriteString("**Gemini**\n")
		for _, m := range llm.KnownModels["gemini"] {
			fmt.Fprintf(&sb, "• `%s`%s\n", m, visionTag(m))
		}
		sb.WriteString("\n")
	}
	if pc.GroqKey != "" {
		sb.WriteString("**Groq**\n")
		for _, m := range llm.KnownModels["groq"] {
			fmt.Fprintf(&sb, "• `%s`%s\n", m, visionTag(m))
		}
		sb.WriteString("\n")
	}
	if pc.OpenRouterKey != "" {
		sb.WriteString("**OpenRouter**\n")
		for _, m := range llm.KnownModels["openrouter"] {
			fmt.Fprintf(&sb, "• `%s`%s\n", m, visionTag(m))
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
	case "DISCORD_BOT_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY",
		"GROQ_API_KEY", "OPENROUTER_API_KEY", "TAVILY_API_KEY":
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
	return key == "ANTHROPIC_API_KEY" || key == "OPENAI_API_KEY" || key == "GEMINI_API_KEY" || key == "OLLAMA_MODEL"
}

// scheduleAction handles /schedules: list (default), pause, resume, delete.
// The bot talks to the store directly here. The agent's schedule tool
// covers the same actions for natural language requests in chat.
func (b *Bot) scheduleAction(action, name string) string {
	if b.schedules == nil {
		return "Scheduling is not enabled in this process."
	}
	action = strings.ToLower(strings.TrimSpace(action))

	switch action {
	case "", "list":
		return formatSchedules(b.schedules.All())
	case "pause", "resume":
		if name == "" {
			return fmt.Sprintf("Usage: `/schedules %s NAME`", action)
		}
		s, err := b.schedules.SetEnabled(name, action == "resume")
		if err != nil {
			return "Failed: " + err.Error()
		}
		if action == "pause" {
			return fmt.Sprintf("Paused `%s`.", s.Name)
		}
		return fmt.Sprintf("Resumed `%s`. Next run: %s", s.Name, formatScheduleNext(s))
	case "delete":
		if name == "" {
			return "Usage: `/schedules delete NAME`"
		}
		ok, err := b.schedules.Delete(name)
		if err != nil {
			return "Failed: " + err.Error()
		}
		if !ok {
			return fmt.Sprintf("No schedule named `%s`.", name)
		}
		return fmt.Sprintf("Deleted `%s`.", name)
	case "logs":
		if name == "" {
			return "Usage: `/schedules logs NAME`"
		}
		s, ok := b.schedules.Find(name)
		if !ok {
			return fmt.Sprintf("No schedule named `%s`.", name)
		}
		return formatScheduleLogs(s)
	default:
		return "Unknown action. Use list, pause, resume, delete, or logs."
	}
}

func formatSchedules(list []schedule.Schedule) string {
	if len(list) == 0 {
		return "No schedules. Ask nevinho to set one up: \"every morning summarize HN\"."
	}
	var sb strings.Builder
	sb.WriteString("**Schedules**\n")
	for _, s := range list {
		state := "▶"
		if !s.Enabled {
			state = "⏸"
		}
		fmt.Fprintf(&sb, "\n%s `%s`\n", state, s.Name)
		fmt.Fprintf(&sb, "  cron: `%s`%s\n", s.Cron, formatTzSuffix(s.Timezone))
		fmt.Fprintf(&sb, "  next: %s\n", formatScheduleNext(s))
		fmt.Fprintf(&sb, "  prompt: %s\n", s.Prompt)
		if len(s.Runs) > 0 {
			fmt.Fprintf(&sb, "  last: %s\n", formatLastRunInline(s.Runs[0]))
		}
	}
	sb.WriteString("\nManage with `/schedules pause|resume|delete|logs NAME`.")
	return sb.String()
}

func formatTzSuffix(tz string) string {
	if tz == "" {
		return ""
	}
	return " (" + tz + ")"
}

// formatScheduleNext renders NextRun in the schedule's own timezone for
// the Discord listing.
func formatScheduleNext(s schedule.Schedule) string {
	if s.NextRun.IsZero() {
		return "never"
	}
	return s.NextRunIn().Format("Mon 2006-01-02 15:04 MST")
}

func formatScheduleLogs(s schedule.Schedule) string {
	if len(s.Runs) == 0 {
		return fmt.Sprintf("`%s` has no recorded runs yet.", s.Name)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Run history for `%s`** (last %d)\n", s.Name, len(s.Runs))
	for _, r := range s.Runs {
		mark := "✅"
		if !r.Success {
			mark = "❌"
		}
		fmt.Fprintf(&sb, "\n%s `%s`  %s",
			mark,
			r.StartedAt.Format("2006-01-02 15:04:05"),
			r.Duration.Truncate(time.Millisecond),
		)
		switch {
		case r.Error != "":
			fmt.Fprintf(&sb, "\n   %s", truncateText(r.Error, 200))
		case r.Preview != "":
			fmt.Fprintf(&sb, "\n   %s", truncateText(r.Preview, 200))
		}
	}
	return sb.String()
}

func formatLastRunInline(r schedule.RunLog) string {
	mark := "✅"
	if !r.Success {
		mark = "❌"
	}
	when := r.StartedAt.Format("Mon 15:04")
	tail := ""
	if r.Error != "" {
		tail = " " + truncateText(r.Error, 80)
	} else if r.Preview != "" {
		tail = " " + truncateText(r.Preview, 80)
	}
	return fmt.Sprintf("%s %s%s", mark, when, tail)
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

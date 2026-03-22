package discord

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/auth"
)

const maxMessageLen = 2000

type Bot struct {
	session     *discordgo.Session
	ownerID     string
	agent       *agent.Agent
	credentials *auth.Store
	cmds        []*discordgo.ApplicationCommand
}

func New(token, ownerID string, a *agent.Agent, creds *auth.Store) (*Bot, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent

	bot := &Bot{
		session:     session,
		ownerID:     ownerID,
		agent:       a,
		credentials: creds,
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

// --- slash commands ---

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
		Name:        "connect",
		Description: "Connect an external service",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "service",
				Description: "Service to connect",
				Required:    true,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "GitHub", Value: "github"},
					{Name: "Google", Value: "google"},
				},
			},
		},
	},
	{
		Name:        "disconnect",
		Description: "Disconnect an external service",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "service",
				Description: "Service to disconnect",
				Required:    true,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "GitHub", Value: "github"},
					{Name: "Google", Value: "google"},
				},
			},
		},
	},
	{
		Name:        "accounts",
		Description: "Show connected external services",
	},
	{
		Name:        "help",
		Description: "Show available commands and capabilities",
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
			reply = fmt.Sprintf("Current model: `%s`", b.agent.Model())
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
				reply = "**Approved paths:**\n"
				for _, p := range paths {
					reply += fmt.Sprintf("• `%s`\n", p)
				}
				reply += "\nUse `/paths clear` to revoke all."
			}
		}

	case "connect":
		service := data.Options[0].StringValue()
		b.handleConnect(s, i, userID, service)
		return // response handled inside handleConnect

	case "disconnect":
		service := data.Options[0].StringValue()
		reply = b.handleDisconnect(userID, service)

	case "accounts":
		reply = b.handleAccounts(userID)
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: reply},
	})
}

// --- connect/disconnect/accounts ---

func (b *Bot) handleConnect(s *discordgo.Session, i *discordgo.InteractionCreate, userID, service string) {
	ctx := context.Background()
	svc := auth.Service(service)

	// Already connected?
	if tok := b.credentials.Get(userID, svc); tok != nil {
		label := tok.Username
		if label == "" {
			label = tok.Email
		}
		respond(s, i, fmt.Sprintf("Already connected to %s as **%s**.\nUse `/disconnect %s` first to reconnect.", service, label, service))
		return
	}

	switch svc {
	case auth.GitHub:
		clientID := os.Getenv("GITHUB_CLIENT_ID")
		if clientID == "" {
			respond(s, i, "GitHub not configured. Set `GITHUB_CLIENT_ID` in .env\n\nCreate one at: https://github.com/settings/applications/new")
			return
		}

		flow := &auth.GitHubFlow{ClientID: clientID}
		dc, err := flow.StartDeviceFlow(ctx)
		if err != nil {
			respond(s, i, fmt.Sprintf("Failed to start GitHub auth: %v", err))
			return
		}

		respond(s, i, fmt.Sprintf("**Connect GitHub**\n\n"+
			"1. Open: %s\n"+
			"2. Enter code: `%s`\n\n"+
			"Waiting for authorization...", dc.VerificationURI, dc.UserCode))

		go b.pollGitHub(s, i.ChannelID, userID, flow, dc)

	case auth.Google:
		clientID := os.Getenv("GOOGLE_CLIENT_ID")
		clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
		if clientID == "" || clientSecret == "" {
			respond(s, i, "Google not configured. Set `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` in .env\n\nCreate at: https://console.cloud.google.com/apis/credentials")
			return
		}

		flow := &auth.GoogleFlow{ClientID: clientID, ClientSecret: clientSecret}
		dc, err := flow.StartDeviceFlow(ctx)
		if err != nil {
			respond(s, i, fmt.Sprintf("Failed to start Google auth: %v", err))
			return
		}

		respond(s, i, fmt.Sprintf("**Connect Google**\n\n"+
			"1. Open: %s\n"+
			"2. Enter code: `%s`\n\n"+
			"Waiting for authorization...", dc.VerificationURI, dc.UserCode))

		go b.pollGoogle(s, i.ChannelID, userID, flow, dc)

	default:
		respond(s, i, fmt.Sprintf("Unknown service: %s", service))
	}
}

func (b *Bot) pollGitHub(s *discordgo.Session, channelID, userID string, flow *auth.GitHubFlow, dc *auth.DeviceCode) {
	tok, err := flow.PollForToken(context.Background(), dc)
	if err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("GitHub connection failed: %v", err))
		return
	}
	if err := b.credentials.Set(userID, auth.GitHub, tok); err != nil {
		s.ChannelMessageSend(channelID, "Failed to save GitHub credentials.")
		return
	}
	s.ChannelMessageSend(channelID, fmt.Sprintf("Connected to GitHub as **%s**!", tok.Username))
}

func (b *Bot) pollGoogle(s *discordgo.Session, channelID, userID string, flow *auth.GoogleFlow, dc *auth.DeviceCode) {
	tok, err := flow.PollForToken(context.Background(), dc)
	if err != nil {
		s.ChannelMessageSend(channelID, fmt.Sprintf("Google connection failed: %v", err))
		return
	}
	if err := b.credentials.Set(userID, auth.Google, tok); err != nil {
		s.ChannelMessageSend(channelID, "Failed to save Google credentials.")
		return
	}
	label := tok.Email
	if label == "" {
		label = tok.Username
	}
	s.ChannelMessageSend(channelID, fmt.Sprintf("Connected to Google as **%s**!", label))
}

func (b *Bot) handleDisconnect(userID, service string) string {
	svc := auth.Service(service)
	if tok := b.credentials.Get(userID, svc); tok == nil {
		return fmt.Sprintf("Not connected to %s.", service)
	}
	if err := b.credentials.Delete(userID, svc); err != nil {
		return fmt.Sprintf("Failed to disconnect: %v", err)
	}
	return fmt.Sprintf("Disconnected from %s.", service)
}

func (b *Bot) handleAccounts(userID string) string {
	connected := b.credentials.Connected(userID)
	if len(connected) == 0 {
		return "No services connected.\n\nUse `/connect github` or `/connect google` to get started."
	}

	msg := "**Connected services:**\n"
	for svc, tok := range connected {
		label := tok.Username
		if label == "" {
			label = tok.Email
		}
		msg += fmt.Sprintf("• **%s** — %s\n", svc, label)
	}
	msg += "\nUse `/disconnect <service>` to remove."
	return msg
}

func respond(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	})
}

// --- text message handling ---

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
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Current model: `%s`", b.agent.Model()))
		return
	case lower == "/status":
		s.ChannelMessageSend(m.ChannelID, b.agent.Status())
		return
	case lower == "/accounts":
		s.ChannelMessageSend(m.ChannelID, b.handleAccounts(m.Author.ID))
		return
	case lower == "/paths":
		paths := b.agent.ApprovedPaths()
		if len(paths) == 0 {
			s.ChannelMessageSend(m.ChannelID, "No approved paths.")
		} else {
			msg := "**Approved paths:**\n"
			for _, p := range paths {
				msg += fmt.Sprintf("• `%s`\n", p)
			}
			msg += "\nUse `/paths clear` to revoke all."
			s.ChannelMessageSend(m.ChannelID, msg)
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
	case strings.HasPrefix(lower, "/connect "):
		service := strings.TrimSpace(text[9:])
		b.handleConnectText(s, m.ChannelID, m.Author.ID, service)
		return
	case strings.HasPrefix(lower, "/disconnect "):
		service := strings.TrimSpace(text[12:])
		s.ChannelMessageSend(m.ChannelID, b.handleDisconnect(m.Author.ID, service))
		return
	}

	s.ChannelTyping(m.ChannelID)

	response, err := b.agent.Chat(m.Author.ID, text)
	if err != nil {
		log.Printf("agent error for user %s: %v", m.Author.ID, err)
		s.ChannelMessageSend(m.ChannelID, "Something went wrong. Try again.")
		return
	}

	if response == "" {
		response = "Done. (no text response)"
	}

	for _, chunk := range splitMessage(response) {
		s.ChannelMessageSend(m.ChannelID, chunk)
	}
}

// handleConnectText handles text-based /connect commands (non-slash).
func (b *Bot) handleConnectText(s *discordgo.Session, channelID, userID, service string) {
	ctx := context.Background()
	svc := auth.Service(strings.ToLower(service))

	if tok := b.credentials.Get(userID, svc); tok != nil {
		label := tok.Username
		if label == "" {
			label = tok.Email
		}
		s.ChannelMessageSend(channelID, fmt.Sprintf("Already connected to %s as **%s**.\nUse `/disconnect %s` first.", service, label, service))
		return
	}

	switch svc {
	case auth.GitHub:
		clientID := os.Getenv("GITHUB_CLIENT_ID")
		if clientID == "" {
			s.ChannelMessageSend(channelID, "GitHub not configured. Set `GITHUB_CLIENT_ID` in .env")
			return
		}
		flow := &auth.GitHubFlow{ClientID: clientID}
		dc, err := flow.StartDeviceFlow(ctx)
		if err != nil {
			s.ChannelMessageSend(channelID, fmt.Sprintf("Failed to start GitHub auth: %v", err))
			return
		}
		s.ChannelMessageSend(channelID, fmt.Sprintf("**Connect GitHub**\n\n"+
			"1. Open: %s\n"+
			"2. Enter code: `%s`\n\n"+
			"Waiting for authorization...", dc.VerificationURI, dc.UserCode))
		go b.pollGitHub(s, channelID, userID, flow, dc)

	case auth.Google:
		clientID := os.Getenv("GOOGLE_CLIENT_ID")
		clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
		if clientID == "" || clientSecret == "" {
			s.ChannelMessageSend(channelID, "Google not configured. Set `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` in .env")
			return
		}
		flow := &auth.GoogleFlow{ClientID: clientID, ClientSecret: clientSecret}
		dc, err := flow.StartDeviceFlow(ctx)
		if err != nil {
			s.ChannelMessageSend(channelID, fmt.Sprintf("Failed to start Google auth: %v", err))
			return
		}
		s.ChannelMessageSend(channelID, fmt.Sprintf("**Connect Google**\n\n"+
			"1. Open: %s\n"+
			"2. Enter code: `%s`\n\n"+
			"Waiting for authorization...", dc.VerificationURI, dc.UserCode))
		go b.pollGoogle(s, channelID, userID, flow, dc)

	default:
		s.ChannelMessageSend(channelID, fmt.Sprintf("Unknown service: `%s`. Try `github` or `google`.", service))
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
• /model — show or switch model
• /status — uptime, tokens, model info
• /paths — manage approved write paths
• /connect — link GitHub or Google account
• /disconnect — unlink a service
• /accounts — show connected services
• /help — show this message

Just type what you need. I'll figure it out.`
}

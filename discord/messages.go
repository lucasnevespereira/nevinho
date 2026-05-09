package discord

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
)

func (b *Bot) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in onMessage: %v", r)
		}
	}()
	if s.State != nil && s.State.User != nil && m.Author.ID == s.State.User.ID {
		return
	}

	if m.Author.ID != b.ownerID {
		logger.Info(fmt.Sprintf("ignoring message from non owner %s", m.Author.ID))
		return
	}

	// nevinho is DM only. We use m.GuildID rather than s.Channel(m.ChannelID)
	// because the channel state cache may not be populated for DM channels
	// the bot has not seen yet, which would silently drop the first message.
	if m.GuildID != "" {
		logger.Info("ignoring guild message (DM only bot)")
		return
	}

	text := strings.TrimSpace(m.Content)
	isVoice := false
	voiceMessageID := ""

	if text == "" && len(m.Attachments) > 0 {
		for _, att := range m.Attachments {
			if isAudioAttachment(att) {
				s.ChannelTyping(m.ChannelID)
				stopTranscribeTyping := keepTyping(s, m.ChannelID)
				transcribed := b.transcribeAttachment(s, m.ChannelID, att)
				stopTranscribeTyping()
				if transcribed != "" {
					text = transcribed
					isVoice = true
					voiceMessageID = m.ID
				}
				break
			}
		}
	}

	var images []llm.Image
	for _, att := range m.Attachments {
		mt := imageMediaType(att)
		if mt == "" {
			continue
		}
		if len(images) >= maxImagesPerMessage {
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Only the first %d images were used.", maxImagesPerMessage))
			break
		}
		if att.Size > maxImageBytes {
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Skipped %s (over 5MB).", att.Filename))
			continue
		}
		img, err := b.downloadImage(att, mt)
		if err != nil {
			logger.Err(fmt.Errorf("image %s: %w", att.Filename, err))
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Failed to load %s.", att.Filename))
			continue
		}
		images = append(images, img)
	}

	// Embed images. When the user pastes a URL, Discord renders a preview
	// and the image lives in m.Embeds, not m.Attachments. Pull from there
	// so link previews work like uploads.
	for _, em := range m.Embeds {
		if len(images) >= maxImagesPerMessage {
			break
		}
		url := ""
		switch {
		case em.Image != nil && em.Image.URL != "":
			url = em.Image.URL
		case em.Thumbnail != nil && em.Thumbnail.URL != "":
			url = em.Thumbnail.URL
		}
		if url == "" {
			continue
		}
		img, err := b.downloadImageURL(url, "")
		if err != nil {
			logger.Err(fmt.Errorf("embed image %s: %w", url, err))
			continue
		}
		images = append(images, img)
	}

	if text == "" && len(images) == 0 {
		return
	}

	if len(images) > 0 && !llm.ModelSupportsVision(b.agent.Model()) {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Current model `%s` cannot read images. Switch to a vision capable model with `/model`.", b.agent.Model()))
		return
	}

	if b.handleTextCommand(s, m, text) {
		return
	}

	s.ChannelTyping(m.ChannelID)
	stopTyping := keepTyping(s, m.ChannelID)

	indicator := newActivityIndicator(s, m.ChannelID)
	b.agent.SetToolCallback(m.Author.ID, indicator.onEvent)

	response, err := b.agent.Chat(m.Author.ID, text, isVoice, images)
	b.agent.SetToolCallback(m.Author.ID, nil)
	indicator.Close()
	stopTyping()
	if err != nil {
		log.Printf("agent error for user %s: %v", m.Author.ID, err)
		s.ChannelMessageSend(m.ChannelID, friendlyError(err))
		return
	}

	for _, f := range b.agent.DrainFileDisplays(m.Author.ID) {
		block := "```" + f.Lang + "\n" + f.Content + "\n```"
		hasNestedFences := strings.Contains(f.Content, "```")
		if !hasNestedFences && len(block) <= maxMessageLen {
			s.ChannelMessageSend(m.ChannelID, block)
		} else {
			s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
				Files: []*discordgo.File{
					{
						Name:   filepath.Base(f.Path),
						Reader: strings.NewReader(f.Content),
					},
				},
			})
		}
	}

	if response == "" {
		response = "Done. (no text response)"
	}

	if b.agent.HasPendingApproval(m.Author.ID) {
		s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
			Content: cleanForDiscord(response),
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{Label: "Approve", Style: discordgo.SuccessButton, CustomID: "approve"},
						discordgo.Button{Label: "Deny", Style: discordgo.DangerButton, CustomID: "deny"},
					},
				},
			},
		})
		return
	}

	response = cleanForDiscord(response)
	chunks := splitMessage(response)
	for i, chunk := range chunks {
		send := &discordgo.MessageSend{
			Content: chunk,
			Flags:   discordgo.MessageFlagsSuppressEmbeds,
		}
		if i == 0 && isVoice {
			send.Content = fmt.Sprintf("-# 🎤 %q\n%s", text, chunk)
			send.Reference = &discordgo.MessageReference{
				MessageID: voiceMessageID,
				ChannelID: m.ChannelID,
			}
		}
		s.ChannelMessageSendComplex(m.ChannelID, send)
	}
}

// handleTextCommand dispatches plain-text slash-style commands typed in DMs.
// Returns true when the input matched a command and was fully handled.
func (b *Bot) handleTextCommand(s *discordgo.Session, m *discordgo.MessageCreate, text string) bool {
	lower := strings.ToLower(text)
	switch {
	case lower == "/cancel":
		if b.agent.Cancel(m.Author.ID) {
			s.ChannelMessageSend(m.ChannelID, "Cancelled.")
		} else {
			s.ChannelMessageSend(m.ChannelID, "Nothing running.")
		}
	case lower == "/forget":
		b.agent.ClearHistory(m.Author.ID)
		s.ChannelMessageSend(m.ChannelID, "Forgotten. Fresh thread.")
	case lower == "/memory":
		s.ChannelMessageSend(m.ChannelID, b.agent.MemoryView())
	case lower == "/summary":
		s.ChannelMessageSend(m.ChannelID, b.agent.SummaryView(m.Author.ID))
	case lower == "/help":
		s.ChannelMessageSend(m.ChannelID, helpMessage())
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
	case lower == "/status":
		s.ChannelMessageSend(m.ChannelID, b.agent.Status())
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
	case lower == "/paths clear":
		b.agent.ClearApprovedPaths()
		s.ChannelMessageSend(m.ChannelID, "All path permissions cleared.")
	case strings.HasPrefix(lower, "/model "):
		name := strings.TrimSpace(text[7:])
		if err := b.agent.SwitchModel(name); err != nil {
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Failed to switch: %v", err))
		} else {
			b.agent.ClearHistory(m.Author.ID)
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Switched to `%s`. History cleared.", name))
		}
	case lower == "/config":
		s.ChannelMessageSend(m.ChannelID, b.configStatus())
	case strings.HasPrefix(lower, "/config "):
		// Delete the user message immediately. /config can carry a secret.
		s.ChannelMessageDelete(m.ChannelID, m.ID)
		parts := strings.Fields(text[8:])
		if len(parts) == 1 {
			s.ChannelMessageSend(m.ChannelID, b.configDelete(parts[0]))
		} else if len(parts) >= 2 {
			s.ChannelMessageSend(m.ChannelID, b.configSet(parts[0], strings.Join(parts[1:], " ")))
		}
	default:
		return false
	}
	return true
}

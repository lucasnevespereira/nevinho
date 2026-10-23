package discord

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/lucasnevespereira/nevinho/agent"
)

func (b *Bot) sessionID() string {
	b.sessionMu.Lock()
	defer b.sessionMu.Unlock()
	return b.activeID
}

func (b *Bot) beginTurn() (string, bool) {
	b.sessionMu.Lock()
	defer b.sessionMu.Unlock()
	if b.busy {
		return "", false
	}
	b.busy = true
	return b.activeID, true
}

func (b *Bot) endTurn() {
	b.sessionMu.Lock()
	b.busy = false
	b.sessionMu.Unlock()
}

func (b *Bot) changeSession(id string, forget bool) (string, error) {
	b.sessionMu.Lock()
	defer b.sessionMu.Unlock()
	if b.busy {
		return "", fmt.Errorf("a response is still running; use /cancel or wait for it")
	}
	if b.agent.HasPendingApproval(b.activeID) {
		return "", fmt.Errorf("answer the pending approval before switching conversations")
	}
	if id == b.activeID && !forget {
		return "Already in this conversation.", nil
	}
	if !forget {
		if err := b.agent.SaveTerminalSession(b.activeID); err != nil {
			return "", err
		}
	} else if err := b.agent.ForgetDiscordSession(b.ownerID, b.activeID); err != nil {
		return "", err
	}
	var next agent.TerminalSession
	var err error
	if id == "" {
		next, err = b.agent.NewDiscordSession(b.ownerID)
	} else {
		next, err = b.agent.OpenDiscordSession(b.ownerID, id)
	}
	if err != nil {
		return "", err
	}
	b.agent.UnloadTerminalSession(b.activeID)
	b.activeID = next.ID
	if id == "" {
		if forget {
			return "Forgotten. Started a fresh conversation.", nil
		}
		return "Started a fresh conversation. Use `/resume` to switch back.", nil
	}
	message := "Resumed: " + next.Title
	if next.ImagesOmitted {
		message += "\nOriginal images are unavailable; reattach them if needed."
	}
	return message, nil
}

func (b *Bot) switchModel(name string) error {
	b.sessionMu.Lock()
	defer b.sessionMu.Unlock()
	if b.busy || b.agent.HasPendingApproval(b.activeID) {
		return fmt.Errorf("finish the current response or approval first")
	}
	if err := b.agent.SaveTerminalSession(b.activeID); err != nil {
		return err
	}
	if err := b.agent.SwitchModel(name); err != nil {
		return err
	}
	next, err := b.agent.NewDiscordSession(b.ownerID)
	if err != nil {
		return err
	}
	b.activeID = next.ID
	return nil
}

func (b *Bot) sessionChoices(page int) (string, []discordgo.MessageComponent) {
	b.sessionMu.Lock()
	if b.busy || b.agent.HasPendingApproval(b.activeID) {
		b.sessionMu.Unlock()
		return "Finish the current response or approval first.", nil
	}
	activeID := b.activeID
	b.sessionMu.Unlock()
	if err := b.agent.SaveTerminalSession(activeID); err != nil {
		return "Could not save the current conversation: " + err.Error(), nil
	}
	sessions, err := b.agent.DiscordSessions(b.ownerID)
	if err != nil && len(sessions) == 0 {
		return "Could not list conversations: " + err.Error(), nil
	}
	if len(sessions) == 0 {
		return "No saved conversations. Use `/new` to start one.", nil
	}
	// Discord select menus support at most 25 options.
	const pageSize = 25
	page = max(0, min(page, (len(sessions)-1)/pageSize))
	start := page * pageSize
	end := min(start+pageSize, len(sessions))
	options := make([]discordgo.SelectMenuOption, 0, end-start)
	active := b.sessionID()
	for _, session := range sessions[start:end] {
		label := []rune(session.Title)
		if len(label) > 80 {
			label = label[:80]
		}
		options = append(options, discordgo.SelectMenuOption{
			Label: string(label), Value: session.ID,
			Description: session.UpdatedAt.Local().Format("Jan 2, 2006 15:04"),
			Default:     session.ID == active,
		})
	}
	message := fmt.Sprintf("Choose a saved conversation (%d–%d of %d):", start+1, end, len(sessions))
	if err != nil {
		message += "\n" + err.Error()
	}
	components := []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.SelectMenu{CustomID: "resume_session", Placeholder: "Select a conversation", Options: options},
	}}}
	if len(sessions) > pageSize {
		buttons := []discordgo.MessageComponent{}
		if page > 0 {
			buttons = append(buttons, discordgo.Button{Label: "Newer", Style: discordgo.SecondaryButton, CustomID: fmt.Sprintf("resume_page:%d", page-1)})
		}
		if end < len(sessions) {
			buttons = append(buttons, discordgo.Button{Label: "Older", Style: discordgo.SecondaryButton, CustomID: fmt.Sprintf("resume_page:%d", page+1)})
		}
		components = append(components, discordgo.ActionsRow{Components: buttons})
	}
	return message, components
}

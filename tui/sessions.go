package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lucasnevespereira/nevinho/agent"
)

func (m *model) bindSession(id string) {
	m.agent.SetToolCallback(m.userID, nil)
	if m.userID != id {
		m.agent.UnloadTerminalSession(m.userID)
	}
	m.userID = id
	events, sink := m.events, m.hooks
	m.agent.SetToolCallback(id, func(ev agent.ToolEvent) {
		emitToolHook(sink, ev)
		select {
		case events <- ev:
		default:
		}
	})
	m.sessionErr = ""
	m.approval = nil
	m.approvalText = ""
	m.approving = false
	m.setLive("")
}

func sessionNotice(s agent.TerminalSession) string {
	notice := "Resumed: " + s.Title
	if s.Title == "New conversation" {
		notice = "New conversation"
	}
	if !s.UpdatedAt.IsZero() && s.Title != "New conversation" {
		notice += " · " + sessionAge(s.UpdatedAt)
	}
	notice += "\n/new fresh conversation · /resume switch session"
	if s.ImagesOmitted {
		notice += "\nOriginal images unavailable; reattach if needed."
	}
	return notice
}

func sessionAge(updated time.Time) string {
	age := time.Since(updated)
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(age.Hours()/24))
	}
}

// Initialization restores model context, not transcript blocks. Errors keep
// chat disabled until the user explicitly starts fresh or picks another session.
func (m *model) initializeSession() {
	scope := terminalUserID(m.cwd)
	var s agent.TerminalSession
	var err error
	if m.agent.GetConfig("ELEPHANT") == "off" {
		s, err = m.agent.NewTerminalSession(scope)
	} else {
		s, err = m.agent.OpenTerminalSession(scope, "")
	}
	if err != nil {
		m.sessionErr = err.Error()
		m.sessionNotice = "Could not resume: " + err.Error() + "\n/new start fresh · /resume choose another session"
		return
	}
	m.bindSession(s.ID)
	m.sessionNotice = sessionNotice(s)
	if m.agent.GetConfig("ELEPHANT") == "off" {
		m.sessionNotice = "Session persistence off · /new fresh conversation"
	}
}

func (m *model) newSession() tea.Cmd {
	if m.sessionErr == "" {
		if err := m.agent.SaveTerminalSession(m.userID); err != nil {
			return m.printBlock(errorBlock{err.Error()})
		}
	}
	s, err := m.agent.NewTerminalSession(terminalUserID(m.cwd))
	if err != nil {
		return m.printBlock(errorBlock{err.Error()})
	}
	m.bindSession(s.ID)
	m.sessionNotice = "New conversation · /resume switch session"
	if m.agent.GetConfig("ELEPHANT") == "off" {
		m.sessionNotice = "New conversation · persistence off; this conversation will not be saved"
	}
	return m.printBlock(hintBlock{m.sessionNotice})
}

func (m *model) resumeSessions() tea.Cmd {
	if m.sessionErr == "" {
		if err := m.agent.SaveTerminalSession(m.userID); err != nil {
			return m.printBlock(errorBlock{err.Error()})
		}
	}
	sessions, err := m.agent.TerminalSessions(terminalUserID(m.cwd))
	if err != nil && len(sessions) == 0 {
		return m.printBlock(errorBlock{err.Error()})
	}
	if len(sessions) == 0 {
		return m.printBlock(hintBlock{"No saved sessions for this working directory."})
	}
	items := make([]selectorItem, 0, len(sessions))
	for _, s := range sessions {
		items = append(items, selectorItem{label: s.Title + " · " + sessionAge(s.UpdatedAt), value: s.ID, marked: s.ID == m.userID})
	}
	m.sel = newSelector("resume session — this working directory", items)
	// Newest first, rather than jumping to an older active item.
	m.sel.cursor = 0
	m.selKind, m.selecting = "resume", true
	if err != nil {
		return m.printBlock(errorBlock{err.Error()})
	}
	return nil
}

func (m *model) resumeSession(id string) tea.Cmd {
	if id == m.userID && m.sessionErr == "" {
		return m.printBlock(hintBlock{"Already in this session."})
	}
	if m.sessionErr == "" {
		if err := m.agent.SaveTerminalSession(m.userID); err != nil {
			return m.printBlock(errorBlock{err.Error()})
		}
	}
	s, err := m.agent.OpenTerminalSession(terminalUserID(m.cwd), id)
	if err != nil {
		return m.printBlock(errorBlock{err.Error()})
	}
	m.bindSession(s.ID)
	m.sessionNotice = sessionNotice(s)
	return m.printBlock(hintBlock{m.sessionNotice})
}

func (m *model) forgetSession() tea.Cmd {
	if m.sessionErr != "" {
		return m.printBlock(errorBlock{"No active session to delete. Use /new or /resume."})
	}
	if err := m.agent.ForgetTerminalSession(terminalUserID(m.cwd), m.userID); err != nil {
		return m.printBlock(errorBlock{err.Error()})
	}
	// Do not checkpoint the just-deleted history on the way to the fresh session.
	m.sessionErr = "session deleted"
	return m.newSession()
}

// Model switching clears provider-specific in-memory history. Keep the old
// checkpoint resumable rather than deleting it or overwriting it on next turn.
func (m *model) modelChanged(notice string) tea.Cmd {
	m.sessionErr = "model changed"
	cmd := m.newSession()
	return tea.Sequence(m.printBlock(hintBlock{notice}), cmd)
}

func (m *model) sessionDetails() tea.Cmd {
	text := fmt.Sprintf("Project: `%s`\nSession: `%s`\nModel: `%s`\n\n", m.cwd, m.userID, m.agent.Model())
	if m.sessionErr != "" {
		text += m.sessionErr
	} else {
		text += m.agent.SummaryView(m.userID)
	}
	return m.printBlock(agentBlock{text})
}

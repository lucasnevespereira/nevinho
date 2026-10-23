package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/llm"
)

type sessionProvider struct {
	calls int
	last  *llm.Request
}

func (p *sessionProvider) Model() string { return "fake" }
func (p *sessionProvider) Complete(_ context.Context, req *llm.Request) (*llm.Response, error) {
	p.calls++
	p.last = req
	return &llm.Response{Text: "done", Assistant: llm.Message{Role: llm.RoleAssistant, Text: "done"}, StopReason: llm.StopEndTurn}, nil
}

func sessionModel(t *testing.T) (model, *sessionProvider) {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &sessionProvider{}
	a := agent.New(p, cfg, "test", "", agent.ModeLocal)
	m := newModel(a, make(chan agent.ToolEvent, 8), make(chan string, 8), t.TempDir())
	m.initializeSession()
	if m.sessionErr != "" {
		t.Fatal(m.sessionErr)
	}
	return m, p
}

func TestNewResumePickerAndContinue(t *testing.T) {
	m, p := sessionModel(t)
	first := m.userID
	if _, err := m.agent.Chat(first, "fix resume regression", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, handled := m.handleSlash("/new"); !handled {
		t.Fatal("new not handled")
	}
	second := m.userID
	if second == first {
		t.Fatal("new retained same session")
	}
	if _, err := m.agent.Chat(second, "other task", false, nil); err != nil {
		t.Fatal(err)
	}
	m.handleSlash("/resume")
	if !m.selecting || m.selKind != "resume" || len(m.sel.items) != 2 {
		t.Fatalf("picker not opened: %+v", m.sel)
	}
	for _, key := range []string{"f", "i", "x"} {
		next, _ := m.updateSelector(key)
		m = next.(model)
	}
	if got := m.sel.visible(); len(got) != 1 || got[0].value != first {
		t.Fatalf("filter = %+v", got)
	}
	calls := p.calls
	next, _ := m.updateSelector("enter")
	m = next.(model)
	if m.userID != first || m.selecting || p.calls != calls {
		t.Fatal("resume did not switch context-only")
	}
	if !strings.Contains(m.sessionNotice, "fix resume regression") {
		t.Fatal("resume notice missing title")
	}
	if _, err := m.agent.Chat(first, "continue", false, nil); err != nil {
		t.Fatal(err)
	}
	if p.last.Messages[0].Text != "fix resume regression" {
		t.Fatal("continued wrong context")
	}
	m.handleSlash("/resume")
	next, _ = m.updateSelector("esc")
	m = next.(model)
	if m.selecting || m.userID != first {
		t.Fatal("escape switched session")
	}
}

func TestForgetRequiresConfirmationAndKeepsOtherSessions(t *testing.T) {
	m, _ := sessionModel(t)
	first := m.userID
	m.handleSlash("/new")
	second := m.userID
	m.handleSlash("/forget")
	if !m.selecting || m.selKind != "forget" || m.sel.cursor != 0 {
		t.Fatal("forget should default to cancel")
	}
	next, _ := m.updateSelector("enter")
	m = next.(model)
	if m.userID != second {
		t.Fatal("cancel deleted session")
	}
	m.handleSlash("/forget")
	next, _ = m.updateSelector("down")
	next, _ = next.(model).updateSelector("enter")
	m = next.(model)
	if m.userID == second {
		t.Fatal("confirmed delete did not start fresh")
	}
	list, err := m.agent.TerminalSessions(terminalUserID(m.cwd))
	if err != nil {
		t.Fatal(err)
	}
	foundFirst := false
	for _, s := range list {
		if s.ID == second {
			t.Fatal("deleted session remains resumable")
		}
		foundFirst = foundFirst || s.ID == first
	}
	if !foundFirst {
		t.Fatal("forget deleted unrelated session")
	}
}

func TestSessionStartupResumesWithoutTranscriptReplay(t *testing.T) {
	m, p := sessionModel(t)
	if _, err := m.agent.Chat(m.userID, "investigate startup", false, nil); err != nil {
		t.Fatal(err)
	}
	calls := p.calls
	reopened := newModel(m.agent, make(chan agent.ToolEvent, 8), make(chan string, 8), m.cwd)
	reopened.initializeSession()
	if reopened.userID != m.userID || reopened.sessionErr != "" || p.calls != calls {
		t.Fatal("startup failed to resume without model call")
	}
	if len(*reopened.blocks) != 0 || !strings.Contains(reopened.sessionNotice, "investigate startup") {
		t.Fatal("startup replayed transcript or omitted notice")
	}
	if !isKnownSlash("/resume") || !isKnownSlash("/new") || !strings.Contains(commandHelp(), "/resume") {
		t.Fatal("commands missing from help")
	}
}

func TestResumeDisabledAndNewStillWorks(t *testing.T) {
	m, _ := sessionModel(t)
	if err := m.agent.SetConfig("ELEPHANT", "off"); err != nil {
		t.Fatal(err)
	}
	m.handleSlash("/resume")
	if m.selecting {
		t.Fatal("disabled persistence opened picker")
	}
	previous := m.userID
	m.handleSlash("/new")
	if m.userID == previous || m.sessionErr != "" {
		t.Fatal("new should work in-memory")
	}
	if !strings.Contains(m.sessionNotice, "persistence off") {
		t.Fatal("missing disabled persistence notice")
	}
}

func TestSessionCommandsBlockedDuringTurn(t *testing.T) {
	m, _ := sessionModel(t)
	m.busy = true
	id := m.userID
	m.input.SetValue("/new")
	next, cmd := m.submit()
	if next.(model).userID != id || cmd != nil {
		t.Fatal("session switched during active turn")
	}
}

func TestSessionNoticeWarnsAboutImages(t *testing.T) {
	got := sessionNotice(agent.TerminalSession{Title: "Inspect photo", ImagesOmitted: true})
	if !strings.Contains(got, "reattach") {
		t.Fatal("missing omitted-image warning")
	}
	if strings.Contains(sessionNotice(agent.TerminalSession{Title: "No images"}), "reattach") {
		t.Fatal("spurious image warning")
	}
}

func TestModelSwitchPreservesPreviousSession(t *testing.T) {
	m, _ := sessionModel(t)
	previous := m.userID
	if _, err := m.agent.Chat(previous, "preserve model-specific context", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.agent.SetConfig("OLLAMA_MODEL", "llama3"); err != nil {
		t.Fatal(err)
	}
	m.handleSlash("/model llama3")
	if m.userID == previous || m.agent.Model() != "llama3" || m.sessionErr != "" {
		t.Fatal("model switch did not start fresh session")
	}
	sessions, err := m.agent.TerminalSessions(terminalUserID(m.cwd))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s.ID == previous && s.Title == "preserve model-specific context" && s.Model == "fake" {
			return
		}
	}
	t.Fatal("model switch deleted previous checkpoint")
}

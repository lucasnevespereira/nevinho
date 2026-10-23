package agent

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/llm"
)

const testScope = "terminal-0123456789abcdef"
const otherScope = "terminal-fedcba9876543210"

func sessionAgent(t *testing.T) *Agent {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(&fallbackProvider{}, cfg, "test", "", ModeLocal)
}

func TestTerminalSessionNewResumeAndRestart(t *testing.T) {
	a := sessionAgent(t)
	first, err := a.OpenTerminalSession(testScope, "")
	if err != nil {
		t.Fatal(err)
	}
	a.appendHistory(first.ID, llm.UserMessage("Fix session restore", nil), assistantMsg("done"))
	if err := a.SaveTerminalSession(first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := a.NewTerminalSession(testScope)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || len(a.messages(second.ID)) != 0 {
		t.Fatal("new did not start separate context")
	}
	a.appendHistory(second.ID, llm.UserMessage("Investigate failures", nil))
	if err := a.SaveTerminalSession(second.ID); err != nil {
		t.Fatal(err)
	}
	list, err := a.TerminalSessions(testScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].Title != "Fix session restore" {
		t.Fatalf("sessions = %+v", list)
	}
	a.UnloadTerminalSession(first.ID)
	if len(a.messages(first.ID)) != 0 {
		t.Fatal("unload retained memory")
	}
	resumed, err := a.OpenTerminalSession(testScope, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Title != "Fix session restore" || len(a.messages(first.ID)) != 2 {
		t.Fatalf("resume failed: %+v", resumed)
	}
	restarted := New(&fallbackProvider{}, a.cfg, "test", "", ModeLocal)
	active, err := restarted.OpenTerminalSession(testScope, "")
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != first.ID || restarted.messages(first.ID)[0].Text != "Fix session restore" {
		t.Fatalf("wrong active session: %+v", active)
	}
	if _, err := restarted.Chat(first.ID, "continue", false, nil); err != nil {
		t.Fatal(err)
	}
	cp, err := restarted.readCheckpoint(first.ID)
	if err != nil || len(cp.Messages) != 4 || cp.Title != "Fix session restore" {
		t.Fatalf("checkpoint = %+v, %v", cp, err)
	}
}

func TestTerminalSessionLegacyMigrationAndForget(t *testing.T) {
	for _, summaryOnly := range []bool{false, true} {
		name := "checkpoint"
		if summaryOnly {
			name = "summary"
		}
		t.Run(name, func(t *testing.T) {
			a := sessionAgent(t)
			if summaryOnly {
				if err := saveSummary(a.cfg.Dir(), testScope, "Work on session restore"); err != nil {
					t.Fatal(err)
				}
			} else {
				a.appendHistory(testScope, llm.UserMessage("Work on session restore", nil), assistantMsg("done"))
				if err := a.saveCheckpoint(testScope); err != nil {
					t.Fatal(err)
				}
			}
			s, err := a.OpenTerminalSession(testScope, "")
			if err != nil {
				t.Fatal(err)
			}
			if s.ID == testScope || len(a.messages(s.ID)) == 0 {
				t.Fatalf("legacy context not migrated: %+v", s)
			}
			list, err := a.TerminalSessions(testScope)
			if err != nil || len(list) != 1 {
				t.Fatalf("list = %+v, %v", list, err)
			}
			if err := a.ForgetTerminalSession(testScope, s.ID); err != nil {
				t.Fatal(err)
			}
			if len(a.messages(s.ID)) != 0 || loadSummary(a.cfg.Dir(), testScope) != "" {
				t.Fatal("forget retained history")
			}
			old, err := a.readCheckpoint(testScope)
			if err != nil || len(old.Messages) != 0 {
				t.Fatalf("legacy fallback retained: %+v, %v", old, err)
			}
			if _, err := a.OpenTerminalSession(testScope, ""); err == nil {
				t.Fatal("deleted session silently resurrected")
			}
			fresh, err := a.NewTerminalSession(testScope)
			if err != nil {
				t.Fatal(err)
			}
			if len(a.messages(fresh.ID)) != 0 {
				t.Fatal("fresh session contains deleted messages")
			}
		})
	}
}

func TestTerminalSessionIsolationAndValidation(t *testing.T) {
	a := sessionAgent(t)
	s, err := a.NewTerminalSession(testScope)
	if err != nil {
		t.Fatal(err)
	}
	list, err := a.TerminalSessions(otherScope)
	if err != nil || len(list) != 0 {
		t.Fatalf("cross-directory leak: %+v, %v", list, err)
	}
	for _, id := range []string{s.ID, "../escape", otherScope + "-session-xyz"} {
		if _, err := a.OpenTerminalSession(otherScope, id); err == nil {
			t.Fatalf("accepted foreign/invalid ID %q", id)
		}
	}
	if err := a.ForgetTerminalSession(otherScope, s.ID); err == nil {
		t.Fatal("foreign session deletion accepted")
	}
	for _, scope := range []string{"../escape", "terminal-x", "discord-owner"} {
		if _, err := a.NewTerminalSession(scope); err == nil {
			t.Fatalf("accepted scope %q", scope)
		}
	}
	active, err := a.OpenTerminalSession(testScope, "")
	if err != nil || active.ID != s.ID {
		t.Fatalf("validation changed active session: %+v, %v", active, err)
	}
}

func TestTerminalSessionPersistenceDisabledWritesNothing(t *testing.T) {
	a := sessionAgent(t)
	if err := a.cfg.Set("ELEPHANT", "off"); err != nil {
		t.Fatal(err)
	}
	s, err := a.NewTerminalSession(testScope)
	if err != nil {
		t.Fatal(err)
	}
	a.appendHistory(s.ID, llm.UserMessage("not saved", nil))
	if err := a.SaveTerminalSession(s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.TerminalSessions(testScope); err == nil {
		t.Fatal("list should explain disabled persistence")
	}
	if _, err := a.OpenTerminalSession(testScope, ""); err == nil {
		t.Fatal("resume should explain disabled persistence")
	}
	if _, err := os.Stat(a.activeSessionPath(testScope)); !os.IsNotExist(err) {
		t.Fatalf("active pointer written: %v", err)
	}
	path, _ := checkpointPath(a.cfg.Dir(), s.ID)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("checkpoint written: %v", err)
	}
}

func TestTerminalSessionMetadataEncryptionAndImages(t *testing.T) {
	a := sessionAgent(t)
	s, err := a.NewTerminalSession(testScope)
	if err != nil {
		t.Fatal(err)
	}
	a.appendHistory(s.ID, llm.UserMessage("private title", []llm.Image{{Data: []byte("private image")}}))
	if err := a.SaveTerminalSession(s.ID); err != nil {
		t.Fatal(err)
	}
	path, _ := checkpointPath(a.cfg.Dir(), s.ID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private")) {
		t.Fatal("plaintext conversation metadata")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("checkpoint permissions: %v, %v", info, err)
	}
	cp, err := a.readCheckpoint(s.ID)
	if err != nil || !cp.ImagesOmitted || len(cp.Messages[0].Images) != 0 {
		t.Fatalf("image metadata = %+v, %v", cp, err)
	}
	// Compaction must not replace the original first-prompt title.
	a.setMessages(s.ID, []llm.Message{llm.UserMessage("[Conversation so far: compacted]", nil)})
	if err := a.SaveTerminalSession(s.ID); err != nil {
		t.Fatal(err)
	}
	resumed, err := a.OpenTerminalSession(testScope, s.ID)
	if err != nil || resumed.Title != "private title" || !resumed.ImagesOmitted {
		t.Fatalf("metadata lost: %+v, %v", resumed, err)
	}
}

func TestTerminalSessionFailedRestorePreservesActive(t *testing.T) {
	a := sessionAgent(t)
	bad, err := a.NewTerminalSession(testScope)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := checkpointPath(a.cfg.Dir(), bad.ID)
	if err := a.writeCheckpoint(path, conversationCheckpoint{Model: "unavailable-model"}); err != nil {
		t.Fatal(err)
	}
	good, err := a.NewTerminalSession(testScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.OpenTerminalSession(testScope, bad.ID); err == nil {
		t.Fatal("unavailable model accepted")
	}
	if a.Model() != "fake" {
		t.Fatal("failed restore changed model")
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OpenTerminalSession(testScope, bad.ID); err == nil {
		t.Fatal("corrupt checkpoint accepted")
	}
	active, err := a.OpenTerminalSession(testScope, "")
	if err != nil || active.ID != good.ID {
		t.Fatalf("failed restore changed active: %+v, %v", active, err)
	}
}

func TestTerminalSessionRestoresSavedModelWithoutExecuting(t *testing.T) {
	a := sessionAgent(t)
	if err := a.cfg.Set("OLLAMA_MODEL", "llama3"); err != nil {
		t.Fatal(err)
	}
	s, err := a.NewTerminalSession(testScope)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := checkpointPath(a.cfg.Dir(), s.ID)
	if err := a.writeCheckpoint(path, conversationCheckpoint{Model: "llama3", Messages: []llm.Message{
		llm.UserMessage("previous task", nil),
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "old-call", Name: "bash", Input: []byte(`{"command":"exit 99"}`)}}},
		llm.ToolResultMessage([]llm.ToolResult{{ID: "old-call", Output: "not executed: approval required"}}),
	}}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	a.SetToolCallback(s.ID, func(ToolEvent) { calls++ })
	a.pendingToolID[s.ID] = "stale-approval"
	if _, err := a.OpenTerminalSession(testScope, s.ID); err != nil {
		t.Fatal(err)
	}
	if a.Model() != "llama3" || len(a.messages(s.ID)) != 3 || calls != 0 {
		t.Fatal("restore did not preserve context-only semantics")
	}
	if a.tools.PendingApproval(s.ID) != nil || a.pendingToolID[s.ID] != "" {
		t.Fatal("restore recreated pending approval")
	}
}

func TestTerminalSessionEnablePersistenceMakesCurrentActive(t *testing.T) {
	a := sessionAgent(t)
	if _, err := a.NewTerminalSession(testScope); err != nil {
		t.Fatal(err)
	}
	if err := a.cfg.Set("ELEPHANT", "off"); err != nil {
		t.Fatal(err)
	}
	current, err := a.NewTerminalSession(testScope)
	if err != nil {
		t.Fatal(err)
	}
	a.appendHistory(current.ID, llm.UserMessage("current in-memory conversation", nil))
	if err := a.cfg.Set("ELEPHANT", "on"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveTerminalSession(current.ID); err != nil {
		t.Fatal(err)
	}
	restarted := New(&fallbackProvider{}, a.cfg, "test", "", ModeLocal)
	active, err := restarted.OpenTerminalSession(testScope, "")
	if err != nil || active.ID != current.ID {
		t.Fatalf("did not resume enabled session: %+v, %v", active, err)
	}
}

func TestTerminalSessionListCanRecoverAroundCorruption(t *testing.T) {
	a := sessionAgent(t)
	bad, err := a.NewTerminalSession(testScope)
	if err != nil {
		t.Fatal(err)
	}
	good, err := a.NewTerminalSession(testScope)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := checkpointPath(a.cfg.Dir(), bad.ID)
	if err := os.WriteFile(path, []byte("corrupted checkpoint"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := a.TerminalSessions(testScope)
	if err == nil || len(list) != 1 || list[0].ID != good.ID {
		t.Fatalf("expected valid sessions and corruption warning: %+v, %v", list, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "corrupted checkpoint" {
		t.Fatal("corrupt checkpoint changed")
	}
}

func TestSessionTitleBoundedAndReadable(t *testing.T) {
	got := sessionTitle([]llm.Message{llm.UserMessage("  Fix\nUnicode "+strings.Repeat("界", 100), nil)})
	if !utf8.ValidString(got) || len([]rune(got)) > 81 || strings.Contains(got, "\n") || !strings.HasPrefix(got, "Fix Unicode") {
		t.Fatalf("bad title: %q", got)
	}
}

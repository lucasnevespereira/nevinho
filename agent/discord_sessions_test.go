package agent

import (
	"testing"

	"github.com/lucasnevespereira/nevinho/llm"
)

func TestDiscordSessionsMigrateLegacyAndRestoreSelection(t *testing.T) {
	a := sessionAgent(t)
	owner := "123456789"
	a.appendHistory(owner, llm.UserMessage("legacy Discord topic", nil), assistantMsg("done"))
	if err := a.saveCheckpoint(owner); err != nil {
		t.Fatal(err)
	}
	first, err := a.OpenDiscordSession(owner, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == owner || first.Title != "legacy Discord topic" {
		t.Fatalf("legacy conversation was not migrated: %+v", first)
	}
	if err := a.SaveTerminalSession(first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := a.NewDiscordSession(owner)
	if err != nil {
		t.Fatal(err)
	}
	a.appendHistory(second.ID, llm.UserMessage("new Discord topic", nil))
	if err := a.SaveTerminalSession(second.ID); err != nil {
		t.Fatal(err)
	}
	list, err := a.DiscordSessions(owner)
	if err != nil || len(list) != 2 {
		t.Fatalf("sessions = %+v, %v", list, err)
	}
	if _, err := a.OpenDiscordSession("another-owner", first.ID); err == nil {
		t.Fatal("another owner opened the conversation")
	}
	restarted := New(&fallbackProvider{}, a.cfg, "test", "", ModeDaemon)
	active, err := restarted.OpenDiscordSession(owner, "")
	if err != nil || active.ID != second.ID {
		t.Fatalf("restart selected %+v, %v", active, err)
	}
	if _, err := restarted.OpenDiscordSession(owner, first.ID); err != nil {
		t.Fatal(err)
	}
	if got := restarted.messages(first.ID); len(got) != 2 || got[0].Text != "legacy Discord topic" {
		t.Fatalf("restored history = %+v", got)
	}
}

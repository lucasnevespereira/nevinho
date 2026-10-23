package discord

import (
	"context"
	"testing"

	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/llm"
)

type sessionProvider struct{}

func (sessionProvider) Complete(context.Context, *llm.Request) (*llm.Response, error) {
	return &llm.Response{}, nil
}
func (sessionProvider) Model() string { return "fake" }

func TestChangeSessionBlocksActiveTurn(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New(sessionProvider{}, cfg, "test", "", agent.ModeDaemon)
	first, err := a.NewDiscordSession("owner")
	if err != nil {
		t.Fatal(err)
	}
	b := &Bot{agent: a, ownerID: "owner", activeID: first.ID}
	if _, ok := b.beginTurn(); !ok {
		t.Fatal("first turn should start")
	}
	if _, err := b.changeSession("", false); err == nil {
		t.Fatal("switched during an active response")
	}
	b.endTurn()
	if _, err := b.changeSession("", false); err != nil {
		t.Fatal(err)
	}
	if b.sessionID() == first.ID {
		t.Fatal("new conversation reused the previous identity")
	}
}

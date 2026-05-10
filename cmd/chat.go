package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/config"
)

const chatUserID = "cli-local"

// Chat starts a local REPL talking to the same agent core that Discord uses.
// Useful for debugging when Discord is misbehaving and you want to confirm
// the agent + LLM + tools chain works end to end.
//
//	nevinho chat       interactive REPL
//	/quit, /q, exit    leave
//	/forget            wipe REPL session history
//	/help              show in-REPL commands
func Chat(configDir, version, selfDoc string) {
	cfg, err := config.Load(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}
	provider := detectProvider(cfg)
	a := agent.New(provider, cfg, version, selfDoc)

	fmt.Printf("nevinho %s — chatting with %s\n", version, a.Model())
	fmt.Println("type /quit to exit, /forget to wipe history, /help for more")
	fmt.Println()

	in := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("> ")
		line, err := in.ReadString('\n')
		if err != nil {
			fmt.Println()
			return
		}
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}

		switch text {
		case "/quit", "/q", "exit", "quit":
			return
		case "/forget":
			a.ClearHistory(chatUserID)
			fmt.Println("(history cleared)")
			continue
		case "/help":
			fmt.Println("/quit, /q, exit  leave the REPL")
			fmt.Println("/forget          wipe history for this session")
			fmt.Println("/help            this help")
			continue
		}

		response, err := a.Chat(agent.ChatRequest{
			UserID: chatUserID,
			Text:   text,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		fmt.Printf("nevinho: %s\n\n", response)
	}
}

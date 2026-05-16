package cmd

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/tui"
)

// Chat launches the local terminal UI, talking to the same agent core the
// Discord transport uses. Tools run in strict mode since this is the
// user's own machine.
//
//	nevinho chat       terminal UI
func Chat(configDir, version, selfDoc string) {
	if !isTTY() {
		fmt.Fprintln(os.Stderr, "nevinho chat needs an interactive terminal")
		os.Exit(1)
	}

	cfg, err := config.Load(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}
	// Keep boot-time logger.Info lines out of the TUI. Route them to
	// chat.log if possible, otherwise drop them. The model is already in
	// the status bar so the user does not need the boot line on screen.
	if f, err := os.OpenFile(filepath.Join(configDir, "chat.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		log.SetOutput(f)
	} else {
		log.SetOutput(io.Discard)
	}

	provider := detectProvider(cfg)
	// Local mode: the agent knows it is in a terminal on the user's own
	// machine, which sets its prompt and gates every bash command behind
	// approval.
	a := agent.New(provider, cfg, version, selfDoc, agent.ModeLocal)

	cwd, _ := os.Getwd()
	if err := tui.Run(a, cwd, configDir); err != nil {
		fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		os.Exit(1)
	}
}

// isTTY reports whether stdout is an interactive terminal. The TUI needs
// one; without it (piped, redirected, CI) chat bails with a clear message
// instead of a raw terminal error.
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

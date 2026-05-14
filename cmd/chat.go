package cmd

import (
	"fmt"
	"os"

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
	provider := detectProvider(cfg)
	a := agent.New(provider, cfg, version, selfDoc)
	// Local chat runs on the user's own machine, so gate every bash command
	// behind approval rather than just the ones the heuristic flags.
	a.SetStrictTools(true)

	cwd, _ := os.Getwd()
	if err := tui.Run(a, cwd); err != nil {
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

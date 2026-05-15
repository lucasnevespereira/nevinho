package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/lucasnevespereira/nevinho/cmd"
	"github.com/lucasnevespereira/nevinho/config"
)

//go:embed NEVINHO.md
var selfDoc string

var version = "dev"

func main() {
	_ = godotenv.Load()

	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".nevinho")

	arg := ""
	if len(os.Args) > 1 {
		arg = os.Args[1]
	}

	switch arg {
	case "", "chat":
		// No argument launches the terminal UI, the local coding agent.
		cmd.Chat(configDir, version, selfDoc)
	case "setup":
		if err := config.RunSetup(configDir); err != nil {
			log.Fatal(err)
		}
	case "config":
		cmd.Config(configDir, os.Args[2:])
	case "start":
		cmd.Start(configDir, version, selfDoc)
	case "stop":
		cmd.Stop()
	case "logs":
		cmd.Logs(os.Args[2:])
	case "upgrade":
		cmd.Upgrade(version)
	case "uninstall":
		cmd.Uninstall(configDir, os.Args[2:])
	case "status":
		cmd.Status(version)
	case "version", "--version", "-v":
		cmd.Version(version)
	case "serve":
		cmd.Serve(configDir, version, selfDoc)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", arg)
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Println("nevinho " + version)
	fmt.Println()
	fmt.Println("Local. A coding agent in your terminal:")
	fmt.Println("  nevinho           open the terminal UI (same as 'chat')")
	fmt.Println()
	fmt.Println("VPS. An always-on agent reachable from Discord:")
	fmt.Println("  nevinho setup     configure providers, Discord, and voice")
	fmt.Println("  nevinho start     start the always-on bot")
	fmt.Println("  nevinho stop      stop the bot")
	fmt.Println("  nevinho status    check if the bot is running")
	fmt.Println("  nevinho logs      show live logs (--full, --last N)")
	fmt.Println()
	fmt.Println("Shared:")
	fmt.Println("  nevinho config    view, set, or delete config keys")
	fmt.Println("  nevinho upgrade   update to the latest version")
	fmt.Println("  nevinho uninstall remove service, binary, and ~/.nevinho")
	fmt.Println("  nevinho version   show version")
}

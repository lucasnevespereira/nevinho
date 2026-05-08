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
	case "status":
		cmd.Status(version)
	case "version":
		fmt.Println("nevinho " + version)
	case "serve":
		cmd.Serve(configDir, version, selfDoc)
	default:
		printUsage()
	}
}

func printUsage() {
	fmt.Println("nevinho " + version)
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  nevinho setup    configure Discord token and LLM keys")
	fmt.Println("  nevinho config   view, set, or delete config keys")
	fmt.Println("  nevinho start    start the bot")
	fmt.Println("  nevinho stop     stop the bot")
	fmt.Println("  nevinho logs     show live logs (--full, --last N)")
	fmt.Println("  nevinho upgrade  update to latest version")
	fmt.Println("  nevinho status   check if bot is running")
	fmt.Println("  nevinho version  show version")
}

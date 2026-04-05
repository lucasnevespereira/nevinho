package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/discord"
	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
)

var version = "dev"

func main() {
	_ = godotenv.Load()

	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".nevinho")

	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "setup":
		if err := config.RunSetup(configDir); err != nil {
			log.Fatal(err)
		}
	case "start":
		startService(configDir)
	case "stop":
		stopService()
	case "logs":
		showLogs()
	case "upgrade":
		upgrade()
	case "status":
		serviceStatus()
	case "version":
		fmt.Println("nevinho " + version)
	case "--run":
		run(configDir)
	default:
		printUsage()
	}
}

func run(configDir string) {
	logger.Init()

	cfg, err := config.Load(configDir)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if cfg.DiscordBotToken == "" {
		log.Fatal("DISCORD_BOT_TOKEN is required (run nevinho setup)")
	}
	if cfg.DiscordOwnerID == "" {
		log.Fatal("DISCORD_OWNER_ID is required (run nevinho setup)")
	}

	provider := detectProvider(cfg)

	a := agent.New(provider, cfg, version)
	bot, err := discord.New(cfg.DiscordBotToken, cfg.DiscordOwnerID, a, cfg)
	if err != nil {
		log.Fatalf("failed to create bot: %v", err)
	}

	if err := bot.Start(); err != nil {
		log.Fatalf("failed to start bot: %v", err)
	}

	logger.Info("nevinho is online")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	logger.Info("shutting down...")
	bot.Stop()
}

func detectProvider(cfg *config.Config) llm.Provider {
	pc := cfg.ProviderConfig()

	// Use saved model preference if available
	if cfg.Model != "" {
		if p, err := llm.Resolve(cfg.Model, pc); err == nil {
			logger.Info("provider: " + cfg.Model + " (saved)")
			return p
		}
	}

	switch {
	case cfg.OllamaModel != "":
		logger.Info("provider: ollama (" + cfg.OllamaModel + ")")
		return llm.NewOpenAI("", pc.OllamaURL, cfg.OllamaModel)
	case pc.AnthropicKey != "":
		logger.Info("provider: anthropic")
		return llm.NewAnthropic(pc.AnthropicKey, "", "")
	case pc.OpenAIKey != "":
		logger.Info("provider: openai")
		return llm.NewOpenAI(pc.OpenAIKey, "", "")
	default:
		log.Fatal("no LLM provider configured (run nevinho setup)")
		return nil
	}
}

func printUsage() {
	fmt.Println("nevinho " + version)
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  nevinho setup    configure Discord token and LLM keys")
	fmt.Println("  nevinho start    start the bot")
	fmt.Println("  nevinho stop     stop the bot")
	fmt.Println("  nevinho logs     show live logs")
	fmt.Println("  nevinho upgrade  update to latest version")
	fmt.Println("  nevinho status   check if bot is running")
	fmt.Println("  nevinho version  show version")
}

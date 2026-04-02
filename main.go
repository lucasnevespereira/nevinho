package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/auth"
	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/discord"
	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
)

func main() {
	_ = godotenv.Load()

	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".config", "nevinho")

	if len(os.Args) > 1 && os.Args[1] == "--setup" {
		runSetup(configDir)
		return
	}

	logger.Init()

	cfg, err := config.Load(configDir)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if cfg.DiscordBotToken == "" {
		log.Fatal("DISCORD_BOT_TOKEN is required (run nevinho --setup or set in .env)")
	}
	if cfg.DiscordOwnerID == "" {
		log.Fatal("DISCORD_OWNER_ID is required (run nevinho --setup or set in .env)")
	}

	providerCfg := buildProviderConfig(cfg)
	provider := detectProvider(cfg, providerCfg)

	creds, err := auth.NewStore(configDir)
	if err != nil {
		log.Fatalf("failed to init credentials: %v", err)
	}

	a := agent.New(provider, providerCfg, cfg)
	bot, err := discord.New(cfg.DiscordBotToken, cfg.DiscordOwnerID, a, creds, cfg)
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

func buildProviderConfig(cfg *config.Config) agent.ProviderConfig {
	pc := agent.ProviderConfig{
		AnthropicKey: cfg.AnthropicAPIKey,
		OpenAIKey:    cfg.OpenAIAPIKey,
	}
	if cfg.OllamaModel != "" {
		pc.OllamaURL = "http://localhost:11434"
	}
	return pc
}

func detectProvider(cfg *config.Config, pc agent.ProviderConfig) llm.Provider {
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
		log.Fatal("no LLM provider configured (run nevinho --setup or set keys in .env)")
		return nil
	}
}

func runSetup(configDir string) {
	fmt.Println("nevinho setup")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	prompt := func(label string) string {
		fmt.Printf("%s: ", label)
		scanner.Scan()
		return strings.TrimSpace(scanner.Text())
	}

	cfg, err := config.Load(configDir)
	if err != nil {
		log.Fatalf("failed to init config: %v", err)
	}

	cfg.DiscordBotToken = prompt("Discord bot token")
	cfg.DiscordOwnerID = prompt("Discord owner ID")

	fmt.Println()
	fmt.Println("LLM provider:")
	fmt.Println("  1) Anthropic (Claude)")
	fmt.Println("  2) OpenAI (GPT)")
	fmt.Println("  3) Ollama (local)")
	choice := prompt("Choice [1/2/3]")

	switch choice {
	case "1":
		cfg.AnthropicAPIKey = prompt("Anthropic API key")
	case "2":
		cfg.OpenAIAPIKey = prompt("OpenAI API key")
	case "3":
		cfg.OllamaModel = prompt("Ollama model name (e.g. llama3)")
	}

	brave := prompt("Brave Search API key (optional, press Enter to skip)")
	if brave != "" {
		cfg.BraveAPIKey = brave
	}

	if err := cfg.Save(); err != nil {
		log.Fatalf("failed to save config: %v", err)
	}

	fmt.Println()
	fmt.Printf("Config saved to %s\n", configDir)
	fmt.Println("Run 'nevinho' to start.")
}

package main

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/auth"
	"github.com/lucasnevespereira/nevinho/discord"
	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
)

func main() {
	_ = godotenv.Load()
	logger.Init()

	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_BOT_TOKEN is required")
	}

	ownerID := os.Getenv("DISCORD_OWNER_ID")
	if ownerID == "" {
		log.Fatal("DISCORD_OWNER_ID is required (your Discord user ID)")
	}

	config := agent.ProviderConfig{
		AnthropicKey: os.Getenv("ANTHROPIC_API_KEY"),
		OpenAIKey:    os.Getenv("OPENAI_API_KEY"),
	}
	if os.Getenv("OLLAMA_MODEL") != "" {
		config.OllamaURL = "http://localhost:11434"
	}

	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".config", "nevinho")
	creds, err := auth.NewStore(configDir)
	if err != nil {
		log.Fatalf("failed to init credentials: %v", err)
	}

	provider := detectProvider(config)
	a := agent.New(provider, config)
	bot, err := discord.New(token, ownerID, a, creds)
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

func detectProvider(config agent.ProviderConfig) llm.Provider {
	ollamaModel := os.Getenv("OLLAMA_MODEL")

	switch {
	case ollamaModel != "":
		logger.Info("provider: ollama (" + ollamaModel + ")")
		return llm.NewOpenAI("", config.OllamaURL, ollamaModel)
	case config.AnthropicKey != "":
		logger.Info("provider: anthropic")
		return llm.NewAnthropic(config.AnthropicKey, "", "")
	case config.OpenAIKey != "":
		logger.Info("provider: openai")
		return llm.NewOpenAI(config.OpenAIKey, "", "")
	default:
		log.Fatal("set ANTHROPIC_API_KEY, OPENAI_API_KEY, or OLLAMA_MODEL in .env")
		return nil
	}
}

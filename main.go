package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/lucasnevespereira/nevinho/agent"
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

	provider := detectProvider()
	a := agent.New(provider)
	bot, err := discord.New(token, ownerID, a)
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

func detectProvider() llm.Provider {
	ollamaModel := os.Getenv("OLLAMA_MODEL")
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	openaiKey := os.Getenv("OPENAI_API_KEY")

	switch {
	case ollamaModel != "":
		logger.Info("provider: ollama (" + ollamaModel + ")")
		return llm.NewOpenAI("", "http://localhost:11434", ollamaModel)
	case anthropicKey != "":
		logger.Info("provider: anthropic")
		return llm.NewAnthropic(anthropicKey, "", "")
	case openaiKey != "":
		logger.Info("provider: openai")
		return llm.NewOpenAI(openaiKey, "", "")
	default:
		log.Fatal("set ANTHROPIC_API_KEY, OPENAI_API_KEY, or OLLAMA_MODEL in .env")
		return nil
	}
}

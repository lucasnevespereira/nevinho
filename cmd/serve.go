package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/discord"
	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
)

// Serve is the bot main loop. On Linux, systemd invokes `nevinho serve`. On
// macOS it runs in the foreground when the user calls `nevinho start`.
func Serve(configDir, version, selfDoc string) {
	logger.Init()

	checkPID(configDir)

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

	a := agent.New(provider, cfg, version, selfDoc)
	bot, err := discord.New(cfg.DiscordBotToken, cfg.DiscordOwnerID, a, cfg)
	if err != nil {
		log.Fatalf("failed to create bot: %v", err)
	}

	if err := bot.Start(); err != nil {
		log.Fatalf("failed to start bot: %v", err)
	}

	writePID(configDir)

	logger.Info("nevinho is online")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	logger.Info("shutting down...")

	os.Remove(pidPath(configDir))

	persistCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	a.PersistAll(persistCtx)
	cancel()

	bot.Stop()
}

func pidPath(configDir string) string {
	return filepath.Join(configDir, "nevinho.pid")
}

func checkPID(configDir string) {
	data, err := os.ReadFile(pidPath(configDir))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if proc.Signal(syscall.Signal(0)) == nil {
		log.Fatalf("nevinho is already running (PID %d)", pid)
	}
}

func writePID(configDir string) {
	os.WriteFile(pidPath(configDir), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)
}

func detectProvider(cfg *config.Config) llm.Provider {
	pc := cfg.ProviderConfig()

	// Use saved model preference if available. On error (unknown model,
	// missing key) log the reason so the operator sees why nevinho fell
	// back to a different provider instead of silently picking one.
	if cfg.Model != "" {
		p, err := llm.Resolve(cfg.Model, pc)
		if err == nil {
			logger.Info("provider: " + cfg.Model + " (saved)")
			return p
		}
		logger.Info("saved model " + cfg.Model + " unusable: " + err.Error() + ", falling back")
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

package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lucasnevespereira/nevinho/voice"
)

func RunSetup(configDir string) error {
	fmt.Println("nevinho setup")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	prompt := func(label, current string) string {
		if current != "" {
			fmt.Printf("%s [%s]: ", label, maskSecret(current))
		} else {
			fmt.Printf("%s: ", label)
		}
		scanner.Scan()
		val := strings.TrimSpace(scanner.Text())
		if val == "" {
			return current
		}
		return val
	}

	confirm := func(label string) bool {
		fmt.Printf("%s [Y/n]: ", label)
		scanner.Scan()
		val := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return val == "" || val == "y" || val == "yes"
	}

	cfg, err := Load(configDir)
	if err != nil {
		return fmt.Errorf("failed to init config: %w", err)
	}

	// Discord
	fmt.Println("Discord")
	cfg.DiscordBotToken = prompt("  Bot token", cfg.DiscordBotToken)
	cfg.DiscordOwnerID = prompt("  Owner ID", cfg.DiscordOwnerID)

	// LLM provider
	fmt.Println()
	fmt.Println("LLM provider (press Enter to skip)")
	cfg.AnthropicAPIKey = prompt("  Anthropic API key", cfg.AnthropicAPIKey)
	cfg.OpenAIAPIKey = prompt("  OpenAI API key", cfg.OpenAIAPIKey)
	cfg.OllamaModel = prompt("  Ollama model (e.g. llama3)", cfg.OllamaModel)

	// Optional keys
	fmt.Println()
	fmt.Println("Optional")
	cfg.BraveAPIKey = prompt("  Brave Search API key", cfg.BraveAPIKey)

	// Voice messages
	fmt.Println()
	whisperDir := filepath.Join(configDir, "whisper")
	if voice.IsAvailable(whisperDir) {
		fmt.Println("Voice messages: enabled")
	} else if confirm("Enable voice messages?") {
		if err := voice.Setup(whisperDir); err != nil {
			fmt.Printf("Voice setup failed: %v\n", err)
			fmt.Println("You can retry later with 'nevinho setup'.")
		}
	}

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println()
	fmt.Printf("Config saved to %s\n", configDir)
	fmt.Println("Run 'nevinho start' to start.")
	return nil
}

// maskSecret shows first 4 and last 4 chars, masks the rest.
func maskSecret(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

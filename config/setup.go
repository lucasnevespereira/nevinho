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

	// Enter keeps current value, "-" clears it
	prompt := func(label, current string) string {
		if current != "" {
			fmt.Printf("  %s [%s]: ", label, maskSecret(current))
		} else {
			fmt.Printf("  %s: ", label)
		}
		scanner.Scan()
		val := strings.TrimSpace(scanner.Text())
		if val == "-" {
			return ""
		}
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

	// Show hint only on re-run (when some values already exist)
	if cfg.DiscordBotToken != "" || cfg.AnthropicAPIKey != "" || cfg.OpenAIAPIKey != "" {
		fmt.Println("Press Enter to keep current value, - to clear.")
		fmt.Println()
	}

	// Discord
	fmt.Println("Discord")
	cfg.DiscordBotToken = prompt("Bot token", cfg.DiscordBotToken)
	cfg.DiscordOwnerID = prompt("Your Discord user ID", cfg.DiscordOwnerID)

	// LLM providers
	fmt.Println()
	fmt.Println("LLM providers")
	cfg.AnthropicAPIKey = prompt("Anthropic API key", cfg.AnthropicAPIKey)
	cfg.OpenAIAPIKey = prompt("OpenAI API key", cfg.OpenAIAPIKey)
	cfg.OllamaModel = prompt("Ollama model (e.g. llama3)", cfg.OllamaModel)

	// Warn if no provider
	if cfg.AnthropicAPIKey == "" && cfg.OpenAIAPIKey == "" && cfg.OllamaModel == "" {
		fmt.Println()
		fmt.Println("  Warning: no LLM provider configured. nevinho needs at least one.")
	}

	// Optional
	fmt.Println()
	fmt.Println("Optional")
	cfg.TavilyAPIKey = prompt("Tavily Search API key", cfg.TavilyAPIKey)

	// Voice messages
	fmt.Println()
	fmt.Println("Voice messages")
	whisperDir := filepath.Join(configDir, "whisper")
	if voice.IsAvailable(whisperDir) {
		fmt.Println("  Enabled.")
	} else if confirm("  Enable voice messages?") {
		if err := voice.Setup(whisperDir); err != nil {
			fmt.Printf("Voice setup failed: %v\n", err)
			fmt.Println("You can retry later with 'nevinho setup'.")
		}
	}

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	// Summary
	fmt.Println()
	fmt.Println("Configuration")
	printStatus("  Discord", cfg.DiscordBotToken != "" && cfg.DiscordOwnerID != "")
	printStatus("  Anthropic", cfg.AnthropicAPIKey != "")
	printStatus("  OpenAI", cfg.OpenAIAPIKey != "")
	printStatus("  Ollama", cfg.OllamaModel != "")
	printStatus("  Tavily Search", cfg.TavilyAPIKey != "")
	printStatus("  Voice", voice.IsAvailable(whisperDir))

	fmt.Println()
	fmt.Printf("Saved to %s\n", configDir)
	fmt.Println("Run 'nevinho start' to start.")
	return nil
}

func maskSecret(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

func printStatus(label string, ok bool) {
	if ok {
		fmt.Printf("%s: yes\n", label)
	} else {
		fmt.Printf("%s: no\n", label)
	}
}

package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func RunSetup(configDir string) error {
	fmt.Println("nevinho setup")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	prompt := func(label string) string {
		fmt.Printf("%s: ", label)
		scanner.Scan()
		return strings.TrimSpace(scanner.Text())
	}

	cfg, err := Load(configDir)
	if err != nil {
		return fmt.Errorf("failed to init config: %w", err)
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
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println()
	fmt.Printf("Config saved to %s\n", configDir)
	fmt.Println("Run 'nevinho' to start.")
	return nil
}

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
	fmt.Println("LLM providers (configure at least one)")
	cfg.AnthropicAPIKey = prompt("Anthropic API key", cfg.AnthropicAPIKey)
	cfg.OpenAIAPIKey = prompt("OpenAI API key", cfg.OpenAIAPIKey)
	cfg.GeminiAPIKey = prompt("Gemini API key", cfg.GeminiAPIKey)
	cfg.GroqAPIKey = prompt("Groq API key", cfg.GroqAPIKey)
	cfg.OpenRouterAPIKey = prompt("OpenRouter API key", cfg.OpenRouterAPIKey)
	cfg.OllamaModel = prompt("Ollama model (e.g. llama3)", cfg.OllamaModel)

	if cfg.AnthropicAPIKey == "" && cfg.OpenAIAPIKey == "" && cfg.OllamaModel == "" &&
		cfg.GeminiAPIKey == "" && cfg.GroqAPIKey == "" && cfg.OpenRouterAPIKey == "" {
		fmt.Println()
		fmt.Println("  Warning: no LLM provider configured. nevinho needs at least one.")
		fmt.Println("  See README for signup URLs.")
	}

	// Default model. Suggest one based on the first configured provider so
	// reinstall does not carry over a stale or invalid saved name.
	if cfg.AnthropicAPIKey != "" || cfg.OpenAIAPIKey != "" || cfg.OllamaModel != "" ||
		cfg.GeminiAPIKey != "" || cfg.GroqAPIKey != "" || cfg.OpenRouterAPIKey != "" {
		fmt.Println()
		fmt.Println("Default model")
		suggested := cfg.Model
		if suggested == "" {
			suggested = suggestDefaultModel(cfg)
		}
		cfg.Model = prompt("Model name", suggested)
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
	printStatus("  Gemini", cfg.GeminiAPIKey != "")
	printStatus("  Groq", cfg.GroqAPIKey != "")
	printStatus("  OpenRouter", cfg.OpenRouterAPIKey != "")
	printStatus("  Ollama", cfg.OllamaModel != "")
	printStatus("  Tavily Search", cfg.TavilyAPIKey != "")
	printStatus("  Voice", voice.IsAvailable(whisperDir))
	if cfg.Model != "" {
		fmt.Printf("  Model: %s\n", cfg.Model)
	}

	fmt.Println()
	fmt.Printf("Saved to %s\n", configDir)
	fmt.Println("Run 'nevinho start' to start.")
	return nil
}

// suggestDefaultModel picks a sensible model name from the providers the
// user just configured. Free providers come first when no paid key is
// set so the bot starts on a no cost path. Order beyond that is
// preference for general quality at a reasonable price.
func suggestDefaultModel(cfg *Config) string {
	switch {
	case cfg.AnthropicAPIKey != "":
		return "claude-haiku-4-5"
	case cfg.OpenAIAPIKey != "":
		return "gpt-4o-mini"
	case cfg.GeminiAPIKey != "":
		return "gemini-2.0-flash"
	case cfg.GroqAPIKey != "":
		return "groq:llama-3.3-70b-versatile"
	case cfg.OpenRouterAPIKey != "":
		return "openrouter:meta-llama/llama-3.3-70b-instruct:free"
	case cfg.OllamaModel != "":
		return cfg.OllamaModel
	}
	return ""
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

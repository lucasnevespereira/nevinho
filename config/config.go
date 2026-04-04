package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/lucasnevespereira/nevinho/crypto"
)

type Config struct {
	mu       sync.RWMutex
	dir      string
	key      [32]byte
	filePath string

	DiscordBotToken string `json:"discord_bot_token"`
	DiscordOwnerID  string `json:"discord_owner_id"`
	AnthropicAPIKey string `json:"anthropic_api_key"`
	OpenAIAPIKey    string `json:"openai_api_key"`
	OllamaModel     string `json:"ollama_model"`
	BraveAPIKey     string `json:"brave_api_key"`
}

// keymap maps user-facing key names to struct field pointers.
func (c *Config) keymap() map[string]*string {
	return map[string]*string{
		"DISCORD_BOT_TOKEN": &c.DiscordBotToken,
		"DISCORD_OWNER_ID":  &c.DiscordOwnerID,
		"ANTHROPIC_API_KEY": &c.AnthropicAPIKey,
		"OPENAI_API_KEY":    &c.OpenAIAPIKey,
		"OLLAMA_MODEL":      &c.OllamaModel,
		"BRAVE_API_KEY":     &c.BraveAPIKey,
	}
}

type KeyStatus struct {
	Name   string
	Set    bool
	Source string // "config" or "env"
}

func Load(configDir string) (*Config, error) {
	key, err := crypto.LoadOrCreateKey(configDir)
	if err != nil {
		return nil, fmt.Errorf("config key: %w", err)
	}

	c := &Config{
		dir:      configDir,
		key:      key,
		filePath: filepath.Join(configDir, "config.enc"),
	}

	// Load encrypted config if it exists
	if data, err := os.ReadFile(c.filePath); err == nil {
		if plain, err := crypto.Decrypt(c.key, data); err == nil {
			json.Unmarshal(plain, c)
		}
	}

	// Env vars override encrypted config (per-key)
	for envKey, field := range c.keymap() {
		if val := os.Getenv(envKey); val != "" {
			*field = val
		}
	}

	return c, nil
}

func (c *Config) Save() error {
	c.mu.RLock()
	plain, err := json.Marshal(c)
	c.mu.RUnlock()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(c.dir, 0755); err != nil {
		return err
	}

	enc, err := crypto.Encrypt(c.key, plain)
	if err != nil {
		return err
	}
	return os.WriteFile(c.filePath, enc, 0600)
}

func (c *Config) Get(key string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	field, ok := c.keymap()[key]
	if !ok {
		return "", fmt.Errorf("unknown key: %s", key)
	}
	return *field, nil
}

func (c *Config) Set(key, value string) error {
	c.mu.Lock()
	field, ok := c.keymap()[key]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("unknown key: %s", key)
	}
	*field = value
	c.mu.Unlock()
	return c.Save()
}

func (c *Config) Delete(key string) error {
	return c.Set(key, "")
}

func (c *Config) Keys() []KeyStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	order := []string{
		"DISCORD_BOT_TOKEN", "DISCORD_OWNER_ID",
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OLLAMA_MODEL",
		"BRAVE_API_KEY",
	}

	km := c.keymap()
	var out []KeyStatus
	for _, name := range order {
		field := km[name]
		status := KeyStatus{Name: name, Set: *field != ""}
		if status.Set && os.Getenv(name) != "" {
			status.Source = "env"
		} else if status.Set {
			status.Source = "config"
		}
		out = append(out, status)
	}
	return out
}

type ProviderConfig struct {
	AnthropicKey string
	OpenAIKey    string
	OllamaURL    string
}

func (c *Config) ProviderConfig() ProviderConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	pc := ProviderConfig{
		AnthropicKey: c.AnthropicAPIKey,
		OpenAIKey:    c.OpenAIAPIKey,
	}
	if c.OllamaModel != "" {
		pc.OllamaURL = "http://localhost:11434"
	}
	return pc
}

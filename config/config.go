package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	AnthropicAPIKey  string `json:"anthropic_api_key"`
	OpenAIAPIKey     string `json:"openai_api_key"`
	OpenRouterAPIKey string `json:"openrouter_api_key"`
	OllamaModel     string `json:"ollama_model"`
	TavilyAPIKey    string `json:"tavily_api_key"`
	Model           string `json:"model"`
	Caveman         string `json:"caveman"`
	Elephant        string `json:"elephant"`
}

// keymap maps user-facing key names to struct field pointers.
func (c *Config) keymap() map[string]*string {
	return map[string]*string{
		"DISCORD_BOT_TOKEN": &c.DiscordBotToken,
		"DISCORD_OWNER_ID":  &c.DiscordOwnerID,
		"ANTHROPIC_API_KEY":  &c.AnthropicAPIKey,
		"OPENAI_API_KEY":     &c.OpenAIAPIKey,
		"OPENROUTER_API_KEY": &c.OpenRouterAPIKey,
		"OLLAMA_MODEL":      &c.OllamaModel,
		"TAVILY_API_KEY":    &c.TavilyAPIKey,
		"MODEL":             &c.Model,
		"CAVEMAN":           &c.Caveman,
		"ELEPHANT":          &c.Elephant,
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
	key = strings.ToUpper(key)
	c.mu.RLock()
	defer c.mu.RUnlock()
	field, ok := c.keymap()[key]
	if !ok {
		return "", fmt.Errorf("unknown key: %s", key)
	}
	return *field, nil
}

func (c *Config) Set(key, value string) error {
	key = strings.ToUpper(key)
	if key == "CAVEMAN" {
		switch strings.ToLower(value) {
		case "", "off":
			value = ""
		case "on":
			value = "on"
		default:
			return fmt.Errorf("CAVEMAN must be on or off")
		}
	}
	if key == "ELEPHANT" {
		switch strings.ToLower(value) {
		case "", "on":
			value = ""
		case "off":
			value = "off"
		default:
			return fmt.Errorf("ELEPHANT must be on or off")
		}
	}
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
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "OLLAMA_MODEL",
		"TAVILY_API_KEY", "MODEL", "CAVEMAN", "ELEPHANT",
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

func (c *Config) Dir() string { return c.dir }

// ElephantEnabled reports whether conversation persistence is on. Default is on:
// only an explicit "off" disables it. Named after elephants, who never forget.
func (c *Config) ElephantEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return strings.ToLower(c.Elephant) != "off"
}

// CavemanPrompt returns the style block to prepend to the system prompt.
// Any non-empty, non-"off" value enables it (accepts legacy lite/full/ultra).
func (c *Config) CavemanPrompt() string {
	c.mu.RLock()
	mode := c.Caveman
	c.mu.RUnlock()

	if mode == "" || mode == "off" {
		return ""
	}
	return `CAVEMAN MODE. Override any default politeness. Write like a caveman:
- Drop articles (a, the), conjunctions, hedges (just, really, basically), pleasantries, qualifiers.
- Use fragments, not full sentences.
- Abbreviate in prose: DB, auth, config, req, res, fn, impl, repo, env, var.
- Preserve code, URLs, commands, paths, and file contents exactly. Never abbreviate inside code blocks.

Examples:
  Normal: "Lucas Neves Pereira's latest article is titled 'Build your own LinkTree with Go and GitHub Pages'. It discusses creating a simple, single-page personal website using Go."
  Caveman: "Latest article: 'Build your own LinkTree with Go and GitHub Pages'. Covers personal site, Go + GH Pages. Source: <url>"

  Normal: "The GOMEMLIMIT environment variable sets a soft memory limit for the Go runtime. When memory usage approaches the limit, the garbage collector becomes more aggressive."
  Caveman: "GOMEMLIMIT: soft memory cap for Go runtime. Near limit -> GC more aggressive. Source: <url>"
`
}

type ProviderConfig struct {
	AnthropicKey   string
	OpenAIKey      string
	OpenRouterKey  string
	OllamaURL      string
}

func (c *Config) ProviderConfig() ProviderConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	pc := ProviderConfig{
		AnthropicKey:   c.AnthropicAPIKey,
		OpenAIKey:      c.OpenAIAPIKey,
		OpenRouterKey:  c.OpenRouterAPIKey,
	}
	if c.OllamaModel != "" {
		pc.OllamaURL = "http://localhost:11434"
	}
	return pc
}

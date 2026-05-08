package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/lucasnevespereira/nevinho/config"
)

// ConfigCmd dispatches `nevinho config ...` subcommands.
//
//	nevinho config                  list all keys
//	nevinho config get KEY          print one value, masked if secret
//	nevinho config set KEY VALUE    set a value
//	nevinho config delete KEY       clear a value
func ConfigCmd(configDir string, args []string) {
	cfg, err := config.Load(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	if len(args) == 0 {
		printConfigList(cfg)
		return
	}

	switch args[0] {
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: nevinho config get KEY")
			os.Exit(2)
		}
		printConfigValue(cfg, args[1])
	case "set":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: nevinho config set KEY VALUE")
			os.Exit(2)
		}
		key := args[1]
		value := strings.Join(args[2:], " ")
		if err := cfg.Set(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("set %s\n", key)
	case "delete", "clear", "unset":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: nevinho config delete KEY")
			os.Exit(2)
		}
		key := args[1]
		if err := cfg.Delete(key); err != nil {
			fmt.Fprintf(os.Stderr, "failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("cleared %s\n", key)
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand. Use: get, set, delete, or no args to list.")
		os.Exit(2)
	}
}

func printConfigList(cfg *config.Config) {
	keys := cfg.Keys()
	for _, k := range keys {
		if !k.Set {
			fmt.Printf("  %-22s  not set\n", k.Name)
			continue
		}
		if isSecretKey(k.Name) {
			fmt.Printf("  %-22s  set (%s)\n", k.Name, k.Source)
			continue
		}
		val, _ := cfg.Get(k.Name)
		fmt.Printf("  %-22s  %s (%s)\n", k.Name, val, k.Source)
	}
}

func printConfigValue(cfg *config.Config, key string) {
	val, err := cfg.Get(key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if val == "" {
		fmt.Println("(unset)")
		return
	}
	if isSecretKey(key) {
		fmt.Println(maskSecret(val))
		return
	}
	fmt.Println(val)
}

func isSecretKey(key string) bool {
	switch key {
	case "DISCORD_BOT_TOKEN",
		"ANTHROPIC_API_KEY",
		"OPENAI_API_KEY",
		"OPENROUTER_API_KEY",
		"TAVILY_API_KEY":
		return true
	}
	return false
}

// maskSecret keeps a 4 char prefix and 4 char suffix so the operator can
// recognize which value is stored without revealing it.
func maskSecret(v string) string {
	if len(v) <= 8 {
		return strings.Repeat("*", len(v))
	}
	return v[:4] + "..." + v[len(v)-4:]
}

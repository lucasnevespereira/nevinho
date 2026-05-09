package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/lucasnevespereira/nevinho/config"
)

// Config dispatches `nevinho config ...` subcommands.
//
//	nevinho config                       list all keys
//	nevinho config get KEY               print one value, masked if secret
//	nevinho config get KEY --reveal      print one value unmasked
//	nevinho config set KEY VALUE         set a value
//	nevinho config delete KEY            clear a value
func Config(configDir string, args []string) {
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
	case "help", "-h", "--help":
		printConfigUsage(os.Stdout)
	case "get":
		if len(args) < 2 {
			printConfigUsage(os.Stderr)
			os.Exit(2)
		}
		reveal := false
		for _, a := range args[2:] {
			if a == "--reveal" {
				reveal = true
			}
		}
		printConfigValue(cfg, args[1], reveal)
	case "set":
		if len(args) < 3 {
			printConfigUsage(os.Stderr)
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
			printConfigUsage(os.Stderr)
			os.Exit(2)
		}
		key := args[1]
		if err := cfg.Delete(key); err != nil {
			fmt.Fprintf(os.Stderr, "failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("cleared %s\n", key)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", args[0])
		printConfigUsage(os.Stderr)
		os.Exit(2)
	}
}

func printConfigUsage(w *os.File) {
	fmt.Fprintln(w, "Usage: nevinho config [SUBCOMMAND]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  (none)                  list all keys")
	fmt.Fprintln(w, "  get KEY [--reveal]      print one value, masked unless --reveal")
	fmt.Fprintln(w, "  set KEY VALUE           set a value")
	fmt.Fprintln(w, "  delete KEY              clear a value")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Known keys:")
	fmt.Fprintln(w, "  DISCORD_BOT_TOKEN, DISCORD_OWNER_ID")
	fmt.Fprintln(w, "  ANTHROPIC_API_KEY, OPENAI_API_KEY, OPENROUTER_API_KEY")
	fmt.Fprintln(w, "  OLLAMA_MODEL, TAVILY_API_KEY")
	fmt.Fprintln(w, "  MODEL, CAVEMAN, ELEPHANT")
}

func printConfigList(cfg *config.Config) {
	keys := cfg.Keys()
	for _, k := range keys {
		if def, ok := defaultedToggle(k.Name); ok && !k.Set {
			fmt.Printf("  %-22s  %s (default)\n", k.Name, def)
			continue
		}
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

// defaultedToggle returns the effective default value for keys whose
// semantics treat unset as "use the default". Lets `nevinho config`
// show CAVEMAN/ELEPHANT as "on (default)" or "off (default)" instead
// of the misleading "not set".
func defaultedToggle(key string) (string, bool) {
	switch key {
	case "ELEPHANT":
		return "on", true
	case "CAVEMAN":
		return "off", true
	}
	return "", false
}

func printConfigValue(cfg *config.Config, key string, reveal bool) {
	val, err := cfg.Get(key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if val == "" {
		if def, ok := defaultedToggle(strings.ToUpper(key)); ok {
			fmt.Printf("%s (default)\n", def)
			return
		}
		fmt.Println("(unset)")
		return
	}
	if isSecretKey(key) && !reveal {
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

package discord

import (
	"fmt"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/lucasnevespereira/nevinho/config"
)

func TestModelPickerKeepsSourcesAndDiscordLimits(t *testing.T) {
	var models []string
	for _, source := range []string{"anthropic", "openai", "gemini", "groq", "openrouter"} {
		models = append(models, config.KnownModels[source]...)
	}
	for i := 0; i < 30; i++ {
		models = append(models, fmt.Sprintf("chatgpt:model-%d", i))
	}
	sources := modelSourceOptions(models)
	if len(sources) != 6 {
		t.Fatalf("source count = %d, want 6", len(sources))
	}
	if sources[1].Label != "OpenAI API key" || sources[5].Label != "OpenAI · ChatGPT plan" {
		t.Fatalf("source labels = %v", sources)
	}
	rows := modelChoiceComponents(models, "OpenAI · ChatGPT plan", "chatgpt:model-27")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	count, defaults := 0, 0
	for i, row := range rows {
		menu := row.(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
		if menu.CustomID != fmt.Sprintf("model_select:%d", i) || len(menu.Options) > 25 {
			t.Fatalf("row %d: id=%s options=%d", i, menu.CustomID, len(menu.Options))
		}
		for _, option := range menu.Options {
			count++
			if option.Default {
				defaults++
			}
			if option.Value == "chatgpt:model-27" && option.Label != "model-27" {
				t.Fatalf("label = %q", option.Label)
			}
		}
	}
	if count != 30 || defaults != 1 {
		t.Fatalf("count=%d defaults=%d", count, defaults)
	}
}

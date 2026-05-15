package tui

import (
	"strings"

	"github.com/lucasnevespereira/nevinho/config"
)

// selectorItem is one row of a selector: what shows, what it resolves to
// when chosen, and whether it carries the ✓ mark (the active/current one).
type selectorItem struct {
	label  string
	value  string
	marked bool
}

// selector is a filterable single-pick list. It backs both the model
// picker and the config picker; the model owns one while it is selecting.
type selector struct {
	title  string
	items  []selectorItem
	filter string
	cursor int
}

func newSelector(title string, items []selectorItem) selector {
	s := selector{title: title, items: items}
	for i, it := range items {
		if it.marked {
			s.cursor = i
			break
		}
	}
	return s
}

// visible returns the items matching the current filter.
func (s selector) visible() []selectorItem {
	if s.filter == "" {
		return s.items
	}
	f := strings.ToLower(s.filter)
	var out []selectorItem
	for _, it := range s.items {
		if strings.Contains(strings.ToLower(it.label), f) {
			out = append(out, it)
		}
	}
	return out
}

// update applies one keypress. It returns the updated selector, the chosen
// value (empty unless one was picked this press), and whether the selector
// stays open.
func (s selector) update(key string) (next selector, chosen string, open bool) {
	switch key {
	case "esc":
		return s, "", false
	case "enter":
		vis := s.visible()
		if s.cursor < len(vis) {
			return s, vis[s.cursor].value, false
		}
	case "up", "ctrl+p":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "ctrl+n":
		if s.cursor < len(s.visible())-1 {
			s.cursor++
		}
	case "backspace":
		if s.filter != "" {
			s.filter = s.filter[:len(s.filter)-1]
			s.cursor = 0
		}
	default:
		if len(key) == 1 { // a printable character extends the filter
			s.filter += key
			s.cursor = 0
		}
	}
	return s, "", true
}

// view renders the picker: a help line, the current filter, and the list
// with → on the cursor and ✓ on the marked item.
func (s selector) view() string {
	var b strings.Builder
	b.WriteString(styleHint.Render(s.title + " — type to filter · ↑↓ move · enter select · esc cancel"))
	b.WriteString("\n")
	if s.filter != "" {
		b.WriteString(styleToolHead.Render("filter ") + s.filter)
	}
	b.WriteString("\n")

	vis := s.visible()
	if len(vis) == 0 {
		b.WriteString(styleHint.Render("  no match"))
		return b.String()
	}
	for i, it := range vis {
		row := "  " + it.label
		if i == s.cursor {
			row = styleSelected.Render("→ " + it.label)
		}
		if it.marked {
			row += styleHint.Render("  ✓")
		}
		b.WriteString(row + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// modelItems builds selector rows for the /model picker.
func modelItems(models []string, current string) []selectorItem {
	items := make([]selectorItem, len(models))
	for i, name := range models {
		items[i] = selectorItem{label: name, value: name, marked: name == current}
	}
	return items
}

// configLabels maps config keys to human labels for the /config picker.
// Keys absent here are not offered in the TUI: Discord keys belong to
// `nevinho setup`, and MODEL has its own /model picker.
var configLabels = map[string]string{
	"ANTHROPIC_API_KEY":  "Anthropic API key",
	"OPENAI_API_KEY":     "OpenAI API key",
	"GEMINI_API_KEY":     "Gemini API key",
	"GROQ_API_KEY":       "Groq API key",
	"OPENROUTER_API_KEY": "OpenRouter API key",
	"OLLAMA_MODEL":       "Ollama model",
	"TAVILY_API_KEY":     "Tavily search key",
	"CAVEMAN":            "Caveman mode (on / off)",
	"ELEPHANT":           "Conversation memory (on / off)",
}

// configItems builds selector rows for the /config picker, labeled and
// marked with ✓ when already set.
func configItems(keys []config.KeyStatus) []selectorItem {
	var items []selectorItem
	for _, k := range keys {
		label, ok := configLabels[k.Name]
		if !ok {
			continue
		}
		items = append(items, selectorItem{label: label, value: k.Name, marked: k.Set})
	}
	return items
}

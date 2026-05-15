package tui

import (
	"strings"

	"github.com/lucasnevespereira/nevinho/config"
)

// selectorItem is one row of a selector: what shows, what it resolves to
// when chosen, and whether it carries the ✓ mark (the active/current one).
// toggle items flip on/off in place via space. non-toggle items pick on
// enter.
type selectorItem struct {
	label  string
	value  string
	marked bool
	toggle bool
}

// selector is a filterable single-pick list. It backs both the model
// picker and the config picker. The model owns one while it is selecting.
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
	case " ":
		// Space flips a toggle item in place (selector stays open). For
		// other items it just extends the filter, like any printable.
		vis := s.visible()
		if s.cursor < len(vis) && vis[s.cursor].toggle {
			return s, vis[s.cursor].value, true
		}
		s.filter += " "
		s.cursor = 0
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
// with → on the cursor, ✓ on set items, and on/off on toggle items.
func (s selector) view() string {
	var b strings.Builder
	b.WriteString(styleHint.Render(s.title + " · type to filter · ↑↓ move · enter select · space toggle · esc cancel"))
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
		prefix := "  "
		row := prefix + it.label
		if i == s.cursor {
			row = styleSelected.Render("→ " + it.label)
		}
		switch {
		case it.toggle:
			state := "off"
			if it.marked {
				state = "on"
			}
			row += "  " + styleHint.Render(state)
		case it.marked:
			row += "  " + styleHint.Render("✓")
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

// pathItems builds selector rows for the /paths picker. Every row is
// marked (all listed paths are currently approved). enter on a row will
// revoke it.
func pathItems(paths []string) []selectorItem {
	items := make([]selectorItem, len(paths))
	for i, p := range paths {
		items[i] = selectorItem{label: p, value: p, marked: true}
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
	"CAVEMAN":            "Caveman mode",
	"ELEPHANT":           "Elephant (session memory)",
}

// configToggles is the set of config keys that are on/off booleans, so the
// selector lets the user flip them with space instead of typing "on"/"off".
var configToggles = map[string]bool{
	"CAVEMAN":  true,
	"ELEPHANT": true,
}

// configItems builds selector rows for the /config picker. getValue reads
// each toggle key's current value so its ✓ mark reflects reality.
func configItems(keys []config.KeyStatus, getValue func(string) string) []selectorItem {
	var items []selectorItem
	for _, k := range keys {
		label, ok := configLabels[k.Name]
		if !ok {
			continue
		}
		toggle := configToggles[k.Name]
		marked := k.Set
		if toggle {
			marked = getValue(k.Name) == "on"
		}
		items = append(items, selectorItem{label: label, value: k.Name, marked: marked, toggle: toggle})
	}
	return items
}

package tui

import "strings"

// modelSelector is the interactive model picker shown when /model is run
// without an argument. The model owns one while it is selecting.
type modelSelector struct {
	models  []string // every available model, unfiltered
	current string   // the model in use, marked with a check
	filter  string
	cursor  int // index into the filtered list
}

func newModelSelector(models []string, current string) modelSelector {
	s := modelSelector{models: models, current: current}
	for i, m := range models {
		if m == current {
			s.cursor = i
			break
		}
	}
	return s
}

// visible returns the models matching the current filter.
func (s modelSelector) visible() []string {
	if s.filter == "" {
		return s.models
	}
	f := strings.ToLower(s.filter)
	var out []string
	for _, m := range s.models {
		if strings.Contains(strings.ToLower(m), f) {
			out = append(out, m)
		}
	}
	return out
}

// update applies one keypress. It returns the updated selector, the chosen
// model (empty unless one was picked this press), and whether the selector
// stays open.
func (s modelSelector) update(key string) (next modelSelector, chosen string, open bool) {
	switch key {
	case "esc":
		return s, "", false
	case "enter":
		vis := s.visible()
		if s.cursor < len(vis) {
			return s, vis[s.cursor], false
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
// with → on the cursor and ✓ on the model currently in use.
func (s modelSelector) view() string {
	var b strings.Builder
	b.WriteString(styleHint.Render("pick a model — type to filter · ↑↓ move · enter select · esc cancel"))
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
	for i, m := range vis {
		row := "  " + m
		if i == s.cursor {
			row = styleSelected.Render("→ " + m)
		}
		if m == s.current {
			row += styleHint.Render("  ✓")
		}
		b.WriteString(row + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

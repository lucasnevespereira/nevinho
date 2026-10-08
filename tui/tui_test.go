package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// A resize reprints the transcript only once the size has settled, and
// every block is rendered at the new width.
func TestResizeReflowsTranscript(t *testing.T) {
	m := newModel(nil, nil, nil, "")
	m.width, m.ready = 40, true
	m.printBlock(userBlock{"hello"})

	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	next, _ = next.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = next.(model)

	if _, cmd := m.Update(reflowMsg(1)); cmd != nil {
		t.Fatal("stale reflow should be ignored")
	}
	if _, cmd := m.Update(reflowMsg(2)); cmd == nil {
		t.Fatal("settled resize should reflow")
	}
	for _, ln := range strings.Split((*m.blocks)[0].render(m.contentWidth()), "\n") {
		if w := lipgloss.Width(ln); w != 80 {
			t.Fatalf("block row is %d wide, want 80", w)
		}
	}
}

// Agent prose wraps at the same column as a tool card and never runs
// wider than the terminal.
func TestProseMatchesCardWidth(t *testing.T) {
	const w = 60
	words := strings.TrimSpace(strings.Repeat("word ", 40))

	perRow := func(out string) int {
		n := 0
		for _, ln := range strings.Split(out, "\n") {
			if lipgloss.Width(ln) > w {
				t.Fatalf("row is %d wide, terminal is %d", lipgloss.Width(ln), w)
			}
			n = max(n, strings.Count(ln, "word"))
		}
		return n
	}
	prose := perRow(agentBlock{words}.render(w))
	card := perRow(toolBlock{name: "bash", output: words}.render(w))
	if prose != card {
		t.Fatalf("prose fits %d words per row, card fits %d", prose, card)
	}
}

func TestApprovalPickerStaysOneRow(t *testing.T) {
	for _, w := range []int{30, 120} {
		m := newModel(nil, nil, nil, "")
		m.width = w
		// One content row plus the top and bottom rules.
		if rows := lipgloss.Height(m.approvalPicker()); rows != 3 {
			t.Fatalf("width %d: picker is %d rows, want 3", w, rows)
		}
	}
}

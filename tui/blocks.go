package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
)

// cardPreviewLines caps how many lines of tool output a card shows.
const cardPreviewLines = 12

// block is one rendered unit of the transcript. Each renders itself to the
// given width, so a terminal resize just re-renders every block.
type block interface {
	render(width int) string
}

// userBlock is a message the user sent: a full-width tinted bar, no label.
type userBlock struct{ text string }

func (b userBlock) render(w int) string {
	return styleUser.Width(w).Render(b.text)
}

// agentBlock is the agent's reply, rendered as markdown.
type agentBlock struct{ text string }

func (b agentBlock) render(w int) string {
	return renderMarkdown(strings.TrimRight(b.text, "\n"), w)
}

// hintBlock is dim helper text (the greeting, slash-command output).
type hintBlock struct{ text string }

func (b hintBlock) render(w int) string {
	return styleHint.Width(w).Render(b.text)
}

// errorBlock is a failed turn, already run through llm.FriendlyError.
type errorBlock struct{ msg string }

func (b errorBlock) render(w int) string {
	return styleErr.Width(w).Render("⚠ " + b.msg)
}

// toolBlock is a finished tool call: a header line plus a boxed preview of
// the output, tinted red when the tool errored. expanded shows the full
// output instead of the preview; ctrl+o toggles it.
type toolBlock struct {
	name, detail, output string
	isError              bool
	expanded             bool
}

func (b toolBlock) render(w int) string {
	card := styleCard
	if b.isError {
		card = styleCardErr
	}
	return toolHeader(b.name, b.detail) + "\n" + card.Width(w).Render(toolBody(b.output, b.expanded))
}

// toolHeader renders the one-line title of a tool card: a verb plus its
// target. bash shows the command itself.
func toolHeader(name, detail string) string {
	if name == "bash" {
		return styleToolHead.Render("$ ") + styleHint.Render(detail)
	}
	verb := name
	switch name {
	case "file_read":
		verb = "read"
	case "file_write":
		verb = "write"
	case "file_edit":
		verb = "edit"
	case "file_list":
		verb = "list"
	case "web_search":
		verb = "search"
	case "web_read":
		verb = "fetch"
	}
	head := styleToolHead.Render(verb)
	if detail != "" {
		head += " " + styleHint.Render(detail)
	}
	return head
}

// toolBody renders a tool card's output: the full text when expanded,
// otherwise a capped preview with a "more" hint.
func toolBody(output string, expanded bool) string {
	output = strings.TrimRight(output, "\n")
	if output == "" {
		return styleHint.Render("(no output)")
	}
	lines := strings.Split(output, "\n")
	if expanded || len(lines) <= cardPreviewLines {
		return output
	}
	shown := strings.Join(lines[:cardPreviewLines], "\n")
	return shown + "\n" + styleHint.Render(fmt.Sprintf("… %d more lines  (ctrl+o)", len(lines)-cardPreviewLines))
}

// mdRenderer is cached across blocks and rebuilt only when the width changes.
var (
	mdRenderer *glamour.TermRenderer
	mdWidth    int
)

// renderMarkdown renders agent prose as markdown for the terminal. On any
// error it falls back to the raw text, so a reply is never lost.
func renderMarkdown(s string, w int) string {
	if w < 24 {
		return s
	}
	if mdRenderer == nil || mdWidth != w {
		r, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(w-4),
		)
		if err != nil {
			return s
		}
		mdRenderer, mdWidth = r, w
	}
	out, err := mdRenderer.Render(s)
	if err != nil {
		return s
	}
	return strings.Trim(out, "\n")
}

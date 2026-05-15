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

// approvalBlock is the agent asking permission for an action. It renders in
// the warning colour and carries the y/n affordance inline, so the prompt
// sits with the action instead of pinned to the bottom of the screen.
type approvalBlock struct{ text string }

func (b approvalBlock) render(w int) string {
	msg := strings.ReplaceAll(strings.TrimRight(b.text, "\n"), "`", "")
	prompt := "   y approve   ·   n deny   ·   esc cancel"
	return styleApproveLn.Width(w).Render("⏸  " + msg + "\n" + prompt)
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
	// w-2 is the card's content width (the style pads one column each side).
	return toolHeader(b.name, b.detail) + "\n" + card.Width(w).Render(toolBody(b.output, b.expanded, w-2))
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
// otherwise a capped preview with a "more" hint. When the output looks
// like a unified diff, its lines are tinted green/red.
func toolBody(output string, expanded bool, width int) string {
	output = strings.TrimRight(output, "\n")
	if output == "" {
		return styleHint.Render("(no output)")
	}
	lines := strings.Split(output, "\n")
	more := 0
	if !expanded && len(lines) > cardPreviewLines {
		more = len(lines) - cardPreviewLines
		lines = lines[:cardPreviewLines]
	}
	if looksLikeDiff(output) {
		colorizeDiff(lines, width)
	}
	body := strings.Join(lines, "\n")
	if more > 0 {
		body += "\n" + styleHint.Render(fmt.Sprintf("… %d more lines  (ctrl+o)", more))
	}
	return body
}

// looksLikeDiff reports whether output is a unified diff, so plain command
// output with leading +/- (an ls listing, a markdown list) is not tinted.
func looksLikeDiff(s string) bool {
	return strings.Contains(s, "diff --git") || strings.Contains(s, "\n@@") || strings.HasPrefix(s, "@@")
}

// colorizeDiff tints diff lines in place: green for additions, red for
// deletions, each padded to the card width so the tint runs full bleed.
func colorizeDiff(lines []string, width int) {
	for i, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "+++") || strings.HasPrefix(ln, "---"):
			lines[i] = styleDiffMeta.Render(ln)
		case strings.HasPrefix(ln, "@@"):
			lines[i] = styleDiffHunk.Render(ln)
		case strings.HasPrefix(ln, "+"):
			lines[i] = styleDiffAdd.Width(width).Render(ln)
		case strings.HasPrefix(ln, "-"):
			lines[i] = styleDiffDel.Width(width).Render(ln)
		}
	}
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
		// A fixed dark style, not WithAutoStyle: auto-style queries the
		// terminal for its background colour, and that reply leaks into
		// Bubble Tea's input as garbage keystrokes.
		r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle("dark"),
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

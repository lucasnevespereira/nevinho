package tui

import (
	"encoding/json"
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

// agentBlock is the agent's reply, rendered as markdown with a thin
// left-edge bar so it is visually distinct from a tool card. If the model
// narrates a tool call as prose, the bar makes it clear it is still just
// model text, not a real tool result.
type agentBlock struct{ text string }

func (b agentBlock) render(w int) string {
	// styleAgentBar adds a 2-col gutter (border + padding) on the left, so
	// the markdown renderer wraps inside the remaining width.
	md := renderMarkdown(strings.TrimRight(b.text, "\n"), w-2)
	return styleAgentBar.Render(md)
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

// toolBlock is a finished tool call: a header line plus a boxed preview.
// file_write previews the written content (green), file_edit previews the
// diff (green/red), both from the tool input; other tools preview output.
// expanded shows the full body; ctrl+o toggles it.
type toolBlock struct {
	name, detail, output string
	input                json.RawMessage
	isError              bool
	expanded             bool
}

func (b toolBlock) render(w int) string {
	card := styleCard
	if b.isError {
		card = styleCardErr
	}
	return toolHeader(b.name, b.detail) + "\n" + card.Width(w).Render(b.body())
}

// body picks what the card shows: the written content for file_write, an
// old→new diff for file_edit, the tool output for everything else.
func (b toolBlock) body() string {
	if !b.isError {
		switch b.name {
		case "file_write":
			if s := writePreview(b.input, b.expanded); s != "" {
				return s
			}
		case "file_edit":
			if s := editPreview(b.input, b.expanded); s != "" {
				return s
			}
		}
	}
	return toolBody(b.output, b.expanded)
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
func toolBody(output string, expanded bool) string {
	output = strings.TrimRight(output, "\n")
	if output == "" {
		return styleHint.Render("(no output)")
	}
	lines, more := capLines(strings.Split(output, "\n"), expanded)
	if looksLikeDiff(output) {
		colorizeDiff(lines)
	}
	return joinWithMore(lines, more)
}

// editPair is one find/replace from a file_edit call.
type editPair struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// writePreview renders a file_write card body: the written content, every
// line tinted green since the whole file is new content.
func writePreview(input json.RawMessage, expanded bool) string {
	var in struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(input, &in) != nil || in.Content == "" {
		return ""
	}
	lines, more := capLines(strings.Split(strings.TrimRight(in.Content, "\n"), "\n"), expanded)
	for i, ln := range lines {
		lines[i] = styleDiffAdd.Render(ln)
	}
	return joinWithMore(lines, more)
}

// editPreview renders a file_edit card body: each replacement as a diff,
// old lines red, new lines green.
func editPreview(input json.RawMessage, expanded bool) string {
	var in struct {
		OldText string     `json:"old_text"`
		NewText string     `json:"new_text"`
		Edits   []editPair `json:"edits"`
	}
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	edits := in.Edits
	if len(edits) == 0 && (in.OldText != "" || in.NewText != "") {
		edits = []editPair{{in.OldText, in.NewText}}
	}
	if len(edits) == 0 {
		return ""
	}
	var lines []string
	for _, e := range edits {
		for _, ln := range strings.Split(strings.TrimRight(e.OldText, "\n"), "\n") {
			lines = append(lines, styleDiffDel.Render("- "+ln))
		}
		for _, ln := range strings.Split(strings.TrimRight(e.NewText, "\n"), "\n") {
			lines = append(lines, styleDiffAdd.Render("+ "+ln))
		}
	}
	lines, more := capLines(lines, expanded)
	return joinWithMore(lines, more)
}

// capLines trims a slice to the preview cap unless expanded, returning the
// trimmed slice and how many lines were dropped.
func capLines(lines []string, expanded bool) ([]string, int) {
	if expanded || len(lines) <= cardPreviewLines {
		return lines, 0
	}
	return lines[:cardPreviewLines], len(lines) - cardPreviewLines
}

// joinWithMore joins body lines, appending a hint when lines were dropped.
func joinWithMore(lines []string, more int) string {
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
func colorizeDiff(lines []string) {
	for i, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "+++") || strings.HasPrefix(ln, "---"):
			lines[i] = styleDiffMeta.Render(ln)
		case strings.HasPrefix(ln, "@@"):
			lines[i] = styleDiffHunk.Render(ln)
		case strings.HasPrefix(ln, "+"):
			lines[i] = styleDiffAdd.Render(ln)
		case strings.HasPrefix(ln, "-"):
			lines[i] = styleDiffDel.Render(ln)
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

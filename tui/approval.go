package tui

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

// approvalContent is shared by the live chooser and its full scrollback copy.
// Directory grants persist, so they must never be labeled "Approve once".
func (m model) approvalContent() (title, detail, scope, action string) {
	if m.approval == nil {
		return "Approval required", approvalLiteral(m.approvalText), "Review the action before approving.", "Approve"
	}
	detail = approvalLiteral(m.approval.Detail)
	switch m.approval.Kind {
	case "code":
		if m.cwd != "" {
			detail = "cwd: " + approvalLiteral(m.cwd) + "\n\n" + detail
		}
		return "Run command?", detail, "Runs once. Future commands still need approval.", "Approve once"
	case "path":
		return "Allow directory writes?", detail, "Saved for this directory and its children. Revoke with /paths.", "Allow directory"
	default:
		return "Approval required", detail, "Review the action before approving.", "Approve"
	}
}

// approvalLiteral preserves shell syntax while making terminal controls visible.
// An escape sequence in a command must not hide what is being approved.
func approvalLiteral(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r != '\n' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf)) {
			quoted := strconv.QuoteRuneToASCII(r)
			b.WriteString(quoted[1 : len(quoted)-1])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// approvalPicker keeps the action and its permission scope next to the keys.
// Long details remain available in scrollback instead of filling the live area.
func (m model) approvalPicker() string {
	w := max(m.width, 1)
	title, detail, scope, action := m.approvalContent()
	wrap := lipgloss.NewStyle().Width(w)
	lines := strings.Split(wrap.Render(detail), "\n")
	const previewRows = 6
	if len(lines) > previewRows {
		lines = append(lines[:previewRows], styleHint.Render("… full details above"))
	}

	approve, deny := "  "+action, "  Deny"
	if m.approvalCursor == 0 {
		approve = styleSelected.Render("→ " + action)
	} else {
		deny = styleSelected.Render("→ Deny")
	}
	choices := approve + "    " + deny
	if lipgloss.Width(choices) > w {
		choices = approve + "\n" + deny
	}

	body := strings.Join([]string{
		styleApproveLn.Bold(true).Render(title),
		strings.Join(lines, "\n"),
		styleHint.Render(scope),
		"",
		choices,
		styleHint.Render("tab / arrows · enter · y / n · esc denies"),
	}, "\n")
	return styleApprove.Width(w).Render(body)
}

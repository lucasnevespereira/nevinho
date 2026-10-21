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

const approvalPreviewRows = 6

func (m model) approvalLines() []string {
	_, detail, _, _ := m.approvalContent()
	return strings.Split(lipgloss.NewStyle().Width(max(m.width, 1)).Render(detail), "\n")
}

func (m *model) moveApprovalPage(direction int) {
	last := max(0, len(m.approvalLines())-approvalPreviewRows)
	m.approvalOffset = min(max(m.approvalOffset+direction*approvalPreviewRows, 0), last)
}

// approvalPicker keeps the action and its permission scope next to the keys.
// Long details can be paged through without leaving the live chooser.
func (m model) approvalPicker() string {
	w := max(m.width, 1)
	title, _, scope, action := m.approvalContent()
	lines := m.approvalLines()
	start := min(max(m.approvalOffset, 0), max(0, len(lines)-approvalPreviewRows))
	end := min(start+approvalPreviewRows, len(lines))
	preview := strings.Join(lines[start:end], "\n")
	if len(lines) > approvalPreviewRows {
		preview += "\n" + styleHint.Render("lines "+strconv.Itoa(start+1)+"-"+strconv.Itoa(end)+"/"+strconv.Itoa(len(lines))+" · PgUp/PgDn")
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
		preview,
		styleHint.Render(scope),
		"",
		choices,
		styleHint.Render("tab / arrows · enter · y / n · esc denies"),
	}, "\n")
	return styleApprove.Width(w).Render(body)
}

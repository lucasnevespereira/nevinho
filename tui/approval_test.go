package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasnevespereira/nevinho/agent"
)

func approvalModel(kind, detail string) model {
	m := newModel(nil, nil, nil, "/work/nevinho")
	m.width, m.height = 100, 30
	next, _ := m.Update(responseMsg{turn: agent.Turn{
		Kind:     agent.TurnApproval,
		Text:     "original approval prompt",
		Approval: &agent.ApprovalRequest{Kind: kind, Detail: detail},
	}})
	return next.(model)
}

func TestApprovalPickerShowsActionAndScope(t *testing.T) {
	for _, tc := range []struct {
		kind, detail string
		want         []string
	}{
		{"code", "echo `pwd`", []string{"Run command?", "cwd: /work/nevinho", "echo `pwd`", "Runs once", "Approve once", "Deny"}},
		{"path", "/work/other", []string{"Allow directory writes?", "/work/other", "Saved for this directory and its children", "/paths", "Allow directory", "Deny"}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			m := approvalModel(tc.kind, tc.detail)
			view := m.approvalPicker()
			for _, want := range tc.want {
				if !strings.Contains(view, want) {
					t.Errorf("picker missing %q:\n%s", want, view)
				}
			}
			if tc.kind == "path" && strings.Contains(view, "once") {
				t.Error("saved directory access must not be described as one-time")
			}
			transcript := (*m.blocks)[0].render(m.width)
			if !strings.Contains(transcript, tc.detail) {
				t.Errorf("scrollback changed the action's syntax:\n%s", transcript)
			}
		})
	}
}

func TestApprovalPickerBoundsLongDetails(t *testing.T) {
	for _, width := range []int{20, 30, 80, 120} {
		m := approvalModel("code", strings.Repeat("echo 界界界\n", 40)+"echo final-command")
		m.width = width
		view := m.approvalPicker()
		for _, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width %d: row is %d columns", width, got)
			}
		}
		if !strings.Contains(view, "full details above") || strings.Contains(view, "final-command") {
			t.Errorf("width %d: expected a bounded preview with a scrollback hint", width)
		}
		if !strings.Contains((*m.blocks)[0].render(120), "final-command") {
			t.Error("full action must remain available in scrollback")
		}
		if !strings.Contains(view, "Approve once") || !strings.Contains(view, "Deny") {
			t.Error("long details hid the choices")
		}
	}
}

func TestApprovalLiteralMakesControlsVisible(t *testing.T) {
	command := "echo `pwd`\n\x1b[2J\r\t\u202e"
	want := "echo `pwd`\n\\x1b[2J\\r\\t\\u202e"
	if got := approvalLiteral(command); got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
	m := approvalModel("code", command)
	if !strings.Contains((*m.blocks)[0].render(120), "\\x1b[2J") {
		t.Error("scrollback must show terminal controls as text")
	}
}

func TestApprovalPickerNavigationAndDecision(t *testing.T) {
	m := approvalModel("code", "echo hi")
	for _, tc := range []struct {
		key    string
		cursor int
	}{
		{"tab", 1}, {"shift+tab", 0}, {"right", 1}, {"left", 0},
		{"down", 1}, {"up", 0}, {"j", 1}, {"k", 0},
	} {
		next, cmd := m.updateApproval(tc.key)
		m = next.(model)
		if m.approvalCursor != tc.cursor || cmd != nil || !m.approving {
			t.Fatalf("%s should only move selection to %d", tc.key, tc.cursor)
		}
	}
	for _, key := range []string{"enter", "y", "Y", "n", "N", "esc"} {
		m := approvalModel("code", "echo hi")
		next, cmd := m.updateApproval(key)
		resolved := next.(model)
		if resolved.approving || !resolved.busy || cmd == nil {
			t.Errorf("%s should leave the picker and resolve the action", key)
		}
		if resolved.approval != nil || resolved.approvalText != "" {
			t.Errorf("%s left stale approval details", key)
		}
	}
}

func TestApprovalPickerWithoutDetails(t *testing.T) {
	m := newModel(nil, nil, nil, "")
	m.width = 80
	next, _ := m.Update(responseMsg{turn: agent.Turn{Kind: agent.TurnApproval, Text: "Review this action"}})
	m = next.(model)
	if view := m.approvalPicker(); !strings.Contains(view, "Review this action") || strings.Contains(view, "once") {
		t.Fatalf("missing a safe fallback for unknown approval scope:\n%s", view)
	}
}

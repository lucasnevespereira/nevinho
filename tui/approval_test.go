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

func TestApprovalRequestRendersOnlyInLivePicker(t *testing.T) {
	m := approvalModel("code", "pwd")
	if !strings.Contains(m.approvalPicker(), "pwd") {
		t.Fatal("pending action is missing from the live picker")
	}
	if len(*m.blocks) != 0 {
		t.Fatalf("pending approval was also printed into scrollback: %d blocks", len(*m.blocks))
	}
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
			resolved, _ := m.decideApproval(true)
			transcript := (*resolved.(model).blocks)[0].render(m.width)
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
		if !strings.Contains(view, "PgUp/PgDn") || strings.Contains(view, "final-command") {
			t.Errorf("width %d: expected a bounded, pageable preview", width)
		}
		if !strings.Contains(view, "Approve once") || !strings.Contains(view, "Deny") {
			t.Error("long details hid the choices")
		}
		for i := 0; i < 20; i++ {
			next, cmd := m.updateApproval("pgdown")
			m = next.(model)
			if cmd != nil || !m.approving {
				t.Fatal("paging must leave the approval pending")
			}
		}
		view = m.approvalPicker()
		if !strings.Contains(view, "final-command") || !strings.Contains(view, "Approve once") || !strings.Contains(view, "Deny") {
			t.Errorf("width %d: final page must show the end of the action and both choices", width)
		}
		if m.approvalOffset != len(m.approvalLines())-approvalPreviewRows {
			t.Error("paging past the end should stop at the last page")
		}
		next, _ := m.updateApproval("home")
		m = next.(model)
		if m.approvalOffset != 0 || strings.Contains(m.approvalPicker(), "final-command") {
			t.Error("home should return to the beginning of the action")
		}
		next, _ = m.updateApproval("end")
		m = next.(model)
		if !strings.Contains(m.approvalPicker(), "final-command") {
			t.Error("end should show the last action line")
		}
		next, _ = m.updateApproval("pgup")
		m = next.(model)
		if strings.Contains(m.approvalPicker(), "final-command") {
			t.Error("page up should move away from the last action line")
		}
		resolved, _ := m.decideApproval(true)
		if !strings.Contains((*resolved.(model).blocks)[0].render(120), "final-command") {
			t.Error("full action must be recorded in scrollback after the decision")
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
	resolved, _ := m.decideApproval(false)
	if !strings.Contains((*resolved.(model).blocks)[0].render(120), "\\x1b[2J") {
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
		if resolved.approvalOffset != 0 {
			t.Errorf("%s left stale approval position", key)
		}
		if len(*resolved.blocks) != 1 {
			t.Errorf("%s should record exactly one approval decision", key)
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

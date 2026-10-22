package tui

import (
	"errors"
	"testing"

	"github.com/lucasnevespereira/nevinho/agent"
	"github.com/lucasnevespereira/nevinho/hooks"
	"github.com/lucasnevespereira/nevinho/tools"
)

type recordingHooks struct{ events []hooks.Event }

func (r *recordingHooks) Emit(event hooks.Event) { r.events = append(r.events, event) }

func TestTurnHookTransitions(t *testing.T) {
	recorder := &recordingHooks{}
	m := newModel(nil, nil, nil, "/work/nevinho")
	m.hooks = recorder
	m.input.SetValue("hello")
	next, _ := m.submit()
	m = next.(model)
	next, _ = m.Update(responseMsg{turn: agent.Turn{
		Kind:     agent.TurnApproval,
		Approval: &agent.ApprovalRequest{Kind: "code", Detail: "pwd"},
	}})
	m = next.(model)
	next, _ = m.decideApproval(true)
	m = next.(model)
	next, _ = m.Update(responseMsg{turn: agent.Turn{Kind: agent.TurnText, Text: "done"}})
	m = next.(model)

	want := []string{hooks.TurnStarted, hooks.ApprovalRequested, hooks.TurnStarted, hooks.TurnFinished}
	if len(recorder.events) != len(want) {
		t.Fatalf("events = %+v", recorder.events)
	}
	for i, event := range recorder.events {
		if event.Event != want[i] {
			t.Fatalf("event %d = %s, want %s", i, event.Event, want[i])
		}
	}
	if recorder.events[3].Outcome != "completed" {
		t.Fatalf("finished outcome = %q", recorder.events[3].Outcome)
	}
}

func TestTurnHookErrorAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cancelled bool
		response  responseMsg
		want      string
	}{
		{"error", false, responseMsg{err: errors.New("failed")}, "error"},
		{"cancelled", true, responseMsg{turn: agent.Turn{Kind: agent.TurnText, Text: "Cancelled."}}, "cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &recordingHooks{}
			m := newModel(nil, nil, nil, "")
			m.hooks = recorder
			m.busy = true
			m.cancelled = tc.cancelled
			_, _ = m.Update(tc.response)
			if len(recorder.events) != 1 || recorder.events[0].Event != hooks.TurnFinished || recorder.events[0].Outcome != tc.want {
				t.Fatalf("events = %+v", recorder.events)
			}
		})
	}
}

func TestToolHookPayloadOmitsToolContents(t *testing.T) {
	recorder := &recordingHooks{}
	emitToolHook(recorder, agent.ToolEvent{Phase: agent.ToolStart, Name: "bash", Detail: "secret", Input: []byte(`{"key":"secret"}`)})
	emitToolHook(recorder, agent.ToolEvent{Phase: agent.ToolDone, Name: "bash", Status: tools.StatusFailed, Output: "secret"})
	if len(recorder.events) != 2 || recorder.events[0].Event != hooks.ToolStarted || recorder.events[1].Event != hooks.ToolFinished {
		t.Fatalf("events = %+v", recorder.events)
	}
	if recorder.events[1].Tool != "bash" || recorder.events[1].Status != "failed" {
		t.Fatalf("tool result = %+v", recorder.events[1])
	}
}

package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
	"github.com/lucasnevespereira/nevinho/tools"
)

// Answer is the user's decision on a paused tool call.
type Answer int

const (
	Approved Answer = iota
	Denied
)

var approvalWords = []string{"yes", "yep", "yeah", "sure", "ok", "okay", "go ahead", "allow", "approve", "y", "oui"}

var denialWords = []string{"no", "nope", "nah", "deny", "cancel", "stop", "n", "non"}

// answerIn reads a decision out of a plain message. Nil means the message
// is ordinary text, not an answer.
func answerIn(text string) *Answer {
	word := strings.ToLower(strings.TrimSpace(text))
	switch {
	case slices.Contains(approvalWords, word):
		a := Approved
		return &a
	case slices.Contains(denialWords, word):
		a := Denied
		return &a
	}
	return nil
}

// HasPendingApproval reports whether this user has a tool call waiting on a
// decision.
func (a *Agent) HasPendingApproval(userID string) bool {
	return a.tools.PendingApproval(userID) != nil
}

// Resolve answers a pending approval and runs the turn that follows it.
// Transports call this for a button or a keypress. A typed "yes" or "no"
// lands on the same path through Chat.
func (a *Agent) Resolve(userID string, answer Answer) (string, error) {
	return a.ResolveStream(userID, answer, nil)
}

// ResolveStream is Resolve with streaming deltas.
func (a *Agent) ResolveStream(userID string, answer Answer, cb llm.StreamCallback) (string, error) {
	if !a.HasPendingApproval(userID) {
		return "", fmt.Errorf("nothing is waiting for approval")
	}
	return a.chat(userID, "", false, nil, tools.SourceInteractive, cb, &answer)
}

// applyAnswer resolves the held tool call and returns the note that tells
// the model what the user decided. Running the approved code, patching the
// stale placeholder in history, and remembering an approved path all
// happen here, so every transport leaves the same state behind.
func (a *Agent) applyAnswer(ctx context.Context, userID string, answer Answer) string {
	p := a.tools.PendingApproval(userID)
	if p == nil {
		return ""
	}

	if answer == Denied {
		logger.Info("denied: " + p.Detail)
		a.tools.ClearPending(userID)
		a.replacePendingToolResult(userID, "denied by user")
		return "\n[The user declined that action. Do not retry it. Acknowledge and move on.]"
	}

	switch p.Kind {
	case "path":
		a.tools.ApprovePending(userID)
		logger.Info(fmt.Sprintf("approved: %s", p.Detail))
		return "\n[Access granted to " + p.Detail + ". Retry the file operation.]"
	case "code":
		logger.Info("approved: code execution")
		output := a.tools.ExecutePendingCode(ctx, userID)
		a.replacePendingToolResult(userID, output)
		return "\n[Code execution approved. Output:\n" + output + "]"
	}
	return ""
}

// holdForApproval remembers which tool call is paused, so the answer can
// replace its placeholder result later.
func (a *Agent) holdForApproval(userID, toolUseID string) {
	a.mu.Lock()
	a.pendingToolID[userID] = toolUseID
	a.mu.Unlock()
}

// replacePendingToolResult swaps the placeholder the model is holding for
// the real outcome. Without it the model sees an unfinished tool call and
// asks again.
func (a *Agent) replacePendingToolResult(userID, output string) {
	a.mu.Lock()
	id := a.pendingToolID[userID]
	delete(a.pendingToolID, userID)
	hist := a.history[userID]
	a.mu.Unlock()
	if id == "" || len(hist) == 0 {
		return
	}
	updated := a.llm.ReplaceToolResult(hist, id, output)
	a.mu.Lock()
	a.history[userID] = updated
	a.mu.Unlock()
}

// approvalMessage is the reply a paused turn returns: what the agent wants
// permission for.
func approvalMessage(p *tools.Pending) string {
	if p == nil {
		return "Something needs approval."
	}
	switch p.Kind {
	case "path":
		return fmt.Sprintf("I need permission to write to `%s`.", p.Detail)
	case "code":
		return fmt.Sprintf("I want to run this:\n```\n%s\n```", p.Detail)
	default:
		return "Something needs approval."
	}
}

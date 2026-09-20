package tools

import (
	"fmt"
	"strings"
)

// Status is the verdict of one tool call. It is decided where the outcome
// happens, so callers never parse the output text to find out what
// happened.
type Status string

const (
	StatusOK            Status = "ok"
	StatusFailed        Status = "failed"
	StatusBlocked       Status = "blocked"
	StatusNeedsApproval Status = "needs_approval"
)

// Result is what a tool call produces: text for the model, plus the
// verdict.
type Result struct {
	Output string
	Status Status
}

func ok(output string) Result {
	return Result{Output: output, Status: StatusOK}
}

func okf(format string, a ...any) Result {
	return Result{Output: fmt.Sprintf(format, a...), Status: StatusOK}
}

func fail(format string, a ...any) Result {
	return Result{Output: fmt.Sprintf(format, a...), Status: StatusFailed}
}

func blocked(format string, a ...any) Result {
	return Result{Output: fmt.Sprintf(format, a...), Status: StatusBlocked}
}

// needsApproval pauses the call until the user answers. The detail is what
// the user is being asked to allow: a directory or "run_code".
func needsApproval(detail string) Result {
	return Result{Output: approvalPrefix + detail, Status: StatusNeedsApproval}
}

// approvalPrefix marks a paused tool result in the transcript the model
// sees. The system prompt teaches the model to stop when it reads this.
const approvalPrefix = "NEEDS_APPROVAL:"

// IsError reports whether the model should treat the output as a failure.
// A paused call is not a failure: it is waiting on the user.
func (r Result) IsError() bool {
	return r.Status == StatusFailed || r.Status == StatusBlocked
}

// ApprovalDetail returns what the paused call is asking permission for.
func (r Result) ApprovalDetail() string {
	return strings.TrimPrefix(r.Output, approvalPrefix)
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

const codeTimeout = 10 * time.Second

type runCodeInput struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

func (r *Registry) runCode(input json.RawMessage, userID string) string {
	var in runCodeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	// Every code execution requires explicit user approval
	preview := in.Language + ": " + in.Code
	if err := r.checkCodePermission(userID, preview, input); err != nil {
		return err.Error()
	}

	return r.executeCode(input)
}

// executeCode runs code without permission checks (called after approval).
func (r *Registry) executeCode(input json.RawMessage) string {
	var in runCodeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	var cmd *exec.Cmd
	ctx, cancel := context.WithTimeout(context.Background(), codeTimeout)
	defer cancel()

	switch in.Language {
	case "python3":
		cmd = exec.CommandContext(ctx, "python3", "-c", in.Code)
	case "node":
		cmd = exec.CommandContext(ctx, "node", "-e", in.Code)
	case "bash":
		cmd = exec.CommandContext(ctx, "bash", "-c", in.Code)
	default:
		return fmt.Sprintf("unsupported language: %s (use python3, node, or bash)", in.Language)
	}

	output, err := cmd.CombinedOutput()
	result := string(output)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "code execution timed out (10 second limit)"
		}
		result += "\n" + err.Error()
	}

	if len(result) > 5000 {
		result = result[:5000] + "\n...(truncated)"
	}

	if result == "" {
		return "(no output)"
	}

	return result
}

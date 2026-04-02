package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const codeTimeout = 10 * time.Second

// Patterns that require user approval before execution.
var dangerousPatterns = []*regexp.Regexp{
	// Destructive
	regexp.MustCompile(`\brm\b`),
	regexp.MustCompile(`\brmdir\b`),
	regexp.MustCompile(`\bmkfs\b`),
	regexp.MustCompile(`\bdd\b`),
	regexp.MustCompile(`\bshred\b`),

	// Privilege escalation
	regexp.MustCompile(`\bsudo\b`),
	regexp.MustCompile(`\bsu\b`),
	regexp.MustCompile(`\bdoas\b`),

	// Permission changes
	regexp.MustCompile(`\bchmod\b`),
	regexp.MustCompile(`\bchown\b`),
	regexp.MustCompile(`\bchgrp\b`),

	// Process control
	regexp.MustCompile(`\bkill\b`),
	regexp.MustCompile(`\bkillall\b`),
	regexp.MustCompile(`\bpkill\b`),

	// Network exfiltration
	regexp.MustCompile(`\bcurl\b.*\|`),
	regexp.MustCompile(`\bwget\b.*\|`),
	regexp.MustCompile(`\bcurl\b.*-[oO]`),

	// Dangerous redirects
	regexp.MustCompile(`>\s*/dev/`),
	regexp.MustCompile(`>\s*/etc/`),

	// Fork bomb
	regexp.MustCompile(`:\(\)\s*\{`),

	// Eval of remote code
	regexp.MustCompile(`\beval\b.*\$\(`),
}

// Paths that require approval when referenced in code.
var sensitivePaths = []string{
	"/.ssh",
	"/.gnupg",
	"/.gpg",
	"/.aws",
	"/.kube",
	"/.docker",
	"/.config/gcloud",
	"/.env",
	"/.netrc",
	"/.npmrc",
	"/.pypirc",
	"/etc/",
	"/var/",
	"/usr/",
	"/System/",
	"/Library/",
	"id_rsa",
	"id_ed25519",
	"credentials",
	"secret",
	"token",
	"password",
}

type runCodeInput struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

func (r *Registry) runCode(input json.RawMessage, userID string) string {
	var in runCodeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	// Check if the code looks dangerous
	if reason := isDangerous(in.Code); reason != "" {
		preview := in.Language + ": " + in.Code
		if err := r.checkCodePermission(userID, preview, input); err != nil {
			return err.Error()
		}
	}

	return r.executeCode(input)
}

// executeCode runs code without permission checks (called after approval or when safe).
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

// isDangerous checks if code matches dangerous patterns or touches sensitive paths.
// Returns the reason if dangerous, empty string if safe.
func isDangerous(code string) string {
	lower := strings.ToLower(code)

	for _, pat := range dangerousPatterns {
		if pat.MatchString(lower) {
			return "dangerous command: " + pat.String()
		}
	}

	for _, path := range sensitivePaths {
		if strings.Contains(lower, strings.ToLower(path)) {
			return "sensitive path: " + path
		}
	}

	return ""
}

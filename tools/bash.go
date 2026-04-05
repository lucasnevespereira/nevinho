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

const bashTimeout = 120 * time.Second

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
	regexp.MustCompile(`\bcurl\b.*--output`),

	// Dangerous redirects
	regexp.MustCompile(`>\s*/dev/`),
	regexp.MustCompile(`>\s*/etc/`),

	// Fork bomb
	regexp.MustCompile(`:\(\)\s*\{`),

	// Eval of remote code
	regexp.MustCompile(`\beval\b.*\$\(`),
}

// Paths that require approval when referenced in commands.
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

type bashInput struct {
	Command string `json:"command"`
}

func (r *Registry) runBash(input json.RawMessage, userID string) string {
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	if reason := isDangerous(in.Command); reason != "" {
		if err := r.checkCodePermission(userID, in.Command, input); err != nil {
			return err.Error()
		}
	}

	return r.executeBash(in.Command)
}

// executeBash runs a command without permission checks (called after approval or when safe).
func (r *Registry) executeBash(command string) string {
	ctx, cancel := context.WithTimeout(context.Background(), bashTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	output, err := cmd.CombinedOutput()
	result := string(output)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return result + "\n(timed out after 2m)"
		}
		result += "\n" + err.Error()
	}

	if len(result) > maxResponseLen {
		total := len(result)
		result = result[:maxResponseLen] + fmt.Sprintf("\n...(truncated: showing %d of %d chars)", maxResponseLen, total)
	}

	if result == "" {
		return "(no output)"
	}

	return result
}

// ExecutePendingCode runs a previously approved bash command.
func (r *Registry) executePendingBash(input json.RawMessage) string {
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}
	return r.executeBash(in.Command)
}

// isDangerous checks if a command matches dangerous patterns or touches sensitive paths.
func isDangerous(command string) string {
	lower := strings.ToLower(command)

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

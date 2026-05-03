package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

const bashTimeout = 120 * time.Second

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

// Paths that trigger approval prompts.
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

func (r *Registry) runBash(ctx context.Context, input json.RawMessage, userID string) string {
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	if reason := isDangerous(in.Command); reason != "" {
		if err := r.checkCodePermission(userID, in.Command, input); err != nil {
			return err.Error()
		}
	}

	return r.executeBashCtx(ctx, in.Command)
}

// executeBashCtx runs a command with cancellation and timeout.
func (r *Registry) executeBashCtx(parent context.Context, command string) string {
	ctx, cancel := context.WithTimeout(parent, bashTimeout)
	defer cancel()

	// Force no-color output to avoid wasting tokens on ANSI codes
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	// Run in its own process group so cancel can SIGKILL the whole tree.
	// Without this, children like `python3 -m http.server` outlive the bash parent.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	output, err := cmd.CombinedOutput()
	result := stripANSI(string(output))

	if err != nil {
		if parent.Err() != nil {
			return result + "\n(cancelled)"
		}
		if ctx.Err() == context.DeadlineExceeded {
			return result + "\n(timed out after 2m)"
		}
		result += "\n" + err.Error()
	}

	if len(result) > maxResponseLen {
		total := len(result)
		// Keep the tail — end of output has errors, results, and summaries.
		tail := result[total-maxResponseLen:]
		// Start at a line boundary for clean output
		if idx := strings.Index(tail, "\n"); idx != -1 && idx < 200 {
			tail = tail[idx+1:]
		}
		result = fmt.Sprintf("...(showing last %d of %d chars)...\n\n", len(tail), total) + tail
	}

	if result == "" {
		return "(no output)"
	}

	return result
}

// executePendingBash runs a command after user approval.
func (r *Registry) executePendingBash(ctx context.Context, input json.RawMessage) string {
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}
	return r.executeBashCtx(ctx, in.Command)
}

// isDangerous returns a reason string if the command needs approval, empty otherwise.
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

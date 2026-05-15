package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
	"github.com/lucasnevespereira/nevinho/memory"
)

func (a *Agent) appendHistory(userID string, msgs ...json.RawMessage) (evicted []json.RawMessage) {
	a.history[userID] = append(a.history[userID], msgs...)
	if estimateTokens(a.history[userID]) <= maxHistoryTokens {
		return nil
	}
	trimmed := trimHistoryByTokens(a.history[userID], maxHistoryTokens)
	evictedCount := len(a.history[userID]) - len(trimmed)
	evicted = make([]json.RawMessage, evictedCount)
	copy(evicted, a.history[userID][:evictedCount])
	a.history[userID] = trimmed
	return evicted
}

func estimateTokens(msgs []json.RawMessage) int {
	total := 0
	for _, m := range msgs {
		total += len(m) / 4
	}
	return total
}

func trimHistoryByTokens(msgs []json.RawMessage, limit int) []json.RawMessage {
	if estimateTokens(msgs) <= limit {
		return msgs
	}
	start := 0
	for start < len(msgs) && estimateTokens(msgs[start:]) > limit {
		start++
	}
	// Walk forward to find a clean boundary (plain user message)
	for start < len(msgs) {
		var peek struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(msgs[start], &peek); err != nil {
			start++
			continue
		}
		if peek.Role == "tool" {
			start++
			continue
		}
		if peek.Role == "assistant" {
			start++
			continue
		}
		if peek.Role == "user" && len(peek.Content) > 0 && peek.Content[0] == '[' {
			start++
			continue
		}
		break
	}
	if start >= len(msgs) {
		return msgs[len(msgs)-1:]
	}
	return msgs[start:]
}

func flattenMessages(msgs []json.RawMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		var peek struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(m, &peek); err != nil {
			continue
		}
		// Try to extract string content
		var text string
		if err := json.Unmarshal(peek.Content, &text); err == nil {
			runes := []rune(text)
			if len(runes) > 200 {
				text = string(runes[:200]) + "..."
			}
			fmt.Fprintf(&sb, "%s: %s\n", peek.Role, text)
		} else {
			fmt.Fprintf(&sb, "%s: [tool interaction]\n", peek.Role)
		}
	}
	return sb.String()
}

func (a *Agent) ClearHistory(userID string) {
	lock := a.getUserLock(userID)
	lock.Lock()
	defer lock.Unlock()
	delete(a.history, userID)
	a.mu.Lock()
	delete(a.pendingToolID, userID)
	a.mu.Unlock()
	if err := deleteSummary(a.cfg.Dir(), userID); err != nil {
		logger.Err(fmt.Errorf("delete summary: %w", err))
	}
}

// maybeLoadSummary injects the persisted summary into history if elephant is on
// and this user has no in-memory history yet (fresh process start).
func (a *Agent) maybeLoadSummary(userID string) {
	if !a.cfg.ElephantEnabled() {
		return
	}
	if len(a.history[userID]) > 0 {
		return
	}
	summary := loadSummary(a.cfg.Dir(), userID)
	if summary == "" {
		return
	}
	preamble := a.llm.FormatUserMessage("[Previous conversation: "+summary+"]", nil)
	a.history[userID] = append(a.history[userID], preamble)
	logger.Info("loaded persisted summary")
}

// summarizeAndPrepend summarizes evicted messages and prepends them to history.
func (a *Agent) summarizeAndPrepend(userID string, evicted []json.RawMessage) {
	flat := flattenMessages(evicted)
	if flat == "" {
		return
	}
	summarizeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := a.llm.Complete(summarizeCtx, &llm.Request{
		SystemPrompt: "Summarize this conversation excerpt in 2-3 sentences. Focus on what was asked, what was done, and important outcomes.",
		Messages:     []json.RawMessage{a.llm.FormatUserMessage(flat, nil)},
		MaxTokens:    200,
	})
	if err != nil {
		logger.Err(fmt.Errorf("summary failed: %w", err))
		return
	}
	if resp.Text == "" {
		return
	}
	preamble := a.llm.FormatUserMessage("[Conversation so far: "+resp.Text+"]", nil)
	a.history[userID] = append([]json.RawMessage{preamble}, a.history[userID]...)
}

// PersistAll summarizes each active user's in-memory history and writes it to
// disk. Called on shutdown so the next process start can resume context.
// Respects the parent context for early cancellation. Skips users on error so
// one bad summary doesn't block the rest.
func (a *Agent) PersistAll(ctx context.Context) {
	if !a.cfg.ElephantEnabled() {
		return
	}
	a.mu.Lock()
	users := make([]string, 0, len(a.history))
	for u := range a.history {
		users = append(users, u)
	}
	a.mu.Unlock()

	for _, userID := range users {
		if ctx.Err() != nil {
			logger.Info("persist cancelled, skipping remaining users")
			return
		}
		// Skip per-schedule namespaces. Their history is transient
		// state for the runner, not a conversation worth resuming, and
		// persisting them would leak scheduled prompts to disk.
		if strings.HasPrefix(userID, "scheduler:") {
			continue
		}
		a.persistUser(ctx, userID)
	}
}

func (a *Agent) persistUser(ctx context.Context, userID string) {
	a.mu.Lock()
	msgs := a.history[userID]
	a.mu.Unlock()
	if len(msgs) == 0 {
		return
	}
	flat := flattenMessages(msgs)
	if flat == "" {
		return
	}
	resp, err := a.llm.Complete(ctx, &llm.Request{
		SystemPrompt: "Summarize this conversation in 3-5 sentences. Capture what the user was working on, key decisions, and unresolved threads. Be specific enough that the next session can pick up where this left off.",
		Messages:     []json.RawMessage{a.llm.FormatUserMessage(flat, nil)},
		MaxTokens:    400,
	})
	if err != nil {
		logger.Err(fmt.Errorf("persist summary for user: %w", err))
		return
	}
	if resp.Text == "" {
		return
	}
	if err := saveSummary(a.cfg.Dir(), userID, resp.Text); err != nil {
		logger.Err(fmt.Errorf("save summary: %w", err))
		return
	}
	logger.Info("persisted summary")
}

// SummaryView returns the user-visible dump of the persisted conversation
// summary for the given user. Reflects ELEPHANT state in the empty case.
func (a *Agent) SummaryView(userID string) string {
	if !a.cfg.ElephantEnabled() {
		return "Persistence is off (`ELEPHANT=off`). No summary will be saved on shutdown."
	}
	path, err := summaryPath(a.cfg.Dir(), userID)
	if err != nil {
		return "Could not locate summary file."
	}
	info, err := os.Stat(path)
	if err != nil {
		return "No saved summary yet. One will be written when nevinho shuts down."
	}
	body := loadSummary(a.cfg.Dir(), userID)
	if body == "" {
		return "No saved summary yet. One will be written when nevinho shuts down."
	}
	age := time.Since(info.ModTime()).Truncate(time.Second)
	return fmt.Sprintf("**Saved summary** (loads on next restart)\n\n%s\n\n_Last updated: %s ago_", body, formatDuration(age))
}

// MemoryView returns the user-visible dump of memory.md entries.
func (a *Agent) MemoryView() string {
	mem := memory.Load(a.cfg.Dir())
	if mem == "" {
		return "Nothing remembered yet. Tell me to \"remember X\", \"always X\", or \"never X\"."
	}
	var sb strings.Builder
	sb.WriteString("**What I remember about you:**\n")
	for _, line := range strings.Split(mem, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fmt.Fprintf(&sb, "• %s\n", line)
	}
	return sb.String()
}

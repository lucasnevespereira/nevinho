package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
	"github.com/lucasnevespereira/nevinho/memory"
)

// messages returns one user's history. The history map is shared by
// every user, so each read and write of it takes a.mu. The per-user lock
// only keeps one user's turns from interleaving, it does not protect the
// map from another user's turn.
func (a *Agent) messages(userID string) []llm.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.history[userID]
}

// setMessages replaces one user's history. See messages.
func (a *Agent) setMessages(userID string, msgs []llm.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history[userID] = msgs
}

func (a *Agent) appendHistory(userID string, msgs ...llm.Message) (evicted []llm.Message) {
	hist := append(a.messages(userID), msgs...)
	limit, _ := budgetFor(a.llm.Model())
	if estimateTokens(hist) <= limit {
		a.setMessages(userID, hist)
		return nil
	}
	trimmed := trimHistoryByTokens(hist, limit)
	evictedCount := len(hist) - len(trimmed)
	evicted = make([]llm.Message, evictedCount)
	copy(evicted, hist[:evictedCount])
	a.setMessages(userID, trimmed)
	return evicted
}

// appendReply records the model's turn. If the provider had to answer
// without the thinking blocks it was sent, they are stale, and keeping
// them would make every later request fail the same way first.
func (a *Agent) appendReply(userID string, resp *llm.Response) {
	if resp.ThinkingRejected {
		dropThinking(a.messages(userID))
	}
	a.appendHistory(userID, resp.Assistant)
}

// dropThinking makes every assistant turn replay from its text and tool
// calls alone.
func dropThinking(msgs []llm.Message) {
	for i := range msgs {
		msgs[i].Wire = nil
	}
}

func estimateTokens(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		total += m.Size() / 4
	}
	return total
}

func trimHistoryByTokens(msgs []llm.Message, limit int) []llm.Message {
	if estimateTokens(msgs) <= limit {
		return msgs
	}
	start := 0
	for start < len(msgs) && estimateTokens(msgs[start:]) > limit {
		start++
	}
	// Land on a plain user message: an assistant turn or a tool result
	// without the call that produced it confuses the model.
	for start < len(msgs) && msgs[start].Role != llm.RoleUser {
		start++
	}
	if start >= len(msgs) {
		// The turn in progress is over budget on its own. Keep it whole,
		// back to the user message that opened it. Cutting inside it
		// would send a tool result without its call, which every
		// provider rejects.
		for start = len(msgs) - 1; start > 0 && msgs[start].Role != llm.RoleUser; start-- {
		}
	}
	return msgs[start:]
}

// capToolResult keeps the start and the end of an oversized tool result.
// The start shows what ran, the end is where errors and summaries land.
func capToolResult(s string) string {
	if len(s) <= maxToolResult {
		return s
	}
	half := maxToolResult / 2
	// ToValidUTF8 drops a character that the byte cut split in two.
	head := strings.ToValidUTF8(s[:half], "")
	tail := strings.ToValidUTF8(s[len(s)-half:], "")
	return fmt.Sprintf("%s\n...(%d chars omitted)...\n%s", head, len(s)-2*half, tail)
}

func flattenMessages(msgs []llm.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		if m.Text == "" {
			fmt.Fprintf(&sb, "%s: [tool interaction]\n", m.Role)
			continue
		}
		text := m.Text
		if runes := []rune(text); len(runes) > 200 {
			text = string(runes[:200]) + "..."
		}
		fmt.Fprintf(&sb, "%s: %s\n", m.Role, text)
	}
	return sb.String()
}

func (a *Agent) ClearHistory(userID string) {
	lock := a.getUserLock(userID)
	lock.Lock()
	defer lock.Unlock()
	a.mu.Lock()
	delete(a.history, userID)
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
	if len(a.messages(userID)) > 0 {
		return
	}
	summary := loadSummary(a.cfg.Dir(), userID)
	if summary == "" {
		return
	}
	preamble := llm.UserMessage("[Previous conversation: "+summary+"]", nil)
	a.setMessages(userID, []llm.Message{preamble})
	logger.Info("loaded persisted summary")
}

// summarizeAndPrepend summarizes evicted messages and prepends them to history.
func (a *Agent) summarizeAndPrepend(userID string, evicted []llm.Message) {
	flat := flattenMessages(evicted)
	if flat == "" {
		return
	}
	summarizeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := a.llm.Complete(summarizeCtx, &llm.Request{
		SystemPrompt: "Summarize this conversation excerpt in 2-3 sentences. Focus on what was asked, what was done, and important outcomes.",
		Messages:     []llm.Message{llm.UserMessage(flat, nil)},
		MaxTokens:    200,
	})
	if err != nil {
		logger.Err(fmt.Errorf("summary failed: %w", err))
		return
	}
	if resp.Text == "" {
		return
	}
	preamble := llm.UserMessage("[Conversation so far: "+resp.Text+"]", nil)
	a.setMessages(userID, append([]llm.Message{preamble}, a.messages(userID)...))
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
		Messages:     []llm.Message{llm.UserMessage(flat, nil)},
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

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
	"github.com/lucasnevespereira/nevinho/memory"
	"github.com/lucasnevespereira/nevinho/schedule"
	"github.com/lucasnevespereira/nevinho/tools"
)

const (
	maxOutputTokens  = 4096
	maxLoops         = 25
	maxHistoryTokens = 30_000
	maxToolResult    = 4000
	chatTimeout      = 5 * time.Minute

	systemPrompt = `You are nevinho, a personal AI assistant running on the user's VPS. The user talks to you from Discord on their phone. They have no terminal access. You are their only way to interact with this machine.

Tools: bash, web_search, web_read, file_list, file_read, file_edit, file_write, grep, find, schedule. Each tool's description spells out what it returns and how failures look. Read those formats literally. Do not guess or paraphrase.

Acting:
- Act without asking on read-only or reversible work. Ask only when credentials are missing, an action is destructive and not clearly requested, or the intent is genuinely ambiguous.
- Before answering questions about a codebase, explore it: file_list for structure, file_read for key files. Base answers on what you read, not assumptions.
- Prefer grep/find over bash for search. Prefer file_edit over file_write for small changes. Prefer file_read over bash cat.

Approval protocol:
- If a tool result starts with "NEEDS_APPROVAL:", the action is paused awaiting the user. Stop calling tools for this turn and stop writing a response. The system will message the user on your behalf. The next user message carries the outcome (approved or denied). Pick up from there.

Citations:
- When you use web_search or web_read results in your reply, add a "Source: <url>" line at the end for each distinct URL. One line per source.

Formatting:
- The user reads on Discord, often on a phone. Keep answers compact. Avoid wide tables, long horizontal lines, and dumping entire files. For diffs, run bash "git diff" instead of re-reading the file.
- Reply in the user's language. If they switch, match the most recent message.

Project scaffolding:
- For multi-file projects (10+ files or multi-hour work), write a plan.md first and check it between steps. Skip plan.md for single-file edits, one-off scripts, or questions.`
)

// ToolEvent fires when the agent begins executing a tool call. Consumers
// (Discord bot, CLI) use it to render a live activity indicator so the user
// sees what the agent is doing instead of staring at a typing indicator.
type ToolEvent struct {
	Name   string
	Detail string
}

// ToolCallback receives ToolEvent values for a specific user.
type ToolCallback func(ToolEvent)

type Agent struct {
	llm     llm.Provider
	tools   *tools.Registry
	cfg     *config.Config
	version string
	selfDoc string

	mu            sync.Mutex
	history       map[string][]json.RawMessage
	userLock      map[string]*sync.Mutex
	cancelFn      map[string]context.CancelFunc
	toolCb        map[string]ToolCallback
	pendingToolID map[string]string
	startTime     time.Time
	tokensIn      int
	tokensOut     int
}

// SetScheduleStore wires the schedule store into the underlying tool
// registry. Optional. When unset, the agent's schedule tool reports
// scheduling as unavailable.
func (a *Agent) SetScheduleStore(s *schedule.Store) {
	a.tools.SetScheduleStore(s)
}

func New(provider llm.Provider, cfg *config.Config, version, selfDoc string) *Agent {
	return &Agent{
		llm:           provider,
		tools:         tools.NewRegistry(cfg),
		cfg:           cfg,
		version:       version,
		selfDoc:       strings.TrimSpace(selfDoc),
		history:       make(map[string][]json.RawMessage),
		userLock:      make(map[string]*sync.Mutex),
		cancelFn:      make(map[string]context.CancelFunc),
		toolCb:        make(map[string]ToolCallback),
		pendingToolID: make(map[string]string),
		startTime:     time.Now(),
	}
}

// SetToolCallback registers a per-user callback fired when a tool call starts.
// Pass nil to clear. Callers should set before Chat and clear in a defer.
func (a *Agent) SetToolCallback(userID string, cb ToolCallback) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cb == nil {
		delete(a.toolCb, userID)
		return
	}
	a.toolCb[userID] = cb
}

func (a *Agent) emitToolEvent(userID, name, detail string) {
	a.mu.Lock()
	cb := a.toolCb[userID]
	a.mu.Unlock()
	if cb == nil {
		return
	}
	defer func() {
		// Never let a buggy consumer callback take down the agent loop.
		_ = recover()
	}()
	cb(ToolEvent{Name: name, Detail: detail})
}

func (a *Agent) Cancel(userID string) bool {
	a.mu.Lock()
	cancel, ok := a.cancelFn[userID]
	a.mu.Unlock()
	if ok && cancel != nil {
		cancel()
		return true
	}
	return false
}

// ChatRequest carries the inputs of one chat turn. Optional fields can
// override the agent's defaults for that turn only without mutating the
// agent's state. Future options (max tokens, temperature, system prompt
// override) belong here too.
type ChatRequest struct {
	UserID  string
	Text    string
	IsVoice bool
	Images  []llm.Image

	// Model, when set, overrides the agent's selected provider for this
	// call only. Useful for scheduled runs that pin a specific model
	// regardless of what the operator currently chats with.
	Model string
}

func (a *Agent) Chat(req ChatRequest) (string, error) {
	provider := a.llm
	if req.Model != "" {
		p, err := llm.Resolve(req.Model, a.cfg.ProviderConfig())
		if err != nil {
			return "", fmt.Errorf("resolve model %q: %w", req.Model, err)
		}
		provider = p
	}

	userID := req.UserID
	text := req.Text
	isVoice := req.IsVoice
	images := req.Images

	lock := a.getUserLock(userID)
	lock.Lock()
	defer lock.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), chatTimeout)
	a.mu.Lock()
	a.cancelFn[userID] = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		delete(a.cancelFn, userID)
		a.mu.Unlock()
	}()

	if p := a.tools.PendingApproval(userID); p != nil && looksLikeApproval(text) {
		switch p.Kind {
		case "path":
			a.tools.ApprovePending(userID)
			logger.Info(fmt.Sprintf("approved: %s", p.Detail))
			text = text + "\n[Access granted to " + p.Detail + ". Retry the file operation.]"
		case "code":
			logger.Info("approved: code execution")
			output := a.tools.ExecutePendingCode(ctx, userID)
			a.replacePendingToolResult(userID, output)
			text = text + "\n[Code execution approved. Output:\n" + output + "]"
		}
	}

	if isVoice {
		logger.Voice(text)
	} else {
		logger.User(text)
	}
	start := time.Now()
	var usage llm.Usage
	var cacheRead int
	var toolsUsed []string

	// Detect user corrections/preferences and persist them
	if entry := memory.DetectCorrection(text); entry != "" {
		if err := memory.Add(a.cfg.Dir(), entry); err != nil {
			logger.Err(fmt.Errorf("memory save failed: %w", err))
		} else {
			logger.Info("memory: saved preference")
		}
	}

	// Caveman block goes first so its style rules aren't drowned out by the
	// longer base prompt and tool descriptions.
	prompt := ""
	if cp := a.cfg.CavemanPrompt(); cp != "" {
		prompt = cp + "\n\n"
	}
	prompt += systemPrompt
	if cwd, err := os.Getwd(); err == nil {
		prompt += "\n\nCurrent working directory: " + cwd
	}
	if home, err := os.UserHomeDir(); err == nil {
		prompt += "\nHome directory: " + home
	}
	if mem := memory.Load(a.cfg.Dir()); mem != "" {
		prompt += "\n\n[Memory]\nThe user has told you these things. Follow them:\n" + mem
	}
	if a.selfDoc != "" {
		prompt += "\n\n" + a.selfDoc
	}

	a.maybeLoadSummary(userID)

	if evicted := a.appendHistory(userID, provider.FormatUserMessage(text, images)); len(evicted) > 2 {
		a.summarizeAndPrepend(userID, evicted)
	}

	for range maxLoops {
		if ctx.Err() != nil {
			return "Cancelled.", nil
		}
		resp, err := provider.Complete(ctx, &llm.Request{
			SystemPrompt: prompt,
			Messages:     a.history[userID],
			Tools:        a.tools.Defs(),
			MaxTokens:    maxOutputTokens,
		})
		if err != nil {
			logger.Err(err)
			return "", err
		}

		usage.In += resp.Usage.In
		usage.Out += resp.Usage.Out
		cacheRead += resp.Usage.CacheRead
		a.appendHistory(userID, resp.AssistantMessage)

		if len(resp.ToolCalls) == 0 {
			a.addTokens(usage.In, usage.Out)
			logger.Done(start, usage.In, usage.Out, cacheRead, toolsUsed, estimateCost(provider.Model(), usage.In, usage.Out))
			logger.Nevinho(resp.Text)
			return resp.Text, nil
		}

		var results []llm.ToolResult
		needsApproval := false
		for _, tc := range resp.ToolCalls {
			toolsUsed = append(toolsUsed, tc.Name)
			detail := toolDetail(tc.Name, tc.Input)
			logger.Tool(tc.Name, detail)
			a.emitToolEvent(userID, tc.Name, detail)
			output := a.executeTool(ctx, tc.Name, tc.Input, userID)
			if len(output) > maxToolResult {
				output = output[:maxToolResult] + "\n...(truncated)"
			}
			errored := isToolError(output)
			logger.ToolResult(tc.Name, output, errored)
			result := llm.ToolResult{ID: tc.ID, Output: output, IsError: errored}
			results = append(results, result)
			if strings.HasPrefix(output, "NEEDS_APPROVAL:") {
				needsApproval = true
				a.mu.Lock()
				a.pendingToolID[userID] = tc.ID
				a.mu.Unlock()
			}
		}

		a.appendHistory(userID, provider.FormatToolResults(results)...)

		if needsApproval {
			p := a.tools.PendingApproval(userID)
			reply := approvalMessage(p)
			a.addTokens(usage.In, usage.Out)
			logger.Done(start, usage.In, usage.Out, cacheRead, toolsUsed, estimateCost(provider.Model(), usage.In, usage.Out))
			logger.Nevinho(reply)
			return reply, nil
		}
	}

	reply := "I hit my limit on tool calls. Try breaking it into smaller tasks."
	a.addTokens(usage.In, usage.Out)
	logger.Done(start, usage.In, usage.Out, cacheRead, toolsUsed, estimateCost(provider.Model(), usage.In, usage.Out))
	logger.Nevinho(reply)
	return reply, nil
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

// PersistAll summarizes each active user's in-memory history and writes it to
// disk. Called on shutdown so the next process start can resume context.
// Respects the parent context for early cancellation; skips users on error so
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

func (a *Agent) Model() string {
	return a.llm.Model()
}

func (a *Agent) SwitchModel(name string) error {
	p, err := llm.Resolve(name, a.cfg.ProviderConfig())
	if err != nil {
		return err
	}

	a.mu.Lock()
	a.llm = p
	a.history = make(map[string][]json.RawMessage)
	a.mu.Unlock()

	if err := a.cfg.Set("MODEL", name); err != nil {
		logger.Err(fmt.Errorf("failed to persist model: %w", err))
	}
	logger.Info("switched to " + name)
	return nil
}

func (a *Agent) Status() string {
	a.mu.Lock()
	in, out := a.tokensIn, a.tokensOut
	a.mu.Unlock()

	model := a.llm.Model()
	uptime := time.Since(a.startTime).Truncate(time.Second)
	paths := a.tools.ApprovedPaths()
	cost := estimateCost(model, in, out)

	var sb strings.Builder
	fmt.Fprintf(&sb, "**nevinho %s**\n\n", a.version)
	fmt.Fprintf(&sb, "Model: `%s`\n", model)
	fmt.Fprintf(&sb, "Uptime: %s\n", formatDuration(uptime))
	fmt.Fprintf(&sb, "Tokens: %d in · %d out\n", in, out)
	fmt.Fprintf(&sb, "Cost: $%.2f\n", cost)

	if len(paths) > 0 {
		fmt.Fprintf(&sb, "\nApproved paths:\n")
		for _, p := range paths {
			fmt.Fprintf(&sb, "• `%s`\n", p)
		}
	}

	return sb.String()
}

func estimateCost(model string, tokensIn, tokensOut int) float64 {
	var inPer1M, outPer1M float64
	switch {
	case strings.Contains(model, "haiku"):
		inPer1M, outPer1M = 0.80, 4.00
	case strings.Contains(model, "sonnet"):
		inPer1M, outPer1M = 3.00, 15.00
	case strings.Contains(model, "opus"):
		inPer1M, outPer1M = 15.00, 75.00
	case strings.Contains(model, "gpt-4o-mini"):
		inPer1M, outPer1M = 0.15, 0.60
	case strings.Contains(model, "gpt-4o"):
		inPer1M, outPer1M = 2.50, 10.00
	case strings.Contains(model, "o3-mini"):
		inPer1M, outPer1M = 1.10, 4.40
	case strings.Contains(model, "o4-mini"):
		inPer1M, outPer1M = 1.10, 4.40
	default:
		return 0 // local models / unknown
	}
	return (float64(tokensIn) * inPer1M / 1_000_000) + (float64(tokensOut) * outPer1M / 1_000_000)
}

func (a *Agent) HasPendingApproval(userID string) bool {
	return a.tools.PendingApproval(userID) != nil
}

func (a *Agent) DrainFileDisplays(userID string) []tools.FileDisplay {
	return a.tools.DrainFileDisplays(userID)
}

func (a *Agent) ClearPending(userID string) {
	a.tools.ClearPending(userID)
}

func (a *Agent) ApprovedPaths() []string {
	return a.tools.ApprovedPaths()
}

func (a *Agent) ClearApprovedPaths() {
	a.tools.ClearApprovedPaths()
}

func (a *Agent) addTokens(in, out int) {
	a.mu.Lock()
	a.tokensIn += in
	a.tokensOut += out
	a.mu.Unlock()
}

func (a *Agent) getUserLock(userID string) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.userLock[userID] == nil {
		a.userLock[userID] = &sync.Mutex{}
	}
	return a.userLock[userID]
}

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

// replacePendingToolResult swaps the stale NEEDS_APPROVAL placeholder in
// history with the real output once the user approves. Without this the LLM
// sees the original tool_use as never-executed and re-emits it, causing an
// approval loop.
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

func (a *Agent) executeTool(ctx context.Context, name string, input json.RawMessage, userID string) (output string) {
	defer func() {
		if r := recover(); r != nil {
			output = fmt.Sprintf("tool crashed: %v", r)
			logger.Err(fmt.Errorf("panic in %s: %v", name, r))
		}
	}()
	return a.tools.Execute(ctx, name, input, userID)
}

func isToolError(output string) bool {
	for _, s := range []string{
		"invalid input:",
		"invalid path:",
		"tool crashed:",
		"Could not find",
		"failed:",
		"(timed out",
		"(cancelled)",
	} {
		if strings.Contains(output, s) {
			return true
		}
	}
	return false
}

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

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
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

func toolDetail(name string, input json.RawMessage) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return ""
	}
	str := func(key string) string {
		raw, ok := fields[key]
		if !ok {
			return ""
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return ""
		}
		return s
	}
	switch name {
	case "web_search":
		return str("query")
	case "web_read":
		return str("url")
	case "bash":
		return str("command")
	case "file_read", "file_write", "file_list", "file_edit":
		return str("path")
	case "grep", "find":
		return str("pattern")
	default:
		return ""
	}
}

var approvalWords = []string{"yes", "yep", "yeah", "sure", "ok", "okay", "go ahead", "allow", "approve", "y", "oui"}

func looksLikeApproval(text string) bool {
	return slices.Contains(approvalWords, strings.ToLower(strings.TrimSpace(text)))
}

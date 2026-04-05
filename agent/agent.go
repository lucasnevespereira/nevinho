package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
	"github.com/lucasnevespereira/nevinho/tools"
)

const (
	maxOutputTokens        = 4096
	maxLoops         = 25
	maxHistoryTokens = 30_000
	maxToolResult    = 4000
	chatTimeout      = 5 * time.Minute

	systemPrompt = `You are nevinho, a personal AI assistant running on the user's VPS. The user talks to you from Discord on their phone. They have no terminal access. You are their only way to interact with this machine.

Tools:
- bash: Run commands
- web_search: Search the web
- web_read: Read a web page
- file_list: List directory contents
- file_read: Read files (supports offset/limit for large files)
- file_edit: Edit a file by replacing an exact string (safer than file_write for small changes)
- file_write: Write an entire file

Guidelines:
- Act, don't ask. You have full access.
- Use bash for system tasks, installs, git, and running code.
- Use web_search then web_read to research topics.
- Use file_list to explore directories, file_read to examine files before modifying them.
- Prefer file_edit over file_write for targeted changes.
- Bash is non-interactive. Always find non-interactive alternatives (e.g. -y, --with-token). If credentials are needed, ask the user to paste them.
- Be concise. The user reads on a phone.
- You reply in Discord. Wrap code and file contents in fenced code blocks with the language tag. Never paste raw file contents without formatting.
- When asked to build a full project, write a plan.md first with structure, features, and steps. Check plan.md before each step and mark completed items. If you lose context, re-read plan.md.`
)

type Agent struct {
	llm     llm.Provider
	tools   *tools.Registry
	cfg     *config.Config
	version string

	mu        sync.Mutex
	history   map[string][]json.RawMessage
	userLock  map[string]*sync.Mutex
	cancelFn  map[string]context.CancelFunc
	startTime time.Time
	tokensIn  int
	tokensOut int
}

func New(provider llm.Provider, cfg *config.Config, version string) *Agent {
	return &Agent{
		llm:       provider,
		tools:     tools.NewRegistry(cfg),
		cfg:       cfg,
		version:   version,
		history:   make(map[string][]json.RawMessage),
		userLock:  make(map[string]*sync.Mutex),
		cancelFn:  make(map[string]context.CancelFunc),
		startTime: time.Now(),
	}
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

func (a *Agent) Chat(userID, text string) (string, error) {
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
			output := a.tools.ExecutePendingCode(userID)
			text = text + "\n[Code execution approved. Output:\n" + output + "]"
		}
	}

	logger.User(text)
	start := time.Now()
	var usage llm.Usage
	var cacheRead int
	var toolsUsed []string

	if evicted := a.appendHistory(userID, a.llm.FormatUserMessage(text)); len(evicted) > 2 {
		a.summarizeAndPrepend(userID, evicted)
	}

	for range maxLoops {
		if ctx.Err() != nil {
			return "Cancelled.", nil
		}
		resp, err := a.llm.Complete(ctx, &llm.Request{
			SystemPrompt: systemPrompt,
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
			logger.Done(start, usage.In, usage.Out, cacheRead, toolsUsed, estimateCost(a.llm.Model(), usage.In, usage.Out))
			logger.Nevinho(resp.Text)
			return resp.Text, nil
		}

		var results []llm.ToolResult
		needsApproval := false
		for _, tc := range resp.ToolCalls {
			toolsUsed = append(toolsUsed, tc.Name)
			logger.Tool(tc.Name, toolDetail(tc.Name, tc.Input))
			output := a.executeTool(tc.Name, tc.Input, userID)
			if len(output) > maxToolResult {
				output = output[:maxToolResult] + "\n...(truncated)"
			}
			results = append(results, llm.ToolResult{ID: tc.ID, Output: output})
			if strings.HasPrefix(output, "NEEDS_APPROVAL:") {
				needsApproval = true
			}
		}

		// The API requires tool results after every assistant message with tool_use
		a.appendHistory(userID, a.llm.FormatToolResults(results)...)

		if needsApproval {
			p := a.tools.PendingApproval(userID)
			reply := approvalMessage(p)
			a.addTokens(usage.In, usage.Out)
			logger.Done(start, usage.In, usage.Out, cacheRead, toolsUsed, estimateCost(a.llm.Model(), usage.In, usage.Out))
			logger.Nevinho(reply)
			return reply, nil
		}
	}

	reply := "I hit my limit on tool calls. Try breaking it into smaller tasks."
	a.addTokens(usage.In, usage.Out)
	logger.Done(start, usage.In, usage.Out, cacheRead, toolsUsed, estimateCost(a.llm.Model(), usage.In, usage.Out))
	logger.Nevinho(reply)
	return reply, nil
}

func (a *Agent) ClearHistory(userID string) {
	lock := a.getUserLock(userID)
	lock.Lock()
	defer lock.Unlock()
	delete(a.history, userID)
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

// estimateCost returns estimated USD cost based on model pricing per 1M tokens.
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

func (a *Agent) executeTool(name string, input json.RawMessage, userID string) (output string) {
	defer func() {
		if r := recover(); r != nil {
			output = fmt.Sprintf("tool crashed: %v", r)
			logger.Err(fmt.Errorf("panic in %s: %v", name, r))
		}
	}()
	return a.tools.Execute(name, input, userID)
}

func approvalMessage(p *tools.Pending) string {
	if p == nil {
		return "Something needs approval. Reply **yes** to allow."
	}
	switch p.Kind {
	case "path":
		return fmt.Sprintf("I need permission to write to `%s`. Reply **yes** to allow.", p.Detail)
	case "code":
		return fmt.Sprintf("I want to run this:\n```\n%s\n```\nReply **yes** to allow.", p.Detail)
	default:
		return "Something needs approval. Reply **yes** to allow."
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

// summarizeAndPrepend asks the LLM to summarize evicted messages and
// inserts the summary as the first message in the user's history.
func (a *Agent) summarizeAndPrepend(userID string, evicted []json.RawMessage) {
	flat := flattenMessages(evicted)
	if flat == "" {
		return
	}
	resp, err := a.llm.Complete(context.Background(), &llm.Request{
		SystemPrompt: "Summarize this conversation excerpt in 2-3 sentences. Focus on what was asked, what was done, and important outcomes.",
		Messages:     []json.RawMessage{a.llm.FormatUserMessage(flat)},
		MaxTokens:    200,
	})
	if err != nil {
		logger.Err(fmt.Errorf("summary failed: %w", err))
		return
	}
	if resp.Text == "" {
		return
	}
	preamble := a.llm.FormatUserMessage("[Conversation so far: " + resp.Text + "]")
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
	default:
		return ""
	}
}

var approvalWords = []string{"yes", "yep", "yeah", "sure", "ok", "okay", "go ahead", "allow", "approve", "y", "oui"}

func looksLikeApproval(text string) bool {
	return slices.Contains(approvalWords, strings.ToLower(strings.TrimSpace(text)))
}

package agent

import (
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
	maxTokens  = 4096
	maxLoops   = 10
	maxHistory = 20

	systemPrompt = `You are nevinho, a personal AI assistant on Discord. You help users by running commands, searching the web, and managing files.

Available tools:
- bash: Execute bash commands
- web_search: Search the web for information
- web_read: Fetch and read a web page
- file_read: Read file contents
- file_write: Create or overwrite files

Guidelines:
- Use bash for system tasks, installs, git, and running code
- Use web_search then web_read to research topics
- Use file_read to examine files before modifying them
- Be concise. Messages should be readable on a phone.
- Prefer action over clarification.`
)

type Agent struct {
	llm     llm.Provider
	tools   *tools.Registry
	cfg     *config.Config
	version string

	mu        sync.Mutex
	history   map[string][]json.RawMessage
	userLock  map[string]*sync.Mutex
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
		startTime: time.Now(),
	}
}

func (a *Agent) Chat(userID, text string) (string, error) {
	lock := a.getUserLock(userID)
	lock.Lock()
	defer lock.Unlock()

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
	var toolsUsed []string

	a.appendHistory(userID, a.llm.FormatUserMessage(text))

	for range maxLoops {
		resp, err := a.llm.Complete(&llm.Request{
			SystemPrompt: systemPrompt,
			Messages:     a.history[userID],
			Tools:        a.tools.Defs(),
			MaxTokens:    maxTokens,
		})
		if err != nil {
			logger.Err(err)
			return "", err
		}

		usage.In += resp.Usage.In
		usage.Out += resp.Usage.Out
		a.history[userID] = append(a.history[userID], resp.AssistantMessage)

		if len(resp.ToolCalls) == 0 {
			a.addTokens(usage.In, usage.Out)
			logger.Done(start, usage.In, usage.Out, toolsUsed)
			logger.Nevinho(resp.Text)
			return resp.Text, nil
		}

		var results []llm.ToolResult
		needsApproval := false
		for _, tc := range resp.ToolCalls {
			toolsUsed = append(toolsUsed, tc.Name)
			logger.Tool(tc.Name, toolDetail(tc.Name, tc.Input))
			output := a.executeTool(tc.Name, tc.Input, userID)
			results = append(results, llm.ToolResult{ID: tc.ID, Output: output})
			if strings.HasPrefix(output, "NEEDS_APPROVAL:") {
				needsApproval = true
			}
		}

		// The API requires tool results after every assistant message with tool_use
		for _, msg := range a.llm.FormatToolResults(results) {
			a.history[userID] = append(a.history[userID], msg)
		}

		if needsApproval {
			p := a.tools.PendingApproval(userID)
			reply := approvalMessage(p)
			a.addTokens(usage.In, usage.Out)
			logger.Done(start, usage.In, usage.Out, toolsUsed)
			logger.Nevinho(reply)
			return reply, nil
		}
	}

	reply := "I hit my limit on tool calls. Try breaking it into smaller tasks."
	a.addTokens(usage.In, usage.Out)
	logger.Done(start, usage.In, usage.Out, toolsUsed)
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
	pc := a.cfg.ProviderConfig()
	var p llm.Provider
	switch {
	case strings.HasPrefix(name, "gpt-") || strings.HasPrefix(name, "o1-") || strings.HasPrefix(name, "o3-") || strings.HasPrefix(name, "o4-"):
		if pc.OpenAIKey == "" {
			return fmt.Errorf("OPENAI_API_KEY not configured")
		}
		p = llm.NewOpenAI(pc.OpenAIKey, "", name)
	case strings.HasPrefix(name, "claude-"):
		if pc.AnthropicKey == "" {
			return fmt.Errorf("ANTHROPIC_API_KEY not configured")
		}
		p = llm.NewAnthropic(pc.AnthropicKey, "", name)
	default:
		if pc.OllamaURL != "" {
			p = llm.NewOpenAI("", pc.OllamaURL, name)
		} else if pc.OpenAIKey != "" {
			p = llm.NewOpenAI(pc.OpenAIKey, "", name)
		} else {
			return fmt.Errorf("unknown model: %s", name)
		}
	}

	a.mu.Lock()
	a.llm = p
	a.history = make(map[string][]json.RawMessage)
	a.mu.Unlock()

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

func (a *Agent) appendHistory(userID string, msg json.RawMessage) {
	a.history[userID] = append(a.history[userID], msg)
	if len(a.history[userID]) > maxHistory {
		a.history[userID] = trimHistory(a.history[userID], maxHistory)
	}
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

func trimHistory(msgs []json.RawMessage, maxLen int) []json.RawMessage {
	if len(msgs) <= maxLen {
		return msgs
	}
	start := len(msgs) - maxLen
	// Walk forward to find a plain user message
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
	case "file_read", "file_write":
		return str("path")
	default:
		return ""
	}
}

var approvalWords = []string{"yes", "yep", "yeah", "sure", "ok", "okay", "go ahead", "allow", "approve", "y", "oui"}

func looksLikeApproval(text string) bool {
	return slices.Contains(approvalWords, strings.ToLower(strings.TrimSpace(text)))
}

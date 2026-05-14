package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
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

// ToolPhase marks whether a ToolEvent fires as a tool starts or finishes.
type ToolPhase string

const (
	ToolStart ToolPhase = "start"
	ToolDone  ToolPhase = "done"
)

// ToolEvent fires as the agent runs a tool call: once when it starts, once
// when it finishes. Consumers render live activity from start events and
// the result preview from done events.
type ToolEvent struct {
	Phase   ToolPhase
	Name    string
	Detail  string
	Output  string // set on ToolDone
	IsError bool   // set on ToolDone
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

// SetScheduleStore wires the schedule store into the underlying tool
// registry. Optional. When unset, the agent's schedule tool reports
// scheduling as unavailable.
func (a *Agent) SetScheduleStore(s *schedule.Store) {
	a.tools.SetScheduleStore(s)
}

// SetStrictTools turns on strict tool permissions, used when nevinho runs
// on the user's own machine. In strict mode every bash command needs
// approval. Off by default, which is the VPS posture.
func (a *Agent) SetStrictTools(strict bool) {
	a.tools.SetStrict(strict)
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

func (a *Agent) emitToolEvent(userID string, ev ToolEvent) {
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
	cb(ev)
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

// SetConfig writes or clears a config key; an empty value clears it. When
// the key authenticates an LLM provider, the provider is reloaded so the
// change takes effect without a restart.
func (a *Agent) SetConfig(key, value string) error {
	var err error
	if value == "" {
		err = a.cfg.Delete(key)
	} else {
		err = a.cfg.Set(key, value)
	}
	if err != nil {
		return err
	}
	if isLLMKey(key) {
		// Best effort: the key is saved regardless. If the current model
		// no longer resolves, the caller can switch with /model.
		_ = a.SwitchModel(a.Model())
	}
	return nil
}

// ConfigKeys reports every config key and whether it is set.
func (a *Agent) ConfigKeys() []config.KeyStatus {
	return a.cfg.Keys()
}

// isLLMKey reports whether a config key authenticates an LLM provider, so a
// change to it should reload the provider.
func isLLMKey(key string) bool {
	switch key {
	case "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY",
		"GROQ_API_KEY", "OPENROUTER_API_KEY", "OLLAMA_MODEL":
		return true
	}
	return false
}

// AvailableModels lists the catalog models whose provider has a key
// configured, so a switch menu shows only models that can actually run.
func (a *Agent) AvailableModels() []string {
	pc := a.cfg.ProviderConfig()
	var out []string
	for _, p := range []struct {
		name string
		set  bool
	}{
		{"anthropic", pc.AnthropicKey != ""},
		{"openai", pc.OpenAIKey != ""},
		{"gemini", pc.GeminiKey != ""},
		{"groq", pc.GroqKey != ""},
		{"openrouter", pc.OpenRouterKey != ""},
	} {
		if p.set {
			out = append(out, config.KnownModels[p.name]...)
		}
	}
	return out
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

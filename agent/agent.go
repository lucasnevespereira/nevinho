package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
	"github.com/lucasnevespereira/nevinho/tools"
)

const (
	maxTokens  = 4096
	maxLoops   = 10
	maxHistory = 20

	systemPrompt = `You are nevinho, a personal AI assistant on Discord. You are resourceful and always try to deliver an answer.

## Style
- Be concise. Messages should be short and readable on a phone.
- Use markdown: **bold**, ` + "`" + `code` + "`" + `, code blocks, bullet points. Keep it clean.

## Tool use
- Always prefer action over clarification. Just do it.
- Be persistent. If one approach fails, try another. Never say "I can't access that" without trying alternatives.
- web_search gives you results with titles, URLs, and descriptions — that's often enough to answer without reading the page.
- web_read works best on articles and docs. It won't work on JS-heavy sites (YouTube, Twitter, Reddit). If it returns garbage, use the search results you already have.
- You can chain tools: search first, then read promising links, then summarize.
- run_code is powerful — use it for calculations, data processing, or anything that needs precision.
- For filesystem tasks (listing, counting, finding files), prefer bash over python — it's simpler and less error-prone.
- When writing python/node, always use print()/console.log() to output results. Use semicolons or proper newlines between statements.
- If a tool call fails, try a different approach instead of repeating the same command.

## File operations
- Absolute paths (~/path, /path) and simple names (notes.txt → workspace) both work.
- If a file write fails due to permissions, tell the user which directory needs access and that they should reply yes to allow it.`
)

type Agent struct {
	llm   llm.Provider
	tools *tools.Registry

	mu       sync.Mutex
	history  map[string][]json.RawMessage
	userLock map[string]*sync.Mutex
}

func New(provider llm.Provider) *Agent {
	return &Agent{
		llm:      provider,
		tools:    tools.NewRegistry(),
		history:  make(map[string][]json.RawMessage),
		userLock: make(map[string]*sync.Mutex),
	}
}

func (a *Agent) Chat(userID, text string) (string, error) {
	// Serialize messages per user to prevent history corruption
	lock := a.getUserLock(userID)
	lock.Lock()
	defer lock.Unlock()

	// Handle pending approval
	if p := a.tools.PendingApproval(userID); p != nil && looksLikeApproval(text) {
		switch p.Kind {
		case "path":
			a.tools.ApprovePending(userID)
			logger.Info(fmt.Sprintf("approved: %s", shortenDetail(p)))
			text = text + "\n[Access granted to " + p.Detail + ". Retry the file operation.]"
		case "code":
			logger.Info("approved: code execution")
			output := a.tools.ExecutePendingCode(userID)
			// Feed the result back to the LLM so it can respond
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

		// No tool calls — return the text response
		if len(resp.ToolCalls) == 0 {
			logger.Done(start, usage.In+usage.Out, toolsUsed)
			logger.Nevinho(resp.Text)
			return resp.Text, nil
		}

		// Execute tools and collect results
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

		// Always append tool results before any early return — the API
		// requires tool responses after every assistant message with tool_calls
		for _, msg := range a.llm.FormatToolResults(results) {
			a.history[userID] = append(a.history[userID], msg)
		}

		if needsApproval {
			p := a.tools.PendingApproval(userID)
			reply := approvalMessage(p)
			logger.Done(start, usage.In+usage.Out, toolsUsed)
			logger.Nevinho(reply)
			return reply, nil
		}
	}

	reply := "I hit my limit on tool calls. Try breaking it into smaller tasks."
	logger.Done(start, usage.In+usage.Out, toolsUsed)
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

// getUserLock returns a per-user mutex so different users can chat
// concurrently while the same user's messages are serialized.
func (a *Agent) getUserLock(userID string) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.userLock[userID] == nil {
		a.userLock[userID] = &sync.Mutex{}
	}
	return a.userLock[userID]
}

// appendHistory adds a message and trims if over the limit, ensuring
// the window always starts on a plain user message (not a tool result
// or assistant with tool_calls, which would break the API contract).
func (a *Agent) appendHistory(userID string, msg json.RawMessage) {
	a.history[userID] = append(a.history[userID], msg)
	if len(a.history[userID]) > maxHistory {
		a.history[userID] = trimHistory(a.history[userID], maxHistory)
	}
}

// executeTool runs a tool with panic recovery so a broken tool can't crash the bot.
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
		return fmt.Sprintf("I need permission to write to `%s`. Reply **yes** to allow.", shortenDetail(p))
	case "code":
		return fmt.Sprintf("I want to run this:\n```\n%s\n```\nReply **yes** to allow.", p.Detail)
	default:
		return "Something needs approval. Reply **yes** to allow."
	}
}

func shortenDetail(p *tools.Pending) string {
	if p.Kind == "path" {
		// Use ~ for home directory in display
		return p.Detail
	}
	return p.Detail
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
		// Skip tool messages (OpenAI tool results)
		if peek.Role == "tool" {
			start++
			continue
		}
		// Skip assistant messages (may have tool_calls)
		if peek.Role == "assistant" {
			start++
			continue
		}
		// Skip Anthropic-style tool results (user message with array content)
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

// toolDetail extracts a one-line summary from tool input for logging.
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
	case "run_code":
		lang := str("language")
		code := str("code")
		if lang != "" && code != "" {
			return lang + ": " + code
		}
		return code
	case "file_read", "file_write":
		return str("path")
	default:
		return ""
	}
}

func looksLikeApproval(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	for _, word := range []string{"yes", "yep", "yeah", "sure", "ok", "okay", "go ahead", "allow", "approve", "y", "oui"} {
		if t == word {
			return true
		}
	}
	return false
}

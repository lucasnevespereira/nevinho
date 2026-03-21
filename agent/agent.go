package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/tools"
)

const (
	maxTokens    = 4096
	maxLoops     = 10
	systemPrompt = `You are a personal AI assistant available through Discord. You help the user with tasks using the tools available to you.

Be concise. Discord messages should be short and readable on a phone.
Use markdown to structure your responses: **bold** for emphasis, ` + "`" + `code` + "`" + ` for technical terms, code blocks for snippets, and bullet points for lists. Keep it readable.

When the user asks you to do something:
1. Do it (don't ask for confirmation unless genuinely ambiguous)
2. Show the result
3. Keep it brief

When using tools, prefer action over clarification. If the user says "check if example.com is up", just do it.

For file operations: use absolute paths (~/path or /path) or simple names (notes.txt goes to workspace).
If a file write is denied due to permissions, tell the user which directory needs access and ask them to confirm. Once they confirm, call approve_path with that directory, then retry the write.`
)

type Agent struct {
	llm     llm.Provider
	tools   *tools.Registry
	history map[string][]json.RawMessage
}

func New(provider llm.Provider) *Agent {
	return &Agent{
		llm:     provider,
		tools:   tools.NewRegistry(),
		history: make(map[string][]json.RawMessage),
	}
}

func (a *Agent) Chat(userID, text string) (string, error) {
	logRecv(text)
	start := time.Now()
	var usage llm.Usage
	var toolsUsed []string

	a.history[userID] = append(a.history[userID], a.llm.FormatUserMessage(text))
	if len(a.history[userID]) > 20 {
		a.history[userID] = a.history[userID][len(a.history[userID])-20:]
	}

	for i := 0; i < maxLoops; i++ {
		resp, err := a.llm.Complete(&llm.Request{
			SystemPrompt: systemPrompt,
			Messages:     a.history[userID],
			Tools:        a.toolDefs(),
			MaxTokens:    maxTokens,
		})
		if err != nil {
			logErr(err)
			return "", err
		}

		usage.In += resp.Usage.In
		usage.Out += resp.Usage.Out
		a.history[userID] = append(a.history[userID], resp.AssistantMessage)

		if len(resp.ToolCalls) == 0 {
			logDone(a.llm.Model(), start, usage, toolsUsed)
			logSend(resp.Text)
			return resp.Text, nil
		}

		var results []llm.ToolResult
		for _, tc := range resp.ToolCalls {
			toolsUsed = append(toolsUsed, tc.Name)
			logTool(tc.Name)
			output := a.tools.Execute(tc.Name, tc.Input, userID)
			results = append(results, llm.ToolResult{ID: tc.ID, Output: output})
		}

		for _, msg := range a.llm.FormatToolResults(results) {
			a.history[userID] = append(a.history[userID], msg)
		}
	}

	reply := "I hit my limit on tool calls. Try breaking it into smaller tasks."
	logDone(a.llm.Model(), start, usage, toolsUsed)
	logSend(reply)
	return reply, nil
}

func (a *Agent) ClearHistory(userID string) {
	delete(a.history, userID)
}

func (a *Agent) toolDefs() []llm.ToolDef {
	return []llm.ToolDef{
		{
			Name:        "web_read",
			Description: "Fetch a URL and return the text content.",
			Schema:      `{"type":"object","properties":{"url":{"type":"string","description":"The URL to fetch"}},"required":["url"]}`,
		},
		{
			Name:        "web_search",
			Description: "Search the web and return top results.",
			Schema:      `{"type":"object","properties":{"query":{"type":"string","description":"The search query"}},"required":["query"]}`,
		},
		{
			Name:        "run_code",
			Description: "Execute a code snippet. Supports python3, node, and bash.",
			Schema:      `{"type":"object","properties":{"language":{"type":"string","enum":["python3","node","bash"],"description":"Programming language"},"code":{"type":"string","description":"Code to execute"}},"required":["language","code"]}`,
		},
		{
			Name:        "file_read",
			Description: "Read a file. Supports absolute paths (~/path, /path) or workspace-relative names.",
			Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"File path"}},"required":["path"]}`,
		},
		{
			Name:        "file_write",
			Description: "Write to a file. Creates directories automatically. Absolute paths require user permission — if denied, ask the user to confirm and call approve_path.",
			Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"File path"},"content":{"type":"string","description":"Content to write"}},"required":["path","content"]}`,
		},
		{
			Name:        "approve_path",
			Description: "Grant write access to a directory. Only call AFTER the user has explicitly confirmed. Never call without user confirmation.",
			Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"Directory path to approve"}},"required":["path"]}`,
		},
	}
}

// --- Logging ---

const (
	colorReset  = "\033[0m"
	colorCyan   = "\033[36m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorDim    = "\033[2m"
)

func logRecv(text string) { log.Printf("%srecv%s  %s", colorCyan, colorReset, truncate(text, 100)) }
func logSend(text string) { log.Printf("%ssend%s  %s", colorGreen, colorReset, truncate(text, 100)) }
func logTool(name string) { log.Printf("%stool%s  %s", colorYellow, colorReset, name) }
func logErr(err error)    { log.Printf("%sfail%s  %v", colorRed, colorReset, err) }

func logDone(model string, start time.Time, usage llm.Usage, toolsUsed []string) {
	elapsed := time.Since(start).Round(time.Millisecond)
	parts := []string{
		model,
		fmt.Sprintf("%dms", elapsed.Milliseconds()),
		fmt.Sprintf("%d in / %d out tokens", usage.In, usage.Out),
	}
	if len(toolsUsed) > 0 {
		parts = append(parts, strings.Join(toolsUsed, ", "))
	}
	log.Printf("%sdone%s  %s", colorDim, colorReset, strings.Join(parts, " | "))
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

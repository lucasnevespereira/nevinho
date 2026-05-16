package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/logger"
	"github.com/lucasnevespereira/nevinho/memory"
	"github.com/lucasnevespereira/nevinho/tools"
)

func (a *Agent) Chat(userID, text string, isVoice bool, images []llm.Image) (string, error) {
	return a.chat(userID, text, isVoice, images, tools.SourceInteractive)
}

// ChatScheduled is the entry point the schedule runner uses. It tags the
// turn with SourceScheduled so the tool registry filters out shell and
// write tools and the dispatch layer refuses them by capability.
func (a *Agent) ChatScheduled(userID, prompt string) (string, error) {
	return a.chat(userID, prompt, false, nil, tools.SourceScheduled)
}

func (a *Agent) chat(userID, text string, isVoice bool, images []llm.Image, source tools.Source) (string, error) {
	lock := a.getUserLock(userID)
	lock.Lock()
	defer lock.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), chatTimeout)
	ctx = tools.WithExecContext(ctx, tools.ExecContext{
		Source: source,
		Caps:   tools.DefaultCaps[source],
		UserID: userID,
	})
	a.mu.Lock()
	a.cancelFn[userID] = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		delete(a.cancelFn, userID)
		a.mu.Unlock()
	}()

	if p := a.tools.PendingApproval(userID); p != nil {
		switch {
		case looksLikeApproval(text):
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
		case looksLikeDenial(text):
			// Clear the pending action and let the model see it was
			// declined, so it acknowledges and moves on instead of looping.
			logger.Info("denied: " + p.Detail)
			a.tools.ClearPending(userID)
			a.replacePendingToolResult(userID, "denied by user")
			text = text + "\n[The user declined that action. Do not retry it. Acknowledge and move on.]"
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
	prompt += a.systemPrompt()
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

	if evicted := a.appendHistory(userID, a.llm.FormatUserMessage(text, images)); len(evicted) > 2 {
		a.summarizeAndPrepend(userID, evicted)
	}

	var lastText string
	for range maxLoops {
		if ctx.Err() != nil {
			return "Cancelled.", nil
		}
		resp, err := a.llm.Complete(ctx, &llm.Request{
			SystemPrompt: prompt,
			Messages:     a.history[userID],
			Tools:        a.tools.DefsFor(ctx),
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
		if resp.Text != "" {
			lastText = resp.Text
		}

		if len(resp.ToolCalls) == 0 {
			reply := resp.Text
			// Empty terminal turn: the model stopped with no tool call
			// and no text. Gemini does this after a tool loop. One more
			// pass with tools off forces it to answer in words.
			if reply == "" {
				nudged, nudgeUsage := a.nudgeForReply(ctx, userID, prompt)
				usage.In += nudgeUsage.In
				usage.Out += nudgeUsage.Out
				cacheRead += nudgeUsage.CacheRead
				reply = nudged
			}
			if reply == "" {
				reply = lastText
			}
			if reply == "" {
				reply = "I finished, but could not put a reply together. Ask me to recap."
			}
			// Output hit the token cap mid-sentence. Flag it instead of
			// sending a silently truncated message.
			if resp.StopReason == llm.StopMaxTokens && reply == resp.Text {
				reply += "\n\n_(cut off at the length limit. Ask me to continue.)_"
			}
			return a.finish(start, usage, cacheRead, toolsUsed, reply)
		}

		var results []llm.ToolResult
		needsApproval := false
		for _, tc := range resp.ToolCalls {
			toolsUsed = append(toolsUsed, tc.Name)
			detail := toolDetail(tc.Name, tc.Input)
			logger.Tool(tc.Name, detail)
			a.emitToolEvent(userID, ToolEvent{Phase: ToolStart, Name: tc.Name, Detail: detail, Input: tc.Input})
			output := a.executeTool(ctx, tc.Name, tc.Input, userID)
			if len(output) > maxToolResult {
				output = output[:maxToolResult] + "\n...(truncated)"
			}
			errored := isToolError(output)
			logger.ToolResult(tc.Name, output, errored)
			a.emitToolEvent(userID, ToolEvent{Phase: ToolDone, Name: tc.Name, Detail: detail, Input: tc.Input, Output: output, IsError: errored})
			result := llm.ToolResult{ID: tc.ID, Output: output, IsError: errored}
			results = append(results, result)
			if strings.HasPrefix(output, "NEEDS_APPROVAL:") {
				needsApproval = true
				a.mu.Lock()
				a.pendingToolID[userID] = tc.ID
				a.mu.Unlock()
			}
		}

		a.appendHistory(userID, a.llm.FormatToolResults(results)...)

		if needsApproval {
			p := a.tools.PendingApproval(userID)
			return a.finish(start, usage, cacheRead, toolsUsed, approvalMessage(p))
		}
	}

	return a.finish(start, usage, cacheRead, toolsUsed, "I hit my limit on tool calls. Try breaking it into smaller tasks.")
}

// finish records token usage, logs the completed turn, and returns the
// reply. Every terminal path in Chat goes through it so accounting and
// logging stay identical no matter which exit the loop takes.
func (a *Agent) finish(start time.Time, usage llm.Usage, cacheRead int, toolsUsed []string, reply string) (string, error) {
	a.addTokens(usage.In, usage.Out)
	logger.Done(start, usage.In, usage.Out, cacheRead, toolsUsed, estimateCost(a.llm.Model(), usage.In, usage.Out))
	logger.Nevinho(reply)
	return reply, nil
}

// nudgeForReply runs one extra completion with tools disabled, used when
// the model ends a turn with neither text nor a tool call. With no tools
// to reach for, it has to answer in plain text. Returns "" if even this
// comes back empty, leaving the caller to fall back.
func (a *Agent) nudgeForReply(ctx context.Context, userID, prompt string) (string, llm.Usage) {
	a.appendHistory(userID, a.llm.FormatUserMessage(
		"[Your last turn was empty. Reply to the user now in plain text. Summarize what you did and answer them.]", nil))
	resp, err := a.llm.Complete(ctx, &llm.Request{
		SystemPrompt: prompt,
		Messages:     a.history[userID],
		MaxTokens:    maxOutputTokens,
	})
	if err != nil {
		logger.Err(fmt.Errorf("reply nudge failed: %w", err))
		return "", llm.Usage{}
	}
	a.appendHistory(userID, resp.AssistantMessage)
	return resp.Text, resp.Usage
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

var denialWords = []string{"no", "nope", "nah", "deny", "cancel", "stop", "n", "non"}

func looksLikeDenial(text string) bool {
	return slices.Contains(denialWords, strings.ToLower(strings.TrimSpace(text)))
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

func (a *Agent) RevokePath(display string) bool {
	return a.tools.RevokePath(display)
}

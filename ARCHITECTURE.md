# Architecture

How nevinho is laid out, how a message flows through it, and how the context window stays small.

The same agent core powers two transports. The Discord bot runs on a VPS and serves the owner over DMs. The TUI runs locally and talks to the same agent over the terminal. Everything below the transport boundary is shared.

---

## System Overview

```
   Local terminal (TUI)        VPS daemon (Discord)
   --------------------        --------------------
   nevinho                     nevinho start
        \                            /
         \                          /
          v                        v
        +----------------------------+
        |  Agent.Chat(userID, text)  |
        |  one core, two callers     |
        |   . per-user lock          |
        |   . cancellable context    |
        |   . approval gate          |
        |   . loop, max 25 turns     |
        +-------------|--------------+
                      v
        +----------------------------+
        |  Context window            |
        |  [system prompt] (cached)  |
        |  [tool defs]     (cached)  |
        |  [summary preamble]        |
        |  [user/assistant/tool msgs]|
        |  budget: 30k tokens        |
        +-------------|--------------+
                      v
        +----------------------------+
        |  LLM provider              |
        |  anthropic / openai /      |
        |  gemini / groq /           |
        |  openrouter / ollama       |
        +----------------------------+
                      |
                      v
        +----------------------------+
        |  Tool registry             |
        |   . bash (2m timeout)      |
        |   . file_read/write/edit   |
        |   . web_search / web_read  |
        |   . find / grep / file_list|
        |   . schedule (daemon only) |
        |   . approval flow          |
        +----------------------------+
```

---

## Two Transports, One Core

`agent.Agent` does not know it is talking to a terminal or a chat client. The transport handles I/O and approvals. The agent handles the loop.

The only thing that differs is `RunMode`, set when the agent is constructed.

- `ModeLocal` is for the TUI. Strict approvals. Every bash command and every write outside the cwd needs a yes. The system prompt tells the model it is running on the user's own machine.
- `ModeDaemon` is for Discord on a VPS. Same approval mechanism but with a longer trust list for the owner. System prompt tells the model it is the owner's remote assistant.

Both modes pass the same `userID` through `Chat()`. Local always uses `cli-local`. Discord uses the Discord user ID. Per-user state (history, locks, pending approvals) is keyed by this string.

---

## The Agentic Loop

This is the heart of `agent.Chat()`. One user message can trigger many LLM calls as the model uses tools.

```
User sends message
    |
    v
appendHistory(user message)
    |. maxHistoryTokens exceeded? trim oldest, summarize evicted
    |
    v
+--[LOOP start, max 25 iterations]--+
|                                    |
|  ctx cancelled? return             |
|         |                          |
|         v                          |
|  llm.Complete(                     |
|    system prompt, history,         |
|    tool defs                       |
|  )                                 |
|         |                          |
|         v                          |
|  appendHistory(assistant reply)    |
|         |                          |
|         v                          |
|  no tool calls? return text        |
|         |                          |
|  has tool calls:                   |
|    for each call:                  |
|      . emit ToolStart event        |
|      . execute via registry        |
|      . cap result at 4 KB          |
|      . emit ToolDone event         |
|         |                          |
|         v                          |
|  appendHistory(tool results)       |
|         |                          |
|         v                          |
|  needs approval? return early      |
|         |                          |
|  next iteration ---> LOOP          |
|                                    |
+------------------------------------+
```

Every terminal path in `Chat()` goes through `finish()`. That is where token accounting, logging, and the final reply assembly live. If the model ends a turn with neither text nor a tool call (Gemini does this after a tool loop), `nudgeForReply()` runs one more completion with tools off so the user always gets a reply.

---

## Context Engineering

Every token entering the context window has to earn its place. Four mechanisms enforce this.

### 1. Prompt caching

System prompt and tool definitions are marked with `cache_control: ephemeral` on providers that support it. Turns 2+ reuse the cached prefix instead of re-tokenizing ~900 tokens.

Savings show up as `cache_read_input_tokens` in the response and feed the `/status` cost line.

### 2. Tool result capping

Every tool output is truncated before it enters history.

```
Tool layer:    bash output capped at ~8 KB
               file_read capped at 100 KB on disk, less in history
               web_read capped at ~8 KB

Agent layer:   all tool results capped at 4 KB before history
```

This stops one `cat` or one page fetch from bloating every future turn.

### 3. Token-aware history trimming

History is bounded by `maxHistoryTokens` (30k), not by message count.

```
appendHistory(new message)
    |
    v
estimateTokens(history) > 30,000?
    |
    no -> done
    |
    yes -> trimHistoryByTokens:
             1. find earliest index where remaining msgs fit the budget
             2. walk forward to a clean boundary:
                . skip orphaned tool results
                . skip orphaned assistant messages
                . skip tool_use array messages
                . land on a plain user message
             3. return msgs[start:]
```

Estimation is `len(json_bytes) / 4` per message. Rough but consistent. Off by 20% just means trimming a little earlier or later.

### 4. Summarize on trim

Evicted messages do not just vanish. The agent asks the LLM to summarize them in 2 to 3 sentences and prepends that summary to history. About 200 output tokens once, in exchange for context that would otherwise be lost.

---

## Persistence (Elephant)

When `ELEPHANT=on`, the agent writes a summary of each active user's history to disk on shutdown. On next start the summary loads back so the conversation resumes with context intact. Off by default.

Files live in `~/.nevinho/summaries/<userID>.md`. `/session` in the TUI dumps the current summary.

User preferences detected from corrections (the model picking up "always", "never", "remember") are stored separately in `~/.nevinho/memory.md` and survive `/forget`. `/memory` in the TUI dumps them.

---

## Concurrency

```
Agent (shared)
  |
  +. mu (sync.Mutex): history, userLock, cancelFn, pendingToolID
  |
  +. userLock[userID]: one mutex per user, serializes Chat() calls
  |
  +. cancelFn[userID]: per-user cancellation via context
```

Different users run in parallel. The same user's messages serialize so no interleaving inside one conversation. `Cancel(userID)` calls the stored cancel function, which the loop checks each iteration.

---

## Approval Flow

```
Tool execution
    |
    v
Path or command needs approval?
    |.. no -> execute, return output
    |
    |.. yes -> store Pending{Kind, Detail} on the registry
               return "NEEDS_APPROVAL: <reason>"
               agent returns the prompt as its reply
               user replies yes or no
               next Chat() call detects the pending state:
                 . approval words -> execute, replace placeholder result
                 . denial words   -> clear pending, tell model to move on
```

Approved paths persist in `~/.nevinho/approved_paths.json`. `/paths` in the TUI lists them and lets you revoke one. `/paths clear` wipes the lot.

In `ModeLocal`, every bash command goes through this gate. In `ModeDaemon`, only dangerous patterns (rm, sudo, curl-piped-to-shell, and similar) trip it.

---

## TUI Rendering

The TUI uses Bubble Tea but does not enter the alternate screen. Conversation blocks are pushed straight into the terminal's regular scrollback with `tea.Println`. The terminal handles wheel scroll, text selection, and URL clicking natively, the same way Claude Code and opencode do it.

Only the live region at the bottom (input box, working line, status bar, pickers) is managed by Bubble Tea. Blocks rendered to scrollback are capped at 100 columns so wide terminals stay readable.

Tool events come from the agent over a buffered channel (capacity 64). The TUI listens on it and prints a card per `ToolDone` event. A full channel drops the event rather than blocking the agent.

---

## What Enters the Context Window

```
+--------------------------------------------------+
| System prompt             ~220 tokens   (cached) |
| Tool definitions          ~680 tokens   (cached) |
|--------------------------------------------------|
| [Summary preamble]        50 to 100 tokens       |
| User message 1            variable               |
| Assistant reply 1         variable               |
| User message 2 (tools)    variable               |
| Tool results              variable, capped 4 KB  |
| ...                                              |
| Latest user message       variable               |
|--------------------------------------------------|
| maxHistoryTokens          ~30,000 tokens         |
| Max output                4,096 tokens           |
+--------------------------------------------------+
```

The cached prefix (~900 tokens) is essentially free after the first turn. The rest is the sliding window of the conversation.

---

## Constants

| Name | Value | Purpose |
|------|-------|---------|
| `maxOutputTokens` | 4,096 | Max output tokens per LLM call |
| `maxLoops` | 25 | Max tool-call iterations per `Chat()` |
| `maxHistoryTokens` | 30,000 | Token budget for conversation history |
| `maxToolResult` | 4,000 | Max bytes per tool result in history |
| `chatTimeout` | 5 min | Whole-turn timeout |
| `bashTimeout` | 120 s | Bash command timeout |
| `httpTimeout` | 15 s | HTTP request timeout |
| `maxContentWidth` | 100 | TUI block render cap |

---

## Package Map

```
nevinho/
  main.go                CLI entry point. nevinho with no args launches the TUI.
  cmd/
    chat.go              nevinho chat. Same as no-arg launch.
    serve.go             nevinho start. Discord daemon.
    service.go           nevinho service install/uninstall. systemd unit.
    config.go            nevinho config get/set/clear.
    upgrade.go           nevinho upgrade. Self-update.
  agent/
    agent.go             Agent struct, constructors, public API (Model,
                         SwitchModel, SetConfig, Usage, AvailableModels,
                         Status, RevokePath, and others).
    loop.go              Chat(), the agentic loop, approval handshake,
                         tool execution, nudgeForReply.
    history.go           appendHistory, trimHistoryByTokens,
                         summarizeAndPrepend, MemoryView, SummaryView,
                         ClearHistory.
    persistence.go       Per-user summary path helpers, sanitization.
  llm/
    provider.go          Provider interface, message types, stop reasons.
    anthropic.go         Anthropic Messages API plus prompt caching.
    openai.go            OpenAI chat completions (plus Ollama via the
                         compatible endpoint).
    gemini.go            Gemini generateContent. Tool results go in
                         user-role contents, not function-role.
    errors.go            FriendlyError. Maps provider error codes to
                         human text.
    http.go              Shared HTTP client with retry.
    image.go             Base64 image helpers.
  tools/
    registry.go          Tool dispatch, approval bookkeeping,
                         approved-path persistence.
    bash.go              Shell execution and danger pattern detection.
    file.go              file_read/write/edit/list with path sandboxing.
    find.go, grep.go     Code search.
    web.go               Tavily search and page fetch.
    schedule.go          Cron scheduler (daemon only).
  tui/
    tui.go               Bubble Tea model, inline rendering, slash
                         commands, pickers.
    blocks.go            Render of user, agent, hint, error, approval,
                         and tool blocks.
    selector.go          Filterable picker. Backs /model, /config,
                         /paths.
  discord/
    bot.go, messages.go  Discord session, message handling.
    commands.go          Slash commands.
    indicator.go         Typing indicator while a turn runs.
    attachments.go       Image and voice handling.
  config/
    config.go            Encrypted config with env/file/runtime layers.
    models.go            Known model catalog per provider.
    setup.go             Interactive `nevinho setup` wizard.
  crypto/
    crypto.go            AES-256-GCM for the config store.
  logger/
    logger.go            Coloured terminal output for the daemon.
  memory/
    memory.go            User preference detection and storage.
  schedule/
    store.go             Cron job persistence.
```

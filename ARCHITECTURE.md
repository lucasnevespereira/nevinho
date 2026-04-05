# Architecture

How nevinho processes a message, manages context, and keeps token spend under control.

---

## System Overview

```
Discord DM                  nevinho (VPS)                    LLM API
----------                  -------------                    -------
                     +---------------------------+
  user msg -------->|  Discord Bot               |
                    |  - auth (owner-only)       |
                    |  - slash commands           |
                    |  - typing indicator (8s)    |
                    +------------|---------------+
                                 |
                                 v
                    +---------------------------+
                    |  Agent.Chat()              |
                    |  - per-user lock           |
                    |  - cancellable context     |
                    |  - approval gate           |
                    |  - agentic loop (max 10)   |
                    +------------|---------------+
                                 |
                    +------------|---------------+
                    |  Context Window            |
                    |  ~~~~~~~~~~~~~             |
                    |  [system prompt] (cached)  |
                    |  [tool defs]     (cached)  |
                    |  [summary preamble]        |
                    |  [user/assistant/tool msgs]|
                    |                            |
                    |  Budget: 30k tokens        |
                    +------------|---------------+
                                 |
                                 v
                    +---------------------------+
                    |  LLM Provider              |
                    |  - Anthropic / OpenAI      |   ------>  API
                    |  - prompt caching          |  <------  response
                    |  - raw HTTP, no SDKs       |
                    +---------------------------+
                                 |
                                 v
                    +---------------------------+
                    |  Tool Registry             |
                    |  - bash (2m timeout)       |
                    |  - web_search / web_read   |
                    |  - file_read / file_write  |
                    |  - danger detection        |
                    |  - approval flow           |
                    +---------------------------+
```

---

## The Agentic Loop

This is the core of `agent.Chat()`. One user message can trigger multiple LLM calls as the model uses tools.

```
User sends message
    |
    v
appendHistory(user message)
    |--- budget exceeded? --> trim oldest, summarize evicted
    |
    v
+--[LOOP start, max 10 iterations]--+
|                                     |
|   Check ctx cancelled? --> return   |
|           |                         |
|           v                         |
|   llm.Complete(                     |
|     system prompt,                  |
|     history,                        |
|     tool defs                       |
|   )                                 |
|           |                         |
|           v                         |
|   appendHistory(assistant reply)    |
|           |                         |
|           v                         |
|   No tool calls? --> return text    |
|           |                         |
|   Has tool calls:                   |
|     for each tool call:             |
|       - execute tool                |
|       - cap result at 4KB           |
|       - check if needs approval     |
|           |                         |
|           v                         |
|   appendHistory(tool results)       |
|           |                         |
|           v                         |
|   Needs approval? --> return early  |
|           |                         |
|   [next iteration] -----> LOOP      |
|                                     |
+-------------------------------------+
```

---

## Context Engineering

Every token entering the context window must earn its place. Four mechanisms enforce this:

### 1. Prompt Caching

The system prompt and tool definitions are marked with `cache_control: ephemeral` so the API caches them. Turns 2+ reuse the cached prefix instead of re-tokenizing ~900 tokens.

```
Request to Anthropic API:
  system: [{text: "...", cache_control: {type: "ephemeral"}}]
  tools:  [..., {last_tool, cache_control: {type: "ephemeral"}}]
```

Savings show up as `cache_read_input_tokens` in the response.

### 2. Tool Result Capping

Every tool output is truncated before it enters history:

```
Tool layer:     bash output capped at 8KB
                file_read capped at 8KB
                web_read capped at 8KB

Agent layer:    all tool results capped at 4KB before history
```

This prevents a single `cat` or page fetch from bloating every future turn.

### 3. Token-Aware History Trimming

History is bounded by a **token budget** (30k tokens), not a message count.

```
appendHistory(new message)
    |
    v
estimateTokens(history) > 30,000?
    |
    no --> done
    |
    yes --> trimHistoryByTokens:
              1. Find earliest index where remaining msgs fit budget
              2. Walk forward to clean boundary:
                 - skip orphaned tool results
                 - skip orphaned assistant messages
                 - skip tool_use array messages
                 - land on a plain user message
              3. Return msgs[start:]
```

Token estimation: `len(json_bytes) / 4` per message. Rough but effective. Being off by 20% just means trimming slightly earlier or later.

Why token budget over message count:
- 20 short messages = ~2k tokens (wastes 90% of the window)
- 20 messages with tool results = ~60k tokens (blows the window)
- Token budget adapts to actual content size

### 4. Summarize on Trim

When messages are evicted from history, they don't just disappear. The agent asks the LLM to summarize them:

```
Evicted messages (> 2 messages):
    |
    v
flattenMessages:
    "user: deploy the app to prod..."
    "assistant: [tool interaction]"
    "user: check if nginx is running..."
    |
    v
llm.Complete(
    system: "Summarize in 2-3 sentences..."
    max_tokens: 200
)
    |
    v
Prepend to history:
    [Conversation so far: User asked to deploy the app
     and check nginx. Both tasks completed successfully.]
```

This costs ~200 output tokens once, but preserves context that would otherwise be lost forever. The model no longer hits dead-ends from forgotten instructions.

---

## What Enters the Context Window

On each LLM call, the context window contains:

```
+--------------------------------------------------+
| System Prompt              ~220 tokens   (cached) |
| Tool Definitions           ~680 tokens   (cached) |
|--------------------------------------------------|
| [Summary preamble]         ~50-100 tokens         |
| User message 1             variable               |
| Assistant reply 1          variable               |
| User message 2 (tools)     variable               |
| Tool results               variable (capped 4KB)  |
| ...                                               |
| Latest user message        variable               |
|--------------------------------------------------|
| Total budget:              ~30,000 tokens          |
| Max output:                4,096 tokens            |
+--------------------------------------------------+
```

The cached prefix (~900 tokens) is essentially free after the first turn. The remaining ~29k is the sliding window of conversation.

---

## Concurrency Model

```
Agent (shared)
  |
  +-- mu (sync.Mutex): protects history, userLock, cancelFn maps
  |
  +-- userLock[userID]: one mutex per user, serializes Chat() calls
  |
  +-- cancelFn[userID]: per-user cancellation via context
```

- Each user gets their own lock. Two users can chat simultaneously.
- One user's messages are serialized. No interleaving within a conversation.
- `/cancel` calls `cancelFn[userID]()`, checked at each loop iteration.

---

## Safety & Approval Flow

```
Tool execution
    |
    v
Dangerous command? (rm, sudo, curl|, etc.)
    |--- yes --> store pending approval
    |            return "NEEDS_APPROVAL: ..."
    |            agent pauses, asks user
    |            user replies "yes"
    |            next Chat() call detects approval
    |            executes pending command
    |
    |--- no --> execute normally
    |
    v
Writing to new path?
    |--- yes --> same approval flow
    |--- no (approved path) --> execute
```

Approved paths persist across sessions in `~/.config/nevinho/approved_paths.json`.

---

## Constants

| Name | Value | Purpose |
|------|-------|---------|
| `maxTokens` | 4,096 | Max output tokens per LLM call |
| `maxLoops` | 10 | Max tool-call iterations per Chat() |
| `maxContextTokens` | 30,000 | Token budget for conversation history |
| `maxToolResult` | 4,000 | Max bytes per tool result in history |
| `maxResponseLen` | 8,000 | Max bytes per tool output (tool layer) |
| `maxFileSize` | 100 KB | Max file size for file_read |
| `bashTimeout` | 120s | Bash command timeout |
| `httpTimeout` | 15s | HTTP request timeout |

---

## Package Map

```
nevinho/
  main.go              CLI entry point
  agent/
    agent.go           Chat loop, context management, history trimming
  llm/
    provider.go        Provider interface (Complete, Format*)
    anthropic.go       Anthropic API + prompt caching
    openai.go          OpenAI API (+ Ollama via compatible endpoint)
    http.go            Shared HTTP client
  tools/
    registry.go        Tool dispatch, approval flow, path permissions
    bash.go            Shell execution, danger detection
    file.go            File read/write with sandboxing
    web.go             Web search (Brave/DDG) and page fetch
  discord/
    bot.go             Discord session, message handling, slash commands
  config/
    config.go          Encrypted config with env/file/runtime layers
  crypto/
    crypto.go          AES-256-GCM encryption
  logger/
    logger.go          Colored terminal output
```

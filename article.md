---
title: I Built an AI Agent Harness in Go
published: false
tags: go, ai, discord, opensource
---

Hello there!

I've been using AI tools a lot lately. ChatGPT, Claude, local models with Ollama. They're great for answering questions, but I wanted something that could actually *do* things. Run commands on my server, search the web, edit files. Not just talk about it.

So I started building **nevinho**, a personal AI agent that lives in my Discord DMs. You send it a message, it figures out what tools to use, and it gets things done. All from my phone.

I wrote it in Go and I want to walk through how it works and the decisions behind it.

## Wait, what's a harness?

You'll see me use the word "harness" a lot, so let me explain what I mean.

An LLM by itself doesn't do much. It can't remember your last message, it can't run code, it can't open a web page. It has no memory. Every time you call the API, you have to send all the previous messages again so it knows what you've been talking about.

The harness is everything around the LLM that makes it actually useful. The conversation history, the tool execution, the context management, the safety checks. All the code that turns a stateless API into something that feels like an agent. The LLM is the brain, the harness is the body.

And the thing is, most of the performance of an AI agent doesn't come from the model itself. It comes from what you feed it. A harness that stuffs 12,000 tokens of system prompt before the conversation even starts is already working against you. Nevinho tries to keep things minimal and transparent.

## Why Go

I wanted a single binary I could drop on any machine and run. No runtime, no virtualenv, no node_modules. Go gives me that.

The standard library covers most of what I needed. HTTP clients, JSON encoding, crypto, process execution. I only pulled in two external dependencies: `discordgo` for the bot and `godotenv` for config loading.

Go also makes concurrency explicit. Each user gets their own mutex, tool execution has panic recovery, and the typing indicator runs in goroutines. It's not magic, but it's easy to reason about.

## The architecture

The project has eight packages, each with a single responsibility:

```
nevinho/
├── main.go           // entry point, CLI commands, provider detection
├── agent/            // the core loop, context management, history trimming
├── llm/              // provider abstraction (Anthropic, OpenAI, Ollama)
├── tools/            // bash, web, file operations
├── discord/          // bot interface, slash commands
├── config/           // encrypted config with env/file/runtime layers
├── crypto/           // AES-256-GCM encryption
└── logger/           // colored terminal output
```

The agent doesn't know about Discord. The tools don't know about the LLM. The LLM doesn't know about either. Everything connects through interfaces and simple function calls.

## The LLM provider interface

I wanted to switch between Claude, GPT, and local Ollama models without changing any agent code. So I defined a provider interface:

```go
type Provider interface {
    Complete(ctx context.Context, req *Request) (*Response, error)
    FormatUserMessage(text string) json.RawMessage
    FormatToolResults(results []ToolResult) []json.RawMessage
    Model() string
}
```

`FormatUserMessage` and `FormatToolResults` exist because Anthropic and OpenAI structure their messages differently. Anthropic wraps tool results in a single user message with `tool_result` content blocks. OpenAI expects separate messages with a `tool` role. The provider handles that translation so the rest of the code doesn't have to care.

`Complete` takes a `context.Context` so the harness can cancel things. If a user sends `/cancel` from Discord, the context gets cancelled and the loop stops.

Switching models at runtime is just a slash command. A `Resolve` function figures out which provider to use from the model name:

```go
func Resolve(name string, pc config.ProviderConfig) (Provider, error) {
    switch {
    case strings.HasPrefix(name, "gpt-") || strings.HasPrefix(name, "o1-") ||
         strings.HasPrefix(name, "o3-") || strings.HasPrefix(name, "o4-"):
        return NewOpenAI(pc.OpenAIKey, "", name), nil
    case strings.HasPrefix(name, "claude-"):
        return NewAnthropic(pc.AnthropicKey, "", name), nil
    default:
        if pc.OllamaURL != "" {
            return NewOpenAI("", pc.OllamaURL, name), nil
        }
        return nil, fmt.Errorf("unknown model: %s", name)
    }
}
```

Ollama uses the OpenAI-compatible API, so I reuse the OpenAI provider with a different base URL. Anything that isn't clearly GPT or Claude gets routed to Ollama if it's configured.

History gets cleared on switch because message formats differ between providers.

## The agent loop

This is the core of the whole thing. The `Chat` method sends the conversation to the LLM, and if the model wants to use tools, it executes them and loops back.

```go
func (a *Agent) Chat(userID, text string) (string, error) {
    lock := a.getUserLock(userID)
    lock.Lock()
    defer lock.Unlock()

    ctx, cancel := context.WithTimeout(context.Background(), chatTimeout)
    // ... store cancel for /cancel command

    // If there's a pending approval and the user said "yes", handle it
    if p := a.tools.PendingApproval(userID); p != nil && looksLikeApproval(text) {
        // execute approved action, inject result into text
    }

    a.appendHistory(userID, a.llm.FormatUserMessage(text))

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
            return "", err
        }

        a.appendHistory(userID, resp.AssistantMessage)

        if len(resp.ToolCalls) == 0 {
            return resp.Text, nil
        }

        var results []llm.ToolResult
        needsApproval := false
        for _, tc := range resp.ToolCalls {
            output := a.executeTool(tc.Name, tc.Input, userID)
            if len(output) > maxToolResult {
                output = output[:maxToolResult] + "\n...(truncated)"
            }
            results = append(results, llm.ToolResult{ID: tc.ID, Output: output})
            if strings.HasPrefix(output, "NEEDS_APPROVAL:") {
                needsApproval = true
            }
        }

        a.appendHistory(userID, a.llm.FormatToolResults(results)...)

        if needsApproval {
            return approvalMessage(a.tools.PendingApproval(userID)), nil
        }
    }

    return "I hit my limit on tool calls. Try breaking it into smaller tasks.", nil
}
```

The loop runs up to 25 iterations. Each time, the model either responds with text (we're done) or asks to use tools (execute and go again). This is what lets the agent chain actions: search the web, read a page, then summarize what it found.

The whole call is wrapped in a 5-minute timeout. If the user sends `/cancel`, the loop exits on the next iteration.

The approval check at the top is important. When a tool needs permission, the loop stops and asks the user. On their next message, if it looks like "yes" or "oui", the pending action runs and the result gets injected into the conversation.

Each user gets their own mutex so conversations don't corrupt each other's history.

## Keeping the context window clean

This is honestly where I spent the most time thinking. The model only sees what you put in its context window. If you waste that space on a massive system prompt or let one tool output bloat 10 future turns, performance drops.

Nevinho's system prompt is about 25 lines. Under 1,000 tokens. For comparison, tools like Claude Code use 12,000+ tokens just for their system prompt. Frontier models already know what a coding agent is from training. You don't need to explain it again in the prompt.

For tool results, every output gets truncated before it enters history. If bash returns a huge log, it gets capped at 4KB in the conversation. The model still sees enough to understand what happened, but it won't eat the entire context window.

The conversation history itself is bounded by a token budget of about 30k tokens, not a fixed number of messages. This matters because a quick "yes" and a 10KB tool output shouldn't use the same amount of space.

When messages get evicted because they're too old, they don't just disappear. The harness asks the model to summarize them in 2-3 sentences and puts that summary at the top of the conversation. It costs a small extra call, but the model no longer forgets what you asked for three messages ago.

I also enabled prompt caching on the Anthropic API. The system prompt and tool definitions get cached so they don't need to be re-processed on every turn. Sounds small, but it adds up in a multi-turn conversation.

## Tools

The tool registry dispatches to seven tools:

```go
func (r *Registry) Execute(name string, input json.RawMessage, userID string) string {
    switch name {
    case "web_read":
        return r.webRead(input)
    case "web_search":
        return r.webSearch(input)
    case "bash":
        return r.runBash(input, userID)
    case "file_read":
        return r.fileRead(input, userID)
    case "file_write":
        return r.fileWrite(input, userID)
    case "file_list":
        return r.fileList(input, userID)
    case "file_edit":
        return r.fileEdit(input, userID)
    default:
        return fmt.Sprintf("unknown tool: %s", name)
    }
}
```

Each tool gets a `json.RawMessage` input and returns a string. Tool definitions with JSON schemas are sent to the LLM so it knows what's available.

**Web search** uses the Brave Search API if you have a key, with DuckDuckGo HTML scraping as a zero-config fallback. The DDG fallback parses the actual HTML result page, no API key needed.

**Web read** fetches a URL and strips the HTML down to readable text. It walks the DOM, skips script/style/nav/footer elements, and looks for a `<main>` or `<article>` tag to focus on content.

**Bash** is the most versatile tool. It runs shell commands with a 2-minute timeout. Dangerous commands like rm, sudo, or curl piped to shell require user approval before anything runs.

**File operations** include read, write, list, and edit. `file_read` supports offset and limit for large files. `file_edit` replaces an exact string in a file, which is safer than rewriting the whole thing for a small change. Write operations to new directories require approval.

I intentionally keep the toolset small. Seven tools is plenty. The model doesn't need 20+ specialized tools with verbose descriptions. If bash can do it, I don't build a tool for it.

## The approval system

Letting an AI run arbitrary commands is a bad idea. So dangerous operations need approval.

The bash tool scans for dangerous patterns using regex before executing anything:

```go
var dangerousPatterns = []*regexp.Regexp{
    regexp.MustCompile(`\brm\b`),
    regexp.MustCompile(`\bsudo\b`),
    regexp.MustCompile(`\bchmod\b`),
    regexp.MustCompile(`\bkill\b`),
    regexp.MustCompile(`\bcurl\b.*\|`),
    regexp.MustCompile(`:\(\)\s*\{`), // fork bomb
    // ... and more
}
```

It also checks for sensitive paths like `.ssh`, `.aws`, `.env`, and `credentials`. If anything matches, the tool returns a `NEEDS_APPROVAL:` prefix instead of executing. The agent loop catches this, sends the user a preview of what it wants to run, and stops.

The user replies "yes" (or "oui", I'm French after all) and the pending action runs. Approved directories persist to a JSON file on disk so you don't get asked twice.

There are two types of approval: path approval (for writing to new directories) and code approval (for dangerous commands). Path approvals are remembered. Code approvals are one-shot.

## URL validation

Before fetching any URL, the web tools validate it to prevent SSRF:

```go
func validateURL(rawURL string) error {
    u, err := url.Parse(rawURL)
    if err != nil {
        return fmt.Errorf("invalid URL")
    }
    if u.Scheme != "http" && u.Scheme != "https" {
        return fmt.Errorf("only http/https allowed")
    }

    ips, err := net.LookupIP(u.Hostname())
    if err != nil {
        return fmt.Errorf("cannot resolve host")
    }
    for _, ip := range ips {
        if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
            return fmt.Errorf("internal addresses not allowed")
        }
    }
    return nil
}
```

Only http/https allowed, and it does a DNS lookup to block requests to localhost, private IPs, and link-local addresses. Without this, the model could try to hit internal services.

## Discord as the interface

I picked Discord because I already use it daily and it works on my phone. The bot only responds to DMs from a single owner configured via `DISCORD_OWNER_ID`.

Messages over 2000 characters get split at newline boundaries. Slash commands handle `/new`, `/model`, `/status`, `/paths`, `/cancel`, and `/help`. The same commands also work as plain text messages.

## What's next

The core harness is in good shape. Retries with exponential backoff, token-aware trimming, cost tracking, prompt caching, all done. But there's still stuff I want to add.

**Streaming.** Right now the bot waits for the full response before showing anything. On long answers, Discord's typing indicator expires and it looks like the bot is dead. I want to stream tokens and update the message as they come in.

**Memory.** The agent doesn't learn from corrections yet. If I tell it "use npm, not yarn" today, it won't remember tomorrow. I want the harness to detect those patterns and store them, without adding memory instructions to the system prompt.

**Scheduled tasks.** Nevinho runs 24/7 on a VPS. It should be able to run prompts on a schedule and report back. "Every morning at 9am, check if my services are up." That kind of thing.

I'm working through them one at a time.

## What I learned

**The LLM API differences are annoying but manageable.** Anthropic and OpenAI have different message formats, different tool call structures, different system prompt handling. But once you define a clean interface, each adapter is about 100 lines.

**Context management is the real work.** The agent loop itself is simple. The hard part is controlling what the model sees: keeping the system prompt small, capping tool outputs, trimming history without losing important context. That's what makes a harness good or bad.

**Safety takes more thinking than features.** Approving dangerous operations, validating URLs, checking for sensitive paths. Every tool I add needs its own threat model.

**Go was the right choice for this.** Single binary, good concurrency primitives, a rich standard library. No framework needed. The whole project is about 4,700 lines across 21 files.

## Try it out

The project is open source. Feel free to check it out, open issues, or contribute.

**GitHub:** [github.com/lucasnevespereira/nevinho](https://github.com/lucasnevespereira/nevinho)

Hope you find it useful!

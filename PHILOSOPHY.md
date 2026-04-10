# Nevinho — Design Philosophy & Performance Guidelines

Internal reference document. Not tracked in git.

This captures the principles, patterns, and anti-patterns that guide nevinho's architecture, drawn from research into high-performance coding agents and LLM harness design.

---

## Core Thesis

A performant AI harness is one that is **transparent**, **minimal**, and gives the user **full control over what enters the model's context window**. Less is more — both in tooling and in the system prompt.

---

## 1. Context Engineering Is Everything

The single most important factor in agent performance is what goes into the context window. Every token must earn its place.

**Key rules:**
- Nothing should be injected behind the user's back — full transparency about what enters the context
- Prompt caching is critical for multi-turn conversations. Compaction strategies that destroy the cached prefix (pruning tool results randomly) make every turn expensive
- Custom compaction beats generic compaction — current implementations across the industry are "not good"
- Full cost tracking is non-negotiable — many harnesses don't do this properly

**Applied to nevinho:**
- System prompt + tool definitions stay under 1,000 tokens total
- Tool results are capped before entering history (don't let a single web_read bloat 10 future turns)
- History trimming is token-aware, not message-count-based
- When messages are evicted, they are summarized — not silently dropped

> Source: Mario Zechner (Pi) — "What I learned building an opinionated and minimal coding agent"
> Source: Anthropic prompt caching documentation

---

## 2. Minimal Toolset Wins

Pi ships with exactly 4 tools: read, write, edit, bash. Nevinho has 5: web_read, web_search, run_code, file_read, file_write. Both are deliberately minimal.

**Why this works:**
- Frontier models are heavily RL-trained to understand coding agents — they don't need 20+ tools with verbose descriptions telling them what they already know
- Additional tools increase context overhead without proportional quality gains
- Terminal-Bench results (Terminus agent) prove that even the most minimal interface (just tmux keystrokes + VT codes) performs at the top of leaderboards
- "Bash is all you need" — most specialized tools can be replaced by a well-prompted bash call

**Anti-pattern: MCP overhead**
Popular MCP implementations consume 7-9% of the context window upfront (13-18k tokens) just for tool definitions. The alternative is progressive disclosure — agents pay token costs only when they actually need a specific tool, not upfront for every possible tool.

**Applied to nevinho:**
- Resist adding tools. If bash can do it, don't build a tool for it
- Tool descriptions are one sentence each — the model knows what "search the web" means
- No MCP. Tool definitions cost ~500 tokens. MCP would be 30x worse for zero benefit at this scale

> Source: Mario Zechner (Pi) — Video presentation "I Hated Every Coding Agent, So I Built My Own"
> Source: Terminal-Bench (Terminus agent) leaderboard results

---

## 3. System Prompt Should Be Tiny

Pi's entire system prompt fits on one slide. Nevinho's is ~25 lines.

**Why:**
- Frontier models are already trained to know what a coding agent is. Repeating this wastes tokens
- Long system prompts compete with actual conversation content for context window space
- Instructions the model already follows from training are pure overhead

**Compared token counts (approximate):**

| Harness       | System prompt tokens |
|---------------|---------------------|
| Claude Code   | ~12,000+            |
| Cursor        | ~8,000+             |
| Pi            | ~800                |
| Nevinho       | ~900                |

**Applied to nevinho:**
- System prompt stays under 1,000 tokens — hard rule
- Only include instructions that change default model behavior
- No personality descriptions, no "you are a helpful assistant" filler
- Review the prompt quarterly: if the model follows an instruction without it being there, remove it

> Source: Mario Zechner (Pi) — Token count comparison across coding harnesses

---

## 4. No Hidden Behavior

**Principles:**
- No stealth tool injection or hidden modes
- Full observability into every interaction
- Session format should be clean and post-processable
- The user should always be able to answer: "what did the model see?"

**Applied to nevinho:**
- Logger shows every tool call, every token count, every timing
- `!context` command exposes what's in the model's history
- `!cost` command shows token spend
- No "plan mode" or hidden system messages injected mid-conversation

> Source: Mario Zechner (Pi) — On observability and transparency in agent harnesses

---

## 5. Replace Ephemeral State With Files

Models struggle with maintaining internal state across long conversations. Files are persistent, versionable, and reusable across sessions.

| Instead of...         | Use...                                                    |
|-----------------------|-----------------------------------------------------------|
| In-memory history     | Summaries written to disk, loaded on restart              |
| Ephemeral plan mode   | PLAN.md — persistent, versionable, shareable              |
| Built-in to-dos       | TODO.md with checkboxes                                   |
| Sub-agents            | Separate sessions with consolidated findings              |

**Applied to nevinho:**
- Conversation summaries persist to `~/.config/nevinho/summaries/{userID}.md`
- User preferences persist to `~/.config/nevinho/users/{userID}.json`
- Conversations survive restarts — no cold start amnesia

> Source: Mario Zechner (Pi) — File-based state management pattern

---

## 6. The LSP Anti-Pattern (for reference)

Not directly applicable to nevinho (Discord bot, not IDE), but worth understanding:

- When an agent makes sequential edits, code is naturally broken mid-task
- Injecting "this is broken" LSP errors after each tool call confuses the model
- The model may give up or produce worse code trying to fix intermediate states
- Type checking and linting should only happen at natural synchronization points (when the agent thinks it's done)

**Lesson for nevinho:** Don't inject feedback about intermediate states. Let tool chains complete before evaluating results.

> Source: Mario Zechner (Pi) — On LSP error injection during editing sessions

---

## 7. Sub-Agents Are a Black Box

Mario Zechner is explicitly against built-in sub-agents: "they're not as observable."

**Problems:**
- Spawning multiple agents to parallelize feature work doesn't work well — "unless you don't care if your codebase devolves into a pile of garbage"
- Sub-agents create black boxes within black boxes
- You lose control over what enters each agent's context

**Alternative:** Do context gathering in separate sessions, consolidate findings into reusable artifacts, then feed those to a single session.

**Applied to nevinho:**
- Single agent, single context per user
- No sub-agent spawning
- If a task is too complex, the agent tells the user to break it down

> Source: Mario Zechner (Pi) — Video presentation and blog post

---

## 8. Build on SDKs, Not Abstraction Layers

Build directly on provider SDKs and APIs rather than abstraction layers like Vercel AI SDK or LangChain.

**Why:**
- Full control over the API surface area
- No leaky abstractions hiding important details (like caching headers, token counts, stop reasons)
- Easier to implement provider-specific optimizations (prompt caching, extended thinking, etc.)
- Less dependency churn — provider APIs are more stable than wrapper libraries

**Applied to nevinho:**
- Raw HTTP calls in `llm/http.go` directly to Anthropic/OpenAI APIs
- Provider interface is 4 methods — no framework, no middleware
- Adding prompt caching = adding one header field, not waiting for a library update

> Source: Mario Zechner (Pi) — On avoiding abstraction layers
> Source: Anthropic SDK documentation
> Source: OpenAI API documentation

---

## 9. Slow Down

From Mario's follow-up essay "Thoughts on slowing the fuck down":

- Agent velocity creates a false sense of progress
- Agents compound mistakes at rates that humans would naturally bottleneck
- Reserve agent work for scoped, evaluable tasks
- Maintain human control over architecture
- Treat agent output as junior contributions requiring review

**Applied to nevinho:**
- `maxLoops = 10` prevents runaway tool chains
- Approval system for destructive operations (file writes, code execution)
- The agent suggests breaking down complex tasks rather than attempting everything in one shot

> Source: Mario Zechner — "Thoughts on slowing the fuck down" (essay)

---

## 10. Performance Benchmarks to Target

Based on the principles above, nevinho should aim for:

| Metric                          | Target              |
|---------------------------------|---------------------|
| System prompt + tool defs       | < 1,000 tokens      |
| Tool result size in history     | < 4,000 chars each  |
| History token budget per user   | ~30,000 tokens      |
| Prompt cache hit rate (turn 2+) | > 90%               |
| Max tool loops per message      | 10                  |
| Cost visibility                 | Per-message logging  |

---

## Sources

- Mario Zechner — "What I learned building an opinionated and minimal coding agent" (blog post)
- Mario Zechner — "I Hated Every Coding Agent, So I Built My Own" (video presentation)
- Mario Zechner — "Thoughts on slowing the fuck down" (essay)
- Armin Ronacher — "Pi: The Minimal Agent Within OpenClaw" (blog post)
- Terminal-Bench — Terminus agent benchmark results
- Anthropic — Prompt caching documentation
- Anthropic — Messages API documentation
- OpenAI — Chat Completions API documentation
- Pi Coding Agent — npm package (npmjs.com)

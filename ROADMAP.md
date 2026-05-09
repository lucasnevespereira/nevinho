# Roadmap

Nevinho is a sharp personal AI harness that runs in your Discord DMs. Tight context, observable, voice-native, self-hostable.

## Principles

- Every token in the context window must earn its place.
- Observability over magic. The user always knows what's happening.
- Constraints beat feature bloat.
- Raw SDKs, not abstraction layers.
- Grow by deepening the core, not widening the surface.

---

## Now

In priority order. One at a time.

### 1. Tool activity indicator on Discord

Biggest friction today is the 20-40s silent wait. Discord cannot do true token streaming (rate-limited, 2k char cap, mobile flicker). Realistic fix: show which tool is running.

- [x] Per-user tool-start callback on the agent, panic-safe.
- [x] Discord handler posts a subtext indicator (`-# running bash: ls -la`) on the first tool call, edits it on subsequent calls.
- [x] `MessageFlagsSuppressNotifications` so the indicator stays silent on mobile.
- [x] Edit throttle of 600ms to stay well under Discord's rate limit.
- [x] Indicator deleted when the final response ships.

### 2. Observability surface

Cheap, compounding. Ship before Skills so you can measure context impact.

- [ ] `/status` shows cache hit rate (`cache_read / (cache_read + cache_creation)`) per session and lifetime.
- [ ] `/status` shows per-tool call count and token cost.
- [ ] `/why` command dumps what was injected into the last turn: memory entries loaded, skills loaded, cached prefix size, raw input tokens.

### 3. Skills (progressive disclosure)

Markdown files with YAML frontmatter in `~/.nevinho/skills/`. System prompt carries only a short index. Full body enters context only when the model loads it. Skills teach workflows; memory stores facts about the user.

- [ ] Skill file format: frontmatter with `name`, `description`; body is markdown instructions.
- [ ] On `Chat()`, scan the dir and append an index (name + one-line description) to the system prompt. Cap at 30 skills.
- [ ] `load_skill(name)` tool returns the full body.
- [ ] `/skills` lists available skills. `/skills <name>` shows the body.
- [ ] Ship 3 built-in skills as templates (`git-commit`, `code-review`, `write-article`).

### Polish in parallel (no feature weight)

- [ ] Structured logs. One line per event, `key=value`. Greppable.
- [ ] `file_edit` strict uniqueness. If `old_string` matches more than once, error with the count instead of fuzzy-matching.
- [ ] Web fetch hardening.
  - [ ] Swap raw HTML stripping for `github.com/JohannesKaufmann/html-to-markdown/v2` on the direct-fetch path.
  - [ ] Revalidate SSRF blocklist on every HTTP redirect via `http.Client.CheckRedirect`.
  - [ ] Fail closed on DNS resolution failure.
  - [ ] Block metadata hostnames by name (`metadata.google.internal`, `metadata`) in addition to IP.

### Reliability (24/7 unattended)

Personal bot on a VPS. Silent failures are the worst kind.

- [ ] Crash heartbeat. On boot after an unclean shutdown, DM the owner: `restarted after crash at <ts>, last seen <ts>`. Write a `last_alive` timestamp every minute. Compare on boot.
- [ ] Log rotation. `nevinho logs` file grows unbounded today. Wire `logrotate` config (or in-process rotation) to cap size and keep N days. Ship the config in `install.sh`.

## Next

Queued. Starts once Now lands.

### OpenAI-compatible provider

One file covers Groq, Together, OpenRouter, LM Studio, vLLM, local servers.

- [ ] `llm/compat.go` pointing at any URL via `OPENAI_COMPAT_URL`, `OPENAI_COMPAT_KEY`, `OPENAI_COMPAT_MODEL`.
- [ ] `nevinho setup` offers the new provider with presets for Groq, OpenRouter, LM Studio.

### CLI mode (`nevinho chat`)

Local REPL over the same agent core. Terminal streaming is free.

- [ ] `nevinho chat` command. Interactive REPL calling the same `agent.Chat()` Discord uses.
- [ ] Raw token streaming to stdout.
- [ ] Markdown rendering via `glamour`, fallback to raw text in non-TTY.
- [ ] Session JSONL in `~/.nevinho/sessions/cli.jsonl`. `nevinho chat --new` starts fresh.
- [ ] `nevinho setup --cli` skips Discord prompts. `nevinho setup --discord` wires Discord later.

### Google Gemini provider

Native integration for 1M context and Gemini-specific features.

- [ ] `llm/gemini.go`.
- [ ] Implicit caching support.
- [ ] Setup integration.

### Scheduled tasks: follow-ups

Foundation shipped. Remaining work:

- [ ] `pause` / `resume` actions on the agent tool.
- [ ] `/schedules` Discord command (slash + plain text).
- [ ] Non-interactive bash allowlist mode during scheduled runs so safe commands (read-only) can complete without an approval prompt.
- [ ] `last_run` and `next_run` exposed in `/status`.
- [ ] Surface failed-run history (last N errors per schedule).

## Later

Real but deferred. Ship when the above is solid or when a user hits the pain.

- [ ] Semantic history compaction. Group evicted turns by topic before summarizing.
- [ ] Tool-chain summarization. Collapse intermediate tool outputs after a chain completes.
- [ ] Memory relevance ranking. Rank entries against the current message, inject top-K only.
- [ ] Memory auto-expiry. `last_used` timestamp. Stale entries drop out.
- [ ] Web tooling polish. URL cache (LRU, 5 min TTL). PDF extraction. Search result dedup.
- [ ] WhatsApp transport via `whatsmeow`. Needs a `transport/` interface refactor and real user demand first.
- [ ] Proactive heartbeat. Periodic checklist drives agent actions. Ship only if distinct from scheduled tasks.
- [ ] `delegate_research` tool. Fresh context, runs a research task, returns a summary. Only if long research chains blow the main context.

## Won't build

- **MCP.** Tool definitions are ~500 tokens. MCP overhead (13-18k) is 30x worse at this scale.
- **Sub-agents.** Fragment the context we work hard to keep clean.
- **LSP integration.** Not an IDE.
- **Abstraction layers over provider SDKs.** Lose control over request shape.
- **Prompt bloat.** System prompt stays under 1,000 tokens.
- **Per-user analytics.** Personal bot. Global counters are enough.
- **Parallel tool calls.** Sequential works. Added complexity is not worth it.

---

## Shipped

- [x] **Context engineering.** Prompt caching on system and tools. 30k-token history budget. Trim with summary preamble. 4k-char tool-result cap.
- [x] **Reliability.** Exponential-backoff retries on 429/5xx with `Retry-After`. Cancellable via `context.Context`. 5-minute top-level timeout. Cost, token, and duration per message.
- [x] **Harness memory.** Auto-injected `memory.md`. Pattern-triggered writes ("remember X", "always X"). 20-entry cap with dedup.
- [x] **Voice.** Discord voice notes through OpenAI Whisper to agent input. Supports ogg, mp3, wav, m4a, webm.
- [x] **Web tooling.** Tavily advanced search and extract. Jina Reader fallback. Polite headers. Explicit error signaling. 429/5xx retries.
- [x] **Conversation persistence (ELEPHANT).** On shutdown, summarize each user's history to `~/.nevinho/summaries/{userID}.md`. Reload as `[Previous conversation: ...]` preamble on next start. Default on, toggle via `ELEPHANT=off`. `/forget` wipes both in-memory history and the saved summary.
- [x] **Self-knowledge.** `NEVINHO.md` at repo root, embedded in the binary via `go:embed` and auto-injected into the system prompt. Covers identity, persistence model, commands, architecture, and source-lookup strategy (local file_read for dev installs, web_read against GitHub raw for binary installs). `/memory` and `/summary` expose persisted state to the user.
- [x] **Image input.** Discord image attachments (JPEG/PNG/GIF/WebP) routed inline to vision-capable models. 5MB per image, 4 per message. Vision capability checked per model before send. Anthropic and OpenAI both supported.

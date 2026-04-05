# Roadmap

Guiding principles:
- Every token entering the context window must earn its place
- Observability over magic: the user should always know what's happening
- Constraints produce better outcomes than feature bloat
- Build on raw SDKs, not abstraction layers

---

## P0: Context Engineering (done)

- [x] **Prompt caching** -- `cache_control` breakpoints on system prompt and tool definitions. Turns 2+ reuse the cached prefix.
- [x] **Cap tool results before history** -- Truncate tool outputs to ~4k chars before appending to history.
- [x] **Token-aware history trimming** -- Replace fixed message count with a ~30k token budget. Approximate with `len/4`, trim oldest first.
- [x] **Summarize on trim** -- When messages are evicted, summarize them into a "conversation so far" preamble instead of silently dropping them.

## P1: Reliability

A single transient error or stuck call should not kill a conversation.

- [x] **Retry with exponential backoff** -- On 429/5xx, retry up to 3 times with backoff. Parse `Retry-After` for 429s. Thread `context.Context` through the HTTP layer so retries are cancellable.
- [x] **Top-level timeout on Chat()** -- Wrap the chat loop in a 5-minute deadline. Prevents a stuck API call or infinite tool loop from hanging the bot.
- [x] **Cost-per-message logging** -- Extend `logger.Done()` to include estimated cost (e.g., `1.2s · 1,340 tokens · $0.002`). Already have `estimateCost()`, just needs wiring.

## P2: Harness-Level Memory

The agent should learn from corrections and preferences without polluting the system prompt with memory instructions.

- [ ] **Auto-inject memory into system prompt** -- On `Chat()`, read `~/.nevinho/memory.md` and append its content to the system prompt as a `[Memory]` block. File missing or empty = no-op. Content gets prompt-cached with the rest of the system prefix.
- [ ] **Harness-driven memory writes** -- After each assistant reply, the harness scans for correction patterns (e.g. "use X instead of Y", "I prefer X", "don't do X") and appends one-line entries to `memory.md`. The LLM never sees write instructions -- the harness owns the file.
- [ ] **Memory cap** -- Hard limit of ~500 tokens (~20 entries). Oldest entries rotate out. Keeps the cache-key stable and the prompt lean.

## P3: Streaming

Waiting 20-40s with only a typing indicator provides no feedback. Streaming fixes this.

- [ ] **Streaming responses** -- Use the streaming API endpoint. Edit the Discord message as tokens arrive instead of waiting for the full response.

## P4: Persist Across Restarts

Conversations should survive process restarts and upgrades.

- [ ] **Conversation summaries to disk** -- On trim or shutdown, write a summary to `~/.nevinho/summaries/{userID}.md`. On next message from that user, load the summary as context preamble.

## P5: Scheduled Tasks

Nevinho runs 24/7 on a VPS. It should be able to run prompts on a schedule and report back to Discord.

- [ ] **Schedule tool** -- A single `schedule` tool with actions: `create`, `list`, `delete`, `pause`, `resume`. The agent translates "every morning at 9am fetch tech news" into a cron expression. The user never writes cron syntax.
- [ ] **Schedule store** -- Encrypted JSON at `~/.nevinho/schedules.enc`. Each entry: ID, name, cron expression, prompt, enabled flag, last_run, next_run. Uses existing crypto package.
- [ ] **Scheduler goroutine** -- A 1-minute ticker in the main loop. On tick, scan schedules for `next_run <= now`. Execute due tasks by calling `agent.Chat("sched:{id}", prompt)` with a dedicated history namespace so scheduled runs don't pollute manual conversations. Send results to the owner's Discord DM with a `**[{name}]**` header. Errors are reported, not swallowed.
- [ ] **Cron parser** -- Minimal 5-field parser (`minute hour dom month dow`) plus shortcuts (`@daily`, `@hourly`, `@weekly`). ~100 LOC, no external dependency. Computes `next_run` from an expression and a reference time.
- [ ] **Discord commands** -- `/schedules` to list active schedules with next run time. `/schedules pause|resume|delete <name>` to manage them.
- [ ] **Concurrency guard** -- Scheduled tasks use virtual user ID `sched:{id}` for their own lock and history. Max one scheduled task runs at a time (channel semaphore) to prevent resource exhaustion. Manual chat is never blocked.
- [ ] **Missed run policy** -- On startup, recompute `next_run` for all tasks. Never execute missed runs. Old news isn't news.

Limits: max 10 active schedules, minimum interval 5 minutes, 5-minute execution timeout per run.

## P6: Richer Interactions

Only after the foundation is solid.

- [ ] **Image/attachment support** -- Accept images in Discord messages and pass them to vision-capable models.

---

## What we intentionally don't build

- **No MCP** -- Tool definitions are ~500 tokens. MCP overhead (13-18k tokens) would be 30x worse for no benefit at this scale.
- **No sub-agents** -- Single agent, single context. Sub-agents are a black box within a black box.
- **No LSP integration** -- Not an IDE. No mid-task error injection.
- **No abstraction layers** -- Raw HTTP to provider APIs. Full control over request shape.
- **No prompt bloat** -- System prompt stays under 1,000 tokens. Resist the urge to add instructions the model already knows from training.
- **No platform abstraction** -- Built for Discord. Don't abstract for Slack/Telegram until someone actually asks.
- **No per-user tracking** -- Personal bot. Global token counters are enough.
- **No parallel tool calls** -- Sequential execution works. Low ROI for the added complexity.

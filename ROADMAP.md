# Roadmap

Guiding principles:
- Every token entering the context window must earn its place
- Observability over magic — the user should always know what's happening
- Constraints produce better outcomes than feature bloat
- Build on raw SDKs, not abstraction layers

---

## P0: Context Engineering

These are the highest-impact changes. They directly reduce token waste and make every conversation cheaper and smarter.

- [x] **Prompt caching** — Add `cache_control` breakpoints to the system prompt and tool definitions so turns 2+ reuse the cached prefix instead of re-processing ~900 tokens every call. Single biggest cost reduction available.
- [x] **Cap tool results before history** — Truncate tool outputs to ~4k chars before appending to conversation history. A single large page fetch currently bloats the entire context window for all remaining turns.
- [x] **Token-aware history trimming** — Replace the fixed `maxHistory = 20` message count with a token budget (~30k tokens). Count tokens approximately (`len/4`) and trim from the oldest messages first. This prevents both wasted context (20 short messages = underuse) and blown context (20 long tool results = overflow).
- [ ] **Summarize on trim** — When messages are evicted from history, summarize them into a single "conversation so far" preamble instead of silently dropping them. The model loses less context and the user doesn't hit dead-ends from forgotten instructions.

## P1: Observability & Cost Tracking

Pi emphasizes full transparency about what enters the context and what it costs. Users should never be surprised by token spend.

- [ ] **Per-user token tracking** — Track input/output tokens per user (not just global). Store in memory alongside the conversation history.
- [ ] **`!cost` command** — Expose a Discord command that shows the current conversation's token count, estimated cost (model-specific pricing), and number of turns.
- [ ] **`!context` command** — Show how many messages are in history, approximate token usage, and how close the user is to triggering a trim. Full transparency into what the model "remembers."
- [ ] **Cost-per-message logging** — Extend `logger.Done()` to include estimated cost alongside token count (e.g., `1.2s · 1,340 tokens · $0.002`).

## P2: Reliability

These keep nevinho running smoothly under real-world conditions.

- [ ] **Retry with exponential backoff** — On 429/5xx errors, retry up to 3 times with backoff instead of failing immediately.
- [ ] **Top-level timeout on Chat()** — Prevent a stuck API call or infinite tool loop from hanging the bot indefinitely.
- [ ] **Refresh typing indicator** — Keep the Discord typing indicator alive during long tool loops so the user knows nevinho is working.
- [ ] **Parallel independent tool calls** — When the model requests multiple tools with no dependencies, execute them concurrently.

## P3: Persistent Context

Pi replaces ephemeral state with files. For a Discord bot that restarts, this means conversations shouldn't vanish.

- [ ] **Conversation summaries to disk** — On trim or shutdown, write a summary to `~/.config/nevinho/summaries/{userID}.md`. On next message from that user, load the summary as context preamble. Conversations survive restarts.
- [ ] **Per-user preferences file** — Store user-specific settings (preferred language, approved paths, model preference) in `~/.config/nevinho/users/{userID}.json` so they persist across sessions.

## P4: Richer Interactions

Only after the foundation is solid.

- [ ] **Streaming responses** — Edit the Discord message as tokens arrive instead of waiting for the full response.
- [ ] **Image/attachment support** — Accept images in Discord messages and pass them to vision-capable models.
- [ ] **Voice message transcription** — Transcribe voice messages with Whisper before processing.

## P5: Platform Expansion

- [ ] **Platform-agnostic interface** — Abstract the Discord-specific layer so Slack/Telegram adapters can plug in.
- [ ] **Group chat support** — Respond on @mention, maintain separate history per channel.

---

## What we intentionally don't build

Aligned with our minimalist philosophy:

- **No MCP** — Tool definitions are ~500 tokens. MCP overhead (13-18k tokens) would be 30x worse for no benefit at this scale.
- **No sub-agents** — Single agent, single context. Sub-agents are a black box within a black box.
- **No LSP integration** — Not an IDE. No mid-task error injection.
- **No abstraction layers** — Raw HTTP to provider APIs. Full control over request shape.
- **No prompt bloat** — System prompt stays under 1,000 tokens. Resist the urge to add instructions the model already knows from training.

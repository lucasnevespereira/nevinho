# Roadmap

## P0 — Reliability

- [ ] **Retry with backoff** — 429 rate limits and 5xx errors crash the conversation. Retry 2-3 times with exponential backoff before giving up.
- [ ] **Typing indicator refresh** — Discord's typing indicator expires after ~10s. Long tool loops leave the user staring at nothing. Refresh it in a background goroutine during Chat().
- [ ] **Parallel tool execution** — When the LLM returns multiple tool calls in one turn (e.g. 3 web_search), run them concurrently with goroutines instead of sequentially.

## P1 — Smarter context

- [ ] **Token-aware history** — Replace the 20-message cap with a token budget. Estimate tokens per message (~4 chars/token) and trim to fit the model's context window. A single turn with 5 large tool results shouldn't blow the window.
- [ ] **Tool result size control** — Large tool results (10K chars) eat context fast. Cap per-result size relative to remaining budget, or summarize long outputs before sending them back to the LLM.
- [ ] **Conversation summary on trim** — Instead of dropping old messages, compress them into a single summary message so the LLM retains context from earlier in the conversation.

## P2 — Richer interactions

- [ ] **Voice message support** — Detect Discord voice messages (.ogg attachments), transcribe with OpenAI Whisper API, and feed the text to the agent. Enables hands-free interaction from phone.
- [ ] **Image/attachment support** — Discord users send screenshots. Pass them to vision-capable models (GPT-4o, Claude) as base64 or URL. Requires extending FormatUserMessage to handle multimodal content.
- [ ] **Streaming edits** — Send an initial "thinking..." message, then edit it as tokens arrive. Much better UX than waiting 5-10s for the full response. Requires streaming API support in the llm package.
- [ ] **Voice responses** — Reply with audio (TTS) for a fully conversational experience. OpenAI and ElevenLabs both offer TTS APIs.
- [ ] **Per-chat timeout** — Top-level timeout for the entire Chat() call (e.g. 2 minutes). If the agent gets stuck in a tool loop, cancel gracefully instead of hanging.

## P3 — Platform expansion

- [x] ~~**Slash commands on Discord**~~ — Registered as application commands with autocomplete.
- [ ] **Platform-agnostic bot interface** — Extract a `Bot` interface so Discord, Slack, Telegram share the same agent. The agent shouldn't know which platform it's running on.
- [ ] **Group chat support** — Respond when mentioned in group channels, not just DMs. Requires mention detection and per-channel history.

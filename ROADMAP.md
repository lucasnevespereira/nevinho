# Roadmap

## P0: Reliability

- [ ] Retry with exponential backoff on 429/5xx errors
- [ ] Refresh typing indicator during long tool loops
- [ ] Run independent tool calls in parallel
- [ ] Top-level timeout on Chat() to prevent hangs

## P1: Smarter context

- [ ] Token-aware history trimming instead of message count
- [ ] Cap tool result size before appending to history

## P2: Richer interactions

- [ ] Streaming responses (edit message as tokens arrive)
- [ ] Image/attachment support with vision models
- [ ] Voice message transcription (Whisper)
- [ ] Summarize old messages on trim instead of dropping them

## P3: Platform expansion

- [ ] Wire GitHub/Google OAuth tokens to actual tools
- [ ] Platform-agnostic bot interface (Slack, Telegram)
- [ ] Group chat support (respond on mention)

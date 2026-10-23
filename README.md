<p align="center">
  <img src="assets/nevinho.png" width="200" />
</p>

<h1 align="center">nevinho</h1>

<p align="center">
  <a href="https://github.com/lucasnevespereira/nevinho/stargazers"><img src="https://img.shields.io/github/stars/lucasnevespereira/nevinho?style=flat&logo=github" alt="Stars" /></a>
  <a href="https://github.com/lucasnevespereira/nevinho/network/members"><img src="https://img.shields.io/github/forks/lucasnevespereira/nevinho?style=flat&logo=github" alt="Forks" /></a>
  <a href="https://github.com/lucasnevespereira/nevinho/issues"><img src="https://img.shields.io/github/issues/lucasnevespereira/nevinho?style=flat&logo=github" alt="Issues" /></a>
  <a href="https://github.com/lucasnevespereira/nevinho/releases"><img src="https://img.shields.io/github/v/release/lucasnevespereira/nevinho?style=flat&color=blue" alt="Release" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/lucasnevespereira/nevinho?style=flat&color=green" alt="License" /></a>
</p>

A personal AI agent you self-host. Reach it from a Discord DM, or run it as a coding agent in your terminal. Supports Anthropic, OpenAI, Gemini, Groq, OpenRouter, and Ollama.
Tools for bash, code search, web search, file editing, scheduled tasks.

## Install

```bash
curl -sSL https://raw.githubusercontent.com/lucasnevespereira/nevinho/main/install.sh | bash
```

One binary lands in your PATH. From there, two ways to use it.

### Discord bot (Linux VPS)

```bash
nevinho setup     # Discord token, owner ID, LLM keys, voice
nevinho start     # runs as a background service
```

No Discord bot yet? Follow [SETUP.md](SETUP.md) first to create one.

### Terminal client

```bash
nevinho
```

A fresh local install needs no setup wizard. Configure providers from inside the session with `/config`.
You can also run `nevinho login` to use an eligible ChatGPT plan.

### Upgrade and uninstall

`nevinho upgrade` updates the binary in place and refreshes the service if installed. `nevinho uninstall` removes whatever is on disk (`--keep-config` preserves `~/.nevinho`).

## CLI

```
nevinho            open the terminal client (same as 'chat')
nevinho setup      configure Discord, providers, and voice
nevinho start      start the Discord bot as a background service
nevinho stop       stop the bot
nevinho status     check if the bot is running
nevinho logs       show live logs (--full, --last N)
nevinho config     view, set, or delete config keys
nevinho login      connect an eligible ChatGPT plan
nevinho upgrade    update to the latest version
nevinho uninstall  remove what is installed
nevinho version    show version
```

## Build from source

```bash
git clone https://github.com/lucasnevespereira/nevinho.git
cd nevinho
make build        # binary at ./bin/nevinho
```

For development, copy `.env.example` to `.env`, fill `DISCORD_BOT_TOKEN`, `DISCORD_OWNER_ID`, and at least one LLM key, then `make run`. See [SETUP.md](SETUP.md) for Discord bot creation steps.

## Providers

Configure one or more LLM backends with `nevinho setup` or the in-session `/config` picker.

To use an eligible ChatGPT Plus or Pro plan, run `nevinho login` on the machine where Nevinho runs. Open the printed link and sign in. You can also run `/login` from the terminal UI when already inside Nevinho. It pauses the UI for browser sign-in and then switches to the connected plan's model. On a remote VM, the browser's loopback redirect may fail to load. Copy its full final URL from the address bar and paste it into Nevinho's waiting terminal. Login discovers the models available to your account, selects the first one, and adds `chatgpt:` models to `/model`. Run `nevinho login --new` or `/login --new` to register a different ChatGPT account. `nevinho login chatgpt` also works. This uses [OpenAI's Sign in with ChatGPT flow](https://developers.openai.com/siwc/token-sharing-open-source), so plan usage and limits apply. It does not use an API key. Nevinho stores renewable credentials in `~/.nevinho/chatgpt-auth.json` with owner-only permissions. Use [ChatGPT Settings](https://chatgpt.com/#settings) to manage connected app access and usage.

| Provider   | Env var               | Default model                                     | Get a key                              |
| ---------- | --------------------- | ------------------------------------------------- | -------------------------------------- |
| Anthropic  | `ANTHROPIC_API_KEY`   | claude-haiku-4-5                                  | https://console.anthropic.com          |
| OpenAI     | `OPENAI_API_KEY`      | gpt-4o-mini                                       | https://platform.openai.com/api-keys   |
| Gemini     | `GEMINI_API_KEY`      | gemini-2.5-flash                                  | https://aistudio.google.com/app/apikey |
| Groq       | `GROQ_API_KEY`        | groq:llama-3.3-70b-versatile                      | https://console.groq.com/keys          |
| OpenRouter | `OPENROUTER_API_KEY`  | openrouter:nvidia/nemotron-3-super-120b-a12b:free | https://openrouter.ai/keys             |
| Ollama     | `OLLAMA_MODEL=llama3` | any local model                                   | run locally                            |

Groq and OpenRouter both offer a free tier with daily request quotas. Useful for testing without burning credits.

Groq model names are prefixed with `groq:`. OpenRouter routes are prefixed with `openrouter:`. Both providers accept any model name in their catalog after the prefix.

ChatGPT plan models are prefixed with `chatgpt:` and come from the signed-in account's model list. The OpenAI API key provider remains available separately.

Switch models with `/model` (picker) or `/model <name>`.
The terminal picker and Discord model menus show whether a model uses the ChatGPT plan, an API key, or OpenRouter. The saved model ID remains unchanged when you switch.

## Discord conversations

In a DM with the bot, `/new` starts a separate conversation and keeps the current one saved. `/resume` opens a private, paged picker of saved conversations. Each one restores its model and recent text context; images must be attached again. You can also select a conversation with `/resume <id>` in a plain-text DM or the command's `id` option. `/forget` deletes only the current conversation and starts a fresh one. Existing Discord conversation history is imported on first launch after upgrading.

Conversation switching waits for the current response to finish and for any pending tool approval to be answered. With `ELEPHANT=off`, `/new` still starts a fresh in-memory conversation, while `/resume` is unavailable.

## Tools

| Tool         | What it does                                       |
| ------------ | -------------------------------------------------- |
| `bash`       | Run any bash command                               |
| `grep`       | Search file contents by pattern                    |
| `find`       | Find files by name                                 |
| `web_search` | Search via Tavily API or DuckDuckGo fallback       |
| `web_read`   | Fetch a URL and extract readable text              |
| `file_list`  | List directory contents                            |
| `file_read`  | Read a file (supports pagination)                  |
| `file_edit`  | Replace exact text in a file with fuzzy matching   |
| `file_write` | Write an entire file (directory approval required) |

The agent chains tools automatically. Ask it to "find the latest Go release" and it will search, read the page, and summarize.

## Voice messages

Send voice messages in Discord and nevinho transcribes them using a local Whisper model. No extra API keys, no cost.

Enable during `nevinho setup`. Requires `ffmpeg` and a C compiler (auto-installed if missing). The Whisper model (~75MB) is stored in `~/.nevinho/whisper/`.

## Images

Attach images to a message and nevinho passes them straight to a vision-capable model. JPEG, PNG, GIF, WebP. Up to 4 images per message, 5MB each. Works with any Claude 4.x model, the GPT-4o family, and Ollama vision models like llava or llama3.2-vision.

If the current model cannot read images, nevinho replies with a hint to switch via `/model` instead of dropping the message silently.

## Scheduled tasks

Tell nevinho "every morning at 9, summarize the top 5 Hacker News stories" and it sets up a cron schedule. The runner ticks once a minute, fires anything due, and DMs you the result.

Limits: 10 schedules total, 5 minute minimum interval, 5 minute per-run timeout. Scheduled prompts run without the interactive approval flow, so don't ask schedules to do destructive things.

Cron accepts standard 5-field expressions (`0 9 * * *`), descriptors (`@daily`, `@hourly`, `@weekly`), and durations (`@every 30m`, `@every 6h`).

> **Time zone**: schedules accept an IANA timezone (e.g. `Europe/Paris`). The agent picks one up from natural language ("every day at 9am Paris time"). Without one, the cron runs in the host's local time.

## Safety

nevinho asks before running anything risky.

**Bash commands** are scanned against patterns like `rm`, `sudo`, `chmod`, `kill`, pipe to `curl`, and fork bombs. Sensitive paths (`.ssh`, `.aws`, `.env`, credentials) also trigger approval.

**File writes** outside the per-user workspace require directory approval. Approved paths persist across restarts.

**URL fetching** validates scheme (http/https only) and resolves DNS to block requests to localhost, private IPs, and link-local addresses.

The terminal client tightens this further: every shell command and every write outside the current directory asks, with an inline yes/no picker. Use `/paths` to see what is currently trusted and revoke any entry.

## Terminal use

nevinho also runs locally in your terminal. Same agent, same config. Conversation blocks render inline so your terminal handles wheel scroll, text selection, and URL clicks the same as for any other shell output.

Voice messages and scheduled tasks are Discord-only for now. They need a long-running process and an inbox, which the terminal client does not have.

Slash commands inside the terminal:

| Command        | What it does                                |
| -------------- | ------------------------------------------- |
| `/model`       | Pick a model from an interactive list       |
| `/login`       | Sign in with ChatGPT                        |
| `/config`      | Set provider keys, toggle CAVEMAN, ELEPHANT |
| `/memory`      | Show what nevinho remembers about you       |
| `/new`         | Start fresh; keep the previous conversation |
| `/resume`      | Search saved sessions for this directory    |
| `/session`     | Show current session details and summary    |
| `/status`      | Model, usage, cost, turn cap, limit hits    |
| `/paths`       | List approved paths, enter to revoke one    |
| `/paths clear` | Revoke all path permissions                 |
| `/forget`      | Delete current session, with confirmation   |
| `/help`        | Show the command list                       |
| `/quit`        | Leave (or ctrl+c)                           |

### Terminal sessions

Launch `nevinho` to continue the active session for the current working directory. A short notice identifies the restored conversation; previous transcript blocks are not replayed into scrollback.

- `/new` starts a separate conversation without deleting the old one.
- `/resume` opens a searchable picker, newest activity first. Type to filter, Enter to restore, Esc to cancel. The current session is saved before switching. Titles come from the first user message, without an extra model call.
- Resuming restores the saved model; if its provider is unavailable, an error leaves the checkpoint untouched. Tools and pending approvals are never replayed. Original images must be reattached when needed.
- `/forget` defaults to Cancel. Confirming deletion removes only the current session and starts a fresh one; other sessions remain resumable. Terminal scrollback is not erased.
- `ELEPHANT=off` disables saving and `/resume`; `/new` still works in memory. Existing saved sessions are not deleted.

Session IDs are scoped to the same canonical working-directory identity as before, not the Git root. Existing per-directory checkpoints or fallback summaries migrate automatically on first launch. Concurrent terminal processes sharing one session are not coordinated; use `/new` for separate work.

### Terminal hooks

`nevinho chat` can run your commands when a turn starts, needs approval, finishes, uses a tool, or exits. Add `~/.nevinho/hooks.json`:

```json
{
  "turn_finished": [["/usr/bin/logger", "nevinho finished a turn"]]
}
```

Restart Nevinho after editing the file. See [Terminal hooks](docs/hooks.md) for events, payloads, and a Farol setup example.

## Config

Provider configuration is encrypted and stored in `~/.nevinho/`. Terminal hooks use a separate editable JSON file in the same directory:

```
config.enc          encrypted configuration (AES-256-GCM)
chatgpt-auth.json   ChatGPT plan credentials (owner-only permissions)
secret.key          auto-generated encryption key
approved_paths.json persisted write permissions
memory.md           learned user preferences
summaries/          encrypted turn checkpoints and shutdown summaries (ELEPHANT)
whisper/            local Whisper model and binary (if voice enabled)
chat.log            terminal client runtime log (8 MiB, 3 rotated backups)
hooks.json          optional terminal lifecycle commands
last_alive.json     daemon heartbeat and clean-shutdown marker
```

`.env` in the project directory also works for development. Env vars take priority over the encrypted config.

Set `CAVEMAN=on` via `/config` for a token-saving caveman-style response. Off by default.

Set `ELEPHANT=off` via `/config` to stop persisting conversations across restarts. On by default. When on, nevinho saves an encrypted checkpoint after each completed turn and restores it on restart. Image bytes are omitted. The Discord daemon also writes a short summary on graceful shutdown as a fallback.

The Discord daemon updates `last_alive.json` every minute. If it restarts after an unclean stop, it sends the owner one DM with the last recorded heartbeat time.

Terminal sessions are scoped to the directory where you launch `nevinho`. Each session has an encrypted checkpoint containing its title, model, last activity, and recent history. An owner-only `.active` file stores only the opaque active session ID. `/forget` deletes the current session plus the old per-directory migration fallback, leaving other sessions intact. Older terminal summaries under the former shared `terminal` ID remain on disk but are not loaded into a project automatically.

## Project structure

```
main.go      entry point, CLI dispatch
cmd/         chat, serve, setup, config, upgrade, uninstall, service
agent/       agent loop, history, persistence, run mode
llm/         provider interface and implementations
tools/       bash, file_read/write/edit, find, grep, web, schedule
tui/         terminal client, blocks, selector
hooks/       optional terminal lifecycle command runner
discord/     bot, message handling, slash commands, indicator
config/      encrypted config, model catalog, setup wizard
crypto/      AES-256-GCM
memory/      preference learning
schedule/    cron job store
voice/       local Whisper transcription
logger/      coloured terminal output
```

## Demo

<table>
  <tr>
    <td><img src="assets/demo-discord.png" alt="Voice message, weather tool, code generation on Discord mobile" /></td>
    <td><img src="assets/demo-rust-websearch.jpeg" alt="Agent running web_search tool to answer a question" /></td>
  </tr>
</table>

## Documentation

- [Architecture](ARCHITECTURE.md). How nevinho processes a message, manages context, and stays small.
- [Setup](SETUP.md). Discord bot creation, VPS install, voice setup.
- [Roadmap](ROADMAP.md). Shipped and next.

## License

[MIT](LICENSE)

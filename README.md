<p align="center">
  <img src="assets/nevinho.png" width="200" />
</p>

# nevinho

A personal agent harness that runs in your Discord DMs.
Supports Anthropic, OpenAI, and Ollama.
Comes with tools for web search, code execution, and file management.

## Providers

Works with multiple LLM backends. Set one in your `.env`:

| Provider  | Env var               | Default model    |
| --------- | --------------------- | ---------------- |
| Anthropic | `ANTHROPIC_API_KEY`   | claude-haiku-4-5 |
| OpenAI    | `OPENAI_API_KEY`      | gpt-4o-mini      |
| Ollama    | `OLLAMA_MODEL=llama3` | any local model  |

Detection priority: Ollama > Anthropic > OpenAI.

You can switch models at runtime with `/model claude-sonnet-4-6` or `/model gpt-4o`.

## Setup

```bash
cp .env.example .env
# fill in DISCORD_BOT_TOKEN, DISCORD_OWNER_ID, and at least one LLM key
go run main.go
```

See [SETUP.md](SETUP.md) for Discord bot creation steps.

## Tools

| Tool         | What it does                                |
| ------------ | ------------------------------------------- |
| `web_search` | Search via Brave API or DuckDuckGo fallback |
| `web_read`   | Fetch a URL and extract readable text       |
| `run_code`   | Execute Python, Node, or bash (10s timeout) |
| `file_read`  | Read a file from workspace or absolute path |
| `file_write` | Write a file (absolute paths need approval) |

The agent chains tools automatically. Ask it to "find the latest Go release" and it will search, read the page, and summarize.

## Commands

| Command                 | What it does                          |
| ----------------------- | ------------------------------------- |
| `/new`                  | Start a fresh conversation            |
| `/model`                | Show current model                    |
| `/model <name>`         | Switch model                          |
| `/status`               | Uptime, token usage, model info       |
| `/paths`                | List approved write paths             |
| `/paths clear`          | Revoke all path permissions           |
| `/connect <service>`    | Link GitHub or Google via device flow |
| `/disconnect <service>` | Unlink a service                      |
| `/accounts`             | Show connected services               |
| `/help`                 | Show capabilities                     |

All commands also work as plain text messages in the DM.

## Safety

Dangerous operations require approval before execution.

**Code execution** is scanned against patterns like `rm`, `sudo`, `chmod`, `kill`, pipe to `curl`, and fork bombs. Sensitive paths (`.ssh`, `.aws`, `.env`, credentials) also trigger approval. The agent shows a preview and waits for you to reply "yes".

**File writes** outside the per-user workspace require one-time directory approval. Approved paths persist across restarts.

**URL fetching** validates scheme (http/https only) and resolves DNS to block requests to localhost, private IPs, and link-local addresses.

## Config

Data lives in `~/.config/nevinho/`:

```
workspace/          per-user sandboxed file storage
approved_paths.json persisted write permissions
credentials.enc     encrypted OAuth tokens (AES-256-GCM)
secret.key          auto-generated encryption key
```

## Project structure

```
main.go      entry point, provider detection
agent/       chat loop, tool orchestration, approval flow
llm/         provider interface (Anthropic, OpenAI, Ollama)
tools/       web search, code execution, file I/O
discord/     bot, slash commands, message handling
auth/        OAuth device flow, encrypted credential storage
logger/      colored terminal output
```

## License

[MIT](LICENSE)

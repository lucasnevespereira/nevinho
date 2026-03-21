<p align="center">
  <img src="assets/nevinho.png" width="200" />
</p>

# nevinho

A personal AI assistant that lives in your Discord DMs. Ask it questions, browse the web, run code, save notes — all from your phone.

## What it does

- Browse and read web pages
- Search the web (via Brave Search API)
- Run code snippets (Python, Node.js, bash)
- Read and write files with permission controls
- Conversation history (per user)

## Providers

Works with multiple LLM backends. Set one in your `.env`:

| Provider | Env var | Model |
|----------|---------|-------|
| Anthropic | `ANTHROPIC_API_KEY` | claude-haiku-4-5 |
| OpenAI | `OPENAI_API_KEY` | gpt-4o-mini |
| Ollama | `OLLAMA_MODEL=llama3` | any local model |

First match wins: Ollama > Anthropic > OpenAI.

## Setup

```bash
cp .env.example .env
# fill in DISCORD_BOT_TOKEN, DISCORD_OWNER_ID, and one LLM key
go run main.go
```

See [setup.md](setup.md) for Discord bot creation steps.

## Commands

| Command | What it does |
|---------|-------------|
| `/new` | Start a fresh conversation |
| `/forget` | Clear all history |
| `/help` | Show available tools |

## Project structure

```
main.go          — entry point, provider detection
agent/           — chat loop, tool definitions, logging
llm/             — provider interface (Anthropic, OpenAI, Ollama)
discord/         — Discord bot, message handling
tools/           — web, code execution, file I/O
```

## Config

Data is stored in `~/.config/nevinho/`:

```
workspace/           — saved files per user
approved_paths.json  — write permissions for absolute paths
```

## License

MIT

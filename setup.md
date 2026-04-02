# Nevinho — Setup Guide

## 1. Create the Discord bot

1. Go to https://discord.com/developers/applications
2. Click **New Application** → name it "Nevinho" → Create
3. Go to **Bot** tab
4. Click **Reset Token** → copy the token → save it somewhere safe
5. Scroll down to **Privileged Gateway Intents**
6. Enable **Message Content Intent** → Save

## 2. Generate the invite link

1. Go to **OAuth2** → **URL Generator**
2. Scopes: check `bot` and `applications.commands`
3. Bot Permissions:
   - **General:** View Channels
   - **Text:** Send Messages, Create Private Threads, Send Messages in Threads, Manage Threads, Embed Links, Attach Files, Read Message History, Mention Everyone, Use External Emojis, Use External Stickers, Add Reactions, Use Slash Commands, Use Embedded Activities, Use External Apps, Create Polls, Send Voice Messages
   - **Voice:** Connect, Speak, Use Embedded Activities
4. Copy the generated URL at the bottom
5. Open it in your browser → select your Discord server

## 3. Get your Discord user ID

1. Open Discord → go to Settings → Advanced → enable **Developer Mode**
2. Right-click on your own username → **Copy User ID**

## 4. Get API keys

You need at least one LLM provider. You can configure multiple and switch between them with `/model`.

**Anthropic (Claude):**
1. Go to https://console.anthropic.com
2. Go to **API Keys** → **Create Key**
3. Add credits ($5-10 is enough for months)

**OpenAI (GPT):**
1. Go to https://platform.openai.com
2. Go to **API Keys** → **Create new secret key**
3. Add credits

**Ollama (local models):**
1. Install from https://ollama.com
2. Pull a model: `ollama pull llama3`

**Brave Search (optional, for web search):**
1. Go to https://brave.com/search/api
2. Get a free API key (2000 queries/month)

## 5. Configure the project

```bash
cd nevinho
cp .env.example .env
```

Edit `.env` and fill in:

```env
DISCORD_BOT_TOKEN=paste_your_bot_token
DISCORD_OWNER_ID=paste_your_discord_user_id

# At least one LLM provider (priority: Ollama > Anthropic > OpenAI)
ANTHROPIC_API_KEY=paste_your_anthropic_key
OPENAI_API_KEY=paste_your_openai_key
OLLAMA_MODEL=llama3

# Optional
BRAVE_API_KEY=paste_your_brave_key
```

## 6. Run it locally

```bash
make run
```

You should see:

```
provider: openai
nevinho is online
```

## 7. Run it on a VPS

```bash
# Install Go if needed: https://go.dev/doc/install

git clone https://github.com/lucasnevespereira/nevinho.git
cd nevinho
cp .env.example .env
# Edit .env with your keys
make build
```

Start it in the background so it survives after you close SSH:

```bash
nohup ./bin/nevinho > nevinho.log 2>&1 &
```

Check logs: `tail -f nevinho.log`

Stop it: `pkill nevinho`

## 8. Test it

Open Discord. DM the bot. Try:

- "What's 2+2?"
- "Search for the latest news about Go"
- "Save a note: buy groceries tomorrow"
- `/model` — show current model
- `/model gpt-4o` — switch to GPT-4o
- `/status` — check uptime and token usage
- `/new` — fresh conversation

## Troubleshooting

**Bot doesn't respond to DMs:**
- Make sure Message Content Intent is enabled (step 1.6)
- Make sure your DISCORD_OWNER_ID matches your actual Discord user ID
- Check the terminal for error logs

**Slash commands don't show up:**
- Make sure `applications.commands` scope is checked (step 2.2)
- Re-invite the bot with the updated URL
- It may take a few minutes for Discord to propagate commands

**API errors:**
- Verify your API keys are correct
- Check you have credits on your provider's dashboard

**"cannot find package" errors:**
- Run `go mod tidy` in the project directory

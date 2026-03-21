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
2. Scopes: check **bot**
3. Bot Permissions: check **Send Messages**, **Read Message History**, **Embed Links**
4. Copy the generated URL at the bottom
5. Open it in your browser → select your Discord server (or just DM the bot directly)

## 3. Get your Discord user ID

1. Open Discord → go to Settings → Advanced → enable **Developer Mode**
2. Right-click on your own username → **Copy User ID**

## 4. Get an Anthropic API key

1. Go to https://console.anthropic.com
2. Sign up or log in
3. Go to **API Keys** → **Create Key**
4. Copy the key
5. Add credits ($5-10 is enough for months of testing)

## 5. Configure the project

```bash
cd ~/dev/play/nevinho
cp .env.example .env
```

Edit `.env` and fill in:

```env
DISCORD_BOT_TOKEN=paste_your_bot_token
DISCORD_OWNER_ID=paste_your_discord_user_id
ANTHROPIC_API_KEY=paste_your_anthropic_key
```

## 6. Run it

```bash
go run main.go
```

You should see:

```
nevinho is online. Press Ctrl+C to stop.
```

## 7. Test it

Open Discord. DM the bot. Try:

- "What's 2+2?"
- "Check if google.com is up"
- "Save a note: buy groceries tomorrow"
- "Read my notes"
- `/help`
- `/new` (fresh conversation)

## Troubleshooting

**Bot doesn't respond to DMs:**

- Make sure Message Content Intent is enabled (step 1.6)
- Make sure your DISCORD_OWNER_ID matches your actual Discord user ID
- Check the terminal for error logs

**API errors:**

- Verify your ANTHROPIC_API_KEY is correct
- Check you have credits at console.anthropic.com

**"cannot find package" errors:**

- Run `go mod tidy` in the project directory

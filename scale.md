# Scaling Nevinho — From Personal Bot to Autonomous Agent Platform

## Vision

Nevinho is a personal AI agent you control from your phone (Discord). Anyone installs it, connects their accounts (GitHub, GitLab, etc.), and gets a fully autonomous dev assistant that can clone repos, write code, create PRs — all via Discord DMs.

---

## Part 1: How Authentication Actually Works

The hard problem: user says "push this to my GitHub" in Discord. Nevinho needs a GitHub token — but the user is on their phone. No terminal, no SSH keys, no `gh auth login`.

### Option A: GitHub Device Flow (best for self-hosted)

GitHub's [Device Flow](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#device-flow) was designed for exactly this — devices without a browser redirect.

```
User in Discord          Nevinho                    GitHub
     │                      │                          │
     │  /connect github     │                          │
     │ ──────────────────▶  │                          │
     │                      │  POST /login/device/code │
     │                      │ ────────────────────────▶│
     │                      │  { user_code: "ABCD-1234",
     │                      │    verification_uri }    │
     │  "Go to github.com/  │◀─────────────────────────│
     │   login/device and   │                          │
     │   enter: ABCD-1234"  │                          │
     │◀──────────────────── │                          │
     │                      │                          │
     │  (user opens link    │  Poll: POST /login/      │
     │   on phone browser,  │  oauth/access_token      │
     │   enters code,       │ ────────────────────────▶│
     │   authorizes)        │  { access_token: "..." } │
     │                      │◀─────────────────────────│
     │                      │                          │
     │  "Connected to       │  Store token encrypted   │
     │   GitHub as @lucas!" │                          │
     │◀──────────────────── │                          │
```

**Why this works on phone:**
- User taps the link in Discord → opens phone browser → enters 8-char code → done
- No redirect URI needed, no localhost server
- Works for GitHub, GitLab, Bitbucket, Google (all support device flow)

### Option B: GitHub App (best for hosted/multi-user)

If you run Nevinho as a hosted service (nevinho.dev):

1. Create a GitHub App (not OAuth App)
2. User installs it on their repos: `github.com/apps/nevinho/installations/new`
3. Nevinho gets **installation tokens** scoped to only the repos the user selected
4. No personal tokens stored — GitHub manages permissions

**Advantages:**
- User can revoke access per-repo from GitHub settings
- Fine-grained permissions (read code, write PRs, no admin)
- No long-lived tokens — installation tokens expire in 1 hour, auto-refresh

### Credential Storage

```
┌─────────────────────────────────────────┐
│  Credential Store (per user)            │
│                                         │
│  github_token: encrypted(AES-256-GCM)  │
│  gitlab_token: encrypted(...)           │
│  llm_api_key:  encrypted(...)           │
│                                         │
│  Encryption key: derived from           │
│  user's Discord ID + server secret      │
│  (or user-provided passphrase)          │
└─────────────────────────────────────────┘
```

- Tokens encrypted at rest with AES-256-GCM
- Master key from env var (`NEVINHO_ENCRYPTION_KEY`), never in code
- Each user's tokens isolated — compromising one doesn't leak others
- Self-hosted: stored in `~/.config/nevinho/credentials.enc`
- Hosted: stored in PostgreSQL with per-row encryption

---

## Part 2: The Full Autonomous Flow

User on phone says: *"clone my repo nevinho, add a /ping command, push it and create a PR"*

### What happens:

```
1. Discord message received
2. Agent parses intent: clone → edit → commit → push → PR
3. Agent checks: does user have GitHub connected?
   No  → "Run /connect github first"
   Yes → proceed
4. git_clone(repo="lucasnevespereira/nevinho", branch="main")
   → Clones into /workspace/{user_id}/nevinho/
5. Agent reads relevant files to understand the code
6. Agent writes new code via file_write
7. git_commit(message="feat: add /ping command")
8. git_push(branch="feat/ping-command")
   → Uses stored GitHub token for HTTPS push
9. github_pr_create(title="Add /ping command", body="...")
   → Uses GitHub API with stored token
10. Returns PR URL to user in Discord
```

### New Tools Needed

```go
// Git operations — run in user's workspace
var gitTools = []llm.ToolDef{
    {
        Name:        "git_clone",
        Description: "Clone a repository into the workspace.",
        Schema:      `{"properties":{"repo":{"type":"string","description":"owner/repo"},"branch":{"type":"string"}}}`,
    },
    {
        Name:        "git_status",
        Description: "Show working tree status of the current repo.",
    },
    {
        Name:        "git_diff",
        Description: "Show uncommitted changes.",
    },
    {
        Name:        "git_commit",
        Description: "Stage all changes and commit.",
        Schema:      `{"properties":{"message":{"type":"string"}}}`,
    },
    {
        Name:        "git_push",
        Description: "Push current branch to remote. Creates branch if needed.",
        Schema:      `{"properties":{"branch":{"type":"string"}}}`,
    },
    {
        Name:        "git_pr",
        Description: "Create a pull request on GitHub/GitLab.",
        Schema:      `{"properties":{"title":{"type":"string"},"body":{"type":"string"},"base":{"type":"string"}}}`,
    },
}
```

### Workspace Model

Each user gets an isolated workspace on disk (self-hosted) or in a container (hosted):

```
/workspace/
  ├── {user_id_1}/
  │   ├── nevinho/          ← cloned repo
  │   └── my-app/           ← another repo
  └── {user_id_2}/
      └── project/
```

Git operations use the stored token for HTTPS auth:

```go
func (r *Registry) gitClone(input json.RawMessage, userID string) string {
    // Get user's GitHub token from credential store
    token, err := r.credentials.Get(userID, "github")
    if err != nil {
        return "GitHub not connected. Use /connect github"
    }

    // Clone using HTTPS with token
    // https://x-access-token:{token}@github.com/{repo}.git
    repoURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s.git", token, in.Repo)
    cmd := exec.Command("git", "clone", repoURL, targetDir)
    // ...
}
```

---

## Part 3: Discord Commands for Account Management

```
/connect github     → Start Device Flow, link GitHub account
/connect gitlab     → Same for GitLab
/connect llm        → Set LLM API key (Anthropic/OpenAI)
/disconnect github  → Revoke and delete stored token
/accounts           → Show connected services
/workspace          → List cloned repos
/workspace clear    → Delete all workspace files
```

### The /connect flow in practice

User types `/connect github` in Discord DM:

```
🔗 Connect GitHub

1. Open this link: https://github.com/login/device
2. Enter code: ABCD-1234
3. Authorize "Nevinho"

⏳ Waiting for authorization... (expires in 15 minutes)
```

Nevinho polls GitHub's token endpoint every 5 seconds. When authorized:

```
✅ Connected to GitHub as @lucasnevespereira
You have access to 42 repositories.

Try: "clone my repo nevinho and show me the README"
```

---

## Part 4: Confirmation UX for Dangerous Actions

The agent should never silently push code or create PRs. Use Discord's interactive components:

```
🔀 Ready to push and create PR

Branch: feat/ping-command
Commits: 1 (feat: add /ping command)
Target: main
Files changed: 2 (+45 -0)

[✅ Push & Create PR]  [❌ Cancel]  [👁 Show Diff]
```

Discord buttons (Message Components) let the user approve from phone with one tap.

### Confirmation tiers:

| Action | Confirmation |
|--------|-------------|
| Clone, read files, git status | No — safe, read-only |
| Write files, commit | No — local to workspace |
| Push to branch | Yes — visible to others |
| Push to main/master | Yes + warning |
| Force push | Yes + double confirm |
| Create/close PR | Yes |
| Delete branch/repo | Yes + type repo name |

---

## Part 5: Architecture for Distribution

### Self-Hosted (single binary)

```bash
# Install
curl -fsSL https://nevinho.dev/install.sh | sh

# Setup wizard
nevinho init
# → Creates .env with guided prompts
# → "Paste your Discord bot token: ..."
# → "Paste your Anthropic API key (or press Enter to skip): ..."

# Run
nevinho start
# → "Nevinho is online. DM your bot on Discord to get started."
# → "Type /connect github in Discord to link your GitHub."
```

Everything runs locally. Repos cloned to `~/.nevinho/workspace/`. Credentials in `~/.nevinho/credentials.enc`. Single binary, zero external dependencies except Docker (optional, for sandboxed code execution).

### Hosted (nevinho.dev)

```
┌──────────┐     ┌──────────────┐     ┌─────────────┐
│ Discord   │────▶│  API Gateway  │────▶│ Agent Pool  │
│ Webhook   │     │  (auth, rate  │     │ (stateless  │
└──────────┘     │   limiting)   │     │  workers)   │
                 └──────────────┘     └──────┬──────┘
                                             │
                 ┌──────────────┐     ┌──────┴──────┐
                 │  Credential  │     │ Tool Runner  │
                 │  Vault       │◀────│ (sandboxed)  │
                 │  (encrypted) │     └──────┬──────┘
                 └──────────────┘            │
                                      ┌──────┴──────┐
                                      │  Workspace   │
                                      │  Storage     │
                                      │  (per-user)  │
                                      └─────────────┘
```

Users sign up → connect Discord → connect GitHub → go. They never touch a terminal.

Revenue model:
- **Free:** BYOK (bring your own LLM key), 50 messages/day, 1 workspace
- **Pro ($10/mo):** Managed LLM (no key needed), unlimited messages, 10 workspaces, priority queue
- **Team ($25/user/mo):** Shared workspaces, org-level GitHub App, audit logs

---

## Part 6: Multi-Service Auth Matrix

| Service | Auth Method | Scopes Needed |
|---------|------------|---------------|
| GitHub | Device Flow / GitHub App | `repo`, `workflow`, `read:org` |
| GitLab | Device Flow / OAuth | `api`, `read_repository`, `write_repository` |
| Bitbucket | OAuth 2.0 | `repository:write`, `pullrequest:write` |
| Vercel | OAuth | `deployments:read`, `projects:read` |
| Railway | API Token (pasted) | Full access |
| Supabase | API Token (pasted) | Project-scoped |
| AWS | Access Key (pasted) | IAM-scoped |

For services without Device Flow, fall back to:
1. OAuth with a callback URL (hosted only)
2. User pastes a token in Discord DM (encrypted immediately, message deleted)

---

## Part 7: What to Build First

| Priority | What | Unlocks |
|----------|------|---------|
| **1** | `/connect github` with Device Flow | Git operations from phone |
| **2** | `git_clone`, `git_status`, `git_diff` | Agent can read repos |
| **3** | `git_commit`, `git_push`, `git_pr` | Agent can ship code |
| **4** | Discord buttons for confirmations | Safe push/PR from phone |
| **5** | Encrypted credential store | Multi-service support |
| **6** | `nevinho init` CLI wizard | Easy self-hosted setup |
| **7** | Docker Compose distribution | One-command deploy |
| **8** | Hosted version | Non-technical users |

### Step 1 implementation sketch

```go
// discord/bot.go — add to slash commands
{
    Name:        "connect",
    Description: "Connect an external service (github, gitlab)",
    Options: []*discordgo.ApplicationCommandOption{
        {
            Type:        discordgo.ApplicationCommandOptionString,
            Name:        "service",
            Description: "Service to connect",
            Required:    true,
            Choices: []*discordgo.ApplicationCommandOptionChoice{
                {Name: "github", Value: "github"},
                {Name: "gitlab", Value: "gitlab"},
            },
        },
    },
}

// auth/device_flow.go
func StartGitHubDeviceFlow(clientID string) (*DeviceCode, error) {
    // POST https://github.com/login/device/code
    // Returns: user_code, device_code, verification_uri
}

func PollForToken(clientID, deviceCode string) (string, error) {
    // POST https://github.com/login/oauth/access_token
    // Poll every interval until user authorizes or timeout
}
```

You'd register a GitHub OAuth App (free) with Device Flow enabled. The `client_id` goes in your `.env`, no `client_secret` needed for device flow on public clients.

---

## Cost Estimates

| Component | Self-Hosted | Hosted (1K users) |
|-----------|------------|-------------------|
| LLM API | BYOK ($0) | $500-2K/mo (pass-through or markup) |
| Compute | Your machine ($0) | $50-100/mo (Fly.io/Railway) |
| Storage | Local disk ($0) | $15/mo (S3/R2) |
| Database | SQLite ($0) | $15/mo (Postgres) |
| GitHub OAuth App | Free | Free |
| Domain + TLS | $12/year | $12/year |
| **Total** | **~$0** | **$100-150/mo + LLM** |

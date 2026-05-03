package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/lucasnevespereira/nevinho/config"
	"github.com/lucasnevespereira/nevinho/llm"
)

const maxResponseLen = 8000

type Pending struct {
	Kind   string // "path" or "code"
	Detail string
	Code   *pendingCode
}

// FileDisplay holds file content to be sent to Discord.
type FileDisplay struct {
	Path    string
	Lang    string
	Content string
}

type pendingCode struct {
	Input  json.RawMessage
	UserID string
}

type Registry struct {
	mu       sync.Mutex
	cfg      *config.Config
	approved map[string]bool
	pending  map[string]*Pending
	files    map[string][]FileDisplay
	permFile string
}

func NewRegistry(cfg *config.Config) *Registry {
	r := &Registry{
		cfg:      cfg,
		approved: make(map[string]bool),
		pending:  make(map[string]*Pending),
		files:    make(map[string][]FileDisplay),
		permFile: filepath.Join(cfg.Dir(), "approved_paths.json"),
	}
	r.loadApproved()
	return r
}

func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage, userID string) string {
	switch name {
	case "web_read":
		return r.webRead(input)
	case "web_search":
		return r.webSearch(input)
	case "bash":
		return r.runBash(ctx, input, userID)
	case "file_read":
		return r.fileRead(input, userID)
	case "file_write":
		return r.fileWrite(input, userID)
	case "file_list":
		return r.fileList(input, userID)
	case "file_edit":
		return r.fileEdit(input, userID)
	case "grep":
		return r.grepSearch(ctx, input, userID)
	case "find":
		return r.findFiles(ctx, input, userID)
	default:
		return fmt.Sprintf("unknown tool: %s", name)
	}
}

func (r *Registry) Defs() []llm.ToolDef {
	return []llm.ToolDef{
		{
			Name: "web_read",
			Description: `Fetch a web page and return its content as markdown. Headings, lists, links, and tables are preserved. Scripts, styles, nav, and footer are stripped.
Use instead of bash curl/wget for reading web content. Do not use for JSON APIs; use bash curl with the right Accept header.
Output: markdown text. Truncated to 8000 chars with "...(truncated)" appended when longer.
Errors (literal prefixes, match exactly):
- "invalid input: ..." bad JSON args.
- "blocked: ..." SSRF-blocked host, bad scheme, or private IP. Do not retry.
- "failed to fetch: ..." network error. Detail is one of: "timed out", "DNS lookup failed", "connection refused", "connection reset", "TLS/certificate error", "HTTP 401/403/404/408/429", "HTTP <code> server error", "HTTP <code>".
- "no content extracted" page had no readable body.
Retry "failed to fetch: HTTP 5xx" or "timed out" at most once. On 401/403/404, try a different source. Cite the URL at the end of your reply on its own line: "Source: <url>".`,
			Schema: `{"type":"object","properties":{"url":{"type":"string","description":"Full URL to fetch (must be http or https)"}},"required":["url"]}`,
		},
		{
			Name: "web_search",
			Description: `Search the web. Returns a summarized answer (when available) followed by source titles, URLs, and snippets.
Use to find relevant pages, then web_read for full content when snippets are not enough.
When to use:
- Knowledge questions where an authoritative source exists (docs, specs, standards, reference manuals). The user wants citable sources, so do not answer from memory when one is available.
- Anything time-sensitive: versions, releases, news, pricing, current status.
Optional params:
- region: 2-letter country code ("ch", "us", "fr", "de") to bias results. Use for location-specific queries ("jobs in Sion", "restaurants near Zurich").
- time_range: "d", "w", "m", "y" (past day/week/month/year). Use for news or anything where freshness matters.
Output: plain text. Answer section (when available) then numbered sources with title, URL, and snippet.
Errors (literal prefixes):
- "invalid input: ..." bad JSON args.
- "search failed: ..." network or HTTP error. Same detail vocabulary as web_read.
- "no results found" query returned nothing. Try broader terms.
- "failed to parse search results" upstream returned malformed data.
Retry "search failed: HTTP 5xx" or "timed out" at most once. Cite each source URL at the end of your reply on its own line: "Source: <url>".`,
			Schema: `{"type":"object","properties":{"query":{"type":"string","description":"Search query"},"region":{"type":"string","description":"2-letter country code to bias results (e.g. \"ch\", \"us\")"},"time_range":{"type":"string","enum":["d","w","m","y"],"description":"Freshness filter: d=day, w=week, m=month, y=year"}},"required":["query"]}`,
		},
		{
			Name: "bash",
			Description: `Run a bash command and return combined stdout/stderr. 2-minute timeout. Non-interactive only.
Rules:
- No interactive prompts. Pass -y or --yes to package managers. Commands that wait for input will time out.
- No long-running commands: tail -f, watch, sleep, servers, REPLs. They hit the timeout and return nothing useful. Use a one-shot form.
- Prefer file_read, file_write, file_edit over cat, echo, sed. Prefer the grep and find tools over bash grep and find.
- Clone into ~/apps unless the user says otherwise. Never into / or ~. Report the full path in your reply.
- rm, sudo, chmod, kill, dd, curl piped to a shell, and curl with output flags require user approval. Paths under ~/.ssh, ~/.aws, ~/.kube, /etc, ~/.env, and anything named secret/token/password also require approval.
Output: combined stdout and stderr with ANSI stripped. "(no output)" when empty. Appended "(timed out after 2m)" on timeout, "(cancelled)" on cancel, raw error string on other failures. Output over 8000 chars is tail-truncated with a "...(showing last <N> of <M> chars)..." header.
Errors (literal prefixes):
- "invalid input: ..." bad JSON args.
- "NEEDS_APPROVAL:run_code" dangerous command or sensitive path. Stop and wait for the user.
On approval the tool re-runs and returns the actual command output.`,
			Schema: `{"type":"object","properties":{"command":{"type":"string","description":"Bash command to execute"}},"required":["command"]}`,
		},
		{
			Name: "file_read",
			Description: `Read a file. Default returns up to 200 lines. Use offset to paginate large files.
Rules:
- Always file_read before file_edit. Copy old_text exactly from the read output including whitespace.
- When the result ends with "[truncated, use offset=<N> to continue]", call file_read again with that offset.
- When the user asks what a file contains, summarize in your own words. Do not paste the whole file unless explicitly asked.
- For source code, quote only the relevant snippet.
Output: header "[<path>, lines <start>-<end> of <total>]" then content. Appends "[truncated, use offset=<N> to continue]" when there is more.
Errors (literal prefixes):
- "invalid input: ..." bad JSON args.
- "invalid path: ..." path could not be resolved.
- "file not found: <path>".
- "failed to read: ..." permission or I/O error.
- "offset <N> exceeds file length (<M> lines)".`,
			Schema: `{"type":"object","properties":{"path":{"type":"string","description":"Absolute file path"},"offset":{"type":"integer","description":"Line number to start from (1-indexed)"},"limit":{"type":"integer","description":"Max lines to return (default 200)"}},"required":["path"]}`,
		},
		{
			Name: "file_write",
			Description: `Write a file, replacing its full content. Creates parent directories. Requires directory approval.
Rules:
- Prefer file_edit for changes to existing files. Use file_write for new files or complete rewrites.
- Content over 100KB is rejected. Split into smaller files.
- Report the full path of the written file in your reply.
Output: "saved to <path>" on success.
Errors (literal prefixes):
- "invalid input: ..." bad JSON args.
- "content too large (max 100KB)".
- "invalid path: ...".
- "NEEDS_APPROVAL:<dir>" first write to a new directory. Stop and wait for the user; on approval the retry will succeed.
- "failed to create directory: ...", "failed to write: ..." I/O errors.`,
			Schema: `{"type":"object","properties":{"path":{"type":"string","description":"Absolute file path"},"content":{"type":"string","description":"Full file content"}},"required":["path","content"]}`,
		},
		{
			Name: "file_list",
			Description: `List entries in a directory. Directories get a trailing slash; files show size.
Use before reading or editing to confirm a path exists and to map project structure.
Output: first line is the directory (home-abbreviated with ~). Each entry is indented two spaces. Format "<name>/" for directories, "<name> (<size>)" for files. "<dir> (empty)" when the directory has no entries.
Errors (literal prefixes):
- "invalid input: ...", "invalid path: ...", "failed to list: ...".`,
			Schema: `{"type":"object","properties":{"path":{"type":"string","description":"Directory path to list (defaults to current directory)"}},"required":[]}`,
		},
		{
			Name: "file_edit",
			Description: `Replace exact text in a file. One call can apply multiple non-overlapping edits.
Rules:
- Always file_read first. Copy old_text byte-for-byte from the output, including whitespace.
- old_text must match exactly once. Add surrounding context until it does.
- Keep old_text minimal while still unique.
- Multiple changes in the same file go in one call with edits[]. All matches are against the original content, not incremental.
- If a match fails, re-read the file. Content may have changed.
- Requires directory approval for the target's parent.
Output: "edited <path> (1 block replaced)" or "edited <path> (<N> blocks replaced)".
Errors (literal prefixes):
- "invalid input: ...", "invalid path: ...", "file not found: ...".
- "at least one edit is required ...".
- "edits[<i>]: old_text must not be empty" or "edits[<i>]: old_text and new_text are identical, no change needed".
- "Could not find old_text in <path>. ..." re-read to see the exact content, then retry.
- "Found <N> occurrences of old_text in <path>. Include more surrounding context ..." add more context.
- "edits overlap ..." merge nearby changes into one edit or split into separate file_edit calls.
- "NEEDS_APPROVAL:<dir>".
- "failed to write: ...".`,
			Schema: `{"type":"object","properties":{"path":{"type":"string","description":"Absolute file path"},"old_text":{"type":"string","description":"Exact text to find"},"new_text":{"type":"string","description":"Replacement text"},"edits":{"type":"array","description":"One or more replacements","items":{"type":"object","properties":{"old_text":{"type":"string","description":"Exact text to find (must be unique in file)"},"new_text":{"type":"string","description":"Text to replace it with"}},"required":["old_text","new_text"]}}},"required":["path"]}`,
		},
		{
			Name: "grep",
			Description: `Search file contents for a regex. Returns matching lines with file path and line number.
Use instead of bash grep: faster, structured output, paths made relative to the search root.
Rules:
- Default path is the current directory. Pass path only when searching elsewhere.
- Long lines are truncated at 500 chars with "...(truncated)".
- Default limit is 100 matches. Raise limit only when you need more.
Output: "<relpath>:<line>:<text>" per match. Appends "[<N> matches limit reached. Use limit=<M> for more, or refine pattern]" when capped. Appends "[Some lines truncated to 500 chars]" when line truncation happened.
Errors (literal prefixes):
- "invalid input: ...", "invalid path: ...", "pattern is required".
- "no matches found" nothing matched. Try ignore_case or a broader pattern.
- "grep timed out after 30s ..." narrow the path or pattern.
- "grep failed: ..." other error.`,
			Schema: `{"type":"object","properties":{"pattern":{"type":"string","description":"Search pattern (basic regex)"},"path":{"type":"string","description":"Directory or file to search (defaults to current directory)"},"glob":{"type":"string","description":"File name filter, e.g. \"*.go\""},"ignore_case":{"type":"boolean","description":"Case-insensitive search"},"context_lines":{"type":"integer","description":"Lines of context around matches"},"limit":{"type":"integer","description":"Max matches (default 100)"}},"required":["pattern"]}`,
		},
		{
			Name: "find",
			Description: `Find files or directories by name pattern (shell glob). Excludes .git, node_modules, __pycache__, .venv.
Rules:
- Default search root is the current working directory. Only pass path when the user explicitly references a different location (e.g. "in ~/apps").
- Use type="d" for directories, "f" (default) for files.
- Default limit is 500.
- To list a directory, use file_list. find is for locating specific names.
Output: one relative path per line. Appends "[<N> results limit reached. Use limit=<M> for more, or refine pattern]" when capped.
Errors (literal prefixes):
- "invalid input: ...", "invalid path: ...", "pattern is required ...".
- "no files found matching pattern" pattern did not hit.
- "find timed out after 30s ..." narrow the path or pattern.
- "find failed: ..." other error.`,
			Schema: `{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern, e.g. \"*.go\", \"Makefile\", \"myproject\""},"path":{"type":"string","description":"Directory to search in (defaults to current directory)"},"type":{"type":"string","description":"Type: \"f\" for files (default), \"d\" for directories"},"limit":{"type":"integer","description":"Max results (default 500)"}},"required":["pattern"]}`,
		},
	}
}

func (r *Registry) ApprovedPaths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var paths []string
	for p := range r.approved {
		paths = append(paths, shortenHome(p))
	}
	return paths
}

func (r *Registry) ClearApprovedPaths() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.approved = make(map[string]bool)
	r.saveApproved()
}

func (r *Registry) PendingApproval(userID string) *Pending {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending[userID]
}

func (r *Registry) QueueFileDisplay(userID, path, lang, content string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files[userID] = append(r.files[userID], FileDisplay{Path: path, Lang: lang, Content: content})
}

func (r *Registry) DrainFileDisplays(userID string) []FileDisplay {
	r.mu.Lock()
	defer r.mu.Unlock()
	files := r.files[userID]
	delete(r.files, userID)
	return files
}

func (r *Registry) ClearPending(userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pending, userID)
}

func (r *Registry) ApprovePending(userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.pending[userID]
	if !ok {
		return
	}
	if p.Kind == "path" {
		r.approved[p.Detail] = true
		r.saveApproved()
	}
	delete(r.pending, userID)
}

func (r *Registry) ExecutePendingCode(ctx context.Context, userID string) string {
	r.mu.Lock()
	p := r.pending[userID]
	r.mu.Unlock()
	if p == nil || p.Kind != "code" || p.Code == nil {
		return ""
	}
	r.mu.Lock()
	delete(r.pending, userID)
	r.mu.Unlock()
	return r.executePendingBash(ctx, p.Code.Input)
}

func (r *Registry) checkWritePermission(resolved, userID string) error {
	dir := filepath.Dir(resolved)
	r.mu.Lock()
	defer r.mu.Unlock()
	check := dir
	for check != "/" && check != "." {
		if r.approved[check] {
			return nil
		}
		check = filepath.Dir(check)
	}
	r.pending[userID] = &Pending{Kind: "path", Detail: dir}
	return fmt.Errorf("NEEDS_APPROVAL:%s", shortenHome(dir))
}

func (r *Registry) checkCodePermission(userID, preview string, input json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[userID] = &Pending{
		Kind:   "code",
		Detail: preview,
		Code:   &pendingCode{Input: input, UserID: userID},
	}
	return fmt.Errorf("NEEDS_APPROVAL:run_code")
}

func (r *Registry) loadApproved() {
	data, err := os.ReadFile(r.permFile)
	if err != nil {
		return
	}
	var paths []string
	if err := json.Unmarshal(data, &paths); err != nil {
		return
	}
	for _, p := range paths {
		r.approved[p] = true
	}
}

func (r *Registry) saveApproved() {
	var paths []string
	for p := range r.approved {
		paths = append(paths, p)
	}
	data, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		log.Printf("failed to marshal approved paths: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(r.permFile), 0755); err != nil {
		log.Printf("failed to create config dir: %v", err)
		return
	}
	if err := os.WriteFile(r.permFile, data, 0644); err != nil {
		log.Printf("failed to save approved paths: %v", err)
	}
}

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
			Description: `Fetch a web page and return its readable text content. Strips scripts, styles, nav, and boilerplate.
Use this instead of bash curl/wget for reading web content.
When you use information from a fetched page in your answer, cite the source URL at the end on its own line: "Source: <url>".`,
			Schema: `{"type":"object","properties":{"url":{"type":"string","description":"Full URL to fetch (must be http or https)"}},"required":["url"]}`,
		},
		{
			Name: "web_search",
			Description: `Search the web and return titles, URLs, and snippets for the top results.
Use this to find relevant pages, then use web_read to get full content.
When to use:
- Any knowledge question where an authoritative source exists (official docs, specs, project homepage, standards, reference manuals). This includes "what is X", "how does X work", "how do I do X", "why does X happen", comparisons, and best practices. The user wants references they can share with colleagues, so do not answer from memory alone when a citeable source is available.
- Anything time-sensitive: versions, releases, news, pricing, current status.
When you use information from search results or pages you read, cite each source URL at the end of your answer on its own line: "Source: <url>". Multiple sources get multiple lines.`,
			Schema: `{"type":"object","properties":{"query":{"type":"string","description":"Search query"}},"required":["query"]}`,
		},
		{
			Name: "bash",
			Description: `Run a bash command and return combined stdout/stderr. Times out after 2 minutes. Non-interactive only.
Guidelines:
- Use -y or --yes flags for package managers. Never use interactive prompts.
- Prefer file_read/file_write/file_edit over cat/echo/sed for file operations.
- Prefer grep/find tools over bash grep/find for code search.
- When cloning repos, clone into ~/apps unless told otherwise. Never clone into / or ~.
- Always report the full path of created/cloned directories in your response.
- Commands like rm, sudo, chmod, and curl with output flags require user approval.`,
			Schema: `{"type":"object","properties":{"command":{"type":"string","description":"Bash command to execute"}},"required":["command"]}`,
		},
		{
			Name: "file_read",
			Description: `Read a file's contents. Output is truncated to 200 lines. Use offset to continue reading large files.
Guidelines:
- Always read a file before editing it. Copy old_text exactly from the read output.
- If the output says "truncated, use offset=N to continue", call file_read again with that offset.
- The header shows [path, lines X-Y of Z] so you know where you are in the file.
- When the user asks about a file (e.g. "what does this README say?", "what's in this config?"), read it then answer in your own words. Do not paste the raw file content back unless the user explicitly asks to see the file (e.g. "show me the full file", "paste the README").
- For source code, quote only the relevant snippet, not the whole file.`,
			Schema: `{"type":"object","properties":{"path":{"type":"string","description":"Absolute file path"},"offset":{"type":"integer","description":"Line number to start from (1-indexed)"},"limit":{"type":"integer","description":"Max lines to return (default 200)"}},"required":["path"]}`,
		},
		{
			Name: "file_write",
			Description: `Write content to a file, replacing it entirely. Creates parent directories if needed. Requires directory approval.
Guidelines:
- For small changes, prefer file_edit over file_write. It is safer and cheaper.
- Always tell the user the full path of the file you wrote.`,
			Schema: `{"type":"object","properties":{"path":{"type":"string","description":"Absolute file path"},"content":{"type":"string","description":"Full file content"}},"required":["path","content"]}`,
		},
		{
			Name: "file_list",
			Description: `List files and directories at a path. Directories have a trailing /; files show size.
Guidelines:
- Use this to explore project structure before reading or editing files.
- When the user mentions a project without a path, use file_list to find it.`,
			Schema: `{"type":"object","properties":{"path":{"type":"string","description":"Directory path to list (defaults to current directory)"}},"required":[]}`,
		},
		{
			Name: "file_edit",
			Description: `Replace exact text in a file. Supports multiple edits in one call via the edits array.
Guidelines:
- Always file_read the file first, then copy old_text exactly from the output.
- Each old_text must appear exactly once in the file. Include enough context to be unique.
- Keep old_text as small as possible while still being unique.
- When changing multiple locations in one file, use one call with multiple edits[] entries.
- All edits are matched against the original file content, not incrementally.
- If a match fails, read the file again to get the exact current content.`,
			Schema: `{"type":"object","properties":{"path":{"type":"string","description":"Absolute file path"},"old_text":{"type":"string","description":"Exact text to find"},"new_text":{"type":"string","description":"Replacement text"},"edits":{"type":"array","description":"One or more replacements","items":{"type":"object","properties":{"old_text":{"type":"string","description":"Exact text to find (must be unique in file)"},"new_text":{"type":"string","description":"Text to replace it with"}},"required":["old_text","new_text"]}}},"required":["path"]}`,
		},
		{
			Name: "grep",
			Description: `Search file contents for a regex pattern. Returns matching lines with file paths and line numbers.
Guidelines:
- Use this instead of bash grep for all code search. It is faster and returns structured output.
- When the user asks about a symbol, function, or string, use grep to find it before asking for a path.
- Long lines are truncated to 500 chars. Default limit is 100 matches.`,
			Schema: `{"type":"object","properties":{"pattern":{"type":"string","description":"Search pattern (basic regex)"},"path":{"type":"string","description":"Directory or file to search (defaults to current directory)"},"glob":{"type":"string","description":"File name filter, e.g. \"*.go\""},"ignore_case":{"type":"boolean","description":"Case-insensitive search"},"context_lines":{"type":"integer","description":"Lines of context around matches"},"limit":{"type":"integer","description":"Max matches (default 100)"}},"required":["pattern"]}`,
		},
		{
			Name: "find",
			Description: `Find files or directories by glob pattern. Returns relative paths. Excludes .git, node_modules, __pycache__, .venv.
Guidelines:
- To explore a project structure, use file_list instead. Use find to locate specific files.
- When the user mentions a file without a full path, use find to locate it first.
- When looking for a project or directory by name, set type to "d".
- If the user says "in apps/" or similar, resolve the full path (e.g. ~/apps). If no directory is given, search from ~.
- Default limit is 500 results.`,
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

func (r *Registry) ExecutePendingCode(userID string) string {
	r.mu.Lock()
	p := r.pending[userID]
	r.mu.Unlock()
	if p == nil || p.Kind != "code" || p.Code == nil {
		return ""
	}
	r.mu.Lock()
	delete(r.pending, userID)
	r.mu.Unlock()
	return r.executePendingBash(p.Code.Input)
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

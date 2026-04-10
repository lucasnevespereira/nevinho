package tools

import (
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

// FileDisplay holds file content to be sent directly to Discord.
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

func (r *Registry) Execute(name string, input json.RawMessage, userID string) string {
	switch name {
	case "web_read":
		return r.webRead(input)
	case "web_search":
		return r.webSearch(input)
	case "bash":
		return r.runBash(input, userID)
	case "file_read":
		return r.fileRead(input, userID)
	case "file_write":
		return r.fileWrite(input, userID)
	case "file_list":
		return r.fileList(input, userID)
	case "file_edit":
		return r.fileEdit(input, userID)
	case "grep":
		return r.grepSearch(input, userID)
	case "find":
		return r.findFiles(input, userID)
	default:
		return fmt.Sprintf("unknown tool: %s", name)
	}
}

func (r *Registry) Defs() []llm.ToolDef {
	return []llm.ToolDef{
		{
			Name:        "web_read",
			Description: "Fetch a web page and return its readable text content. Strips scripts, styles, nav, and boilerplate. Prefer this over bash curl for reading web content.",
			Schema:      `{"type":"object","properties":{"url":{"type":"string","description":"Full URL to fetch (must be http or https)"}},"required":["url"]}`,
		},
		{
			Name:        "web_search",
			Description: "Search the web and return titles, URLs, and snippets for the top results. Use this to find relevant pages, then web_read to get their full content.",
			Schema:      `{"type":"object","properties":{"query":{"type":"string","description":"Search query"}},"required":["query"]}`,
		},
		{
			Name:        "bash",
			Description: "Run a bash command and return its combined stdout/stderr. Times out after 2 minutes. Commands like rm, sudo, chmod, and curl with output flags require user approval. Non-interactive only — use flags like -y. Prefer file_read/file_write over cat/echo for file operations.",
			Schema:      `{"type":"object","properties":{"command":{"type":"string","description":"Bash command to execute"}},"required":["command"]}`,
		},
		{
			Name:        "file_read",
			Description: "Read a file's contents. Supports absolute paths (/path, ~/path). For large files use offset (1-indexed line number to start from) and limit (number of lines) to paginate. The response header shows which lines are returned and the total.",
			Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"File path"},"offset":{"type":"integer","description":"Line number to start reading from, 1-indexed (optional)"},"limit":{"type":"integer","description":"Maximum number of lines to return (optional)"}},"required":["path"]}`,
		},
		{
			Name:        "file_write",
			Description: "Write content to a file, replacing it entirely. Creates the file and any parent directories if needed. For small changes prefer file_edit — it's safer and cheaper. Use absolute paths. Requires directory approval.",
			Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"File path"},"content":{"type":"string","description":"Full content to write"}},"required":["path","content"]}`,
		},
		{
			Name:        "file_list",
			Description: "List files and directories at a path. Directories have a trailing slash; files show their size. Use absolute paths. Use this to explore project structure before reading files.",
			Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"Directory to list (optional, defaults to workspace root)"}},"required":[]}`,
		},
		{
			Name:        "file_edit",
			Description: "Replace exact text in a file. Supports multiple edits in one call via the edits array. Always file_read the file first, then copy old_text exactly from the output. Each old_text must appear exactly once. Keep old_text as small as possible while still being unique. When changing multiple locations in one file, use one call with multiple edits[] entries.",
			Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"File path"},"old_text":{"type":"string","description":"Exact text to find (legacy, prefer edits[])"},"new_text":{"type":"string","description":"Replacement text (legacy, prefer edits[])"},"edits":{"type":"array","description":"One or more replacements. Each matched against the original file, not incrementally.","items":{"type":"object","properties":{"old_text":{"type":"string","description":"Exact text to find (must be unique in file)"},"new_text":{"type":"string","description":"Text to replace it with"}},"required":["old_text","new_text"]}}},"required":["path"]}`,
		},
		{
			Name:        "grep",
			Description: "Search file contents for a pattern. Returns matching lines with file paths and line numbers. Faster and more precise than bash grep for code search.",
			Schema:      `{"type":"object","properties":{"pattern":{"type":"string","description":"Search pattern (basic regex)"},"path":{"type":"string","description":"Directory or file to search"},"glob":{"type":"string","description":"File name filter, e.g. \"*.go\""},"ignore_case":{"type":"boolean","description":"Case-insensitive search"},"context_lines":{"type":"integer","description":"Lines of context around matches"},"limit":{"type":"integer","description":"Max matches (default 100)"}},"required":["pattern","path"]}`,
		},
		{
			Name:        "find",
			Description: "Find files by name pattern. Returns relative paths. Use to locate files before reading them.",
			Schema:      `{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern, e.g. \"*.go\", \"Makefile\""},"path":{"type":"string","description":"Directory to search in"},"limit":{"type":"integer","description":"Max results (default 500)"}},"required":["pattern","path"]}`,
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

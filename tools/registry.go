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

func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".nevinho"
	}
	return filepath.Join(home, ".config", "nevinho")
}

type Pending struct {
	Kind   string // "path" or "code"
	Detail string
	Code   *pendingCode
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
	permFile string
}

func NewRegistry(cfg *config.Config) *Registry {
	base := configDir()
	r := &Registry{
		cfg:      cfg,
		approved: make(map[string]bool),
		pending:  make(map[string]*Pending),
		permFile: filepath.Join(base, "approved_paths.json"),
	}
	if err := os.MkdirAll(filepath.Join(base, "workspace"), 0755); err != nil {
		log.Printf("failed to create workspace: %v", err)
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
			Description: "Read a file's contents. Supports absolute paths (/path, ~/path) and workspace-relative names. For large files use offset (1-indexed line number to start from) and limit (number of lines) to paginate. The response header shows which lines are returned and the total.",
			Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"File path"},"offset":{"type":"integer","description":"Line number to start reading from, 1-indexed (optional)"},"limit":{"type":"integer","description":"Maximum number of lines to return (optional)"}},"required":["path"]}`,
		},
		{
			Name:        "file_write",
			Description: "Write content to a file, replacing it entirely. Creates the file and any parent directories if needed. For small changes prefer file_edit — it's safer and cheaper. Paths outside the workspace require user approval.",
			Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"File path"},"content":{"type":"string","description":"Full content to write"}},"required":["path","content"]}`,
		},
		{
			Name:        "file_list",
			Description: "List files and directories at a path. Directories have a trailing slash; files show their size. Defaults to the workspace root when no path is given. Use this to explore project structure before reading files.",
			Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"Directory to list (optional, defaults to workspace root)"}},"required":[]}`,
		},
		{
			Name:        "file_edit",
			Description: "Make a targeted edit to an existing file by replacing an exact string. old_text must appear exactly once in the file — if it appears zero or multiple times the edit is rejected. Safer and cheaper than file_write for small changes.",
			Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"File path"},"old_text":{"type":"string","description":"Exact text to find (must appear exactly once in the file)"},"new_text":{"type":"string","description":"Text to replace it with"}},"required":["path","old_text","new_text"]}`,
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

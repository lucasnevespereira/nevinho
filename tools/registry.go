package tools

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/lucasnevespereira/nevinho/llm"
)

const maxResponseLen = 10000 // chars, shared across tools

func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".nevinho"
	}
	return filepath.Join(home, ".config", "nevinho")
}

// Pending represents an action awaiting user approval.
type Pending struct {
	Kind   string // "path" or "code"
	Detail string // display info: resolved dir path, or code preview
	Code   *pendingCode
}

type pendingCode struct {
	Input  json.RawMessage
	UserID string
}

// Registry manages tool execution and permissions.
type Registry struct {
	mu       sync.Mutex
	approved map[string]bool     // approved filesystem paths (persisted)
	pending  map[string]*Pending // userID → what's awaiting approval
	permFile string
}

func NewRegistry() *Registry {
	base := configDir()
	r := &Registry{
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
	case "run_code":
		return r.runCode(input, userID)
	case "file_read":
		return r.fileRead(input, userID)
	case "file_write":
		return r.fileWrite(input, userID)
	default:
		return fmt.Sprintf("unknown tool: %s", name)
	}
}

func (r *Registry) Defs() []llm.ToolDef {
	return []llm.ToolDef{
		{
			Name:        "web_read",
			Description: "Fetch a URL and return the text content.",
			Schema:      `{"type":"object","properties":{"url":{"type":"string","description":"The URL to fetch"}},"required":["url"]}`,
		},
		{
			Name:        "web_search",
			Description: "Search the web and return top results.",
			Schema:      `{"type":"object","properties":{"query":{"type":"string","description":"The search query"}},"required":["query"]}`,
		},
		{
			Name:        "run_code",
			Description: "Execute a code snippet. Supports python3, node, and bash. Requires user approval for each execution.",
			Schema:      `{"type":"object","properties":{"language":{"type":"string","enum":["python3","node","bash"],"description":"Programming language"},"code":{"type":"string","description":"Code to execute"}},"required":["language","code"]}`,
		},
		{
			Name:        "file_read",
			Description: "Read a file. Supports absolute paths (~/path, /path) or workspace-relative names.",
			Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"File path"}},"required":["path"]}`,
		},
		{
			Name:        "file_write",
			Description: "Write to a file. Creates directories automatically. Absolute paths require user permission.",
			Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"File path"},"content":{"type":"string","description":"Content to write"}},"required":["path","content"]}`,
		},
	}
}

// --- permissions ---

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
	// For "code", approval is consumed by re-executing the pending command
	delete(r.pending, userID)
}

// ExecutePendingCode runs the code that was blocked and is now approved.
func (r *Registry) ExecutePendingCode(userID string) string {
	r.mu.Lock()
	p := r.pending[userID]
	r.mu.Unlock()
	if p == nil || p.Kind != "code" || p.Code == nil {
		return ""
	}
	// Mark approved so the re-execution passes the permission check
	r.mu.Lock()
	delete(r.pending, userID)
	r.mu.Unlock()
	return r.executeCode(p.Code.Input)
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

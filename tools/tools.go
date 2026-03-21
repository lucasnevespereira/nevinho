package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	maxFileSize    = 100 * 1024 // 100KB
	maxResponseLen = 10000      // chars
	codeTimeout    = 10 * time.Second
	httpTimeout    = 15 * time.Second
)

func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".nevinho"
	}
	return filepath.Join(home, ".config", "nevinho")
}

type Registry struct {
	approved map[string]bool
	permFile string
}

func NewRegistry() *Registry {
	base := configDir()
	r := &Registry{
		approved: make(map[string]bool),
		permFile: filepath.Join(base, "approved_paths.json"),
	}
	os.MkdirAll(filepath.Join(base, "workspace"), 0755)
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
		return r.runCode(input)
	case "file_read":
		return r.fileRead(input, userID)
	case "file_write":
		return r.fileWrite(input, userID)
	case "approve_path":
		return r.approvePath(input)
	default:
		return fmt.Sprintf("unknown tool: %s", name)
	}
}

// --- web_read ---

type webReadInput struct {
	URL string `json:"url"`
}

func (r *Registry) webRead(input json.RawMessage) string {
	var in webReadInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	if err := validateURL(in.URL); err != nil {
		return fmt.Sprintf("blocked: %v", err)
	}

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(in.URL)
	if err != nil {
		return fmt.Sprintf("failed to fetch: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Sprintf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024))
	if err != nil {
		return fmt.Sprintf("failed to read body: %v", err)
	}

	text := extractText(string(body))
	if len(text) > maxResponseLen {
		text = text[:maxResponseLen] + "\n...(truncated)"
	}

	return text
}

// --- web_search ---

type webSearchInput struct {
	Query string `json:"query"`
}

func (r *Registry) webSearch(input json.RawMessage) string {
	var in webSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	apiKey := os.Getenv("BRAVE_API_KEY")
	if apiKey == "" {
		return "web search not configured (BRAVE_API_KEY not set). Try web_read with a specific URL instead."
	}

	client := &http.Client{Timeout: httpTimeout}
	reqURL := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=5", url.QueryEscape(in.Query))

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return fmt.Sprintf("failed to create request: %v", err)
	}
	req.Header.Set("X-Subscription-Token", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("search failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Sprintf("failed to parse results: %v", err)
	}

	var sb strings.Builder
	for i, r := range result.Web.Results {
		sb.WriteString(fmt.Sprintf("%d. **%s**\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Description))
	}

	if sb.Len() == 0 {
		return "no results found"
	}

	return sb.String()
}

// --- run_code ---

type runCodeInput struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

func (r *Registry) runCode(input json.RawMessage) string {
	var in runCodeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	var cmd *exec.Cmd
	ctx, cancel := context.WithTimeout(context.Background(), codeTimeout)
	defer cancel()

	switch in.Language {
	case "python3":
		cmd = exec.CommandContext(ctx, "python3", "-c", in.Code)
	case "node":
		cmd = exec.CommandContext(ctx, "node", "-e", in.Code)
	case "bash":
		cmd = exec.CommandContext(ctx, "bash", "-c", in.Code)
	default:
		return fmt.Sprintf("unsupported language: %s (use python3, node, or bash)", in.Language)
	}

	output, err := cmd.CombinedOutput()
	result := string(output)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "code execution timed out (10 second limit)"
		}
		result += "\n" + err.Error()
	}

	if len(result) > 5000 {
		result = result[:5000] + "\n...(truncated)"
	}

	if result == "" {
		return "(no output)"
	}

	return result
}

// --- file_read ---

type fileReadInput struct {
	Path string `json:"path"`
}

func (r *Registry) fileRead(input json.RawMessage, userID string) string {
	var in fileReadInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	resolved, err := resolvePath(in.Path, userID)
	if err != nil {
		return fmt.Sprintf("invalid path: %v", err)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("file not found: %s", in.Path)
		}
		return fmt.Sprintf("failed to read: %v", err)
	}

	content := string(data)
	if len(content) > maxResponseLen {
		content = content[:maxResponseLen] + "\n...(truncated)"
	}

	return content
}

// --- file_write ---

type fileWriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (r *Registry) fileWrite(input json.RawMessage, userID string) string {
	var in fileWriteInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	if len(in.Content) > maxFileSize {
		return fmt.Sprintf("content too large (max %dKB)", maxFileSize/1024)
	}

	resolved, err := resolvePath(in.Path, userID)
	if err != nil {
		return fmt.Sprintf("invalid path: %v", err)
	}

	// Check write permissions for paths outside workspace
	if !isWorkspacePath(resolved) {
		if err := r.checkWritePermission(resolved); err != nil {
			return err.Error()
		}
	}

	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Sprintf("failed to create directory: %v", err)
	}

	if err := os.WriteFile(resolved, []byte(in.Content), 0644); err != nil {
		return fmt.Sprintf("failed to write: %v", err)
	}

	return fmt.Sprintf("saved to %s", in.Path)
}

// --- approve_path ---

func (r *Registry) approvePath(input json.RawMessage) string {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	resolved, err := expandHome(in.Path)
	if err != nil {
		return fmt.Sprintf("invalid path: %v", err)
	}

	r.approved[resolved] = true
	r.saveApproved()
	return fmt.Sprintf("Access granted to %s", in.Path)
}

// --- path helpers ---

func resolvePath(path, userID string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		resolved, err := expandHome(path)
		if err != nil {
			return "", err
		}
		return resolved, nil
	}

	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}

	// Relative paths go to workspace
	base := filepath.Join(configDir(), "workspace", userID)
	full := filepath.Join(base, filepath.Clean(path))

	absBase, _ := filepath.Abs(base)
	absFull, _ := filepath.Abs(full)
	if !strings.HasPrefix(absFull, absBase) {
		return "", fmt.Errorf("path traversal blocked")
	}

	return full, nil
}

func expandHome(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot resolve home dir: %w", err)
		}
		return filepath.Join(home, path[2:]), nil
	}
	return filepath.Clean(path), nil
}

func isWorkspacePath(path string) bool {
	abs, _ := filepath.Abs(path)
	wsAbs, _ := filepath.Abs(filepath.Join(configDir(), "workspace"))
	return strings.HasPrefix(abs, wsAbs)
}

func (r *Registry) checkWritePermission(resolved string) error {
	dir := filepath.Dir(resolved)

	// Walk up to check if any ancestor is approved
	check := dir
	for check != "/" && check != "." {
		if r.approved[check] {
			return nil
		}
		check = filepath.Dir(check)
	}

	return fmt.Errorf("Permission needed to write to %s. Ask the user to confirm, then call approve_path with this directory.", shortenHome(dir))
}

func shortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// --- permissions persistence ---

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
	data, _ := json.MarshalIndent(paths, "", "  ")
	os.MkdirAll(filepath.Dir(r.permFile), 0755)
	os.WriteFile(r.permFile, data, 0644)
}

// --- URL validation ---

func validateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL")
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http/https allowed")
	}

	host := u.Hostname()

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve host")
	}

	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return fmt.Errorf("internal addresses not allowed")
		}
	}

	return nil
}

// --- HTML text extraction ---

func extractText(htmlContent string) string {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return htmlContent
	}

	var sb strings.Builder
	var extract func(*html.Node)
	extract = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "nav" || n.Data == "header" || n.Data == "footer") {
			return
		}
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				sb.WriteString(text)
				sb.WriteString(" ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extract(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "br", "h1", "h2", "h3", "h4", "h5", "h6", "li", "tr":
				sb.WriteString("\n")
			}
		}
	}

	extract(doc)
	return strings.TrimSpace(sb.String())
}

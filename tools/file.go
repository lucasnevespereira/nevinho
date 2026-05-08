package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lucasnevespereira/nevinho/safeio"
)

const (
	maxFileSize  = 100 * 1024 // 100KB
	maxReadLines = 200        // max lines returned in one read
)

type fileReadInput struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"` // 1-indexed line to start from
	Limit  int    `json:"limit"`  // max lines to return
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
	totalLines := strings.Count(content, "\n") + 1

	lines := strings.Split(content, "\n")

	start := 0
	if in.Offset > 0 {
		start = in.Offset - 1
	}
	if start >= totalLines {
		return fmt.Sprintf("offset %d exceeds file length (%d lines)", in.Offset, totalLines)
	}

	limit := in.Limit
	if limit <= 0 {
		limit = maxReadLines
	}

	end := start + limit
	if end > totalLines {
		end = totalLines
	}

	chunk := strings.Join(lines[start:end], "\n")

	// Truncate by bytes if still too large
	if len(chunk) > maxResponseLen {
		chunk = chunk[:maxResponseLen]
		if idx := strings.LastIndex(chunk, "\n"); idx != -1 {
			chunk = chunk[:idx]
		}
		end = start + strings.Count(chunk, "\n") + 1
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s, lines %d-%d of %d]\n%s", in.Path, start+1, end, totalLines, chunk)
	if end < totalLines {
		fmt.Fprintf(&sb, "\n[truncated, use offset=%d to continue]", end+1)
	}
	return sb.String()
}

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

	if err := r.checkWritePermission(resolved, userID); err != nil {
		return err.Error()
	}

	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Sprintf("failed to create directory: %v", err)
	}

	if err := safeio.WriteFile(resolved, []byte(in.Content), 0o644); err != nil {
		return fmt.Sprintf("failed to write: %v", err)
	}

	return fmt.Sprintf("saved to %s", in.Path)
}

type fileListInput struct {
	Path string `json:"path"`
}

func (r *Registry) fileList(input json.RawMessage, userID string) string {
	var in fileListInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	if in.Path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "path is required — use an absolute path"
		}
		in.Path = cwd
	}

	dirPath, err := resolvePath(in.Path, userID)
	if err != nil {
		return fmt.Sprintf("invalid path: %v", err)
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Sprintf("failed to list: %v", err)
	}

	if len(entries) == 0 {
		return fmt.Sprintf("%s (empty)", shortenHome(dirPath))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n", shortenHome(dirPath))
	for _, e := range entries {
		if e.IsDir() {
			fmt.Fprintf(&sb, "  %s/\n", e.Name())
		} else {
			info, _ := e.Info()
			if info != nil {
				fmt.Fprintf(&sb, "  %s (%s)\n", e.Name(), formatSize(info.Size()))
			} else {
				fmt.Fprintf(&sb, "  %s\n", e.Name())
			}
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func formatSize(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	}
}

type editPair struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

type fileEditInput struct {
	Path    string     `json:"path"`
	OldText string     `json:"old_text"`
	NewText string     `json:"new_text"`
	Edits   []editPair `json:"edits"`
}

type matchedEdit struct {
	start   int
	end     int
	oldText string
	newText string
}

func (r *Registry) fileEdit(input json.RawMessage, userID string) string {
	var in fileEditInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	// Support both flat old_text/new_text and edits[] array
	edits := in.Edits
	if len(edits) == 0 && in.OldText != "" {
		edits = []editPair{{OldText: in.OldText, NewText: in.NewText}}
	}
	if len(edits) == 0 {
		return "at least one edit is required (use edits[] or old_text/new_text)"
	}

	for i, e := range edits {
		if e.OldText == "" {
			return fmt.Sprintf("edits[%d]: old_text must not be empty", i)
		}
		if e.OldText == e.NewText {
			return fmt.Sprintf("edits[%d]: old_text and new_text are identical — no change needed", i)
		}
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

	if len(data) > maxFileSize {
		return fmt.Sprintf("file too large to edit (%dKB, max %dKB). Use file_write or split the change.", len(data)/1024, maxFileSize/1024)
	}

	raw := string(data)
	content, hasBOM := stripBOM(raw)
	content, hasCRLF := normalizeLineEndings(content)

	var matched []matchedEdit
	for i, e := range edits {
		oldText, _ := normalizeLineEndings(e.OldText)
		newText, _ := normalizeLineEndings(e.NewText)

		idx, count, actualOld := findEditMatch(content, oldText)
		if count == 0 {
			if len(edits) == 1 {
				return fmt.Sprintf("Could not find old_text in %s. The text must match exactly including whitespace and newlines. Use file_read first to see the exact content.", in.Path)
			}
			return fmt.Sprintf("Could not find edits[%d].old_text in %s. The text must match exactly. Use file_read first.", i, in.Path)
		}
		if count > 1 {
			if len(edits) == 1 {
				return fmt.Sprintf("Found %d occurrences of old_text in %s. Include more surrounding context to make it unique.", count, in.Path)
			}
			return fmt.Sprintf("Found %d occurrences of edits[%d].old_text in %s. Include more context to make it unique.", count, i, in.Path)
		}

		matched = append(matched, matchedEdit{
			start:   idx,
			end:     idx + len(actualOld),
			oldText: actualOld,
			newText: newText,
		})
	}

	// Sort by position and check for overlaps
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].start < matched[j].start
	})
	for i := 1; i < len(matched); i++ {
		if matched[i].start < matched[i-1].end {
			return "edits overlap — merge nearby changes into one edit or split into separate file_edit calls"
		}
	}

	if err := r.checkWritePermission(resolved, userID); err != nil {
		return err.Error()
	}

	// Apply edits in reverse order so positions stay valid
	newContent := content
	for i := len(matched) - 1; i >= 0; i-- {
		m := matched[i]
		newContent = newContent[:m.start] + m.newText + newContent[m.end:]
	}

	if hasCRLF {
		newContent = restoreCRLF(newContent)
	}
	if hasBOM {
		newContent = utf8BOM + newContent
	}

	if err := safeio.WriteFile(resolved, []byte(newContent), 0o644); err != nil {
		return fmt.Sprintf("failed to write: %v", err)
	}

	// Build diff display for Discord (diff code block gets colored)
	diff := buildEditDiff(content, matched, 3)
	r.QueueFileDisplay(userID, in.Path, "diff", diff)

	if len(matched) == 1 {
		return fmt.Sprintf("edited %s (1 block replaced)", in.Path)
	}
	return fmt.Sprintf("edited %s (%d blocks replaced)", in.Path, len(matched))
}

const utf8BOM = "\xef\xbb\xbf"

func stripBOM(s string) (string, bool) {
	if strings.HasPrefix(s, utf8BOM) {
		return s[len(utf8BOM):], true
	}
	return s, false
}

func normalizeLineEndings(s string) (string, bool) {
	if strings.Contains(s, "\r\n") {
		return strings.ReplaceAll(s, "\r\n", "\n"), true
	}
	return s, false
}

func restoreCRLF(s string) string {
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// normalizeForFuzzyMatch strips trailing whitespace and converts unicode
// quotes, dashes, and spaces to their ASCII equivalents for matching.
func normalizeForFuzzyMatch(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}
	s = strings.Join(lines, "\n")

	for _, pair := range [][2]string{
		{"\u2018", "'"}, {"\u2019", "'"},
		{"\u201A", "'"}, {"\u201B", "'"},
		{"\u201C", "\""}, {"\u201D", "\""},
		{"\u201E", "\""}, {"\u201F", "\""},
	} {
		s = strings.ReplaceAll(s, pair[0], pair[1])
	}

	for _, dash := range []string{
		"\u2010", "\u2011", "\u2012", "\u2013", "\u2014", "\u2015", "\u2212",
	} {
		s = strings.ReplaceAll(s, dash, "-")
	}

	for _, sp := range []string{
		"\u00A0", "\u2002", "\u2003", "\u2004", "\u2005",
		"\u2006", "\u2007", "\u2008", "\u2009", "\u200A",
		"\u202F", "\u205F", "\u3000",
	} {
		s = strings.ReplaceAll(s, sp, " ")
	}

	// Remove zero-width characters
	for _, zw := range []string{"\u200B", "\u200C", "\u200D", "\uFEFF"} {
		s = strings.ReplaceAll(s, zw, "")
	}

	return s
}

// findEditMatch tries exact match first, falls back to fuzzy.
func findEditMatch(content, oldText string) (int, int, string) {
	count := strings.Count(content, oldText)
	if count == 1 {
		return strings.Index(content, oldText), 1, oldText
	}
	if count > 1 {
		return -1, count, ""
	}
	return fuzzyFind(content, oldText)
}

// fuzzyFind compares normalized lines to handle whitespace/unicode differences.
func fuzzyFind(content, oldText string) (int, int, string) {
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(oldText, "\n")

	if len(oldLines) == 0 {
		return -1, 0, ""
	}

	normContentLines := make([]string, len(contentLines))
	for i, l := range contentLines {
		normContentLines[i] = normalizeForFuzzyMatch(l)
	}
	normOldLines := make([]string, len(oldLines))
	for i, l := range oldLines {
		normOldLines[i] = normalizeForFuzzyMatch(l)
	}

	var matches []int
	for i := 0; i <= len(contentLines)-len(normOldLines); i++ {
		allMatch := true
		for j := 0; j < len(normOldLines); j++ {
			if normContentLines[i+j] != normOldLines[j] {
				allMatch = false
				break
			}
		}
		if allMatch {
			matches = append(matches, i)
		}
	}

	if len(matches) != 1 {
		return -1, len(matches), ""
	}

	matchLineIdx := matches[0]
	origText := strings.Join(contentLines[matchLineIdx:matchLineIdx+len(oldLines)], "\n")

	byteOffset := 0
	for i := 0; i < matchLineIdx; i++ {
		byteOffset += len(contentLines[i]) + 1 // +1 for \n
	}

	return byteOffset, 1, origText
}

func buildEditDiff(content string, edits []matchedEdit, contextN int) string {
	fileLines := strings.Split(content, "\n")
	var sb strings.Builder

	for i, edit := range edits {
		if i > 0 {
			sb.WriteString("\n")
		}

		startLine := lineIndexAt(content, edit.start)
		oldLines := strings.Split(edit.oldText, "\n")
		newLines := strings.Split(edit.newText, "\n")
		endLine := startLine + len(oldLines)

		ctxStart := startLine - contextN
		if ctxStart < 0 {
			ctxStart = 0
		}
		for i := ctxStart; i < startLine; i++ {
			if i < len(fileLines) {
				fmt.Fprintf(&sb, " %4d  %s\n", i+1, fileLines[i])
			}
		}

		for j, line := range oldLines {
			fmt.Fprintf(&sb, "-%4d  %s\n", startLine+j+1, line)
		}

		for _, line := range newLines {
			fmt.Fprintf(&sb, "+      %s\n", line)
		}

		ctxEnd := endLine + contextN
		if ctxEnd > len(fileLines) {
			ctxEnd = len(fileLines)
		}
		for i := endLine; i < ctxEnd; i++ {
			fmt.Fprintf(&sb, " %4d  %s\n", i+1, fileLines[i])
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

func lineIndexAt(content string, byteOffset int) int {
	if byteOffset > len(content) {
		byteOffset = len(content)
	}
	return strings.Count(content[:byteOffset], "\n")
}

func resolvePath(path, _ string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		return expandHome(path)
	}

	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}

	// Resolve relative paths against cwd
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("relative paths are not supported — use an absolute path")
	}
	return filepath.Clean(filepath.Join(cwd, path)), nil
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

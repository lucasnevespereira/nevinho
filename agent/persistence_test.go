package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSummaryRoundtrip(t *testing.T) {
	dir := t.TempDir()
	userID := "123456789"

	if got := loadSummary(dir, userID); got != "" {
		t.Fatalf("expected empty for missing file, got %q", got)
	}

	want := "User was debugging the deploy script. Found a bug in env loading."
	if err := saveSummary(dir, userID, want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got := loadSummary(dir, userID)
	if got != want {
		t.Fatalf("roundtrip mismatch:\nwant: %q\ngot:  %q", want, got)
	}

	if err := deleteSummary(dir, userID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := loadSummary(dir, userID); got != "" {
		t.Fatalf("expected empty after delete, got %q", got)
	}
}

func TestSaveSummaryAtomic(t *testing.T) {
	dir := t.TempDir()
	userID := "123"

	if err := saveSummary(dir, userID, "hello"); err != nil {
		t.Fatalf("save: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, summariesDir))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover .tmp file: %s", e.Name())
		}
	}
}

func TestSaveSummaryEmptyDeletes(t *testing.T) {
	dir := t.TempDir()
	userID := "123"

	if err := saveSummary(dir, userID, "real content"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := saveSummary(dir, userID, "   "); err != nil {
		t.Fatalf("save empty: %v", err)
	}
	if got := loadSummary(dir, userID); got != "" {
		t.Fatalf("empty save should remove file, got %q", got)
	}
}

func TestSaveSummaryTruncates(t *testing.T) {
	dir := t.TempDir()
	userID := "123"

	huge := strings.Repeat("x", summaryMaxSize*2)
	if err := saveSummary(dir, userID, huge); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := loadSummary(dir, userID)
	if len(got) > summaryMaxSize {
		t.Fatalf("expected truncation to %d, got %d", summaryMaxSize, len(got))
	}
}

func TestSanitizeUserIDRejectsTraversal(t *testing.T) {
	bad := []string{
		"",
		"  ",
		"../etc/passwd",
		"foo/bar",
		"foo.bar",
		`foo\bar`,
	}
	for _, id := range bad {
		if got := sanitizeUserID(id); got != "" {
			t.Errorf("expected reject for %q, got %q", id, got)
		}
	}

	good := []string{
		"123456789",
		"abc123",
		"user_42",
	}
	for _, id := range good {
		if got := sanitizeUserID(id); got != id {
			t.Errorf("expected pass for %q, got %q", id, got)
		}
	}
}

func TestDeleteSummaryMissingIsNotError(t *testing.T) {
	dir := t.TempDir()
	if err := deleteSummary(dir, "nonexistent"); err != nil {
		t.Fatalf("delete missing should not error, got: %v", err)
	}
}

func TestSaveSummaryRejectsBadUserID(t *testing.T) {
	dir := t.TempDir()
	if err := saveSummary(dir, "../escape", "content"); err == nil {
		t.Fatal("expected error for path traversal user id")
	}
}

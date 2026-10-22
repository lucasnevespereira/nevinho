package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunnerExecutesConfiguredHooksInOrder(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.log")
	t.Setenv("NEVINHO_HOOK_TEST_LOG", logPath)
	script := filepath.Join(dir, "record.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >> \"$NEVINHO_HOOK_TEST_LOG\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		TurnStarted:   {{script}},
		TurnFinished:  {{script}},
		SessionExited: {{script}},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.Emit(Event{Event: TurnStarted})
	r.Emit(Event{Event: TurnFinished, Outcome: "completed"})
	r.Close()
	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d hook calls: %s", len(lines), got)
	}
	for i, want := range []string{TurnStarted, TurnFinished, SessionExited} {
		var event Event
		if err := json.Unmarshal([]byte(lines[i]), &event); err != nil {
			t.Fatal(err)
		}
		if event.Event != want || event.Timestamp.IsZero() {
			t.Fatalf("event %d = %+v, want %s with timestamp", i, event, want)
		}
		if i == 1 && event.Outcome != "completed" {
			t.Fatalf("turn outcome = %q", event.Outcome)
		}
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"invalid JSON", "{"},
		{"unknown event", `{"other":[["echo"]]}`},
		{"empty command", `{"turn_started":[[]]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if r, err := Load(dir); err == nil {
				r.Close()
				t.Fatal("expected config error")
			}
		})
	}
}

func TestFailedHookDoesNotPreventLaterCommands(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "ran")
	r := New(Config{TurnStarted: {{filepath.Join(dir, "missing")}, {"/bin/sh", "-c", "printf yes > \"$NEVINHO_HOOK_TEST_LOG\""}}})
	t.Setenv("NEVINHO_HOOK_TEST_LOG", logPath)
	r.Emit(Event{Event: TurnStarted})
	r.Close()
	got, err := os.ReadFile(logPath)
	if err != nil || string(got) != "yes" {
		t.Fatalf("later hook did not run: %q, %v", got, err)
	}
}

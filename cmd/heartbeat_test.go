package cmd

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestCrashHeartbeatReportsOnlyUncleanRestart(t *testing.T) {
	dir := t.TempDir()
	var notices []string
	notify := func(msg string) error {
		notices = append(notices, msg)
		return nil
	}

	first, err := startCrashHeartbeat(dir, notify, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	initial, exists, err := readHeartbeat(dir)
	if err != nil || !exists || initial.Clean {
		t.Fatalf("initial heartbeat = %+v, exists=%v, err=%v", initial, exists, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		latest, _, err := readHeartbeat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if latest.LastAlive.After(initial.LastAlive) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat did not advance")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}
	clean, _, err := readHeartbeat(dir)
	if err != nil || !clean.Clean {
		t.Fatalf("clean shutdown marker = %+v, err=%v", clean, err)
	}
	if info, err := os.Stat(heartbeatPath(dir)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("heartbeat file mode: info=%v err=%v", info, err)
	}

	second, err := startCrashHeartbeat(dir, notify, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 0 {
		t.Fatalf("clean restart sent notice: %v", notices)
	}
	if err := second.Stop(); err != nil {
		t.Fatal(err)
	}

	if err := writeHeartbeat(dir, false); err != nil {
		t.Fatal(err)
	}
	third, err := startCrashHeartbeat(dir, notify, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "unclean stop") || !strings.Contains(notices[0], "Last heartbeat:") {
		t.Fatalf("unclean restart notices = %v", notices)
	}
	if err := third.Stop(); err != nil {
		t.Fatal(err)
	}
}

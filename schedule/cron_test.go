package schedule

import (
	"testing"
	"time"
)

func TestParseCronAccepts(t *testing.T) {
	good := []string{
		"0 9 * * *",
		"@daily",
		"@hourly",
		"@every 30m",
		"@every 6h",
		"*/15 * * * *",
	}
	for _, expr := range good {
		if _, err := parseCron(expr); err != nil {
			t.Errorf("parseCron(%q) unexpected error: %v", expr, err)
		}
	}
}

func TestParseCronRejects(t *testing.T) {
	bad := []string{
		"",
		"every monday",
		"@daily extra",
		"61 * * * *",
		"0 25 * * *",
	}
	for _, expr := range bad {
		if _, err := parseCron(expr); err == nil {
			t.Errorf("parseCron(%q) should have errored", expr)
		}
	}
}

func TestMinIntervalDailyIs24h(t *testing.T) {
	s, err := parseCron("@daily")
	if err != nil {
		t.Fatal(err)
	}
	got := minInterval(s)
	if got != 24*time.Hour {
		t.Errorf("minInterval(@daily) = %s, want 24h", got)
	}
}

func TestMinIntervalEvery30m(t *testing.T) {
	s, err := parseCron("@every 30m")
	if err != nil {
		t.Fatal(err)
	}
	got := minInterval(s)
	if got != 30*time.Minute {
		t.Errorf("minInterval(@every 30m) = %s, want 30m", got)
	}
}

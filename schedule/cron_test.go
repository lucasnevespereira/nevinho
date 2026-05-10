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

func TestParseCronInTimezoneAcceptsValidTZ(t *testing.T) {
	for _, tz := range []string{"Europe/Paris", "America/New_York", "Asia/Tokyo", "UTC"} {
		if _, err := parseCronInTimezone("0 9 * * *", tz); err != nil {
			t.Errorf("%s should be valid: %v", tz, err)
		}
	}
}

func TestParseCronInTimezoneRejectsInvalidTZ(t *testing.T) {
	for _, tz := range []string{"Europe/Pariss", "Mars/Olympus_Mons", "not-a-tz"} {
		if _, err := parseCronInTimezone("0 9 * * *", tz); err == nil {
			t.Errorf("%s should be invalid", tz)
		}
	}
}

func TestParseCronInTimezoneFiresInThatZone(t *testing.T) {
	paris, _ := time.LoadLocation("Europe/Paris")
	utc, _ := time.LoadLocation("UTC")

	sched, err := parseCronInTimezone("0 9 * * *", "Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}

	// In summer (CEST = UTC+2), 9am Paris = 7am UTC.
	noonUTC := time.Date(2026, 7, 1, 0, 0, 0, 0, utc)
	next := sched.Next(noonUTC)

	if h := next.UTC().Hour(); h != 7 {
		t.Errorf("next fire UTC hour = %d, want 7 (= 9am Paris CEST)", h)
	}
	if h := next.In(paris).Hour(); h != 9 {
		t.Errorf("next fire Paris hour = %d, want 9", h)
	}
}

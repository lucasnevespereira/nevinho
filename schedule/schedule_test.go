package schedule

import (
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := LoadStore(dir)
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	return s
}

func TestCreateAndList(t *testing.T) {
	s := newTestStore(t)
	got, err := s.Create("morning-hn", "@daily", "summarize HN", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Name != "morning-hn" {
		t.Errorf("name = %q", got.Name)
	}
	if got.NextRun.IsZero() {
		t.Error("NextRun not computed")
	}
	if !got.Enabled {
		t.Error("schedule should default to enabled")
	}
	if len(s.All()) != 1 {
		t.Errorf("All() = %d, want 1", len(s.All()))
	}
}

func TestCreateRejectsDuplicateName(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("daily", "@daily", "x", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("daily", "@hourly", "y", ""); err == nil {
		t.Fatal("expected duplicate-name error")
	}
}

func TestCreateRejectsTooFrequent(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Create("spam", "@every 1m", "x", "")
	if err == nil || !strings.Contains(err.Error(), "minimum") {
		t.Errorf("expected min-interval error, got %v", err)
	}
}

func TestCreateWithTimezone(t *testing.T) {
	s := newTestStore(t)
	got, err := s.Create("paris-morning", "0 9 * * *", "summarize", "Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	if got.Timezone != "Europe/Paris" {
		t.Errorf("Timezone = %q, want Europe/Paris", got.Timezone)
	}

	// Reload to confirm persistence.
	loaded, err := LoadStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	got2, _ := loaded.Find("paris-morning")
	if got2.Timezone != "Europe/Paris" {
		t.Errorf("after reload Timezone = %q, want Europe/Paris", got2.Timezone)
	}
}

func TestCreateRejectsInvalidTimezone(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("bad-tz", "0 9 * * *", "x", "Europe/Pariss"); err == nil {
		t.Error("expected invalid timezone to error")
	}
}

func TestCreateRejectsBadCron(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("bad", "every monday", "x", ""); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestCreateRejectsMissingFields(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("", "@daily", "x", ""); err == nil {
		t.Error("missing name should error")
	}
	if _, err := s.Create("a", "", "x", ""); err == nil {
		t.Error("missing cron should error")
	}
	if _, err := s.Create("a", "@daily", "", ""); err == nil {
		t.Error("missing prompt should error")
	}
}

func TestCreateEnforcesMaxSchedules(t *testing.T) {
	s := newTestStore(t)
	for i := range MaxSchedules {
		name := "s" + string(rune('A'+i))
		if _, err := s.Create(name, "@daily", "x", ""); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	if _, err := s.Create("overflow", "@daily", "x", ""); err == nil {
		t.Error("expected max-schedules error")
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("doomed", "@daily", "x", ""); err != nil {
		t.Fatal(err)
	}
	ok, err := s.Delete("doomed")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("Delete should report removal")
	}
	if len(s.All()) != 0 {
		t.Errorf("All() = %d, want 0", len(s.All()))
	}
}

func TestDeleteMissing(t *testing.T) {
	s := newTestStore(t)
	ok, err := s.Delete("nope")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("Delete on missing should report false")
	}
}

func TestSetEnabledTogglesAndRefreshesNextRun(t *testing.T) {
	s := newTestStore(t)
	created, err := s.Create("toggleable", "@daily", "x", "")
	if err != nil {
		t.Fatal(err)
	}

	disabled, err := s.SetEnabled("toggleable", false)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled {
		t.Error("expected disabled")
	}

	time.Sleep(time.Millisecond)
	enabled, err := s.SetEnabled("toggleable", true)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Enabled {
		t.Error("expected enabled")
	}
	if !enabled.NextRun.After(created.NextRun) && !enabled.NextRun.Equal(created.NextRun) {
		t.Errorf("NextRun should be recomputed on resume, got %s", enabled.NextRun)
	}
}

func TestPersistsAcrossLoads(t *testing.T) {
	dir := t.TempDir()
	s1, err := LoadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.Create("persist-me", "@daily", "x", ""); err != nil {
		t.Fatal(err)
	}

	s2, err := LoadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.All()
	if len(got) != 1 || got[0].Name != "persist-me" {
		t.Errorf("after reload: %+v", got)
	}
}

func TestDueSchedulesSkipsMissedRunsAfterDowntime(t *testing.T) {
	s := newTestStore(t)
	created, err := s.Create("hourly", "@every 1h", "x", "")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate nevinho being offline for several hours.
	staleAt := time.Now().Add(-5 * time.Hour)
	s.mu.Lock()
	for i := range s.list {
		if s.list[i].ID == created.ID {
			s.list[i].NextRun = staleAt
		}
	}
	s.mu.Unlock()

	now := time.Now()
	due := s.dueSchedules(now)
	if len(due) != 0 {
		t.Errorf("expected missed runs to be skipped, got %d due", len(due))
	}

	got, _ := s.Find("hourly")
	if !got.NextRun.After(now) {
		t.Errorf("NextRun should be fast forwarded to future, got %s (now=%s)", got.NextRun, now)
	}
}

func TestRecordRunAppendsAndCaps(t *testing.T) {
	s := newTestStore(t)
	created, err := s.Create("trace", "@daily", "x", "")
	if err != nil {
		t.Fatal(err)
	}

	for i := range MaxRunsKept + 5 {
		log := RunLog{
			StartedAt: time.Now().Add(time.Duration(i) * time.Minute),
			Duration:  time.Second,
			Success:   true,
			Preview:   "run " + string(rune('a'+i)),
		}
		if err := s.recordRun(created.ID, log); err != nil {
			t.Fatal(err)
		}
	}

	got, _ := s.Find("trace")
	if len(got.Runs) != MaxRunsKept {
		t.Errorf("Runs len = %d, want %d", len(got.Runs), MaxRunsKept)
	}
	expectNewest := "run " + string(rune('a'+MaxRunsKept+4))
	if got.Runs[0].Preview != expectNewest {
		t.Errorf("expected newest first %q, got %q", expectNewest, got.Runs[0].Preview)
	}
}

func TestRecordRunUpdatesNextAndLast(t *testing.T) {
	s := newTestStore(t)
	created, err := s.Create("ticker", "@every 1h", "x", "")
	if err != nil {
		t.Fatal(err)
	}

	ranAt := created.NextRun.Add(2 * time.Minute)

	if err := s.recordRun(created.ID, RunLog{
		StartedAt: ranAt,
		Duration:  500 * time.Millisecond,
		Success:   true,
	}); err != nil {
		t.Fatal(err)
	}

	got, _ := s.Find("ticker")
	if !got.LastRun.Equal(ranAt) {
		t.Errorf("LastRun = %s, want %s", got.LastRun, ranAt)
	}
	wantNext := ranAt.Add(time.Hour)
	if !got.NextRun.Equal(wantNext) {
		t.Errorf("NextRun = %s, want %s (ranAt + 1h)", got.NextRun, wantNext)
	}
}

func TestPrependCappedKeepsNewestFirst(t *testing.T) {
	older := []RunLog{{Preview: "b"}, {Preview: "c"}}
	got := prependCapped(older, RunLog{Preview: "a"}, 3)

	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Preview != w {
			t.Errorf("got[%d] = %q, want %q", i, got[i].Preview, w)
		}
	}
}

func TestPrependCappedTrims(t *testing.T) {
	older := []RunLog{{Preview: "b"}, {Preview: "c"}, {Preview: "d"}}
	got := prependCapped(older, RunLog{Preview: "a"}, 2)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Preview != "a" || got[1].Preview != "b" {
		t.Errorf("got = %+v, want [a b]", got)
	}
}

func TestDueSchedulesReturnsOnlyEnabledAndPastDue(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.Create("a", "@daily", "x", "")
	_, _ = s.Create("b", "@daily", "x", "")
	_, _ = s.SetEnabled("b", false)

	// Force a's NextRun to the past.
	s.mu.Lock()
	for i := range s.list {
		if s.list[i].ID == a.ID {
			s.list[i].NextRun = time.Now().Add(-1 * time.Minute)
		}
	}
	s.mu.Unlock()

	due := s.dueSchedules(time.Now())
	if len(due) != 1 || due[0].Name != "a" {
		t.Errorf("due = %+v, want only [a]", due)
	}
}

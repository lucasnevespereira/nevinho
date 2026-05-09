// Package schedule runs prompts on a cron timetable. Schedules live in an
// encrypted store, the runner ticks once a minute and fires anything due.
// Designed for a 24/7 VPS bot, single user.
package schedule

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/lucasnevespereira/nevinho/crypto"
	"github.com/lucasnevespereira/nevinho/safeio"
)

// readEncrypted reads and decrypts a file. Returns nil data when the file
// does not exist (treated as empty store on first run).
func readEncrypted(path string, key [32]byte) ([]byte, error) {
	enc, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read schedules: %w", err)
	}
	plain, err := crypto.Decrypt(key, enc)
	if err != nil {
		return nil, fmt.Errorf("decrypt schedules: %w", err)
	}
	return plain, nil
}

const (
	// MaxSchedules caps the store. A personal bot does not need more, and
	// the cap keeps the per-tick scan cheap.
	MaxSchedules = 10

	// MinInterval rejects schedules that fire more often than this. Lower
	// intervals turn nevinho into a polling loop and risk hitting LLM rate
	// limits. Five minutes leaves room for any reasonable use case.
	MinInterval = 5 * time.Minute

	// RunTimeout caps a single run. Scheduled runs do not have an
	// interactive cancel path, so the timeout is the safety net.
	RunTimeout = 5 * time.Minute

	storeFile = "schedules.enc"
)

// Schedule is a saved prompt that runs on a cron timetable.
type Schedule struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Cron    string    `json:"cron"`
	Prompt  string    `json:"prompt"`
	Enabled bool      `json:"enabled"`
	LastRun time.Time `json:"last_run,omitempty"`
	NextRun time.Time `json:"next_run,omitempty"`
	Created time.Time `json:"created"`
}

// Store loads, mutates, and persists schedules atomically. Safe for
// concurrent reads, serialized for writes.
type Store struct {
	dir  string
	key  [32]byte
	mu   sync.RWMutex
	list []Schedule
}

// LoadStore opens (or creates) the encrypted schedule store at
// configDir/schedules.enc. A missing file is treated as an empty store.
func LoadStore(configDir string) (*Store, error) {
	key, err := crypto.LoadOrCreateKey(configDir)
	if err != nil {
		return nil, fmt.Errorf("schedule key: %w", err)
	}
	s := &Store{dir: configDir, key: key}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) path() string {
	return filepath.Join(s.dir, storeFile)
}

func (s *Store) load() error {
	data, err := readEncrypted(s.path(), s.key)
	if err != nil {
		return err
	}
	if data == nil {
		return nil
	}
	var list []Schedule
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("decode schedules: %w", err)
	}
	s.list = list
	return nil
}

func (s *Store) save() error {
	plain, err := json.MarshalIndent(s.list, "", "  ")
	if err != nil {
		return err
	}
	enc, err := crypto.Encrypt(s.key, plain)
	if err != nil {
		return err
	}
	return safeio.WriteFile(s.path(), enc, 0o600)
}

// All returns a copy of every schedule. Safe for callers to mutate.
func (s *Store) All() []Schedule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Schedule, len(s.list))
	copy(out, s.list)
	return out
}

// Find returns the schedule whose name or id matches the given key.
func (s *Store) Find(key string) (Schedule, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sc := range s.list {
		if sc.Name == key || sc.ID == key {
			return sc, true
		}
	}
	return Schedule{}, false
}

// Create validates and persists a new schedule. Names must be unique.
// The cron expression is parsed and the first NextRun is computed before
// storing so the runner does not need to revalidate on every tick.
func (s *Store) Create(name, cronExpr, prompt string) (Schedule, error) {
	name = trim(name)
	cronExpr = trim(cronExpr)
	prompt = trim(prompt)

	if name == "" || cronExpr == "" || prompt == "" {
		return Schedule{}, fmt.Errorf("name, cron, and prompt are required")
	}

	sched, err := parseCron(cronExpr)
	if err != nil {
		return Schedule{}, fmt.Errorf("invalid cron %q: %w", cronExpr, err)
	}
	if interval := minInterval(sched); interval < MinInterval {
		return Schedule{}, fmt.Errorf("schedule fires every %s, minimum is %s", interval.Truncate(time.Second), MinInterval)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.list) >= MaxSchedules {
		return Schedule{}, fmt.Errorf("schedule limit reached (max %d). Delete one first.", MaxSchedules)
	}
	for _, sc := range s.list {
		if sc.Name == name {
			return Schedule{}, fmt.Errorf("schedule %q already exists", name)
		}
	}

	now := time.Now()
	entry := Schedule{
		ID:      newID(),
		Name:    name,
		Cron:    cronExpr,
		Prompt:  prompt,
		Enabled: true,
		NextRun: sched.Next(now),
		Created: now,
	}
	s.list = append(s.list, entry)
	if err := s.save(); err != nil {
		s.list = s.list[:len(s.list)-1]
		return Schedule{}, err
	}
	return entry, nil
}

// Delete removes a schedule by name or id. Returns false if not found.
func (s *Store) Delete(key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sc := range s.list {
		if sc.Name == key || sc.ID == key {
			s.list = append(s.list[:i], s.list[i+1:]...)
			return true, s.save()
		}
	}
	return false, nil
}

// SetEnabled toggles a schedule on or off without removing it. Disabled
// schedules are skipped by the runner but keep their NextRun fresh on
// each successful re-enable.
func (s *Store) SetEnabled(key string, enabled bool) (Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sc := range s.list {
		if sc.Name != key && sc.ID != key {
			continue
		}
		sc.Enabled = enabled
		if enabled {
			parsed, err := parseCron(sc.Cron)
			if err != nil {
				return Schedule{}, err
			}
			sc.NextRun = parsed.Next(time.Now())
		}
		s.list[i] = sc
		return sc, s.save()
	}
	return Schedule{}, fmt.Errorf("schedule %q not found", key)
}

// recordRun updates LastRun and NextRun after a run completes. Caller is
// expected to hold no locks (this acquires the write lock internally).
func (s *Store) recordRun(id string, ranAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sc := range s.list {
		if sc.ID != id {
			continue
		}
		parsed, err := parseCron(sc.Cron)
		if err != nil {
			return err
		}
		sc.LastRun = ranAt
		sc.NextRun = parsed.Next(ranAt)
		s.list[i] = sc
		return s.save()
	}
	return nil
}

// dueSchedules returns enabled schedules whose NextRun is at or before
// now. Sorted by NextRun ascending so older-due ones run first when
// multiple are batched together.
//
// Schedules whose NextRun is more than one cron interval in the past
// (i.e. nevinho was offline across at least one fire window) are
// skipped and their NextRun is fast forwarded to the next future fire.
// This implements the "no missed-run catch-up" policy: a scheduled
// prompt runs at most once per intended fire, never replays.
func (s *Store) dueSchedules(now time.Time) []Schedule {
	s.mu.Lock()
	defer s.mu.Unlock()

	var due []Schedule
	dirty := false
	for i, sc := range s.list {
		if !sc.Enabled || sc.NextRun.IsZero() {
			continue
		}
		if sc.NextRun.After(now) {
			continue
		}
		parsed, err := parseCron(sc.Cron)
		if err != nil {
			continue
		}
		next := parsed.Next(sc.NextRun)
		if next.Before(now) {
			// Multiple fire windows missed while nevinho was offline.
			// Skip this run and fast forward to the next future fire.
			s.list[i].NextRun = parsed.Next(now)
			dirty = true
			continue
		}
		due = append(due, sc)
	}
	if dirty {
		_ = s.save()
	}
	sort.Slice(due, func(i, j int) bool {
		return due[i].NextRun.Before(due[j].NextRun)
	})
	return due
}

package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/safeio"
)

// TerminalSession describes a checkpoint, never executable approval state.
// Its opaque ID is also the agent's conversation ID. Titles stay inside the
// encrypted checkpoint; no plaintext index of conversation content is needed.
type TerminalSession struct {
	ID            string
	Title         string
	Model         string
	UpdatedAt     time.Time
	ImagesOmitted bool
}

func sessionTitle(msgs []llm.Message) string {
	for _, msg := range msgs {
		if msg.Role != llm.RoleUser || strings.TrimSpace(msg.Text) == "" {
			continue
		}
		if strings.HasPrefix(msg.Text, "[Previous conversation:") || strings.HasPrefix(msg.Text, "[Conversation so far:") {
			return "Previous conversation"
		}
		text := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, msg.Text)
		runes := []rune(strings.Join(strings.Fields(text), " "))
		if len(runes) > 80 {
			return string(runes[:80]) + "…"
		}
		return string(runes)
	}
	return ""
}

func validateTerminalScope(scope string) error {
	return validateSessionScope(scope, "terminal-")
}

func validateSessionScope(scope, prefix string) error {
	if !strings.HasPrefix(scope, prefix) || len(scope) != len(prefix)+16 {
		return fmt.Errorf("invalid terminal session scope")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(scope, prefix)); err != nil {
		return fmt.Errorf("invalid terminal session scope")
	}
	return nil
}

func validSessionID(scope, id string) bool {
	prefix := scope + "-session-"
	if !strings.HasPrefix(id, prefix) || len(id) != len(prefix)+16 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(id, prefix))
	return err == nil
}

func (a *Agent) activeSessionPath(scope string) string {
	return filepath.Join(a.cfg.Dir(), summariesDir, scope+".active")
}

func (a *Agent) activateTerminalSession(scope, id string) error {
	return safeio.WriteFile(a.activeSessionPath(scope), []byte(id+"\n"), 0o600)
}

func terminalSession(id string, cp conversationCheckpoint) TerminalSession {
	title := cp.Title
	if title == "" {
		title = sessionTitle(cp.Messages)
	}
	if title == "" {
		title = "New conversation"
	}
	return TerminalSession{ID: id, Title: title, Model: cp.Model, UpdatedAt: cp.UpdatedAt, ImagesOmitted: cp.ImagesOmitted}
}

// TerminalSessions lists only this working directory's encrypted checkpoints.
func (a *Agent) TerminalSessions(scope string) ([]TerminalSession, error) {
	if err := validateTerminalScope(scope); err != nil {
		return nil, err
	}
	return a.sessions(scope)
}

func (a *Agent) sessions(scope string) ([]TerminalSession, error) {
	if !a.cfg.ElephantEnabled() {
		return nil, fmt.Errorf("session persistence is off; enable it with /config ELEPHANT on")
	}
	entries, err := os.ReadDir(filepath.Join(a.cfg.Dir(), summariesDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sessions []TerminalSession
	unreadable := 0
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".history.enc")
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".history.enc") || !validSessionID(scope, id) {
			continue
		}
		cp, err := a.readCheckpoint(id)
		if err != nil || cp.Model == "" {
			unreadable++
			continue
		}
		sessions = append(sessions, terminalSession(id, cp))
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	if unreadable > 0 {
		return sessions, fmt.Errorf("%d saved session(s) unreadable; excluded from picker, checkpoints left untouched", unreadable)
	}
	return sessions, nil
}

// SaveTerminalSession checkpoints completed in-memory work before switching.
// An unloaded session is left untouched. Call only while the terminal is idle.
func (a *Agent) SaveTerminalSession(id string) error {
	if !a.cfg.ElephantEnabled() {
		return nil
	}
	lock := a.getUserLock(id)
	lock.Lock()
	defer lock.Unlock()
	if len(a.messages(id)) == 0 {
		return nil
	}
	return a.saveCheckpoint(id)
}

// UnloadTerminalSession releases idle context without deleting its checkpoint.
func (a *Agent) UnloadTerminalSession(id string) {
	lock := a.getUserLock(id)
	lock.Lock()
	defer lock.Unlock()
	a.ClearPending(id)
	a.mu.Lock()
	delete(a.history, id)
	delete(a.pendingToolID, id)
	a.mu.Unlock()
}

// NewTerminalSession starts a separate empty conversation. With persistence
// disabled it creates only an in-memory identity, without writing any files.
func (a *Agent) NewTerminalSession(scope string) (TerminalSession, error) {
	if err := validateTerminalScope(scope); err != nil {
		return TerminalSession{}, err
	}
	return a.createTerminalSession(scope, conversationCheckpoint{Model: a.Model(), UpdatedAt: time.Now().UTC()})
}

func (a *Agent) createTerminalSession(scope string, cp conversationCheckpoint) (TerminalSession, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return TerminalSession{}, err
	}
	id := scope + "-session-" + hex.EncodeToString(random[:])
	if a.cfg.ElephantEnabled() {
		path, _ := checkpointPath(a.cfg.Dir(), id)
		if err := a.writeCheckpoint(path, cp); err != nil {
			return TerminalSession{}, err
		}
		if err := a.activateTerminalSession(scope, id); err != nil {
			return TerminalSession{}, err
		}
	}
	return terminalSession(id, cp), nil
}

// OpenTerminalSession restores context, never tools, approvals, or image bytes.
// Empty id means the active session on launch. Legacy single checkpoints are
// copied once into session storage; the original remains until /forget.
func (a *Agent) OpenTerminalSession(scope, id string) (TerminalSession, error) {
	if err := validateTerminalScope(scope); err != nil {
		return TerminalSession{}, err
	}
	return a.openSession(scope, id, scope)
}

func (a *Agent) openSession(scope, id, legacyID string) (TerminalSession, error) {
	if !a.cfg.ElephantEnabled() {
		return TerminalSession{}, fmt.Errorf("session persistence is off; enable it with /config ELEPHANT on")
	}
	if id == "" {
		active, err := os.ReadFile(a.activeSessionPath(scope))
		if err == nil {
			id = strings.TrimSpace(string(active))
		} else if !os.IsNotExist(err) {
			return TerminalSession{}, err
		} else {
			return a.migrateSession(scope, legacyID)
		}
	}
	if !validSessionID(scope, id) {
		return TerminalSession{}, fmt.Errorf("session does not belong to this working directory")
	}
	cp, err := a.readCheckpoint(id)
	if err != nil {
		return TerminalSession{}, err
	}
	if cp.Model == "" {
		return TerminalSession{}, fmt.Errorf("saved session is missing; use /new to start fresh")
	}
	if cp.Model != a.Model() {
		if err := a.SwitchModel(cp.Model); err != nil {
			return TerminalSession{}, fmt.Errorf("restore session model %s: %w", cp.Model, err)
		}
	}
	if err := a.activateTerminalSession(scope, id); err != nil {
		return TerminalSession{}, err
	}
	lock := a.getUserLock(id)
	lock.Lock()
	defer lock.Unlock()
	a.ClearPending(id)
	a.mu.Lock()
	delete(a.pendingToolID, id)
	a.mu.Unlock()
	a.setMessages(id, cp.Messages)
	return terminalSession(id, cp), nil
}

func (a *Agent) migrateSession(scope, legacyID string) (TerminalSession, error) {
	cp, err := a.readCheckpoint(legacyID)
	if err != nil {
		return TerminalSession{}, err
	}
	if cp.Model == "" {
		if summary := loadSummary(a.cfg.Dir(), legacyID); summary != "" {
			cp = conversationCheckpoint{Model: a.Model(), Messages: []llm.Message{llm.UserMessage("[Previous conversation: "+summary+"]", nil)}}
		}
	}
	// Resolve the saved model before creating the active pointer. A missing
	// provider must not silently turn an existing conversation into a blank one.
	if cp.Model != "" && cp.Model != a.Model() {
		if err := a.SwitchModel(cp.Model); err != nil {
			return TerminalSession{}, fmt.Errorf("restore legacy session model: %w", err)
		}
	}
	if cp.Model == "" {
		return a.createTerminalSession(scope, conversationCheckpoint{Model: a.Model(), UpdatedAt: time.Now().UTC()})
	}
	cp.Title = sessionTitle(cp.Messages)
	if cp.UpdatedAt.IsZero() {
		cp.UpdatedAt = time.Now().UTC()
	}
	session, err := a.createTerminalSession(scope, cp)
	if err != nil {
		return TerminalSession{}, err
	}
	a.setMessages(session.ID, cp.Messages)
	return session, nil
}

// DiscordScope separates an owner's saved conversations from terminal projects.
// The hash keeps the Discord snowflake out of checkpoint filenames.
func DiscordScope(ownerID string) string {
	sum := sha256.Sum256([]byte(ownerID))
	return "discord-" + hex.EncodeToString(sum[:8])
}

func (a *Agent) OpenDiscordSession(ownerID, id string) (TerminalSession, error) {
	scope := DiscordScope(ownerID)
	return a.openSession(scope, id, ownerID)
}

func (a *Agent) NewDiscordSession(ownerID string) (TerminalSession, error) {
	return a.createTerminalSession(DiscordScope(ownerID), conversationCheckpoint{Model: a.Model(), UpdatedAt: time.Now().UTC()})
}

func (a *Agent) DiscordSessions(ownerID string) ([]TerminalSession, error) {
	return a.sessions(DiscordScope(ownerID))
}

func (a *Agent) ForgetDiscordSession(ownerID, id string) error {
	scope := DiscordScope(ownerID)
	if !validSessionID(scope, id) {
		return fmt.Errorf("session does not belong to this Discord owner")
	}
	if err := deleteCheckpoint(a.cfg.Dir(), id); err != nil {
		return err
	}
	a.ClearPending(id)
	a.ClearHistory(id)
	// Remove the old single-conversation fallback so it cannot reappear.
	if err := deleteCheckpoint(a.cfg.Dir(), ownerID); err != nil {
		return err
	}
	return deleteSummary(a.cfg.Dir(), ownerID)
}

// ForgetTerminalSession also removes the old single-conversation fallback so
// deleted legacy content cannot be migrated back after a future restart.
func (a *Agent) ForgetTerminalSession(scope, id string) error {
	if err := validateTerminalScope(scope); err != nil {
		return err
	}
	if !validSessionID(scope, id) {
		return fmt.Errorf("session does not belong to this working directory")
	}
	// Report deletion failures, instead of only logging them in ClearHistory.
	if err := deleteCheckpoint(a.cfg.Dir(), id); err != nil {
		return err
	}
	if err := deleteSummary(a.cfg.Dir(), id); err != nil {
		return err
	}
	a.ClearPending(id)
	a.ClearHistory(id)
	if err := deleteCheckpoint(a.cfg.Dir(), scope); err != nil {
		return err
	}
	return deleteSummary(a.cfg.Dir(), scope)
}

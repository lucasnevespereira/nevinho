package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucasnevespereira/nevinho/crypto"
	"github.com/lucasnevespereira/nevinho/llm"
	"github.com/lucasnevespereira/nevinho/safeio"
)

const (
	summariesDir   = "summaries"
	summaryMaxSize = 4000 // ~1000 tokens
)

type conversationCheckpoint struct {
	Model         string        `json:"model"`
	Messages      []llm.Message `json:"messages"`
	Title         string        `json:"title,omitempty"`
	UpdatedAt     time.Time     `json:"updated_at,omitempty"`
	ImagesOmitted bool          `json:"images_omitted,omitempty"`
}

func checkpointPath(configDir, userID string) (string, error) {
	path, err := summaryPath(configDir, userID)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, ".md") + ".history.enc", nil
}

// saveCheckpoint stores a completed turn without another model request. Image
// bytes are omitted: the model's text reply remains, while the binary input
// would make a small conversation checkpoint unexpectedly huge.
func (a *Agent) saveCheckpoint(userID string) error {
	path, err := checkpointPath(a.cfg.Dir(), userID)
	if err != nil {
		return err
	}
	msgs := a.messages(userID)
	copyMsgs := make([]llm.Message, len(msgs))
	copy(copyMsgs, msgs)
	previous, err := a.readCheckpoint(userID)
	if err != nil {
		return err // Never overwrite an unreadable checkpoint.
	}
	for i := range copyMsgs {
		previous.ImagesOmitted = previous.ImagesOmitted || len(copyMsgs[i].Images) > 0
		copyMsgs[i].Images = nil
	}
	if previous.Title == "" {
		previous.Title = sessionTitle(copyMsgs)
	}
	previous.Model = a.llm.Model()
	previous.Messages = copyMsgs
	previous.UpdatedAt = time.Now().UTC()
	if err := a.writeCheckpoint(path, previous); err != nil {
		return err
	}
	// A session created while persistence was disabled becomes durable when
	// the user enables it and completes a turn. Make it the launch default too.
	if scope, _, ok := strings.Cut(userID, "-session-"); ok && validateTerminalScope(scope) == nil && validSessionID(scope, userID) {
		return a.activateTerminalSession(scope, userID)
	}
	return nil
}

func (a *Agent) writeCheckpoint(path string, checkpoint conversationCheckpoint) error {
	plain, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	key, err := crypto.LoadOrCreateKey(a.cfg.Dir())
	if err != nil {
		return fmt.Errorf("checkpoint key: %w", err)
	}
	enc, err := crypto.Encrypt(key, plain)
	if err != nil {
		return fmt.Errorf("encrypt checkpoint: %w", err)
	}
	return safeio.WriteFile(path, enc, 0o600)
}

func (a *Agent) readCheckpoint(userID string) (conversationCheckpoint, error) {
	var checkpoint conversationCheckpoint
	path, err := checkpointPath(a.cfg.Dir(), userID)
	if err != nil {
		return checkpoint, err
	}
	enc, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return checkpoint, nil
	}
	if err != nil {
		return checkpoint, err
	}
	key, err := crypto.LoadOrCreateKey(a.cfg.Dir())
	if err != nil {
		return checkpoint, err
	}
	plain, err := crypto.Decrypt(key, enc)
	if err != nil {
		return checkpoint, fmt.Errorf("decrypt checkpoint: %w", err)
	}
	if err := json.Unmarshal(plain, &checkpoint); err != nil {
		return checkpoint, fmt.Errorf("decode checkpoint: %w", err)
	}
	return checkpoint, nil
}

func (a *Agent) loadCheckpoint(userID string) ([]llm.Message, error) {
	checkpoint, err := a.readCheckpoint(userID)
	if err != nil {
		return nil, err
	}
	if checkpoint.Model != a.llm.Model() {
		return nil, nil
	}
	return checkpoint.Messages, nil
}

func deleteCheckpoint(configDir, userID string) error {
	path, err := checkpointPath(configDir, userID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// summaryPath returns the on-disk location for a user's persisted summary.
// The userID is sanitized to prevent path traversal even though Discord IDs
// are numeric snowflakes. We don't trust callers.
func summaryPath(configDir, userID string) (string, error) {
	clean := sanitizeUserID(userID)
	if clean == "" {
		return "", fmt.Errorf("invalid user id")
	}
	return filepath.Join(configDir, summariesDir, clean+".md"), nil
}

func sanitizeUserID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if strings.ContainsAny(id, `/\.`) {
		return ""
	}
	return filepath.Base(id)
}

// loadSummary reads a persisted summary for the user. Returns "" if missing.
func loadSummary(configDir, userID string) string {
	path, err := summaryPath(configDir, userID)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// saveSummary writes the summary atomically through safeio.WriteFile so a
// crash mid write cannot leave a half written file.
func saveSummary(configDir, userID, text string) error {
	path, err := summaryPath(configDir, userID)
	if err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return deleteSummary(configDir, userID)
	}
	if len(text) > summaryMaxSize {
		text = text[:summaryMaxSize]
	}
	if err := safeio.WriteFile(path, []byte(text+"\n"), 0o644); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}

// deleteSummary removes the persisted summary. Missing file is not an error.
func deleteSummary(configDir, userID string) error {
	path, err := summaryPath(configDir, userID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

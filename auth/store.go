package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/lucasnevespereira/nevinho/crypto"
)

// Service identifies an external service.
type Service string

const (
	GitHub Service = "github"
	Google Service = "google"
)

// Token holds OAuth credentials for a connected service.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Username     string `json:"username,omitempty"`
	Email        string `json:"email,omitempty"`
}

// DeviceCode holds the response from a device flow initiation.
type DeviceCode struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	Interval        int // poll interval in seconds
	ExpiresIn       int // seconds until code expires
}

// Store manages encrypted credential storage on disk.
// Tokens are encrypted with AES-256-GCM and persisted so users
// only need to connect a service once.
type Store struct {
	mu       sync.Mutex
	key      [32]byte
	filePath string
	data     map[string]map[Service]*Token // userID → service → token
}

// NewStore creates a credential store, loading existing tokens from disk.
func NewStore(configDir string) (*Store, error) {
	key, err := crypto.LoadOrCreateKey(configDir)
	if err != nil {
		return nil, fmt.Errorf("auth key: %w", err)
	}

	s := &Store{
		key:      key,
		filePath: filepath.Join(configDir, "credentials.enc"),
		data:     make(map[string]map[Service]*Token),
	}
	_ = s.load() // fresh install has no file
	return s, nil
}

// Get returns the token for a user+service, or nil if not connected.
func (s *Store) Get(userID string, svc Service) *Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.data[userID]; ok {
		return m[svc]
	}
	return nil
}

// Set stores a token and persists to disk.
func (s *Store) Set(userID string, svc Service, tok *Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data[userID] == nil {
		s.data[userID] = make(map[Service]*Token)
	}
	s.data[userID][svc] = tok
	return s.save()
}

// Delete removes a token and persists to disk.
func (s *Store) Delete(userID string, svc Service) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.data[userID]; ok {
		delete(m, svc)
		if len(m) == 0 {
			delete(s.data, userID)
		}
	}
	return s.save()
}

// Connected returns all services a user has tokens for.
func (s *Store) Connected(userID string) map[Service]*Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[Service]*Token)
	for svc, tok := range s.data[userID] {
		out[svc] = tok
	}
	return out
}

func (s *Store) load() error {
	enc, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	plain, err := crypto.Decrypt(s.key, enc)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, &s.data)
}

func (s *Store) save() error {
	plain, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	enc, err := crypto.Encrypt(s.key, plain)
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, enc, 0600)
}

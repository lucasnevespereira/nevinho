// Package chatgpt manages Sign in with ChatGPT credentials for Nevinho's
// open-source, self-hosted installation. Tokens never enter config.enc or logs.
package chatgpt

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lucasnevespereira/nevinho/safeio"
)

const (
	authBase = "https://auth.openai.com"
	apiBase  = "https://api.openai.com/v1"
)

type Model struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Visibility  string `json:"visibility"`
}

type account struct {
	ClientID     string    `json:"client_id"`
	Subject      string    `json:"subject"`
	Email        string    `json:"email"`
	IDToken      string    `json:"id_token"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Scopes       string    `json:"scopes"`
	ExpiresAt    time.Time `json:"expires_at"`
	Models       []Model   `json:"models"`
}

type state struct {
	HostID   string             `json:"host_id"`
	Active   string             `json:"active"`
	Accounts map[string]account `json:"accounts"`
}

type Store struct {
	dir    string
	client *http.Client
	base   string
	api    string
}

func NewStore(dir string) *Store {
	return &Store{dir: dir, client: &http.Client{Timeout: 15 * time.Second}, base: authBase, api: apiBase}
}

func (s *Store) path() string { return filepath.Join(s.dir, "chatgpt-auth.json") }

func (s *Store) withLock(fn func(*state) error) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.path()+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	st, err := s.read()
	if err != nil {
		return err
	}
	return fn(&st)
}

func (s *Store) read() (state, error) {
	st := state{Accounts: make(map[string]account)}
	data, err := os.ReadFile(s.path())
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("read ChatGPT credentials: %w", err)
	}
	if st.Accounts == nil {
		st.Accounts = make(map[string]account)
	}
	return st, nil
}

func (s *Store) save(st state) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	// safeio preserves an existing file's mode. Fix that mode before its
	// atomic replacement, so credentials are never installed broadly readable.
	if _, err := os.Stat(s.path()); err == nil {
		if err := os.Chmod(s.path(), 0o600); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return safeio.WriteFile(s.path(), data, 0o600)
}

func randomValue(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64URL(b), nil
}

func (s *Store) hostID() (string, error) {
	var host string
	err := s.withLock(func(st *state) error {
		if st.HostID == "" {
			b := make([]byte, 16)
			if _, err := rand.Read(b); err != nil {
				return err
			}
			b[6] = b[6]&0x0f | 0x40
			b[8] = b[8]&0x3f | 0x80
			st.HostID = fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
			if err := s.save(*st); err != nil {
				return err
			}
		}
		host = st.HostID
		return nil
	})
	return host, err
}

// SignedIn reports whether an active account has a renewable ChatGPT grant.
func (s *Store) SignedIn() bool {
	st, err := s.read()
	if err != nil {
		return false
	}
	a := st.Accounts[st.Active]
	return a.RefreshToken != "" && hasScope(a.Scopes, "chatgpt.tokens.use.direct")
}

func (s *Store) Models() []Model {
	st, err := s.read()
	if err != nil {
		return nil
	}
	return append([]Model(nil), st.Accounts[st.Active].Models...)
}

func (s *Store) ActiveEmail() string {
	st, err := s.read()
	if err != nil {
		return ""
	}
	return st.Accounts[st.Active].Email
}

// AccessToken serializes rotating refresh tokens across the daemon and CLI.
func (s *Store) AccessToken(ctx context.Context) (string, error) {
	var token string
	err := s.withLock(func(st *state) error {
		a := st.Accounts[st.Active]
		if a.RefreshToken == "" || !hasScope(a.Scopes, "chatgpt.tokens.use.direct") {
			return fmt.Errorf("ChatGPT is not connected; run nevinho login")
		}
		if time.Until(a.ExpiresAt) < 2*time.Minute {
			values := url.Values{
				"grant_type": {"refresh_token"}, "client_id": {a.ClientID},
				"refresh_token": {a.RefreshToken}, "resource": {s.api},
			}
			fresh, err := s.exchange(ctx, values)
			if err != nil {
				return fmt.Errorf("refresh ChatGPT session: %w; run nevinho login if it expired", err)
			}
			if fresh.RefreshToken == "" || !hasScope(fresh.Scope, "chatgpt.tokens.use.direct") {
				return fmt.Errorf("refreshed ChatGPT session lacks plan usage permission")
			}
			a.AccessToken = fresh.AccessToken
			a.RefreshToken = fresh.RefreshToken
			a.Scopes = fresh.Scope
			a.ExpiresAt = time.Now().Add(time.Duration(fresh.ExpiresIn) * time.Second)
			st.Accounts[st.Active] = a
			if err := s.save(*st); err != nil {
				return err
			}
		}
		token = a.AccessToken
		return nil
	})
	return token, err
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
}

func (s *Store) exchange(ctx context.Context, values url.Values) (tokenResponse, error) {
	var tok tokenResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/api/accounts/oauth/token", strings.NewReader(values.Encode()))
	if err != nil {
		return tok, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return tok, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return tok, fmt.Errorf("token endpoint returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil {
		return tok, err
	}
	if tok.AccessToken == "" || tok.ExpiresIn <= 0 || !strings.EqualFold(tok.TokenType, "Bearer") {
		return tok, fmt.Errorf("incomplete token response")
	}
	return tok, nil
}

func (s *Store) FetchModels(ctx context.Context) ([]Model, error) {
	token, err := s.AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.api+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model list returned HTTP %d", resp.StatusCode)
	}
	var body struct {
		Models []Model `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, err
	}
	var models []Model
	for _, m := range body.Models {
		if m.Slug != "" && m.Visibility == "list" {
			models = append(models, m)
		}
	}
	if err := s.withLock(func(st *state) error {
		a := st.Accounts[st.Active]
		a.Models = models
		st.Accounts[st.Active] = a
		return s.save(*st)
	}); err != nil {
		return nil, err
	}
	return models, nil
}

func hasScope(scopes, want string) bool {
	for _, scope := range strings.Fields(scopes) {
		if scope == want {
			return true
		}
	}
	return false
}

package chatgpt

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func base64URL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

type Attempt struct {
	store       *Store
	state       string
	nonce       string
	verifier    string
	clientID    string
	subject     string
	redirectURI string
	URL         string
}

func (a *Attempt) AuthorizeURL() string { return a.URL }

// Begin starts a fresh PKCE/OIDC authorization. Register another account by
// passing newAccount=true; otherwise reauthorize the active registration.
func (s *Store) Begin(redirectURI string, newAccount bool) (*Attempt, error) {
	parsed, err := url.Parse(redirectURI)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Path != "/auth/callback" {
		return nil, fmt.Errorf("redirect must be an http://127.0.0.1:<port>/auth/callback URL")
	}
	host, err := s.hostID()
	if err != nil {
		return nil, err
	}
	stateValue, err := randomValue(32)
	if err != nil {
		return nil, err
	}
	nonce, err := randomValue(32)
	if err != nil {
		return nil, err
	}
	verifier, err := randomValue(32)
	if err != nil {
		return nil, err
	}
	a := &Attempt{store: s, state: stateValue, nonce: nonce, verifier: verifier, redirectURI: redirectURI}
	st, err := s.read()
	if err != nil {
		return nil, err
	}
	account := st.Accounts[st.Active]
	a.clientID = "dynamic_agent_client"
	if !newAccount && account.ClientID != "" {
		a.clientID = account.ClientID
		a.subject = account.Subject
	}
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id": {a.clientID}, "ext_agent_host_id": {host},
		"response_type": {"code"}, "redirect_uri": {redirectURI},
		"scope":    {"openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"},
		"resource": {s.api}, "state": {stateValue}, "nonce": {nonce},
		"code_challenge_method": {"S256"}, "code_challenge": {base64URL(challenge[:])},
	}
	if a.clientID == "dynamic_agent_client" {
		q.Set("agent_name_hint", "Nevinho")
	} else {
		// An ID token hint would put a credential in the printed login URL.
		// Returning users can choose their account in the browser instead.
		if account.Email != "" {
			q.Set("login_hint", account.Email)
		}
	}
	a.URL = s.base + "/api/accounts/authorize?" + q.Encode()
	return a, nil
}

// Complete exchanges a callback URL and activates its verified account.
// A failed or mismatched callback leaves existing credentials untouched.
func (a *Attempt) Complete(ctx context.Context, callback string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(callback))
	if err != nil || u.Scheme+"://"+u.Host+u.Path != a.redirectURI {
		return "", fmt.Errorf("callback URL did not match this login attempt")
	}
	q := u.Query()
	if q.Get("state") != a.state {
		return "", fmt.Errorf("ChatGPT login state mismatch")
	}
	if q.Get("error") != "" {
		return "", fmt.Errorf("ChatGPT login declined: %s", q.Get("error"))
	}
	if q.Get("code") == "" {
		return "", fmt.Errorf("ChatGPT callback has no authorization code")
	}
	issued := a.clientID
	if issued == "dynamic_agent_client" {
		issued = q.Get("client_id")
		if issued == "" || issued == "dynamic_agent_client" {
			return "", fmt.Errorf("ChatGPT registration returned no issued client ID")
		}
	} else if q.Get("client_id") != "" && q.Get("client_id") != issued {
		return "", fmt.Errorf("ChatGPT callback changed the registered client")
	}
	values := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {issued},
		"code": {q.Get("code")}, "code_verifier": {a.verifier},
		"redirect_uri": {a.redirectURI}, "resource": {a.store.api},
	}
	tok, err := a.store.exchange(ctx, values)
	if err != nil {
		return "", err
	}
	if tok.IDToken == "" || tok.RefreshToken == "" || !hasScope(tok.Scope, "chatgpt.tokens.use.direct") {
		return "", fmt.Errorf("ChatGPT did not grant plan usage and offline access")
	}
	identity, err := a.store.verifyIDToken(ctx, tok.IDToken, issued, a.nonce)
	if err != nil {
		return "", err
	}
	if a.subject != "" && identity.Subject != a.subject {
		return "", fmt.Errorf("ChatGPT login selected a different account")
	}
	account := account{
		ClientID: issued, Subject: identity.Subject, Email: identity.Email,
		IDToken: tok.IDToken, AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken,
		Scopes: tok.Scope, ExpiresAt: time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
	}
	if err := a.store.withLock(func(st *state) error {
		if old, exists := st.Accounts[issued]; exists && old.Subject != identity.Subject {
			return fmt.Errorf("ChatGPT registration belongs to a different account")
		}
		account.Models = st.Accounts[issued].Models
		st.Accounts[issued] = account
		st.Active = issued
		return a.store.save(*st)
	}); err != nil {
		return "", err
	}
	return identity.Email, nil
}

type identity struct {
	Subject string
	Email   string
}

// verifyIDToken accepts only an RSA SHA-256 signature from OpenAI's JWKS.
// Claims are checked after signature verification, with a small clock skew.
func (s *Store) verifyIDToken(ctx context.Context, token, audience, nonce string) (identity, error) {
	var out identity
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("invalid ChatGPT ID token")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return out, fmt.Errorf("invalid ChatGPT ID token header")
	}
	var header struct{ Alg, Kid string }
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Alg != "RS256" || header.Kid == "" {
		return out, fmt.Errorf("unsupported ChatGPT ID token signature")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/.well-known/jwks.json", nil)
	if err != nil {
		return out, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("ChatGPT signing keys returned HTTP %d", resp.StatusCode)
	}
	var keys struct {
		Keys []struct{ Kid, Kty, Alg, N, E string } `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&keys); err != nil {
		return out, err
	}
	var key *rsa.PublicKey
	for _, k := range keys.Keys {
		if k.Kid != header.Kid || k.Kty != "RSA" || (k.Alg != "" && k.Alg != "RS256") {
			continue
		}
		nBytes, nErr := base64.RawURLEncoding.DecodeString(k.N)
		eBytes, eErr := base64.RawURLEncoding.DecodeString(k.E)
		if nErr != nil || eErr != nil || len(eBytes) == 0 || len(eBytes) > 4 {
			continue
		}
		exponent := 0
		for _, b := range eBytes {
			exponent = exponent<<8 | int(b)
		}
		if exponent < 3 || len(nBytes) < 256 {
			continue
		}
		key = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: exponent}
		break
	}
	if key == nil {
		return out, fmt.Errorf("ChatGPT ID token signing key not found")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return out, fmt.Errorf("invalid ChatGPT ID token signature")
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], sig); err != nil {
		return out, fmt.Errorf("invalid ChatGPT ID token signature")
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return out, fmt.Errorf("invalid ChatGPT ID token claims")
	}
	var claims struct {
		Issuer   string          `json:"iss"`
		Subject  string          `json:"sub"`
		Audience json.RawMessage `json:"aud"`
		Expiry   int64           `json:"exp"`
		IssuedAt int64           `json:"iat"`
		Nonce    string          `json:"nonce"`
		Email    string          `json:"email"`
	}
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return out, fmt.Errorf("invalid ChatGPT ID token claims")
	}
	var aud string
	var audiences []string
	_ = json.Unmarshal(claims.Audience, &aud)
	if aud != "" {
		audiences = []string{aud}
	} else {
		_ = json.Unmarshal(claims.Audience, &audiences)
	}
	validAudience := false
	for _, value := range audiences {
		if value == audience {
			validAudience = true
		}
	}
	now := time.Now().Unix()
	if claims.Issuer != s.base || claims.Subject == "" || !validAudience || claims.Nonce != nonce ||
		claims.Expiry <= now-5 || claims.IssuedAt == 0 || claims.IssuedAt > now+5 {
		return out, fmt.Errorf("ChatGPT ID token claims did not match this login")
	}
	return identity{Subject: claims.Subject, Email: claims.Email}, nil
}

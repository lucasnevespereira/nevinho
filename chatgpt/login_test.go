package chatgpt

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoginRefreshAndModels(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	var nonce string
	refreshes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("resource") != issuer+"/v1" {
				t.Errorf("resource = %q", r.Form.Get("resource"))
			}
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				if r.Form.Get("code") != "test-code" || r.Form.Get("client_id") != "issued-client" {
					t.Errorf("code exchange = %v", r.Form)
				}
				claims := map[string]interface{}{"iss": issuer, "sub": "user-1", "aud": "issued-client", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": nonce, "email": "user@example.com"}
				fmt.Fprintf(w, `{"access_token":"old-access","refresh_token":"old-refresh","token_type":"Bearer","scope":"openid chatgpt.tokens.use.direct","expires_in":1,"id_token":%q}`, signedJWT(t, key, claims))
			case "refresh_token":
				refreshes++
				if r.Form.Get("refresh_token") != "old-refresh" {
					t.Errorf("refresh token = %q", r.Form.Get("refresh_token"))
				}
				fmt.Fprint(w, `{"access_token":"new-access","refresh_token":"new-refresh","token_type":"Bearer","scope":"chatgpt.tokens.use.direct","expires_in":3600}`)
			default:
				t.Errorf("grant = %q", r.Form.Get("grant_type"))
			}
		case "/.well-known/jwks.json":
			n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
			e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())
			fmt.Fprintf(w, `{"keys":[{"kid":"test-key","kty":"RSA","alg":"RS256","n":%q,"e":%q}]}`, n, e)
		case "/v1/models":
			if r.Header.Get("Authorization") != "Bearer new-access" {
				t.Errorf("model auth = %q", r.Header.Get("Authorization"))
			}
			fmt.Fprint(w, `{"models":[{"slug":"gpt-5","display_name":"GPT-5","visibility":"list"},{"slug":"hidden","visibility":"hidden"}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	issuer = srv.URL
	store := NewStore(t.TempDir())
	store.base, store.api = srv.URL, srv.URL+"/v1"
	if err := os.WriteFile(store.path(), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := store.Begin("http://127.0.0.1:12345/auth/callback", false)
	if err != nil {
		t.Fatal(err)
	}
	nonce = a.nonce
	parsed, _ := url.Parse(a.URL)
	if parsed.Query().Get("client_id") != "dynamic_agent_client" || parsed.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorize query = %v", parsed.Query())
	}
	callback := a.redirectURI + "?code=test-code&client_id=issued-client&state=" + url.QueryEscape(a.state)
	if _, err := a.Complete(context.Background(), strings.Replace(callback, "state=", "state=wrong", 1)); err == nil {
		t.Fatal("accepted wrong state")
	}
	if store.SignedIn() {
		t.Fatal("wrong state persisted credentials")
	}
	email, err := a.Complete(context.Background(), callback)
	if err != nil {
		t.Fatal(err)
	}
	if email != "user@example.com" || !store.SignedIn() {
		t.Fatalf("email=%q signedIn=%t", email, store.SignedIn())
	}
	info, err := os.Stat(store.path())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %v, %v", info, err)
	}
	models, err := store.FetchModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Slug != "gpt-5" || refreshes != 1 {
		t.Fatalf("models=%v refreshes=%d", models, refreshes)
	}
	token, err := store.AccessToken(context.Background())
	if err != nil || token != "new-access" || refreshes != 1 {
		t.Fatalf("token=%q refreshes=%d err=%v", token, refreshes, err)
	}
	st, err := store.read()
	if err != nil || st.Accounts[st.Active].RefreshToken != "new-refresh" {
		t.Fatalf("rotated token not saved: %v", err)
	}
}

func TestSelectModelKeepsAvailableChoiceAfterLogin(t *testing.T) {
	models := []Model{{Slug: "gpt-6-luna"}, {Slug: "gpt-6.1-sol"}}
	if got := SelectModel(models, "chatgpt:gpt-6.1-sol"); got != "chatgpt:gpt-6.1-sol" {
		t.Fatalf("renewed login changed the selected model to %q", got)
	}
	for _, current := range []string{"", "gpt-6.1-sol", "chatgpt:removed"} {
		if got := SelectModel(models, current); got != "chatgpt:gpt-6-luna" {
			t.Fatalf("current %q selected %q", current, got)
		}
	}
	if got := SelectModel(nil, "chatgpt:gpt-6.1-sol"); got != "" {
		t.Fatalf("empty catalog selected %q", got)
	}
}

func signedJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]interface{}) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test-key"}`))
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	message := header + "." + payload
	hash := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	return message + "." + base64.RawURLEncoding.EncodeToString(signature)
}

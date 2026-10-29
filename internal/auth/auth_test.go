package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/partite-ai/partite-cli/internal/credstore"
)

const (
	apiURL    = "https://api.test"
	issuerURL = "https://auth.test"
)

// fakeAS is a Partite server and a Kinde authorization server in one handler.
type fakeAS struct {
	t *testing.T

	mu         sync.Mutex
	devicePoll int
	refreshes  int
}

func unsignedJWT(claims map[string]any) string {
	enc := base64.RawURLEncoding
	head := enc.EncodeToString([]byte(`{"alg":"none"}`))
	body, _ := json.Marshal(claims)
	return head + "." + enc.EncodeToString(body) + ".sig"
}

func (f *fakeAS) tokenResponse(w http.ResponseWriter, access string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token":  access,
		"refresh_token": "refresh-1",
		"token_type":    "Bearer",
		"expires_in":    3600,
		"id_token":      unsignedJWT(map[string]any{"email": "ada@example.com", "given_name": "Ada", "family_name": "Lovelace"}),
	})
}

func (f *fakeAS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/oauth2/device/auth":
		r.ParseForm()
		if r.Form.Get("client_id") != "cli-app" || r.Form.Get("audience") != audience || r.Form.Get("org_code") != "org_1" {
			f.t.Errorf("unexpected device request: %v", r.Form)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "dev-1",
			"user_code":                 "ABCD-EFGH",
			"verification_uri":          issuerURL + "/device",
			"verification_uri_complete": issuerURL + "/device?user_code=ABCD-EFGH",
			"expires_in":                600,
			"interval":                  5,
		})
	case r.URL.Path == "/oauth2/token":
		r.ParseForm()
		switch r.Form.Get("grant_type") {
		case "urn:ietf:params:oauth:grant-type:device_code":
			f.devicePoll++
			if f.devicePoll < 3 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":"authorization_pending"}`)
				return
			}
			f.tokenResponse(w, "device-access")
		case "refresh_token":
			f.refreshes++
			if r.Form.Get("refresh_token") != "refresh-1" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			f.tokenResponse(w, "refreshed-access")
		}
	default:
		http.NotFound(w, r)
	}
}

// inMemory sends every request to a handler without a network, so that the
// device flow's polling runs on synctest's fake clock.
type inMemory struct{ h http.Handler }

func (m inMemory) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "127.0.0.1" || strings.HasPrefix(req.URL.Host, "127.0.0.1:") {
		return http.DefaultTransport.RoundTrip(req)
	}
	rec := httptest.NewRecorder()
	m.h.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func TestLoginPollsUntilApproved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		as := &fakeAS{t: t}
		hc := &http.Client{Transport: inMemory{as}}
		t.Setenv("PARTITE_OAUTH_ISSUER", issuerURL)
		t.Setenv("PARTITE_OAUTH_CLIENT_ID", "cli-app")
		server, err := ServerFor(apiURL)
		if err != nil {
			t.Fatal(err)
		}
		var prompt strings.Builder
		start := time.Now()
		creds, err := server.Login(context.Background(), hc, Options{Org: "org_1", Prompt: &prompt, NoBrowser: true})
		if err != nil {
			t.Fatal(err)
		}
		if creds.AccessToken != "device-access" || creds.RefreshToken != "refresh-1" || creds.ClientID != "cli-app" || creds.TokenURL != issuerURL+"/oauth2/token" {
			t.Errorf("credentials: %+v", creds)
		}
		if creds.Email != "ada@example.com" || creds.Name != "Ada Lovelace" {
			t.Errorf("identity: %q %q", creds.Email, creds.Name)
		}
		if !strings.Contains(prompt.String(), "shows the code ABCD-EFGH") || !strings.Contains(prompt.String(), issuerURL+"/device?user_code=ABCD-EFGH") {
			t.Errorf("prompt does not show the code and the page: %s", prompt.String())
		}
		if as.devicePoll != 3 || time.Since(start) != 15*time.Second {
			t.Errorf("polled %d times over %s, want 3 over 15s", as.devicePoll, time.Since(start))
		}
	})
}

func TestTokenSourceRefreshesAndSavesExpiredToken(t *testing.T) {
	t.Setenv("PARTITE_CONFIG_DIR", t.TempDir())
	t.Setenv("PARTITE_NO_KEYRING", "1")
	as := &fakeAS{t: t}
	hc := &http.Client{Transport: inMemory{as}}
	creds := &credstore.Credentials{
		AccessToken:  "old",
		RefreshToken: "refresh-1",
		Expiry:       time.Now().Add(-time.Minute),
		TokenURL:     issuerURL + "/oauth2/token",
		ClientID:     "cli-app",
	}
	ts := NewTokenSource("cloud", hc, creds)
	tok, err := ts.Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "refreshed-access" || as.refreshes != 1 {
		t.Fatalf("token %q after %d refreshes", tok.AccessToken, as.refreshes)
	}
	saved, err := credstore.Get("cloud")
	if err != nil || saved == nil || saved.AccessToken != "refreshed-access" {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	if _, err := ts.Token(); err != nil || as.refreshes != 1 {
		t.Errorf("a fresh token was refreshed again (%d refreshes, %v)", as.refreshes, err)
	}
}

func TestTokenSourceReportsExpiredLoginWhenRefreshIsRejected(t *testing.T) {
	t.Setenv("PARTITE_CONFIG_DIR", t.TempDir())
	t.Setenv("PARTITE_NO_KEYRING", "1")
	hc := &http.Client{Transport: inMemory{&fakeAS{t: t}}}
	ts := NewTokenSource("cloud", hc, &credstore.Credentials{
		AccessToken: "old", RefreshToken: "revoked", Expiry: time.Now().Add(-time.Minute),
		TokenURL: issuerURL + "/oauth2/token", ClientID: "cli-app",
	})
	if _, err := ts.Token(); err != ErrLoginExpired {
		t.Errorf("got %v, want ErrLoginExpired", err)
	}
}

func TestServerForRefusesServersOtherThanCloud(t *testing.T) {
	t.Setenv("PARTITE_OAUTH_ISSUER", "")
	_, err := ServerFor("https://partite.example.com")
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("got %v, want a hint to use an API key", err)
	}
}

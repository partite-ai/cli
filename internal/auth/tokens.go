package auth

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/partite-ai/partite-cli/internal/credstore"
	"golang.org/x/oauth2"
)

// ErrLoginExpired means the stored login can no longer be refreshed.
var ErrLoginExpired = errors.New("login expired")

// TokenSource returns access tokens for a context, refreshing them when they
// expire and writing the new tokens back to the credential store.
type TokenSource struct {
	contextName string
	hc          *http.Client

	mu    sync.Mutex
	creds *credstore.Credentials
}

func NewTokenSource(contextName string, hc *http.Client, creds *credstore.Credentials) *TokenSource {
	return &TokenSource{contextName: contextName, hc: hc, creds: creds}
}

func (ts *TokenSource) Token() (*oauth2.Token, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	tok := &oauth2.Token{
		AccessToken:  ts.creds.AccessToken,
		RefreshToken: ts.creds.RefreshToken,
		Expiry:       ts.creds.Expiry,
		TokenType:    "Bearer",
	}
	if tok.Valid() {
		return tok, nil
	}
	if tok.RefreshToken == "" || ts.creds.TokenURL == "" {
		return nil, ErrLoginExpired
	}
	cfg := &oauth2.Config{
		ClientID: ts.creds.ClientID,
		Endpoint: oauth2.Endpoint{TokenURL: ts.creds.TokenURL, AuthStyle: oauth2.AuthStyleInParams},
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, ts.hc)
	fresh, err := cfg.TokenSource(ctx, tok).Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode < 500 {
			return nil, ErrLoginExpired
		}
		return nil, err
	}
	ts.creds.AccessToken = fresh.AccessToken
	ts.creds.Expiry = fresh.Expiry
	if fresh.RefreshToken != "" {
		ts.creds.RefreshToken = fresh.RefreshToken
	}
	if _, err := credstore.Put(ts.contextName, ts.creds); err != nil {
		return nil, err
	}
	return fresh, nil
}

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strings"

	"github.com/partite-ai/partite-cli/internal/credstore"
	"golang.org/x/oauth2"
)

type Options struct {
	// Org is a Kinde org code. Empty lets the user pick one in the browser.
	Org string
	// Prompt is where messages for the user go.
	Prompt io.Writer
	// NoBrowser prints the login page without trying to open it.
	NoBrowser bool
}

func (s *Server) oauthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID: s.ClientID,
		Scopes:   scopes,
		Endpoint: oauth2.Endpoint{
			TokenURL:      s.TokenURL,
			DeviceAuthURL: s.DeviceAuthURL,
			AuthStyle:     oauth2.AuthStyleInParams,
		},
	}
}

func (s *Server) extraParams(org string) []oauth2.AuthCodeOption {
	opts := []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("audience", audience)}
	if org != "" {
		opts = append(opts, oauth2.SetAuthURLParam("org_code", org))
	}
	return opts
}

// Login runs the device authorization flow (RFC 8628). It opens the login
// page with the code filled in, so on a machine with a browser it is one
// click; elsewhere the user opens the page on another device.
func (s *Server) Login(ctx context.Context, hc *http.Client, o Options) (*credstore.Credentials, error) {
	cfg := s.oauthConfig()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, hc)
	da, err := cfg.DeviceAuth(ctx, s.extraParams(o.Org)...)
	if err != nil {
		return nil, fmt.Errorf("start login: %w", err)
	}
	page := da.VerificationURIComplete
	if page == "" {
		page = da.VerificationURI
		fmt.Fprintf(o.Prompt, "To log in, open this page and enter the code %s:\n\n  %s\n\n", da.UserCode, page)
	} else {
		fmt.Fprintf(o.Prompt, "To log in, open this page and check that it shows the code %s:\n\n  %s\n\n", da.UserCode, page)
	}
	if !o.NoBrowser {
		_ = OpenBrowser(page)
	}
	fmt.Fprintln(o.Prompt, "Waiting for you to finish in the browser (Ctrl-C to cancel)...")
	tok, err := cfg.DeviceAccessToken(ctx, da)
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	return s.credentials(tok), nil
}

func (s *Server) credentials(tok *oauth2.Token) *credstore.Credentials {
	c := &credstore.Credentials{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       tok.Expiry,
		TokenURL:     s.TokenURL,
		ClientID:     s.ClientID,
	}
	if idToken, ok := tok.Extra("id_token").(string); ok {
		var claims struct {
			Email      string `json:"email"`
			Name       string `json:"name"`
			GivenName  string `json:"given_name"`
			FamilyName string `json:"family_name"`
		}
		if DecodeClaims(idToken, &claims) == nil {
			c.Email = claims.Email
			c.Name = claims.Name
			if c.Name == "" {
				c.Name = strings.TrimSpace(claims.GivenName + " " + claims.FamilyName)
			}
		}
	}
	return c
}

// DecodeClaims reads a JWT's payload without checking its signature. The
// server checks the signature; the CLI only uses claims for display.
func DecodeClaims(token string, out any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("not a JWT")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// OpenBrowser opens url in the user's browser.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

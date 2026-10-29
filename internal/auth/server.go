// Package auth logs the CLI in to Partite cloud through Kinde, and keeps the
// tokens fresh.
package auth

import (
	"fmt"
	"os"
	"strings"
)

// CloudURL is the only server the CLI logs in to. Other servers take an API
// key.
const CloudURL = "https://api.partite.ai"

const (
	// The CLI's Kinde "Device and IoT" app in the prod business.
	cloudIssuer   = "https://auth.partite.ai"
	cloudClientID = "43eae8c124244d80ba84a0fc7380c6d6"

	audience   = "https://api.partite.ai"
	apiKeyHint = "use an API key instead: partite login --with-api-key"
)

var scopes = []string{"openid", "profile", "email", "offline"}

// Server is where the CLI logs in.
type Server struct {
	ClientID      string
	TokenURL      string
	DeviceAuthURL string
}

// ServerFor returns where to log in for the Partite server at baseURL.
// PARTITE_OAUTH_ISSUER and PARTITE_OAUTH_CLIENT_ID log in to another Kinde
// business instead, such as Partite's own dev environment.
func ServerFor(baseURL string) (*Server, error) {
	issuer, clientID := cloudIssuer, cloudClientID
	if v := os.Getenv("PARTITE_OAUTH_ISSUER"); v != "" {
		issuer, clientID = strings.TrimRight(v, "/"), os.Getenv("PARTITE_OAUTH_CLIENT_ID")
	} else if strings.TrimRight(baseURL, "/") != CloudURL {
		return nil, fmt.Errorf("partite login works only with Partite cloud (%s), not %s; %s", CloudURL, baseURL, apiKeyHint)
	}
	if clientID == "" {
		return nil, fmt.Errorf("this build of partite cannot log in; %s", apiKeyHint)
	}
	return &Server{
		ClientID:      clientID,
		TokenURL:      issuer + "/oauth2/token",
		DeviceAuthURL: issuer + "/oauth2/device/auth",
	}, nil
}

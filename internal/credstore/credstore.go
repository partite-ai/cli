// Package credstore keeps secrets for each context: OAuth tokens and API key
// tokens. It uses the OS keychain and falls back to a 0600 file when the
// keychain is not available or PARTITE_NO_KEYRING is set.
package credstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/config"
	"github.com/zalando/go-keyring"
)

const keyringService = "partite-cli"

type Credentials struct {
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitzero"`
	// TokenURL and ClientID are what the refresh token must be sent back to.
	TokenURL string `json:"token_url,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	// Email and Name come from the ID token at login, for display only.
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`

	APIToken string `json:"api_token,omitempty"`
	// APIKeySecret is how older versions stored an API key. With the
	// context's key id it makes the token.
	APIKeySecret string `json:"api_key_secret,omitempty"`
}

func useKeyring() bool {
	return os.Getenv("PARTITE_NO_KEYRING") == ""
}

// Get returns nil, nil when the context has no stored credentials.
func Get(contextName string) (*Credentials, error) {
	if useKeyring() {
		s, err := keyring.Get(keyringService, contextName)
		if err == nil {
			var c Credentials
			if err := json.Unmarshal([]byte(s), &c); err != nil {
				return nil, err
			}
			return &c, nil
		}
		if !errors.Is(err, keyring.ErrNotFound) && !keyringUnavailable(err) {
			return nil, err
		}
	}
	all := map[string]*Credentials{}
	if err := readJSON(filePath, &all); err != nil {
		return nil, err
	}
	return all[contextName], nil
}

// Put returns where the credentials were written, "keychain" or a file path.
func Put(contextName string, c *Credentials) (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	if useKeyring() {
		err := keyring.Set(keyringService, contextName, string(data))
		if err == nil {
			// Clear any older copy in the file so the two never disagree.
			_ = deleteFromFile(contextName)
			return "keychain", nil
		}
		if !keyringUnavailable(err) {
			return "", err
		}
	}
	all := map[string]*Credentials{}
	if err := readJSON(filePath, &all); err != nil {
		return "", err
	}
	all[contextName] = c
	if err := writeJSON(filePath, all); err != nil {
		return "", err
	}
	return filePath()
}

func Delete(contextName string) error {
	if useKeyring() {
		err := keyring.Delete(keyringService, contextName)
		if err != nil && !errors.Is(err, keyring.ErrNotFound) && !keyringUnavailable(err) {
			return err
		}
	}
	return deleteFromFile(contextName)
}

// keyringUnavailable is true when there is no keychain to talk to, as on a
// headless Linux box without a secret service.
func keyringUnavailable(err error) bool {
	return errors.Is(err, keyring.ErrUnsupportedPlatform) || !errors.Is(err, keyring.ErrNotFound) && isDBusError(err)
}

// go-keyring returns plain errors from dbus when no secret service runs.
func isDBusError(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "dbus") || strings.Contains(s, "org.freedesktop.secrets")
}

func filePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials.json"), nil
}

// readJSON leaves v as it is when the file does not exist.
func readJSON(path func() (string, error), v any) error {
	p, err := path()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSON(path func() (string, error), v any) error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func deleteFromFile(contextName string) error {
	all := map[string]*Credentials{}
	if err := readJSON(filePath, &all); err != nil {
		return err
	}
	if _, ok := all[contextName]; !ok {
		return nil
	}
	delete(all, contextName)
	return writeJSON(filePath, all)
}

// The caller key of a context signs the tokens the CLI makes for itself to
// talk to a mesh through an access point. It is kept apart from the
// context's credentials, so logging in and out leaves it alone.

func callerKeyAccount(contextName string) string { return contextName + "/caller-key" }

// GetCallerKey returns "" when the context has no caller key.
func GetCallerKey(contextName string) (string, error) {
	if useKeyring() {
		s, err := keyring.Get(keyringService, callerKeyAccount(contextName))
		if err == nil {
			return s, nil
		}
		if !errors.Is(err, keyring.ErrNotFound) && !keyringUnavailable(err) {
			return "", err
		}
	}
	all := map[string]string{}
	if err := readJSON(keyFilePath, &all); err != nil {
		return "", err
	}
	return all[contextName], nil
}

func PutCallerKey(contextName, key string) error {
	if useKeyring() {
		err := keyring.Set(keyringService, callerKeyAccount(contextName), key)
		if err == nil {
			return nil
		}
		if !keyringUnavailable(err) {
			return err
		}
	}
	all := map[string]string{}
	if err := readJSON(keyFilePath, &all); err != nil {
		return err
	}
	all[contextName] = key
	return writeJSON(keyFilePath, all)
}

func DeleteCallerKey(contextName string) error {
	if useKeyring() {
		err := keyring.Delete(keyringService, callerKeyAccount(contextName))
		if err != nil && !errors.Is(err, keyring.ErrNotFound) && !keyringUnavailable(err) {
			return err
		}
	}
	all := map[string]string{}
	if err := readJSON(keyFilePath, &all); err != nil {
		return err
	}
	if _, ok := all[contextName]; !ok {
		return nil
	}
	delete(all, contextName)
	return writeJSON(keyFilePath, all)
}

func keyFilePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "caller-keys.json"), nil
}

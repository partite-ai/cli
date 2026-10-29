// Package config reads and writes the CLI's config file, which lists the
// contexts (servers) the user can talk to and which one is current.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// AuthMethod is how the CLI proves who it is to a context's server.
type AuthMethod string

const (
	AuthLogin  AuthMethod = "login"   // OAuth tokens from `partite login`
	AuthAPIKey AuthMethod = "api-key" // an API key token
	AuthNone   AuthMethod = "none"    // a local mesh without REQUIRE_AUTH
)

type Context struct {
	URL  string     `yaml:"url"`
	Auth AuthMethod `yaml:"auth"`
	// Enterprise is nil when it should be guessed from the URL.
	Enterprise *bool `yaml:"enterprise,omitempty"`
	// APIKeyID is set when Auth is api-key. The token is in the credential store.
	APIKeyID string `yaml:"api_key_id,omitempty"`
	// Org is the Kinde org code to log in to. Empty lets the user pick.
	Org string `yaml:"org,omitempty"`
	// WebURL is the Partite web app for this server, which `view` opens.
	WebURL string `yaml:"web_url,omitempty"`
}

type File struct {
	Current  string              `yaml:"current"`
	Contexts map[string]*Context `yaml:"contexts"`
}

const DefaultContext = "cloud"

// Builtins are always present unless the user overrides them by name.
func Builtins() map[string]*Context {
	return map[string]*Context{
		"cloud": {URL: "https://api.partite.ai", Auth: AuthLogin, WebURL: "https://app.partite.ai"},
		"local": {URL: "http://localhost:8080", Auth: AuthNone},
	}
}

// Dir returns the directory that holds the config file and file-based credentials.
func Dir() (string, error) {
	if d := os.Getenv("PARTITE_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "partite"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "partite"), nil
}

func path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// Load reads the config file. A missing file gives the built-in contexts.
func Load() (*File, error) {
	f := &File{Contexts: map[string]*Context{}}
	p, err := path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := yaml.Unmarshal(data, f); err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
	}
	if f.Contexts == nil {
		f.Contexts = map[string]*Context{}
	}
	for name, c := range Builtins() {
		saved, ok := f.Contexts[name]
		switch {
		case !ok:
			f.Contexts[name] = c
		case saved.WebURL == "" && saved.URL == c.URL:
			// Saved before contexts had a web app.
			saved.WebURL = c.WebURL
		}
	}
	if f.Current == "" {
		f.Current = DefaultContext
	}
	return f, nil
}

func (f *File) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (f *File) Names() []string {
	names := make([]string, 0, len(f.Contexts))
	for n := range f.Contexts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func IsBuiltin(name string) bool {
	_, ok := Builtins()[name]
	return ok
}

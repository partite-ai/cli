package modelprovider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
)

func TestReadSecret(t *testing.T) {
	t.Setenv("MODEL_TEST_KEY", "sk-env")
	file := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(file, []byte("sk-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &cmdutil.App{In: strings.NewReader("sk-stdin\n")}
	for _, tc := range []struct {
		value, env, want string
		fails            bool
	}{
		{value: "", want: ""},
		{value: "sk-literal", want: "sk-literal"},
		{env: "MODEL_TEST_KEY", want: "sk-env"},
		{value: "${MODEL_TEST_KEY}", want: "sk-env"},
		{value: "$MODEL_TEST_KEY", want: "$MODEL_TEST_KEY"},
		{value: "@" + file, want: "sk-file"},
		{value: "@-", want: "sk-stdin"},
		{env: "MODEL_TEST_MISSING", fails: true},
		{value: "${MODEL_TEST_MISSING}", fails: true},
		{value: "x", env: "MODEL_TEST_KEY", fails: true},
	} {
		got, err := readSecret(app, "API key", tc.value, tc.env)
		if (err != nil) != tc.fails || got != tc.want {
			t.Errorf("readSecret(%q, %q) = %q, %v; want %q, fails %v", tc.value, tc.env, got, err, tc.want, tc.fails)
		}
	}
}

func TestProviderConfigKeepsStoredSecret(t *testing.T) {
	app := &cmdutil.App{In: strings.NewReader("")}
	cur := Provider{Name: "p", Fireworks: &FireworksConfig{KeyConfig: KeyConfig{APIKeySet: true}, AccountID: "a"}}
	spec := Provider{Name: "renamed", Fireworks: &FireworksConfig{AccountID: "a"}}
	if _, cfg, err := providerConfig(app, spec, &cur); err != nil || cfg != nil {
		t.Fatalf("unchanged settings: cfg %v, err %v; want no config", cfg, err)
	}
	spec.Fireworks.AccountID = "b"
	if _, _, err := providerConfig(app, spec, &cur); err == nil {
		t.Fatal("changed settings without a key: want an error")
	}
	spec.Anthropic = &KeyConfig{}
	if _, _, err := providerConfig(app, spec, &cur); err == nil {
		t.Fatal("two types: want an error")
	}
}

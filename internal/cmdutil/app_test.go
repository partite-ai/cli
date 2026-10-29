package cmdutil

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/partite-ai/partite-cli/internal/config"
	"github.com/partite-ai/partite-cli/internal/credstore"
)

// An API key goes out as a bearer token, whether it was given as a token or
// in the older key id and secret form.
func TestAPIKeySentAsBearerToken(t *testing.T) {
	const token = "prt_ak_42GDRZA7X7QD5NQ7_TYCG+abc/def="
	for _, tc := range []struct {
		name  string
		env   map[string]string
		creds *credstore.Credentials
	}{
		{"token in env", map[string]string{"PARTITE_API_TOKEN": token}, nil},
		{"key id and secret in env", map[string]string{"PARTITE_API_KEY": "42GDRZA7X7QD5NQ7", "PARTITE_API_SECRET": "TYCG+abc/def="}, nil},
		{"stored token", nil, &credstore.Credentials{APIToken: token}},
		{"stored secret", nil, &credstore.Credentials{APIKeySecret: "TYCG+abc/def="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PARTITE_CONFIG_DIR", t.TempDir())
			t.Setenv("PARTITE_NO_KEYRING", "1")
			for _, k := range []string{"PARTITE_API_TOKEN", "PARTITE_API_KEY", "PARTITE_API_SECRET", "PARTITE_TOKEN"} {
				t.Setenv(k, tc.env[k])
			}
			if tc.creds != nil {
				if _, err := credstore.Put("acme", tc.creds); err != nil {
					t.Fatal(err)
				}
			}
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Authorization")
			}))
			defer srv.Close()

			app := &App{ContextName: "acme", Context: &config.Context{URL: srv.URL, Auth: config.AuthAPIKey, APIKeyID: "42GDRZA7X7QD5NQ7"}}
			hc, err := app.HTTP()
			if err != nil {
				t.Fatal(err)
			}
			resp, err := hc.Get(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if got != "Bearer "+token {
				t.Errorf("Authorization = %q; want %q", got, "Bearer "+token)
			}
		})
	}
}

// Package authcmd has login, logout and whoami.
package authcmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/auth"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	"github.com/partite-ai/partite-cli/internal/config"
	"github.com/partite-ai/partite-cli/internal/credstore"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func New(app *cmdutil.App) []*cobra.Command {
	return []*cobra.Command{newLogin(app), newLogout(app), newWhoami(app)}
}

func newLogin(app *cmdutil.App) *cobra.Command {
	var noBrowser, withAPIKey bool
	var org string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Partite",
		Long: `Log in to the current context's server.

This opens a login page in your browser and waits for you to finish there.
It prints the page and a code too. On a machine without a browser, such as
over SSH, open the page on another device and check that it shows the same
code.

For scripts and CI, use an API key instead of logging in. Either set
PARTITE_API_TOKEN to its token, or store the token in the context with
--with-api-key, which reads it from standard input.`,
		Example: `  partite login
  partite login --org org_1234abcd
  partite login --no-browser
  echo "$PARTITE_API_TOKEN" | partite login --with-api-key`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if withAPIKey {
				return loginWithAPIKey(app)
			}
			return loginOAuth(cmd.Context(), app, org, noBrowser)
		},
	}
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the login page without opening a browser")
	cmd.Flags().StringVar(&org, "org", "", "Kinde org code to log in to (default: choose in the browser)")
	cmd.Flags().BoolVar(&withAPIKey, "with-api-key", false, "store an API key token, read from standard input")
	cmd.MarkFlagsMutuallyExclusive("no-browser", "with-api-key")
	return cmd
}

func loginOAuth(ctx context.Context, app *cmdutil.App, org string, noBrowser bool) error {
	if org == "" {
		org = app.Context.Org
	}
	hc := cmdutil.PlainHTTP()
	server, err := auth.ServerFor(app.BaseURL())
	if err != nil {
		return err
	}
	opts := auth.Options{Org: org, Prompt: app.Err, NoBrowser: noBrowser}
	creds, err := server.Login(ctx, hc, opts)
	if err != nil {
		return err
	}
	where, err := credstore.Put(app.ContextName, creds)
	if err != nil {
		return fmt.Errorf("save the login: %w", err)
	}
	if err := saveContextAuth(app, config.AuthLogin, ""); err != nil {
		return err
	}

	claims := accessClaims(creds.AccessToken)
	who := creds.Email
	if who == "" {
		who = creds.Name
	}
	msg := fmt.Sprintf("Logged in to %s", app.ContextName)
	if who != "" {
		msg += " as " + who
	}
	if claims.OrgCode != "" {
		msg += " (org " + claims.OrgCode + ")"
	}
	fmt.Fprintln(app.Err, msg+".")
	if where != "keychain" {
		fmt.Fprintf(app.Err, "Credentials saved to %s.\n", where)
	}
	if len(claims.Permissions) == 0 {
		fmt.Fprintln(app.Err, "Warning: your login has no permissions in this org, so most commands will fail. Ask an org admin to give you a role.")
	}
	return nil
}

func loginWithAPIKey(app *cmdutil.App) error {
	var token string
	if app.Interactive() {
		fmt.Fprint(app.Err, "API key token: ")
		s, err := readSecret(app.In)
		fmt.Fprintln(app.Err)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(s)
	} else {
		data, err := io.ReadAll(app.In)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(data))
	}
	// Older versions read "<key id>:<secret>".
	if id, secret, ok := strings.Cut(token, ":"); ok && id != "" && secret != "" {
		token = auth.APIKeyToken(id, secret)
	}
	id, ok := auth.APIKeyID(token)
	if !ok {
		return cmdutil.UsageError("expected an API key token (prt_ak_...) on standard input")
	}
	where, err := credstore.Put(app.ContextName, &credstore.Credentials{APIToken: token})
	if err != nil {
		return fmt.Errorf("save the API key: %w", err)
	}
	if err := saveContextAuth(app, config.AuthAPIKey, id); err != nil {
		return err
	}
	fmt.Fprintf(app.Err, "Context %s now uses API key %s.\n", app.ContextName, id)
	if where != "keychain" {
		fmt.Fprintf(app.Err, "Token saved to %s.\n", where)
	}
	return nil
}

func readSecret(in io.Reader) (string, error) {
	if f, ok := in.(interface{ Fd() uintptr }); ok && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))
		return string(b), err
	}
	s, err := bufio.NewReader(in).ReadString('\n')
	return s, err
}

func saveContextAuth(app *cmdutil.App, method config.AuthMethod, keyID string) error {
	c := app.Config.Contexts[app.ContextName]
	c.Auth = method
	c.APIKeyID = keyID
	return app.Config.Save()
}

func newLogout(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "logout",
		Short:   "Remove the stored login or API key for a context",
		Example: "  partite logout\n  partite logout --context acme",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := credstore.Delete(app.ContextName); err != nil {
				return err
			}
			c := app.Config.Contexts[app.ContextName]
			if c.APIKeyID != "" {
				c.APIKeyID = ""
				if err := app.Config.Save(); err != nil {
					return err
				}
			}
			fmt.Fprintf(app.Err, "Logged out of %s.\n", app.ContextName)
			return nil
		},
	}
}

type claims struct {
	OrgCode     string   `json:"org_code"`
	OrgName     string   `json:"org_name"`
	Permissions []string `json:"permissions"`
	Subject     string   `json:"sub"`
	Expiry      int64    `json:"exp"`
}

func accessClaims(token string) claims {
	var c claims
	_ = auth.DecodeClaims(token, &c)
	return c
}

type whoami struct {
	Context     string     `json:"context"`
	URL         string     `json:"url"`
	Auth        string     `json:"auth"`
	Email       string     `json:"email,omitempty"`
	Name        string     `json:"name,omitempty"`
	Org         string     `json:"org,omitempty"`
	APIKeyID    string     `json:"api_key_id,omitempty"`
	Permissions []string   `json:"permissions,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Verified    bool       `json:"verified"`
}

func newWhoami(app *cmdutil.App) *cobra.Command {
	var noCheck bool
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show who you are logged in as, and where",
		Long: `Show the current context, the server, and the identity and permissions
that commands will use. It also makes a request to check that the server
accepts the credentials; --no-check skips that.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := whoami{Context: app.ContextName, URL: app.Context.URL, Auth: string(app.Context.Auth)}
			switch {
			case os.Getenv("PARTITE_API_TOKEN") != "":
				w.Auth = "api-key (PARTITE_API_TOKEN)"
				w.APIKeyID, _ = auth.APIKeyID(os.Getenv("PARTITE_API_TOKEN"))
			case os.Getenv("PARTITE_API_KEY") != "":
				w.Auth = "api-key (PARTITE_API_KEY)"
				w.APIKeyID = os.Getenv("PARTITE_API_KEY")
			case os.Getenv("PARTITE_TOKEN") != "":
				w.Auth = "token (PARTITE_TOKEN)"
				fill(&w, accessClaims(os.Getenv("PARTITE_TOKEN")))
			case app.Context.Auth == config.AuthAPIKey:
				w.APIKeyID = app.Context.APIKeyID
			case app.Context.Auth == config.AuthLogin:
				creds, err := credstore.Get(app.ContextName)
				if err != nil {
					return err
				}
				if creds == nil || creds.AccessToken == "" {
					return &cmdutil.Error{Code: cmdutil.ExitAuth, Msg: fmt.Sprintf("not logged in to %s (%s)", app.ContextName, app.Context.URL), Hint: "Run: partite login"}
				}
				w.Email, w.Name = creds.Email, creds.Name
				fill(&w, accessClaims(creds.AccessToken))
			}
			if !noCheck {
				if err := check(cmd.Context(), app); err != nil {
					return err
				}
				w.Verified = true
				// A refresh during the check may have changed the token.
				if app.Context.Auth == config.AuthLogin && os.Getenv("PARTITE_TOKEN") == "" && !cmdutil.EnvAPIKeySet() {
					if creds, _ := credstore.Get(app.ContextName); creds != nil {
						fill(&w, accessClaims(creds.AccessToken))
					}
				}
			}
			return app.Printer().Object(w, w.Context, []output.Field{
				{Label: "Context", Value: w.Context},
				{Label: "Server", Value: w.URL},
				{Label: "Auth", Value: w.Auth},
				{Label: "User", Value: strings.TrimSpace(w.Name + " " + angle(w.Email))},
				{Label: "Org", Value: w.Org},
				{Label: "API key", Value: w.APIKeyID},
				{Label: "Permissions", Value: strings.Join(w.Permissions, ", ")},
				{Label: "Expires", Value: formatExpiry(w.ExpiresAt)},
			})
		},
	}
	cmd.Flags().BoolVar(&noCheck, "no-check", false, "do not contact the server")
	return cmd
}

// check makes the cheapest authenticated request there is.
func check(ctx context.Context, app *cmdutil.App) error {
	rest, err := app.REST()
	if err != nil {
		return err
	}
	var out map[string]any
	return rest.JSON(ctx, "POST", "/graphql", map[string]string{"query": "{ __typename }"}, &out)
}

func fill(w *whoami, c claims) {
	w.Org = c.OrgCode
	if c.OrgName != "" {
		w.Org = c.OrgName + " (" + c.OrgCode + ")"
	}
	w.Permissions = c.Permissions
	if c.Expiry > 0 {
		t := time.Unix(c.Expiry, 0)
		w.ExpiresAt = &t
	}
}

func angle(email string) string {
	if email == "" {
		return ""
	}
	return "<" + email + ">"
}

func formatExpiry(t *time.Time) string {
	if t == nil {
		return ""
	}
	d := time.Until(*t).Round(time.Minute)
	if d <= 0 {
		return t.Local().Format(time.DateTime) + " (expired; refreshed on next use)"
	}
	return fmt.Sprintf("%s (in %s)", t.Local().Format(time.DateTime), d)
}

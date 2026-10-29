// Package cmdutil has what every command shares: the selected context, API
// clients, output, errors and helpers for names, spec files and prompts.
package cmdutil

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/partite-ai/partite-cli/internal/auth"
	"github.com/partite-ai/partite-cli/internal/config"
	"github.com/partite-ai/partite-cli/internal/credstore"
	"github.com/partite-ai/partite-cli/internal/output"
	"golang.org/x/oauth2"
)

var Version = "dev"

// App is created once per run and passed to every command.
type App struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer

	// Set by the root command's persistent flags.
	ContextFlag string
	OutputFlag  string
	Yes         bool
	NoInput     bool

	Config      *config.File
	ContextName string
	Context     *config.Context

	format     output.Format
	httpClient *http.Client
	gql        graphql.Client
	rest       *REST
}

func NewApp() *App {
	return &App{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
}

// Init loads the config and picks the context. The root command calls it
// before any subcommand runs.
func (a *App) Init() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	a.Config = cfg
	name := a.ContextFlag
	if name == "" {
		name = os.Getenv("PARTITE_CONTEXT")
	}
	if name == "" {
		name = cfg.Current
	}
	ctx, ok := cfg.Contexts[name]
	if !ok {
		return &Error{Code: ExitUsage, Msg: fmt.Sprintf("there is no context named %q", name), Hint: "List contexts with: partite context list"}
	}
	a.ContextName = name
	c := *ctx
	if u := os.Getenv("PARTITE_URL"); u != "" {
		c.URL = u
	}
	a.Context = &c

	if a.OutputFlag != "" {
		f, err := output.ParseFormat(a.OutputFlag)
		if err != nil {
			return UsageError("%s", err)
		}
		a.format = f
	} else if output.IsTerminal(a.Out) {
		a.format = output.Table
	} else {
		a.format = output.JSON
	}
	return nil
}

func (a *App) Format() output.Format { return a.format }

func (a *App) Printer() *output.Printer {
	return &output.Printer{Out: a.Out, Format: a.format}
}

func (a *App) jsonErrors() bool {
	return a.format == output.JSON
}

// Interactive is true when the CLI may ask the user questions.
func (a *App) Interactive() bool {
	return !a.NoInput && output.IsTerminal(a.In) && output.IsTerminal(a.Err)
}

func (a *App) BaseURL() string {
	return strings.TrimRight(a.Context.URL, "/")
}

// OpenWeb prints the URL of a page of the context's web app, and opens it in
// the browser when someone is at the terminal.
func (a *App) OpenWeb(path string) error {
	if a.Context.WebURL == "" {
		return &Error{
			Code: ExitUsage,
			Msg:  fmt.Sprintf("context %s has no web app to open", a.ContextName),
			Hint: fmt.Sprintf("Use the cloud context, or set the web app's URL with: partite context set %s --web-url URL", a.ContextName),
		}
	}
	page := strings.TrimRight(a.Context.WebURL, "/") + "/" + strings.TrimLeft(path, "/")
	fmt.Fprintln(a.Out, page)
	if a.Interactive() {
		_ = auth.OpenBrowser(page)
	}
	return nil
}

// OpenRecord opens a record's page through the web app's direct link
// (link/{org}/{type}/{id}), which first switches the browser to the record's
// org. When the org is not known, as with an API key, it opens page, a format
// with %s for the id.
func (a *App) OpenRecord(linkType, page, id string) error {
	org := a.orgCode()
	if org == "" {
		return a.OpenWeb(fmt.Sprintf(page, url.PathEscape(id)))
	}
	return a.OpenWeb("link/" + url.PathEscape(org) + "/" + linkType + "/" + url.PathEscape(id))
}

// orgCode is the Kinde org of the login token, or else the context's org.
func (a *App) orgCode() string {
	if EnvAPIKeySet() {
		return ""
	}
	token := os.Getenv("PARTITE_TOKEN")
	if token == "" && a.Context.Auth == config.AuthLogin {
		if creds, _ := credstore.Get(a.ContextName); creds != nil {
			token = creds.AccessToken
		}
	}
	var claims struct {
		OrgCode string `json:"org_code"`
	}
	if token != "" && auth.DecodeClaims(token, &claims) == nil && claims.OrgCode != "" {
		return claims.OrgCode
	}
	return a.Context.Org
}

// Enterprise is true when the server has the enterprise-only features.
func (a *App) Enterprise() bool {
	if a.Context.Enterprise != nil {
		return *a.Context.Enterprise
	}
	u, err := url.Parse(a.Context.URL)
	return err == nil && strings.HasSuffix(u.Hostname(), ".partite.ai")
}

func (a *App) envCredentials() bool {
	return EnvAPIKeySet() || os.Getenv("PARTITE_TOKEN") != ""
}

// EnvAPIKeySet is true when the environment names an API key, in
// PARTITE_API_TOKEN or the older PARTITE_API_KEY.
func EnvAPIKeySet() bool {
	return os.Getenv("PARTITE_API_TOKEN") != "" || os.Getenv("PARTITE_API_KEY") != ""
}

// EnvAPIToken returns the API key token in PARTITE_API_TOKEN, or builds one
// from the older PARTITE_API_KEY and PARTITE_API_SECRET.
func EnvAPIToken() (string, error) {
	if token := os.Getenv("PARTITE_API_TOKEN"); token != "" {
		return token, nil
	}
	secret := os.Getenv("PARTITE_API_SECRET")
	if secret == "" {
		return "", UsageError("PARTITE_API_KEY is set but PARTITE_API_SECRET is not")
	}
	return auth.APIKeyToken(os.Getenv("PARTITE_API_KEY"), secret), nil
}

// PlainHTTP returns a client that sends no credentials.
func PlainHTTP() *http.Client {
	return &http.Client{Timeout: 60 * time.Second, Transport: userAgent{http.DefaultTransport}}
}

// HTTP returns a client that sends the context's credentials. It has no
// overall timeout so that streams can stay open.
func (a *App) HTTP() (*http.Client, error) {
	if a.httpClient != nil {
		return a.httpClient, nil
	}
	base := userAgent{http.DefaultTransport}
	var rt http.RoundTripper = base

	switch {
	case EnvAPIKeySet():
		token, err := EnvAPIToken()
		if err != nil {
			return nil, err
		}
		rt = &oauth2.Transport{Base: base, Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})}
	case os.Getenv("PARTITE_TOKEN") != "":
		rt = &oauth2.Transport{Base: base, Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: os.Getenv("PARTITE_TOKEN")})}
	case a.Context.Auth == config.AuthNone:
	default:
		creds, err := credstore.Get(a.ContextName)
		if err != nil {
			return nil, fmt.Errorf("read stored credentials: %w", err)
		}
		switch a.Context.Auth {
		case config.AuthAPIKey:
			var token string
			if creds != nil {
				token = creds.APIToken
				if token == "" && creds.APIKeySecret != "" && a.Context.APIKeyID != "" {
					token = auth.APIKeyToken(a.Context.APIKeyID, creds.APIKeySecret)
				}
			}
			if token == "" {
				return nil, &Error{Code: ExitAuth, Msg: fmt.Sprintf("context %s has no API key", a.ContextName), Hint: a.loginHint()}
			}
			rt = &oauth2.Transport{Base: base, Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})}
		default:
			if creds == nil || creds.AccessToken == "" {
				return nil, &Error{Code: ExitAuth, Msg: fmt.Sprintf("not logged in to %s (%s)", a.ContextName, a.Context.URL), Hint: a.loginHint()}
			}
			plain := &http.Client{Timeout: 30 * time.Second, Transport: base}
			rt = &oauth2.Transport{Base: base, Source: auth.NewTokenSource(a.ContextName, plain, creds)}
		}
	}
	a.httpClient = &http.Client{Transport: rt}
	return a.httpClient, nil
}

// GQL returns the GraphQL client for the context.
func (a *App) GQL() (graphql.Client, error) {
	if a.gql != nil {
		return a.gql, nil
	}
	hc, err := a.HTTP()
	if err != nil {
		return nil, err
	}
	a.gql = graphql.NewClient(a.BaseURL()+"/graphql", &timeoutDoer{hc, 2 * time.Minute})
	return a.gql, nil
}

// REST returns the client for the REST runtime API.
func (a *App) REST() (*REST, error) {
	if a.rest != nil {
		return a.rest, nil
	}
	hc, err := a.HTTP()
	if err != nil {
		return nil, err
	}
	a.rest = &REST{BaseURL: a.BaseURL(), HTTP: hc}
	return a.rest, nil
}

// SetREST makes REST return r. The commands that talk to a mesh use it to go
// through an access point.
func (a *App) SetREST(r *REST) { a.rest = r }

type timeoutDoer struct {
	hc      *http.Client
	timeout time.Duration
}

func (d *timeoutDoer) Do(req *http.Request) (*http.Response, error) {
	c := *d.hc
	c.Timeout = d.timeout
	return c.Do(req)
}

type userAgent struct{ base http.RoundTripper }

func (u userAgent) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", "partite-cli/"+Version)
	return u.base.RoundTrip(req)
}

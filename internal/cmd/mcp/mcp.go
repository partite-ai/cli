// Package mcp manages MCP connections, their tunnel subscriptions and relay
// keys, and calls their tools.
package mcp

import (
	"context"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Connection is the view printed by get and read by -f. Exactly one of
// http_transport and tunnel_transport is set.
type Connection struct {
	ID              string           `json:"id,omitempty"`
	Name            string           `json:"name"`
	HTTPTransport   *HTTPTransport   `json:"http_transport,omitempty"`
	TunnelTransport *TunnelTransport `json:"tunnel_transport,omitempty"`
	CreatedAt       string           `json:"created_at,omitempty"`
	UpdatedAt       string           `json:"updated_at,omitempty"`
}

// HTTPTransport reaches an MCP server at a URL. The *_wo credentials are
// write-only: they are sent when given and never read back.
type HTTPTransport struct {
	URL                    string                  `json:"url"`
	AuthenticationMethod   string                  `json:"authentication_method,omitempty"`
	Headers                []Header                `json:"headers,omitempty"`
	BasicCredentials       *BasicCredentials       `json:"basic_credentials_wo,omitempty"`
	HeaderCredentials      *HeaderCredentials      `json:"header_credentials_wo,omitempty"`
	OAuthClientCredentials *OAuthClientCredentials `json:"oauth_client_credentials_wo,omitempty"`
}

// TunnelTransport has no settings. Its subscriptions are managed with
// partite mcp subscription.
type TunnelTransport struct{}

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type BasicCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type HeaderCredentials struct {
	HeaderName  string `json:"header_name"`
	HeaderValue string `json:"header_value"`
}

type OAuthClientCredentials struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	Scopes       []string `json:"scopes,omitempty"`
}

var authMethods = []string{
	string(gql.McpAuthFlowNone),
	string(gql.McpAuthFlowBasic),
	string(gql.McpAuthFlowHeader),
	string(gql.McpAuthFlowOauth2AuthorizationCode),
	string(gql.McpAuthFlowOauth2ClientCredentials),
}

func fromAPI(d gql.ToolsMcpConnection) Connection {
	c := Connection{ID: d.Id, Name: d.Name, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
	switch {
	case d.Transport == gql.McpTransportTunnel:
		c.TunnelTransport = &TunnelTransport{}
	case d.HttpTransport != nil:
		h := &HTTPTransport{URL: d.HttpTransport.Url, AuthenticationMethod: string(d.HttpTransport.AuthFlow)}
		for _, hd := range d.HttpTransport.Headers {
			h.Headers = append(h.Headers, Header{Name: hd.Name, Value: hd.Value})
		}
		c.HTTPTransport = h
	}
	return c
}

func (c Connection) transport() string {
	if c.TunnelTransport != nil {
		return "tunnel"
	}
	return "http"
}

var Resource = &cmdutil.Resource[Connection]{
	WebList:  "tools/mcpconnections",
	WebPage:  "tools/mcpconnections/%s",
	WebLink:  "mcp",
	Kind:     "MCP connection",
	Command:  "mcp",
	IDPrefix: "cmco",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Connection, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Connection], error) {
			resp, err := gql.ToolsListMcpConnections(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[Connection]{}, err
			}
			conn := resp.McpConnections
			items := make([]Connection, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = fromAPI(e.Node)
			}
			return cmdutil.Page[Connection]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Connection, error) {
		c, err := app.GQL()
		if err != nil {
			return Connection{}, err
		}
		resp, err := gql.ToolsGetMcpConnection(ctx, c, id)
		if err != nil {
			return Connection{}, err
		}
		if resp.McpConnection == nil {
			return Connection{}, cmdutil.NotFound("MCP connection", id, "partite mcp list")
		}
		return fromAPI(*resp.McpConnection), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteMcpConnection(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(c Connection) string { return c.ID },
	Name:    func(c Connection) string { return c.Name },
	Columns: []string{"id", "name", "transport", "url", "auth", "created"},
	Row: func(c Connection) []string {
		url, auth := "", ""
		if c.HTTPTransport != nil {
			url, auth = c.HTTPTransport.URL, c.HTTPTransport.AuthenticationMethod
		}
		return []string{c.ID, c.Name, c.transport(), url, auth, cmdutil.FormatTime(c.CreatedAt)}
	},
	Fields: func(c Connection) []output.Field {
		fields := []output.Field{
			{Label: "ID", Value: c.ID},
			{Label: "Name", Value: c.Name},
			{Label: "Transport", Value: c.transport()},
		}
		if h := c.HTTPTransport; h != nil {
			headers := make([]string, len(h.Headers))
			for i, hd := range h.Headers {
				headers[i] = hd.Name + ": " + hd.Value
			}
			fields = append(fields,
				output.Field{Label: "URL", Value: h.URL},
				output.Field{Label: "Authentication", Value: h.AuthenticationMethod},
				output.Field{Label: "Headers", Value: strings.Join(headers, "\n")},
			)
		}
		return append(fields,
			output.Field{Label: "Created", Value: c.CreatedAt},
			output.Field{Label: "Updated", Value: c.UpdatedAt},
		)
	},
}

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "mcp",
		Aliases: []string{"mcp-connection", "mcp-connections"},
		Short:   "Manage MCP connections and call their tools",
		Long: `An MCP connection lets agents use the tools of an MCP server.

An HTTP connection reaches the server at a URL. Its authentication method is
one of: ` + strings.Join(authMethods, ", ") + `.
Credentials are write-only: they are sent to the server and never shown again.

A tunnel connection needs no inbound endpoint. A relay next to the MCP server
connects out to Partite with a key from partite mcp tunnel-key create, and
subscriptions decide which relays serve which conversations.`,
	}
	cmd.AddCommand(
		Resource.ListCmd(app),
		Resource.GetCmd(app),
		Resource.ViewCmd(app),
		newCreate(app),
		newUpdate(app),
		Resource.ApplyCmd(app, "  partite mcp get github -o yaml > github.yaml\n  partite mcp apply -f github.yaml", func(ctx context.Context, spec Connection, id string) (Connection, error) {
			if err := checkSpec(spec); err != nil {
				return Connection{}, err
			}
			if id == "" {
				return create(ctx, app, spec)
			}
			return update(ctx, app, id, spec)
		}),
		Resource.DeleteCmd(app),
		newTools(app),
		newCall(app),
		newTest(app),
		newAuthorize(app),
		newSubscription(app),
		newTunnelKey(app),
	)
	return cmd
}

func checkSpec(spec Connection) error {
	if (spec.HTTPTransport == nil) == (spec.TunnelTransport == nil) {
		return cmdutil.UsageError("set exactly one of http_transport and tunnel_transport")
	}
	if h := spec.HTTPTransport; h != nil {
		if h.URL == "" {
			return cmdutil.UsageError("http_transport.url is required")
		}
		if h.AuthenticationMethod == "" {
			h.AuthenticationMethod = inferAuthMethod(h)
		}
		h.AuthenticationMethod = strings.ToUpper(h.AuthenticationMethod)
		if !contains(authMethods, h.AuthenticationMethod) {
			return cmdutil.UsageError("unknown authentication method %q; use one of %s", h.AuthenticationMethod, strings.Join(authMethods, ", "))
		}
	}
	return nil
}

func inferAuthMethod(h *HTTPTransport) string {
	switch {
	case h.BasicCredentials != nil:
		return string(gql.McpAuthFlowBasic)
	case h.HeaderCredentials != nil:
		return string(gql.McpAuthFlowHeader)
	case h.OAuthClientCredentials != nil:
		return string(gql.McpAuthFlowOauth2ClientCredentials)
	}
	return string(gql.McpAuthFlowNone)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func headersToAPI(headers []Header) []gql.McpConnectionHttpHeaderInput {
	out := make([]gql.McpConnectionHttpHeaderInput, len(headers))
	for i, h := range headers {
		out[i] = gql.McpConnectionHttpHeaderInput{Name: h.Name, Value: h.Value}
	}
	return out
}

func httpSettings(h *HTTPTransport) *gql.McpConnectionHttpTransportSettings {
	return &gql.McpConnectionHttpTransportSettings{
		Url:      h.URL,
		AuthFlow: gql.McpAuthFlow(h.AuthenticationMethod),
		// The API clears the headers when this is null, so an update sends the
		// ones the connection should keep.
		Headers: headersToAPI(h.Headers),
	}
}

// apiCredentials holds the credential arguments that the create, update and
// test operations share.
type apiCredentials struct {
	oauth  *gql.McpConnectionOauthClientCredentials
	basic  *gql.McpConnectionBasicCredentials
	header *gql.McpConnectionHeaderCredentials
}

func credentialsToAPI(h *HTTPTransport) apiCredentials {
	var out apiCredentials
	if h == nil {
		return out
	}
	if c := h.OAuthClientCredentials; c != nil {
		out.oauth = &gql.McpConnectionOauthClientCredentials{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Scopes: c.Scopes}
	}
	if c := h.BasicCredentials; c != nil {
		out.basic = &gql.McpConnectionBasicCredentials{Username: c.Username, Password: c.Password}
	}
	if c := h.HeaderCredentials; c != nil {
		out.header = &gql.McpConnectionHeaderCredentials{Name: c.HeaderName, Value: c.HeaderValue}
	}
	return out
}

func create(ctx context.Context, app *cmdutil.App, spec Connection) (Connection, error) {
	c, err := app.GQL()
	if err != nil {
		return Connection{}, err
	}
	in := gql.NewMcpConnection{Name: spec.Name, Transport: gql.McpTransportTunnel}
	if spec.HTTPTransport != nil {
		in.Transport = gql.McpTransportHttp
		in.HttpTransport = httpSettings(spec.HTTPTransport)
		creds := credentialsToAPI(spec.HTTPTransport)
		in.OauthClientCredentials, in.BasicCredentials, in.HeaderCredentials = creds.oauth, creds.basic, creds.header
	}
	resp, err := gql.ToolsCreateMcpConnection(ctx, c, in)
	if err != nil {
		return Connection{}, err
	}
	return fromAPI(resp.CreateMcpConnection), nil
}

// update sets the connection to spec. Credentials are sent only when spec has
// them, so stored ones are kept otherwise.
func update(ctx context.Context, app *cmdutil.App, id string, spec Connection) (Connection, error) {
	c, err := app.GQL()
	if err != nil {
		return Connection{}, err
	}
	in := gql.UpdateMcpConnection{Name: &spec.Name}
	transport := gql.McpTransportTunnel
	if spec.HTTPTransport != nil {
		transport = gql.McpTransportHttp
		in.HttpTransport = httpSettings(spec.HTTPTransport)
		creds := credentialsToAPI(spec.HTTPTransport)
		in.OauthClientCredentials, in.BasicCredentials, in.HeaderCredentials = creds.oauth, creds.basic, creds.header
	}
	in.Transport = &transport
	resp, err := gql.ToolsUpdateMcpConnection(ctx, c, id, in)
	if err != nil {
		return Connection{}, err
	}
	return fromAPI(resp.UpdateMcpConnection), nil
}

// credentialFlags are the flags that set an HTTP connection's credentials.
type credentialFlags struct {
	username     string
	password     cmdutil.SecretFlag
	headerName   string
	headerValue  cmdutil.SecretFlag
	clientID     string
	clientSecret cmdutil.SecretFlag
	scopes       []string
}

func (f *credentialFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.username, "username", "", "user name, for BASIC authentication")
	cmdutil.AddSecretFlags(cmd, &f.password, "password", "password, for BASIC authentication")
	cmd.Flags().StringVar(&f.headerName, "header-name", "", "header that carries the credential, for HEADER authentication, such as Authorization")
	cmdutil.AddSecretFlags(cmd, &f.headerValue, "header-value", "header value, for HEADER authentication")
	cmd.Flags().StringVar(&f.clientID, "client-id", "", "OAuth client id, for OAUTH2_CLIENT_CREDENTIALS")
	cmdutil.AddSecretFlags(cmd, &f.clientSecret, "client-secret", "OAuth client secret, for OAUTH2_CLIENT_CREDENTIALS")
	cmd.Flags().StringSliceVar(&f.scopes, "oauth-scope", nil, "OAuth scope to request, for OAUTH2_CLIENT_CREDENTIALS; repeat for more")
}

// method returns the authentication method the passed flags are for, or "".
func (f *credentialFlags) method() string {
	switch {
	case f.username != "" || f.password.Given():
		return string(gql.McpAuthFlowBasic)
	case f.headerName != "" || f.headerValue.Given():
		return string(gql.McpAuthFlowHeader)
	case f.clientID != "" || f.clientSecret.Given() || len(f.scopes) > 0:
		return string(gql.McpAuthFlowOauth2ClientCredentials)
	}
	return ""
}

// fill sets h's credentials from the flags. With required, a method that needs
// credentials gets them even when no flag was passed, by asking for the
// secret. Credentials already in h, from a file, are kept when no flag is
// passed.
func (f *credentialFlags) fill(app *cmdutil.App, h *HTTPTransport, required bool) error {
	given := f.method()
	if given != "" && given != h.AuthenticationMethod {
		return cmdutil.UsageError("those credential flags are for %s, but the authentication method is %s", given, h.AuthenticationMethod)
	}
	need := given != "" || required
	switch h.AuthenticationMethod {
	case string(gql.McpAuthFlowBasic):
		if !need || (given == "" && h.BasicCredentials != nil) {
			return nil
		}
		if f.username == "" {
			return cmdutil.UsageError("--username is required for BASIC authentication")
		}
		pw, err := f.password.Read(app, "password", "Password")
		if err != nil {
			return err
		}
		h.BasicCredentials = &BasicCredentials{Username: f.username, Password: pw}
	case string(gql.McpAuthFlowHeader):
		if !need || (given == "" && h.HeaderCredentials != nil) {
			return nil
		}
		if f.headerName == "" {
			return cmdutil.UsageError("--header-name is required for HEADER authentication")
		}
		v, err := f.headerValue.Read(app, "header-value", "Value of "+f.headerName)
		if err != nil {
			return err
		}
		h.HeaderCredentials = &HeaderCredentials{HeaderName: f.headerName, HeaderValue: v}
	case string(gql.McpAuthFlowOauth2ClientCredentials):
		if !need || (given == "" && h.OAuthClientCredentials != nil) {
			return nil
		}
		if f.clientID == "" {
			return cmdutil.UsageError("--client-id is required for OAUTH2_CLIENT_CREDENTIALS authentication")
		}
		s, err := f.clientSecret.Read(app, "client-secret", "Client secret")
		if err != nil {
			return err
		}
		h.OAuthClientCredentials = &OAuthClientCredentials{ClientID: f.clientID, ClientSecret: s, Scopes: f.scopes}
	}
	return nil
}

func parseHeaders(flags []string) ([]Header, error) {
	headers := make([]Header, 0, len(flags))
	for _, f := range flags {
		name, value, ok := strings.Cut(f, "=")
		if !ok || name == "" {
			name, value, ok = strings.Cut(f, ":")
		}
		if !ok || name == "" {
			return nil, cmdutil.UsageError("--header must be name=value, got %q", f)
		}
		headers = append(headers, Header{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value)})
	}
	return headers, nil
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var spec Connection
	var file, url, auth string
	var tunnel bool
	var headers []string
	var creds credentialFlags
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an MCP connection",
		Long: `Create an MCP connection to a server at --url, or a tunnel connection with
--tunnel. Credentials are asked for on a terminal when they are not passed.

For OAUTH2_AUTHORIZATION_CODE, create the connection and then run
partite mcp authorize to log in through the browser.`,
		Example: `  partite mcp create docs --url https://mcp.example.com/mcp
  partite mcp create github --url https://api.githubcopilot.com/mcp/ \
      --header-name Authorization --header-value-env GITHUB_AUTH
  partite mcp create crm --url https://crm.example.com/mcp --auth OAUTH2_AUTHORIZATION_CODE
  partite mcp authorize crm
  partite mcp create laptop --tunnel`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file != "" {
				if err := app.ReadSpec(file, &spec); err != nil {
					return err
				}
			}
			if len(args) == 1 {
				spec.Name = args[0]
			}
			if spec.Name == "" {
				return cmdutil.UsageError("a name is required")
			}
			f := cmd.Flags()
			if tunnel {
				spec.HTTPTransport, spec.TunnelTransport = nil, &TunnelTransport{}
			}
			if f.Changed("url") || f.Changed("auth") || f.Changed("header") || creds.method() != "" {
				if tunnel {
					return cmdutil.UsageError("--tunnel cannot be combined with HTTP settings")
				}
				if spec.HTTPTransport == nil {
					spec.HTTPTransport = &HTTPTransport{}
				}
				spec.TunnelTransport = nil
			}
			if h := spec.HTTPTransport; h != nil {
				if url != "" {
					h.URL = url
				}
				if auth != "" {
					h.AuthenticationMethod = auth
				}
				if h.AuthenticationMethod == "" {
					h.AuthenticationMethod = creds.method()
				}
				if f.Changed("header") {
					hs, err := parseHeaders(headers)
					if err != nil {
						return err
					}
					h.Headers = hs
				}
			}
			if spec.HTTPTransport == nil && spec.TunnelTransport == nil {
				return cmdutil.UsageError("pass --url for an HTTP connection or --tunnel for a tunnel connection")
			}
			if err := checkSpec(spec); err != nil {
				return err
			}
			if spec.HTTPTransport != nil {
				if err := creds.fill(app, spec.HTTPTransport, true); err != nil {
					return err
				}
			}
			conn, err := create(cmd.Context(), app, spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Created MCP connection %s (%s).", conn.Name, conn.ID)
			if h := conn.HTTPTransport; h != nil && h.AuthenticationMethod == string(gql.McpAuthFlowOauth2AuthorizationCode) {
				app.Printer().Done("Log in to it with: partite mcp authorize %s", conn.Name)
			}
			if conn.TunnelTransport != nil {
				app.Printer().Done("Add a subscription with: partite mcp subscription create %s default", conn.Name)
			}
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, conn)
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "URL of the MCP server")
	cmd.Flags().StringVar(&auth, "auth", "", "authentication method: "+strings.Join(authMethods, ", ")+" (default: from the credential flags, else NONE)")
	cmd.Flags().BoolVar(&tunnel, "tunnel", false, "create a tunnel connection, served by relays")
	cmd.Flags().StringArrayVar(&headers, "header", nil, "header to send on every request, as name=value; repeat for more")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the connection from a YAML or JSON file")
	creds.add(cmd)
	_ = cmd.RegisterFlagCompletionFunc("auth", cobra.FixedCompletions(authMethods, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var name, url, auth string
	var headers []string
	var noHeaders bool
	var creds credentialFlags
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change an MCP connection's name, URL, headers or credentials",
		Long: `Change an MCP connection. Only the flags you pass change. Stored credentials
are kept unless you pass new ones or change the authentication method.`,
		Example: `  partite mcp update docs --url https://mcp.example.com/v2/mcp
  partite mcp update github --header-name Authorization --header-value @token.txt
  partite mcp update docs --auth BASIC --username bot --password-env DOCS_PASSWORD`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := Resource.Resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			cur, err := Resource.Get(cmd.Context(), app, id)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			httpChanged := f.Changed("url") || f.Changed("auth") || f.Changed("header") || noHeaders || creds.method() != ""
			if !f.Changed("name") && !httpChanged {
				return cmdutil.UsageError("nothing to change; pass --name, --url, --auth, --header or credentials")
			}
			if f.Changed("name") {
				cur.Name = name
			}
			if httpChanged {
				h := cur.HTTPTransport
				if h == nil {
					return cmdutil.UsageError("%s is a tunnel connection; only its name can change", cur.Name)
				}
				if url != "" {
					h.URL = url
				}
				authChanged := false
				switch {
				case auth != "":
					authChanged = !strings.EqualFold(auth, h.AuthenticationMethod)
					h.AuthenticationMethod = strings.ToUpper(auth)
				case creds.method() != "" && creds.method() != h.AuthenticationMethod:
					authChanged = true
					h.AuthenticationMethod = creds.method()
				}
				if noHeaders {
					h.Headers = nil
				}
				if f.Changed("header") {
					hs, err := parseHeaders(headers)
					if err != nil {
						return err
					}
					h.Headers = hs
				}
				if err := checkSpec(cur); err != nil {
					return err
				}
				if err := creds.fill(app, h, authChanged); err != nil {
					return err
				}
			}
			conn, err := update(cmd.Context(), app, id, cur)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated MCP connection %s.", conn.Name)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, conn)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&url, "url", "", "new URL of the MCP server")
	cmd.Flags().StringVar(&auth, "auth", "", "new authentication method: "+strings.Join(authMethods, ", "))
	cmd.Flags().StringArrayVar(&headers, "header", nil, "header to send on every request, as name=value, replacing the current ones; repeat for more")
	cmd.Flags().BoolVar(&noHeaders, "no-headers", false, "remove all headers")
	creds.add(cmd)
	_ = cmd.RegisterFlagCompletionFunc("auth", cobra.FixedCompletions(authMethods, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/auth"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Tool is one tool an MCP server offers.
type Tool struct {
	Name         string           `json:"name"`
	Title        string           `json:"title,omitempty"`
	Description  string           `json:"description,omitempty"`
	InputSchema  json.RawMessage  `json:"input_schema"`
	OutputSchema *json.RawMessage `json:"output_schema,omitempty"`
	Annotations  ToolAnnotations  `json:"annotations"`
}

type ToolAnnotations struct {
	ReadOnly    bool `json:"read_only"`
	Destructive bool `json:"destructive"`
	Idempotent  bool `json:"idempotent"`
	OpenWorld   bool `json:"open_world"`
}

// overrides returns the settings to call a connection with when tags are
// given. Tags pick the subscriptions a tunnel serves the call from.
func overrides(tagFlags []string) (*gql.McpConnectionSettingsOverrides, error) {
	if len(tagFlags) == 0 {
		return nil, nil
	}
	tags, err := cmdutil.ParseTags(tagFlags)
	if err != nil {
		return nil, err
	}
	return &gql.McpConnectionSettingsOverrides{Tags: cmdutil.TagsToAPI(tags)}, nil
}

func newTools(app *cmdutil.App) *cobra.Command {
	var tags []string
	cmd := &cobra.Command{
		Use:   "tools <connection>",
		Short: "List the tools an MCP connection offers",
		Long: `List the tools an MCP connection offers, read from the server now.
A tunnel connection answers from what its relays declared when they attached.`,
		Example: `  partite mcp tools github
  partite mcp tools laptop --tag developer=bob
  partite mcp tools github -o json | jq '.[].name'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := Resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			o, err := overrides(tags)
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.ToolsMcpConnectionTools(ctx, c, &id, o)
			if err != nil {
				return err
			}
			tools := make([]Tool, len(resp.McpConnectionTools))
			rows := make([][]string, len(tools))
			for i, t := range resp.McpConnectionTools {
				tools[i] = Tool{
					Name:         t.Name,
					Title:        cmdutil.Deref(t.Title),
					Description:  cmdutil.Deref(t.Description),
					InputSchema:  t.InputSchema,
					OutputSchema: t.OutputSchema,
					Annotations: ToolAnnotations{
						ReadOnly:    t.Annotations.ReadOnly,
						Destructive: t.Annotations.Destructive,
						Idempotent:  t.Annotations.Idempotent,
						OpenWorld:   t.Annotations.OpenWorld,
					},
				}
				var notes []string
				if t.Annotations.ReadOnly {
					notes = append(notes, "read-only")
				}
				if t.Annotations.Destructive {
					notes = append(notes, "destructive")
				}
				rows[i] = []string{t.Name, strings.Join(notes, ","), cmdutil.Deref(t.Description)}
			}
			return app.Printer().List(tools, []string{"name", "hints", "description"}, rows)
		},
	}
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "conversation tag, as name=value, that picks a tunnel's subscription; repeat for more")
	return cmd
}

// CallResult is what an MCP tool call returned.
type CallResult struct {
	IsError           bool             `json:"is_error"`
	Content           json.RawMessage  `json:"content"`
	StructuredContent *json.RawMessage `json:"structured_content,omitempty"`
}

func newCall(app *cmdutil.App) *cobra.Command {
	var input string
	var tags []string
	cmd := &cobra.Command{
		Use:   "call <connection> <tool>",
		Short: "Call an MCP connection's tool",
		Long: `Call one of an MCP connection's tools with JSON arguments and print what the
server answered. This calls the real tool, so whatever it does, it does.

The table format prints text content as text and other content as JSON. The
json and yaml formats print the whole result. The exit code is 1 when the tool
reports an error.`,
		Example: `  partite mcp call docs search --input '{"query": "billing"}'
  partite mcp call docs search --input @args.json -o json
  echo '{"path": "/tmp"}' | partite mcp call laptop list_files --input @- --tag developer=bob`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := Resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			arguments, err := cmdutil.ReadArguments(app, input)
			if err != nil {
				return err
			}
			o, err := overrides(tags)
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.ToolsInvokeMcpConnectionTool(ctx, c, &id, o, args[1], arguments)
			if err != nil {
				return err
			}
			r := resp.InvokeMcpConnectionTool
			result := CallResult{IsError: r.IsError, Content: r.Content, StructuredContent: r.StructuredContent}
			if app.Format() == output.Table {
				printContent(app, result.Content)
			} else if err := app.Printer().Data(result); err != nil {
				return err
			}
			if result.IsError {
				return &cmdutil.Error{Code: cmdutil.ExitError, Msg: fmt.Sprintf("tool %s reported an error", args[1])}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "tool arguments as a JSON object; @file reads a file and @- reads standard input (default: {})")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "conversation tag, as name=value, that picks a tunnel's subscription; repeat for more")
	return cmd
}

// printContent prints MCP content blocks: text as text, anything else as JSON.
func printContent(app *cmdutil.App, content json.RawMessage) {
	var blocks []json.RawMessage
	if json.Unmarshal(content, &blocks) != nil {
		_ = app.Printer().Data(content)
		return
	}
	for _, b := range blocks {
		var text struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(b, &text) == nil && text.Type == "text" {
			fmt.Fprintln(app.Out, text.Text)
			continue
		}
		_ = app.Printer().Data(b)
	}
}

// TestResult is the outcome of a connection test.
type TestResult struct {
	Completed     bool   `json:"completed"`
	Authenticated bool   `json:"authenticated"`
	Message       string `json:"message,omitempty"`
}

func newTest(app *cmdutil.App) *cobra.Command {
	var url, auth string
	var noCredentials bool
	var creds credentialFlags
	cmd := &cobra.Command{
		Use:   "test [connection]",
		Short: "Check that an MCP server is reachable and accepts its credentials",
		Long: `Open an MCP session with a server and close it again. This checks that the
endpoint is reachable, speaks MCP and accepts the credentials.

Name a connection to test it as it is stored. --url and credential flags
replace those parts for the test only. Without a connection, --url is
required. The exit code is 1 when the test fails.`,
		Example: `  partite mcp test github
  partite mcp test github --header-name Authorization --header-value-env NEW_TOKEN
  partite mcp test --url https://mcp.example.com/mcp --no-credentials`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			var id *string
			if len(args) == 1 {
				ref, err := Resource.Resolve(ctx, app, args[0])
				if err != nil {
					return err
				}
				id = &ref
			} else if url == "" {
				return cmdutil.UsageError("name a connection or pass --url")
			}
			method := strings.ToUpper(auth)
			if method == "" {
				method = creds.method()
			}
			h := &HTTPTransport{AuthenticationMethod: method}
			if err := creds.fill(app, h, method != ""); err != nil {
				return err
			}
			api := credentialsToAPI(h)
			var noCreds *bool
			if noCredentials {
				noCreds = &noCredentials
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.ToolsTestMcpConnection(ctx, c, id, cmdutil.OptString(url), api.oauth, nil, api.basic, api.header, noCreds)
			if err != nil {
				return err
			}
			r := resp.TestMcpConnection
			return printAuthResult(app, TestResult{Completed: r.Completed, Authenticated: r.Authenticated, Message: cmdutil.Deref(r.Message)})
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "URL to test instead of the connection's")
	cmd.Flags().StringVar(&auth, "auth", "", "authentication method of the credentials passed: BASIC, HEADER or OAUTH2_CLIENT_CREDENTIALS (default: from the credential flags)")
	cmd.Flags().BoolVar(&noCredentials, "no-credentials", false, "test without any credentials")
	creds.add(cmd)
	return cmd
}

func printAuthResult(app *cmdutil.App, r TestResult) error {
	if app.Format() != output.Table {
		if err := app.Printer().Data(r); err != nil {
			return err
		}
	} else if r.Authenticated {
		fmt.Fprintln(app.Out, "OK: the server answered and accepted the credentials.")
	}
	if r.Authenticated {
		return nil
	}
	return &cmdutil.Error{Code: cmdutil.ExitError, Msg: "test failed: " + orDefault(r.Message, "the server did not accept the connection")}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func newAuthorize(app *cmdutil.App) *cobra.Command {
	var clientID string
	var clientSecret cmdutil.SecretFlag
	var scopes []string
	var noBrowser bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "authorize <connection>",
		Short: "Log an MCP connection in through the browser (OAuth authorization code)",
		Long: `Log an MCP connection in to its server with OAuth. This opens the server's
login page in your browser, waits for you to finish, then saves the result on
the connection and sets its authentication method to OAUTH2_AUTHORIZATION_CODE.

The OAuth client is the connection's current one, the one given with
--client-id, or one registered with the server automatically.`,
		Example: `  partite mcp authorize crm
  partite mcp authorize crm --client-id partite --client-secret-env CRM_SECRET --oauth-scope read
  partite mcp authorize crm --no-browser`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := Resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			conn, err := Resource.Get(ctx, app, id)
			if err != nil {
				return err
			}
			if conn.HTTPTransport == nil {
				return cmdutil.UsageError("%s is a tunnel connection; only HTTP connections use OAuth", conn.Name)
			}
			var secret *string
			if clientSecret.Given() {
				if clientID == "" {
					return cmdutil.UsageError("--client-secret needs --client-id")
				}
				s, err := clientSecret.Read(app, "client-secret", "Client secret")
				if err != nil {
					return err
				}
				secret = &s
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.ToolsInitiateOAuthExchange(ctx, c, conn.HTTPTransport.URL, &id, cmdutil.OptString(clientID), secret, scopes)
			if err != nil {
				return err
			}
			exchange := resp.InitiateOAuthExchange
			fmt.Fprintf(app.Err, "Open this page to log in:\n\n  %s\n\n", exchange.RedirectUrl)
			if !noBrowser {
				if err := auth.OpenBrowser(exchange.RedirectUrl); err != nil {
					fmt.Fprintf(app.Err, "Could not open a browser: %s\n", err)
				}
			}
			fmt.Fprintln(app.Err, "Waiting for the login to finish...")
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			result, err := cmdutil.PollUntil(ctx, 2*time.Second, func(ctx context.Context) (*TestResult, error) {
				r, err := gql.ToolsOAuthExchangeResult(ctx, c, exchange.Id)
				if err != nil {
					return nil, err
				}
				res := r.OauthExchangeResult
				if !res.Completed {
					return nil, nil
				}
				return &TestResult{Completed: true, Authenticated: res.Authenticated, Message: cmdutil.Deref(res.Message)}, nil
			})
			if err != nil {
				if ctx.Err() == context.DeadlineExceeded {
					return &cmdutil.Error{Code: cmdutil.ExitError, Msg: "the login did not finish in time", Hint: "Run the command again, or raise --timeout."}
				}
				return err
			}
			if !result.Authenticated {
				return &cmdutil.Error{Code: cmdutil.ExitAuth, Msg: "login failed: " + orDefault(result.Message, "the server did not authorize the connection")}
			}
			transport := gql.McpTransportHttp
			upd, err := gql.ToolsUpdateMcpConnection(cmd.Context(), c, id, gql.UpdateMcpConnection{
				Transport: &transport,
				HttpTransport: &gql.McpConnectionHttpTransportSettings{
					Url:      conn.HTTPTransport.URL,
					AuthFlow: gql.McpAuthFlowOauth2AuthorizationCode,
					Headers:  headersToAPI(conn.HTTPTransport.Headers),
				},
				OauthAuthorizationCodeCredentials: &gql.McpConnectionOauthAuthorizationCodeCredentials{ExchangeID: exchange.Id},
			})
			if err != nil {
				return err
			}
			updated := fromAPI(upd.UpdateMcpConnection)
			app.Printer().Done("Authorized MCP connection %s.", updated.Name)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, updated)
		},
	}
	cmd.Flags().StringVar(&clientID, "client-id", "", "OAuth client id to use instead of the connection's or a newly registered one")
	cmdutil.AddSecretFlags(cmd, &clientSecret, "client-secret", "OAuth client secret, for a confidential --client-id")
	cmd.Flags().StringSliceVar(&scopes, "oauth-scope", nil, "OAuth scope to request; repeat for more")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the login URL without opening a browser")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "how long to wait for the login")
	return cmd
}

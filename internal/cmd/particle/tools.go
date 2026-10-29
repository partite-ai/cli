package particle

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

func newTools(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "tools <installed-particle>",
		Short:   "List the tools an installed particle offers",
		Example: "  partite particle tools weather\n  partite particle tools weather -o json | jq '.[].name'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := Resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.ToolsInstalledParticleTools(ctx, c, id)
			if err != nil {
				return err
			}
			if resp.InstalledParticle == nil {
				return cmdutil.NotFound("installed particle", args[0], "partite particle list")
			}
			src := resp.InstalledParticle.ActiveArtifact.Tools
			tools := make([]Tool, len(src))
			rows := make([][]string, len(src))
			for i, t := range src {
				tools[i] = Tool{Name: t.Name, Description: cmdutil.Deref(t.Description), InputSchema: t.InputSchema}
				rows[i] = []string{t.Name, tools[i].Description}
			}
			return app.Printer().List(tools, []string{"name", "description"}, rows)
		},
	}
}

// CallResult is what a particle tool returned.
type CallResult struct {
	IsError bool             `json:"is_error"`
	Output  *json.RawMessage `json:"output,omitempty"`
	Message string           `json:"message,omitempty"`
}

func newCall(app *cmdutil.App) *cobra.Command {
	var input string
	cmd := &cobra.Command{
		Use:   "call <installed-particle> <tool>",
		Short: "Call an installed particle's tool",
		Long: `Call one of an installed particle's tools with JSON arguments and print its
output. The particle runs with its configured credentials and workspaces, so
the answer is what an agent would get. The exit code is 1 when the tool
reports an error.`,
		Example: `  partite particle call weather forecast --input '{"city": "Oslo"}'
  partite particle call weather forecast --input @args.json -o json`,
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
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.ToolsInvokeInstalledParticleTool(ctx, c, id, args[1], arguments)
			if err != nil {
				return err
			}
			r := resp.InvokeInstalledParticleTool
			result := CallResult{IsError: r.IsError, Output: r.Output, Message: cmdutil.Deref(r.Message)}
			switch {
			case app.Format() != output.Table:
				if err := app.Printer().Data(result); err != nil {
					return err
				}
			case result.Output != nil:
				var text string
				if json.Unmarshal(*result.Output, &text) == nil {
					fmt.Fprintln(app.Out, text)
				} else if err := app.Printer().Data(result.Output); err != nil {
					return err
				}
			}
			if result.IsError {
				return &cmdutil.Error{Code: cmdutil.ExitError, Msg: fmt.Sprintf("tool %s failed: %s", args[1], result.Message)}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "tool arguments as a JSON object; @file reads a file and @- reads standard input (default: {})")
	return cmd
}

// PingResult is what a particle's health check returned.
type PingResult struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Details string `json:"details,omitempty"`
}

func newPing(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:   "ping <installed-particle>",
		Short: "Run an installed particle's health check",
		Long: `Run an installed particle's health check. The status is OK, DEGRADED,
UNHEALTHY, or NOT_IMPLEMENTED when the particle has no health check. The exit
code is 1 when the status is UNHEALTHY.`,
		Example: "  partite particle ping weather",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := Resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.ToolsPingInstalledParticle(ctx, c, id)
			if err != nil {
				return err
			}
			r := resp.PingInstalledParticle
			result := PingResult{Status: string(r.Status), Message: cmdutil.Deref(r.Message), Details: cmdutil.Deref(r.Details)}
			if err := app.Printer().Object(result, result.Status, []output.Field{
				{Label: "Status", Value: result.Status},
				{Label: "Message", Value: result.Message},
				{Label: "Details", Value: result.Details},
			}); err != nil {
				return err
			}
			if r.Status == gql.ParticlePingStatusUnhealthy {
				return &cmdutil.Error{Code: cmdutil.ExitError, Msg: "particle " + args[0] + " is unhealthy"}
			}
			return nil
		},
	}
}

func newAuthorize(app *cmdutil.App) *cobra.Command {
	var credRef, artifact, clientID, authURL, tokenURL, deviceURL string
	var clientSecret cmdutil.SecretFlag
	var device, noBrowser bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "authorize [installed-particle]",
		Short: "Log a particle's OAuth2 credential in through the browser",
		Long: `Log in to the service behind a particle's OAuth2 credential. This opens the
service's login page in your browser, or with --device shows a code to enter
on another device, and waits for you to finish.

With an installed particle, the result is saved on it. With --artifact
instead, the command prints an exchange id to pass to
partite particle install --oauth-exchange.

Register Partite with the service first. Its redirect URL is the server's
/oauth/callback, such as https://partite.example.com/oauth/callback.`,
		Example: `  partite particle authorize github --client-id Iv1.abc --client-secret-env GITHUB_SECRET
  partite particle authorize github --device --client-id Iv1.abc
  partite particle authorize --artifact cpar_7Hq2... --credential github=oauth --client-id Iv1.abc`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if (len(args) == 1) == (artifact != "") {
				return cmdutil.UsageError("name an installed particle or pass --artifact, not both")
			}
			if clientID == "" {
				return cmdutil.UsageError("--client-id is required: the OAuth client registered with the service")
			}
			var installed Installed
			artID := ""
			if len(args) == 1 {
				id, err := Resource.Resolve(ctx, app, args[0])
				if err != nil {
					return err
				}
				if installed, err = Resource.Get(ctx, app, id); err != nil {
					return err
				}
				artID = installed.ArtifactID
			} else {
				var err error
				if artID, err = artifactID(ctx, app, artifact); err != nil {
					return err
				}
			}
			art, err := getArtifact(ctx, app, artID)
			if err != nil {
				return err
			}
			decl, method, err := pickOAuthMethod(art, credRef)
			if err != nil {
				return err
			}
			variant := gql.ParticleOAuthExchangeVariantAuthCode
			if device || (method.OAuth2 != nil && len(method.OAuth2.Flows) == 1 && method.OAuth2.Flows[0] == string(gql.ParticleOAuth2FlowDeviceCode)) {
				variant = gql.ParticleOAuthExchangeVariantDevice
			}
			in := gql.BeginParticleOAuthExchangeInput{
				ArtifactID:       artID,
				CredentialName:   decl.Name,
				Method:           method.Name,
				Variant:          variant,
				ClientID:         clientID,
				AuthorizationUrl: cmdutil.OptString(authURL),
				TokenUrl:         cmdutil.OptString(tokenURL),
				DeviceAuthUrl:    cmdutil.OptString(deviceURL),
			}
			if variant == gql.ParticleOAuthExchangeVariantAuthCode {
				in.RedirectUrl = cmdutil.Ptr(app.BaseURL() + "/oauth/callback")
			}
			if clientSecret.Given() {
				s, err := clientSecret.Read(app, "client-secret", "Client secret")
				if err != nil {
					return err
				}
				in.ClientSecret = &s
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.ToolsBeginParticleOAuthExchange(ctx, c, in)
			if err != nil {
				return err
			}
			ex := resp.BeginParticleOAuthExchange
			open := ""
			if variant == gql.ParticleOAuthExchangeVariantDevice {
				open = cmdutil.Deref(ex.VerificationUri)
				fmt.Fprintf(app.Err, "Open this page and enter the code %s:\n\n  %s\n\n", cmdutil.Deref(ex.UserCode), open)
			} else {
				open = cmdutil.Deref(ex.AuthorizeUrl)
				fmt.Fprintf(app.Err, "Open this page to log in:\n\n  %s\n\n", open)
			}
			if !noBrowser && open != "" {
				if err := auth.OpenBrowser(open); err != nil {
					fmt.Fprintf(app.Err, "Could not open a browser: %s\n", err)
				}
			}
			fmt.Fprintln(app.Err, "Waiting for the login to finish...")
			waitCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			done, err := cmdutil.PollUntil(waitCtx, 2*time.Second, func(ctx context.Context) (*gql.ToolsParticleOAuthExchangeParticleOAuthExchange, error) {
				r, err := gql.ToolsParticleOAuthExchange(ctx, c, ex.Id)
				if err != nil {
					return nil, err
				}
				if r.ParticleOAuthExchange == nil {
					return nil, &cmdutil.Error{Code: cmdutil.ExitError, Msg: "the login expired or was lost; run the command again"}
				}
				if r.ParticleOAuthExchange.Status == gql.ParticleOAuthExchangeStatusPending {
					return nil, nil
				}
				return r.ParticleOAuthExchange, nil
			})
			if err != nil {
				if waitCtx.Err() == context.DeadlineExceeded {
					return &cmdutil.Error{Code: cmdutil.ExitError, Msg: "the login did not finish in time", Hint: "Run the command again, or raise --timeout."}
				}
				return err
			}
			if done.Status == gql.ParticleOAuthExchangeStatusFailed {
				return &cmdutil.Error{Code: cmdutil.ExitAuth, Msg: "login failed: " + cmdutil.Deref(done.ErrorMessage)}
			}
			cred := Credential{Name: decl.Name, Method: method.Name, OAuth2ExchangeID: ex.Id}
			if installed.ID == "" {
				if app.Format() != output.Table {
					return app.Printer().Data(map[string]string{"oauth2_exchange_id": ex.Id, "credential": decl.Name, "method": method.Name})
				}
				fmt.Fprintf(app.Out, "Logged in. Install with:\n\n  partite particle install <name> --artifact %s --credential %s=%s --oauth-exchange %s\n", artID, decl.Name, method.Name, ex.Id)
				return nil
			}
			p, err := updateInstalled(ctx, app, updateInput{InstalledParticleID: installed.ID, Credentials: credentialInputs([]Credential{cred})})
			if err != nil {
				return err
			}
			app.Printer().Done("Authorized credential %s of %s.", decl.Name, p.Name)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, p)
		},
	}
	cmd.Flags().StringVar(&credRef, "credential", "", "credential to log in, as name=method (default: the particle's only OAuth2 credential)")
	cmd.Flags().StringVar(&artifact, "artifact", "", "artifact id or .particle file, to log in before installing")
	cmd.Flags().StringVar(&clientID, "client-id", "", "OAuth client id registered with the service (required)")
	cmdutil.AddSecretFlags(cmd, &clientSecret, "client-secret", "OAuth client secret, for a confidential client")
	cmd.Flags().BoolVar(&device, "device", false, "use the device flow: enter a code on another device instead of a browser redirect")
	cmd.Flags().StringVar(&authURL, "authorization-url", "", "authorization URL, when the particle declares none")
	cmd.Flags().StringVar(&tokenURL, "token-url", "", "token URL, when the particle declares none")
	cmd.Flags().StringVar(&deviceURL, "device-auth-url", "", "device authorization URL, when the particle declares none")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the login URL without opening a browser")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "how long to wait for the login")
	return cmd
}

// pickOAuthMethod finds the OAuth2 method named by ref (name or name=method),
// or the artifact's only OAuth2 method when ref is "".
func pickOAuthMethod(art Artifact, ref string) (ArtifactCredential, CredentialMethod, error) {
	if ref != "" {
		name, method, _ := strings.Cut(ref, "=")
		decl, m, err := findMethod(art, name, method)
		if err != nil {
			return decl, m, err
		}
		if m.Type != string(gql.ParticleCredentialMethodTypeOauth2) {
			return decl, m, cmdutil.UsageError("credential %s method %s is %s, not OAUTH2", name, m.Name, m.Type)
		}
		return decl, m, nil
	}
	var found []string
	var decl ArtifactCredential
	var method CredentialMethod
	for _, d := range art.Credentials {
		for _, m := range d.Methods {
			if m.Type == string(gql.ParticleCredentialMethodTypeOauth2) {
				found = append(found, d.Name+"="+m.Name)
				decl, method = d, m
			}
		}
	}
	switch len(found) {
	case 0:
		return decl, method, cmdutil.UsageError("%s %s has no OAuth2 credential", art.Name, art.Version)
	case 1:
		return decl, method, nil
	}
	return decl, method, cmdutil.UsageError("%s %s has several OAuth2 methods; pass --credential with one of %s", art.Name, art.Version, strings.Join(found, ", "))
}

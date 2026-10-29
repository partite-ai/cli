package accesspoint

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// findClient resolves an access point and one of its clients, by name or id.
func findClient(ctx context.Context, app *cmdutil.App, apRef, clientRef string) (AccessPoint, Client, error) {
	ap, err := resolveAndGet(ctx, app, apRef)
	if err != nil {
		return AccessPoint{}, Client{}, err
	}
	clientID, err := cmdutil.Resolve(ctx, clientRef, "capc", "client", "access-point client", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
		named := make([]cmdutil.Named, len(ap.Clients))
		for i, c := range ap.Clients {
			named[i] = cmdutil.Named{ID: c.ID, Name: c.Name}
		}
		return named, nil
	})
	if err != nil {
		return AccessPoint{}, Client{}, err
	}
	for _, c := range ap.Clients {
		if c.ID == clientID {
			return ap, c, nil
		}
	}
	return AccessPoint{}, Client{}, cmdutil.NotFound("client", clientRef, "partite access-point client list "+apRef)
}

func readClaims(app *cmdutil.App, value string) (json.RawMessage, error) {
	v, err := app.ReadValue(value)
	if err != nil {
		return nil, err
	}
	raw := json.RawMessage(strings.TrimSpace(v))
	if !json.Valid(raw) {
		return nil, cmdutil.UsageError("--claims is not valid JSON")
	}
	return raw, nil
}

var clientColumns = []string{"id", "name", "client id", "secrets", "created"}

func clientRow(c Client) []string {
	return []string{c.ID, c.Name, c.ClientID, fmt.Sprint(len(c.Secrets)), cmdutil.FormatTime(c.CreatedAt)}
}

func showClient(app *cmdutil.App, c Client) error {
	return app.Printer().Object(c, c.ID, []output.Field{
		{Label: "ID", Value: c.ID},
		{Label: "Name", Value: c.Name},
		{Label: "Client id", Value: c.ClientID},
		{Label: "Claims", Value: string(c.Claims)},
		{Label: "Redirect URIs", Value: strings.Join(c.RedirectURIs, "\n")},
		{Label: "Secrets", Value: fmt.Sprint(len(c.Secrets))},
		{Label: "Created", Value: c.CreatedAt},
		{Label: "Updated", Value: c.UpdatedAt},
	})
}

func newClientCmd(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "client",
		Aliases: []string{"clients"},
		Short:   "Manage the clients an access point issues tokens to",
		Long: `A client is a program the access point issues tokens to. It signs in with its
client id and a secret from partite access-point secret create. Its claims are
copied into every token it gets, where boundaries can read them.

A client with redirect URIs can also use the authorization code flow, as MCP
clients do.`,
	}
	cmd.AddCommand(newClientList(app), newClientCreate(app), newClientUpdate(app), newClientDelete(app))
	return cmd
}

func newClientList(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "list <access-point>",
		Aliases: []string{"ls"},
		Short:   "List an access point's clients",
		Example: "  partite access-point client list partners",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ap, err := resolveAndGet(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			rows := make([][]string, len(ap.Clients))
			for i, c := range ap.Clients {
				rows[i] = clientRow(c)
			}
			return app.Printer().List(ap.Clients, clientColumns, rows)
		},
	}
}

func newClientCreate(app *cmdutil.App) *cobra.Command {
	var claims string
	var redirectURIs []string
	cmd := &cobra.Command{
		Use:   "create <access-point> <name>",
		Short: "Add a client to an access point",
		Long: `Add a client to an access point. It has no secret until you make one with
partite access-point secret create.

Claims are a JSON object copied into every token the client gets. They may
not set iss, sub, aud, exp, nbf, iat or jti. Redirect URIs are https URLs, or
http URLs to localhost or a loopback address, which match on any port.`,
		Example: `  partite access-point client create partners acme --claims '{"partner":"acme"}'
  partite access-point client create mcp claude --redirect-uri https://claude.ai/api/mcp/auth_callback`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			apID, err := AccessPoints.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			in := gql.NewAccessPointClient{AccessPointID: apID, Name: args[1], RedirectUris: redirectURIs}
			if claims != "" {
				raw, err := readClaims(app, claims)
				if err != nil {
					return err
				}
				in.Claims = &raw
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.AccessPointClientCreate(ctx, c, in)
			if err != nil {
				return err
			}
			client := clientFromAPI(resp.CreateAccessPointClient)
			app.Printer().Done("Created client %s (%s) with client id %s.\nMake its secret with: partite access-point secret create %s %s", client.Name, client.ID, client.ClientID, args[0], client.Name)
			if app.Format() == output.Table {
				return nil
			}
			return showClient(app, client)
		},
	}
	cmd.Flags().StringVar(&claims, "claims", "", "JSON object of claims; @file reads a file and @- reads standard input")
	cmd.Flags().StringSliceVar(&redirectURIs, "redirect-uri", nil, "where the authorization code flow may send the client back to; repeat for more")
	return cmd
}

func newClientUpdate(app *cmdutil.App) *cobra.Command {
	var name, claims string
	var redirectURIs []string
	var noRedirectURIs bool
	cmd := &cobra.Command{
		Use:     "update <access-point> <client>",
		Short:   "Change a client's name, claims or redirect URIs",
		Long:    "Change a client. Only the flags you pass change. Tokens it already has keep their old claims.",
		Example: `  partite access-point client update partners acme --claims @acme-claims.json`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			_, client, err := findClient(ctx, app, args[0], args[1])
			if err != nil {
				return err
			}
			f := cmd.Flags()
			var u gql.UpdateAccessPointClient
			if f.Changed("name") {
				u.Name = gql.Opt(name)
			}
			if f.Changed("claims") {
				raw, err := readClaims(app, claims)
				if err != nil {
					return err
				}
				u.Claims = gql.Opt(raw)
			}
			if f.Changed("redirect-uri") && noRedirectURIs {
				return cmdutil.UsageError("pass --redirect-uri or --no-redirect-uris, not both")
			}
			if f.Changed("redirect-uri") {
				u.RedirectUris = gql.Opt(redirectURIs)
			}
			if noRedirectURIs {
				u.RedirectUris = gql.OptNil[[]string](nil)
			}
			if !u.Name.Set && !u.Claims.Set && !u.RedirectUris.Set {
				return cmdutil.UsageError("nothing to change; pass --name, --claims, --redirect-uri or --no-redirect-uris")
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.AccessPointClientUpdate(ctx, c, client.ID, &u)
			if err != nil {
				return err
			}
			updated := clientFromAPI(resp.UpdateAccessPointClient)
			app.Printer().Done("Updated client %s.", updated.Name)
			if app.Format() == output.Table {
				return nil
			}
			return showClient(app, updated)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&claims, "claims", "", "JSON object of claims, replacing the current ones; @file reads a file and @- reads standard input")
	cmd.Flags().StringSliceVar(&redirectURIs, "redirect-uri", nil, "redirect URI, replacing the current ones; repeat for more")
	cmd.Flags().BoolVar(&noRedirectURIs, "no-redirect-uris", false, "remove every redirect URI")
	return cmd
}

func newClientDelete(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <access-point> <client>...",
		Aliases: []string{"rm"},
		Short:   "Delete clients, with their secrets",
		Example: "  partite access-point client delete partners acme --yes",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ids := make([]string, len(args)-1)
			for i, ref := range args[1:] {
				_, client, err := findClient(ctx, app, args[0], ref)
				if err != nil {
					return err
				}
				ids[i] = client.ID
			}
			if err := app.Confirm(fmt.Sprintf("Delete %s from access point %s?", cmdutil.Plural(len(ids), "client"), args[0])); err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			if _, err := gql.AccessPointClientDelete(ctx, c, ids); err != nil {
				return err
			}
			app.Printer().Done("Deleted %s.", cmdutil.Plural(len(ids), "client"))
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]any{"deleted": ids})
			}
			return nil
		},
	}
}

func newSecretCmd(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "secret",
		Aliases: []string{"secrets"},
		Short:   "Manage the secrets clients sign in with",
		Long: `A client signs in with its client id and a secret. The secret is shown once,
when it is made. A client can have more than one secret, so you can make a
new one, move the client to it, then delete the old one.`,
	}
	cmd.AddCommand(newSecretList(app), newSecretCreate(app), newSecretDelete(app))
	return cmd
}

func newSecretList(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "list <access-point> <client>",
		Aliases: []string{"ls"},
		Short:   "List a client's secrets",
		Long:    "List a client's secrets. A secret that stopped being used is safe to delete.",
		Example: "  partite access-point secret list partners acme",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := findClient(cmd.Context(), app, args[0], args[1])
			if err != nil {
				return err
			}
			secrets := client.Secrets
			if secrets == nil {
				secrets = []Secret{}
			}
			rows := make([][]string, len(secrets))
			for i, s := range secrets {
				rows[i] = []string{s.ID, cmdutil.FormatTime(s.LastUsedAt), cmdutil.FormatTime(s.ExpiresAt), cmdutil.FormatTime(s.CreatedAt)}
			}
			return app.Printer().List(secrets, []string{"id", "last used", "expires", "created"}, rows)
		},
	}
}

func newSecretCreate(app *cmdutil.App) *cobra.Command {
	var expiresAt string
	var expiresIn time.Duration
	cmd := &cobra.Command{
		Use:   "create <access-point> <client>",
		Short: "Make a secret for a client and print it",
		Long:  "Make a secret for a client. The secret is printed once and cannot be read again.",
		Example: `  partite access-point secret create partners acme
  partite access-point secret create partners acme --expires-in 2160h -o json | jq -r .secret`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if expiresAt != "" && expiresIn != 0 {
				return cmdutil.UsageError("pass --expires-at or --expires-in, not both")
			}
			if expiresAt != "" {
				t, err := time.Parse(time.RFC3339, expiresAt)
				if err != nil {
					return cmdutil.UsageError("--expires-at must be a time like 2026-12-31T00:00:00Z")
				}
				expiresAt = t.UTC().Format(time.RFC3339)
			}
			if expiresIn != 0 {
				expiresAt = time.Now().Add(expiresIn).UTC().Format(time.RFC3339)
			}
			_, client, err := findClient(ctx, app, args[0], args[1])
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.AccessPointClientSecretCreate(ctx, c, client.ID, cmdutil.OptString(expiresAt))
			if err != nil {
				return err
			}
			s := resp.CreateAccessPointClientSecret.ClientSecret
			secret := resp.CreateAccessPointClientSecret.Secret
			if app.Format() != output.Table {
				return app.Printer().Data(struct {
					Secret
					ClientID     string `json:"client_id"`
					ClientSecret string `json:"secret"`
				}{Secret{ID: s.Id, ExpiresAt: cmdutil.Deref(s.ExpiresAt), CreatedAt: s.CreatedAt}, client.ClientID, secret})
			}
			fmt.Fprintf(app.Out, "Made secret %s for client %s.\n\n", s.Id, client.Name)
			fmt.Fprintf(app.Out, "  client_id:     %s\n", client.ClientID)
			fmt.Fprintf(app.Out, "  client_secret: %s\n\n", secret)
			fmt.Fprintln(app.Out, "Save the secret now. It cannot be shown again.")
			return nil
		},
	}
	cmd.Flags().StringVar(&expiresAt, "expires-at", "", "when the secret stops working, such as 2026-12-31T00:00:00Z (default: never)")
	cmd.Flags().DurationVar(&expiresIn, "expires-in", 0, "how long the secret works, such as 2160h (default: forever)")
	return cmd
}

func newSecretDelete(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <access-point> <client> <secret-id>...",
		Aliases: []string{"rm"},
		Short:   "Delete client secrets",
		Long:    "Delete client secrets. A client signing in with a deleted secret is refused.",
		Example: "  partite access-point secret delete partners acme caps_2abc --yes",
		Args:    cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			_, client, err := findClient(ctx, app, args[0], args[1])
			if err != nil {
				return err
			}
			ids := args[2:]
			for _, id := range ids {
				if !slices.ContainsFunc(client.Secrets, func(s Secret) bool { return s.ID == id }) {
					return cmdutil.NotFound("secret", id, fmt.Sprintf("partite access-point secret list %s %s", args[0], args[1]))
				}
			}
			if err := app.Confirm(fmt.Sprintf("Delete %s of client %s?", cmdutil.Plural(len(ids), "secret"), client.Name)); err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			if _, err := gql.AccessPointClientSecretDelete(ctx, c, ids); err != nil {
				return err
			}
			app.Printer().Done("Deleted %s.", cmdutil.Plural(len(ids), "secret"))
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]any{"deleted": ids})
			}
			return nil
		},
	}
}

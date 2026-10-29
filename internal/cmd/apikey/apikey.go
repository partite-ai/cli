// Package apikey manages API keys.
package apikey

import (
	"context"
	"fmt"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// APIKey is the view printed by get and read by -f.
type APIKey struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Scopes      []string `json:"scopes"`
	Kid         string   `json:"kid,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

// Scopes an API key can have. Keep in step with mesh/pkg/authz/scopes.go.
var knownScopes = []string{"configuration:read", "configuration:write", "mesh:converse", "mesh:trace", "credentials:use"}

func fromAPI(d gql.ApiKeyDetails) APIKey {
	return APIKey{
		ID:          d.Id,
		Name:        d.Name,
		Description: cmdutil.Deref(d.Description),
		Scopes:      d.Scopes,
		Kid:         d.Kid,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}

var Resource = &cmdutil.Resource[APIKey]{
	WebList:  "apikeys",
	WebPage:  "apikeys/%s",
	WebLink:  "api-key",
	Kind:     "API key",
	Command:  "api-key",
	IDPrefix: "ckey",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]APIKey, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[APIKey], error) {
			resp, err := gql.ListApiKeys(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[APIKey]{}, err
			}
			conn := resp.ApiKeys
			items := make([]APIKey, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = fromAPI(e.Node)
			}
			return cmdutil.Page[APIKey]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (APIKey, error) {
		c, err := app.GQL()
		if err != nil {
			return APIKey{}, err
		}
		resp, err := gql.GetApiKey(ctx, c, id)
		if err != nil {
			return APIKey{}, err
		}
		return fromAPI(resp.ApiKey), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteApiKey(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(k APIKey) string { return k.ID },
	Name:    func(k APIKey) string { return k.Name },
	Columns: []string{"id", "name", "kid", "scopes", "created"},
	Row: func(k APIKey) []string {
		return []string{k.ID, k.Name, k.Kid, strings.Join(k.Scopes, ","), cmdutil.FormatTime(k.CreatedAt)}
	},
	Fields: func(k APIKey) []output.Field {
		return []output.Field{
			{Label: "ID", Value: k.ID},
			{Label: "Name", Value: k.Name},
			{Label: "Description", Value: k.Description},
			{Label: "Key id", Value: k.Kid},
			{Label: "Scopes", Value: strings.Join(k.Scopes, ", ")},
			{Label: "Created", Value: k.CreatedAt},
			{Label: "Updated", Value: k.UpdatedAt},
		}
	},
}

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "api-key",
		Aliases: []string{"api-keys", "apikey"},
		Short:   "Manage API keys for scripts and CI",
		Long: `API keys let scripts, CI and other servers call Partite without logging in.
Use one by setting PARTITE_API_TOKEN to its token, or store it in a context
with partite login --with-api-key.

Scopes: ` + strings.Join(knownScopes, ", "),
	}
	cmd.AddCommand(
		Resource.ListCmd(app),
		Resource.GetCmd(app),
		Resource.ViewCmd(app),
		newCreate(app),
		newUpdate(app),
		Resource.ApplyCmd(app, "  partite api-key apply -f ci-key.yaml", func(ctx context.Context, spec APIKey, id string) (APIKey, error) {
			if id == "" {
				k, _, err := create(ctx, app, spec)
				return k, err
			}
			return update(ctx, app, id, spec.Name, spec.Description, spec.Scopes)
		}),
		Resource.DeleteCmd(app),
	)
	return cmd
}

func create(ctx context.Context, app *cmdutil.App, spec APIKey) (APIKey, string, error) {
	c, err := app.GQL()
	if err != nil {
		return APIKey{}, "", err
	}
	resp, err := gql.CreateApiKey(ctx, c, spec.Name, cmdutil.OptString(spec.Description), spec.Scopes)
	if err != nil {
		return APIKey{}, "", err
	}
	return fromAPI(resp.CreateApiKey.ApiKey), resp.CreateApiKey.Token, nil
}

func update(ctx context.Context, app *cmdutil.App, id, name, description string, scopes []string) (APIKey, error) {
	c, err := app.GQL()
	if err != nil {
		return APIKey{}, err
	}
	resp, err := gql.ModifyApiKey(ctx, c, id, cmdutil.OptString(name), &description, scopes)
	if err != nil {
		return APIKey{}, err
	}
	return fromAPI(resp.UpdateApiKey), nil
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var spec APIKey
	var file string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an API key and print its token",
		Long: `Create an API key. The token is printed once and cannot be read again.

Scopes: ` + strings.Join(knownScopes, ", "),
		Example: `  partite api-key create ci --scope configuration:read --scope configuration:write
  partite api-key create bot --scope mesh:converse -o json | jq -r .token`,
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
			if len(spec.Scopes) == 0 {
				return cmdutil.UsageError("at least one --scope is required; choose from %s", strings.Join(knownScopes, ", "))
			}
			k, token, err := create(cmd.Context(), app, spec)
			if err != nil {
				return err
			}
			if app.Format() != output.Table {
				return app.Printer().Data(struct {
					APIKey
					Token string `json:"token"`
				}{k, token})
			}
			fmt.Fprintf(app.Out, "Created API key %s (%s).\n\n", k.Name, k.ID)
			fmt.Fprintf(app.Out, "  PARTITE_API_TOKEN=%s\n\n", token)
			fmt.Fprintln(app.Out, "Save the token now. It cannot be shown again.")
			return nil
		},
	}
	cmd.Flags().StringVar(&spec.Description, "description", "", "description")
	cmd.Flags().StringSliceVar(&spec.Scopes, "scope", nil, "scope to grant; repeat for more")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the key from a YAML or JSON file")
	_ = cmd.RegisterFlagCompletionFunc("scope", cobra.FixedCompletions(knownScopes, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var name, description string
	var scopes []string
	cmd := &cobra.Command{
		Use:     "update <name-or-id>",
		Short:   "Change an API key's name, description or scopes",
		Example: "  partite api-key update ci --scope configuration:read",
		Args:    cobra.ExactArgs(1),
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
			if !f.Changed("name") && !f.Changed("description") && !f.Changed("scope") {
				return cmdutil.UsageError("nothing to change; pass --name, --description or --scope")
			}
			if !f.Changed("name") {
				name = cur.Name
			}
			if !f.Changed("description") {
				description = cur.Description
			}
			if !f.Changed("scope") {
				scopes = cur.Scopes
			}
			k, err := update(cmd.Context(), app, id, name, description, scopes)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated API key %s.", k.Name)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, k)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil, "scopes, replacing the current ones; repeat for more")
	_ = cmd.RegisterFlagCompletionFunc("scope", cobra.FixedCompletions(knownScopes, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

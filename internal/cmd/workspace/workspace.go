// Package workspace manages workspaces: the files and key-values that agents
// and particles keep.
package workspace

import (
	"context"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Workspace is the view printed by get and read by -f.
type Workspace struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

var scopes = []string{string(gql.WorkspaceScopeGlobal), string(gql.WorkspaceScopeConversation)}

func fromAPI(d gql.WorkspaceDetails) Workspace {
	return Workspace{ID: d.Id, Name: d.Name, Scope: string(d.Scope), CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
}

// Resource is exported so that other commands can resolve workspace names.
var Resource = &cmdutil.Resource[Workspace]{
	WebList:  "tools/workspaces",
	WebPage:  "tools/workspaces/%s",
	WebLink:  "workspace",
	Kind:     "workspace",
	Command:  "workspace",
	IDPrefix: "cwsp",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Workspace, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Workspace], error) {
			resp, err := gql.ToolsListWorkspaces(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[Workspace]{}, err
			}
			conn := resp.Workspaces
			items := make([]Workspace, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = fromAPI(e.Node)
			}
			return cmdutil.Page[Workspace]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Workspace, error) {
		c, err := app.GQL()
		if err != nil {
			return Workspace{}, err
		}
		resp, err := gql.GetWorkspace(ctx, c, id)
		if err != nil {
			return Workspace{}, err
		}
		if resp.Workspace == nil {
			return Workspace{}, cmdutil.NotFound("workspace", id, "partite workspace list")
		}
		return fromAPI(*resp.Workspace), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteWorkspace(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(w Workspace) string { return w.ID },
	Name:    func(w Workspace) string { return w.Name },
	Columns: []string{"id", "name", "scope", "created"},
	Row: func(w Workspace) []string {
		return []string{w.ID, w.Name, w.Scope, cmdutil.FormatTime(w.CreatedAt)}
	},
	Fields: func(w Workspace) []output.Field {
		return []output.Field{
			{Label: "ID", Value: w.ID},
			{Label: "Name", Value: w.Name},
			{Label: "Scope", Value: w.Scope},
			{Label: "Created", Value: w.CreatedAt},
			{Label: "Updated", Value: w.UpdatedAt},
		}
	},
}

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspace",
		Aliases: []string{"workspaces", "ws"},
		Short:   "Manage workspaces and their files and key-values",
		Long: `A workspace holds files and key-values for agents and particles.

A GLOBAL workspace is shared by every conversation. A CONVERSATION workspace
gives each conversation its own copy; pass --conversation to the file and kv
commands to pick one.`,
	}
	// rm deletes files, so delete keeps no rm alias.
	del := Resource.DeleteCmd(app)
	del.Aliases = nil
	cmd.AddCommand(
		Resource.ListCmd(app),
		Resource.GetCmd(app),
		Resource.ViewCmd(app),
		newCreate(app),
		newRename(app),
		Resource.ApplyCmd(app, "  partite workspace apply -f notes.yaml", func(ctx context.Context, spec Workspace, id string) (Workspace, error) {
			scope := strings.ToUpper(spec.Scope)
			if id == "" {
				return create(ctx, app, spec.Name, scope)
			}
			cur, err := Resource.Get(ctx, app, id)
			if err != nil {
				return Workspace{}, err
			}
			if scope != "" && scope != cur.Scope {
				return Workspace{}, cmdutil.UsageError("workspace %s has scope %s; the scope cannot change", cur.Name, cur.Scope)
			}
			return cur, nil
		}),
		del,
		newFiles(app),
		newUpload(app),
		newDownload(app),
		newMkdir(app),
		newRm(app),
		newKV(app),
	)
	return cmd
}

func create(ctx context.Context, app *cmdutil.App, name, scope string) (Workspace, error) {
	if scope != scopes[0] && scope != scopes[1] {
		return Workspace{}, cmdutil.UsageError("scope must be one of %s", strings.Join(scopes, ", "))
	}
	c, err := app.GQL()
	if err != nil {
		return Workspace{}, err
	}
	resp, err := gql.CreateWorkspace(ctx, c, gql.NewWorkspace{Name: name, Scope: gql.WorkspaceScope(scope)})
	if err != nil {
		return Workspace{}, err
	}
	return fromAPI(resp.CreateWorkspace), nil
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var scope, file string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a workspace",
		Example: `  partite workspace create notes
  partite workspace create scratch --scope CONVERSATION`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var spec Workspace
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
			if cmd.Flags().Changed("scope") || spec.Scope == "" {
				spec.Scope = scope
			}
			w, err := create(cmd.Context(), app, spec.Name, strings.ToUpper(spec.Scope))
			if err != nil {
				return err
			}
			app.Printer().Done("Created workspace %s (%s).", w.Name, w.ID)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, w)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", string(gql.WorkspaceScopeGlobal), "GLOBAL (shared by every conversation) or CONVERSATION (one per conversation)")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the workspace from a YAML or JSON file")
	_ = cmd.RegisterFlagCompletionFunc("scope", cobra.FixedCompletions(scopes, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func newRename(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "rename <name-or-id> <new-name>",
		Aliases: []string{"update"},
		Short:   "Rename a workspace",
		Example: "  partite workspace rename notes team-notes",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := Resource.Resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.RenameWorkspace(cmd.Context(), c, id, args[1])
			if err != nil {
				return err
			}
			w := fromAPI(resp.RenameWorkspace)
			app.Printer().Done("Renamed workspace %s to %s.", args[0], w.Name)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, w)
		},
	}
}

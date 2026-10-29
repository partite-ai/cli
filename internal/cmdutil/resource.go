package cmdutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Resource describes a resource type so that the common verbs (list, get,
// delete) can be built for it. T is the resource's view: one struct with
// snake_case json tags that is printed by get and read by -f, so that
// `get -o yaml` output can be edited and applied.
type Resource[T any] struct {
	Kind     string // "API key"
	Command  string // "api-key"
	IDPrefix string // "ckey"

	// Enterprise is true when the resource exists only on enterprise servers.
	Enterprise bool
	// ReadOnly is true when the resource cannot be created or changed, so
	// there is no apply.
	ReadOnly bool

	List   func(ctx context.Context, app *App, opts ListOptions) ([]T, error)
	Get    func(ctx context.Context, app *App, id string) (T, error)
	Delete func(ctx context.Context, app *App, ids []string) error

	// WebList and WebPage are the resource's pages in the Partite web app,
	// as paths: the list, and a format with %s for the id. WebLink is the
	// record type in the web app's direct links (see App.OpenRecord). view
	// opens them; without them there is no view.
	WebList string
	WebPage string
	WebLink string

	ID      func(T) string
	Name    func(T) string
	Columns []string // first column must be the id
	Row     func(T) []string
	Fields  func(T) []output.Field
}

type ListOptions struct {
	Limit int
	// Query is a server-side search string; empty means all.
	Query string
}

// RequireEnterprise fails on servers without the enterprise features.
func (r *Resource[T]) RequireEnterprise(app *App) error {
	if r.Enterprise && !app.Enterprise() {
		return &Error{
			Code: ExitUsage,
			Msg:  fmt.Sprintf("%s need an enterprise server, and %s is not one", PluralOf(r.Kind), app.Context.URL),
			Hint: fmt.Sprintf("If it is, run: partite context set %s --enterprise=true", app.ContextName),
		}
	}
	return nil
}

// Resolve turns a name or id into an id.
func (r *Resource[T]) Resolve(ctx context.Context, app *App, ref string) (string, error) {
	if err := r.RequireEnterprise(app); err != nil {
		return "", err
	}
	return Resolve(ctx, ref, r.IDPrefix, r.Kind, r.Command, func(ctx context.Context, name string) ([]Named, error) {
		items, err := r.List(ctx, app, ListOptions{Query: name})
		if err != nil {
			return nil, err
		}
		named := make([]Named, len(items))
		for i, it := range items {
			named[i] = Named{ID: r.ID(it), Name: r.Name(it)}
		}
		return named, nil
	})
}

// FindByName returns the resource with this exact name, or false.
func (r *Resource[T]) FindByName(ctx context.Context, app *App, name string) (T, bool, error) {
	var zero T
	id, err := r.Resolve(ctx, app, name)
	if err != nil {
		if e, ok := err.(*Error); ok && e.Code == ExitNotFound {
			return zero, false, nil
		}
		return zero, false, err
	}
	v, err := r.Get(ctx, app, id)
	return v, err == nil, err
}

// Show prints one resource.
func (r *Resource[T]) Show(app *App, v T) error {
	return app.Printer().Object(v, r.ID(v), r.Fields(v))
}

func (r *Resource[T]) ListCmd(app *App) *cobra.Command {
	var opts ListOptions
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   fmt.Sprintf("List %s", PluralOf(r.Kind)),
		Example: fmt.Sprintf("  partite %s list\n  partite %s list --search prod -o json\n  partite %s list -o id", r.Command, r.Command, r.Command),
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := r.RequireEnterprise(app); err != nil {
				return err
			}
			items, err := r.List(cmd.Context(), app, opts)
			if err != nil {
				return err
			}
			rows := make([][]string, len(items))
			for i, it := range items {
				rows[i] = r.Row(it)
			}
			if items == nil {
				items = []T{}
			}
			return app.Printer().List(items, r.Columns, rows)
		},
	}
	cmd.Flags().IntVar(&opts.Limit, "limit", 0, "most results to return (default: all)")
	cmd.Flags().StringVar(&opts.Query, "search", "", "only return results matching this text")
	return cmd
}

func (r *Resource[T]) GetCmd(app *App) *cobra.Command {
	long := fmt.Sprintf("Show %s.", withArticle(r.Kind))
	if !r.ReadOnly {
		long += fmt.Sprintf(`

With -o yaml or -o json the output is in the same format that -f reads, so
you can save it, edit it and apply it:

  partite %s get NAME -o yaml > %s.yaml
  partite %s apply -f %s.yaml`, r.Command, r.fileStem(), r.Command, r.fileStem())
	}
	return &cobra.Command{
		Use:     "get <name-or-id>",
		Aliases: []string{"show"},
		Short:   fmt.Sprintf("Show %s", withArticle(r.Kind)),
		Long:    long,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := r.Resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			v, err := r.Get(cmd.Context(), app, id)
			if err != nil {
				return err
			}
			return r.Show(app, v)
		},
	}
}

func (r *Resource[T]) DeleteCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <name-or-id>...",
		Aliases: []string{"rm"},
		Short:   fmt.Sprintf("Delete %s", PluralOf(r.Kind)),
		Example: fmt.Sprintf("  partite %s delete my-%s\n  partite %s delete my-%s --yes", r.Command, r.fileStem(), r.Command, r.fileStem()),
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids := make([]string, len(args))
			for i, ref := range args {
				id, err := r.Resolve(cmd.Context(), app, ref)
				if err != nil {
					return err
				}
				ids[i] = id
			}
			what := fmt.Sprintf("%s %s", r.Kind, strings.Join(args, ", "))
			if len(args) > 1 {
				what = fmt.Sprintf("%s: %s", Plural(len(args), r.Kind), strings.Join(args, ", "))
			}
			if err := app.Confirm(fmt.Sprintf("Delete %s?", what)); err != nil {
				return err
			}
			if err := r.Delete(cmd.Context(), app, ids); err != nil {
				return err
			}
			app.Printer().Done("Deleted %s.", what)
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]any{"deleted": ids})
			}
			return nil
		},
	}
}

// ApplyCmd builds `apply -f`: create the resource when no resource has its
// name, otherwise update that one. apply gets the decoded spec and the id of
// the existing resource, or "" to create.
func (r *Resource[T]) ApplyCmd(app *App, example string, apply func(ctx context.Context, spec T, existingID string) (T, error)) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "apply -f <file>",
		Short: fmt.Sprintf("Create or update %s from a file", withArticle(r.Kind)),
		Long: fmt.Sprintf(`Create %s from a YAML or JSON file, or update the one with the same name.
Use -f - to read standard input. Fields that the server sets, such as id and
created_at, are ignored, so the output of get -o yaml can be applied as is.`, withArticle(r.Kind)),
		Example: example,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := r.RequireEnterprise(app); err != nil {
				return err
			}
			var spec T
			if err := app.ReadSpec(file, &spec); err != nil {
				return err
			}
			name := r.Name(spec)
			if name == "" {
				return UsageError("%s has no name", file)
			}
			existing, found, err := r.FindByName(cmd.Context(), app, name)
			if err != nil {
				return err
			}
			id := ""
			if found {
				id = r.ID(existing)
			}
			v, err := apply(cmd.Context(), spec, id)
			if err != nil {
				return err
			}
			if found {
				app.Printer().Done("Updated %s %s (%s).", r.Kind, r.Name(v), r.ID(v))
			} else {
				app.Printer().Done("Created %s %s (%s).", r.Kind, r.Name(v), r.ID(v))
			}
			if app.Format() == output.Table {
				return nil
			}
			return r.Show(app, v)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "YAML or JSON file, or - for standard input (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

// withArticle returns "an agent" or "a mesh". Kinds that start with an
// acronym, like "API key" and "MCP connection", are read letter by letter.
func withArticle(kind string) string {
	first := strings.Fields(kind)[0]
	if strings.ToUpper(first) == first && len(first) > 1 {
		if strings.ContainsRune("AEFHILMNORSX", rune(first[0])) {
			return "an " + kind
		}
		return "a " + kind
	}
	if strings.ContainsRune("aeiouAEIOU", rune(kind[0])) {
		return "an " + kind
	}
	return "a " + kind
}

// fileStem is the command as one word, for file names in examples:
// "memory store" gives "memory-store".
func (r *Resource[T]) fileStem() string {
	return strings.ReplaceAll(r.Command, " ", "-")
}

// ViewCmd opens the resource, or the list of them, in the Partite web app.
func (r *Resource[T]) ViewCmd(app *App) *cobra.Command {
	use, args := "view", cobra.NoArgs
	example := fmt.Sprintf("  partite %s view", r.Command)
	if r.WebPage != "" {
		use, args = "view [name-or-id]", cobra.MaximumNArgs(1)
		example += fmt.Sprintf("\n  partite %s view my-%s", r.Command, r.fileStem())
	}
	return &cobra.Command{
		Use:   use,
		Short: fmt.Sprintf("Open %s in the Partite web app", PluralOf(r.Kind)),
		Long: fmt.Sprintf(`Open %s in the Partite web app, or the list of them when no name is given.
It prints the page's URL too. The context must have a web app, which the cloud
context has.`, withArticle(r.Kind)),
		Example: example,
		Args:    args,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return app.OpenWeb(r.WebList)
			}
			id, err := r.Resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			return app.OpenRecord(r.WebLink, r.WebPage, id)
		},
	}
}

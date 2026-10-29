// Package route manages routes, which split a mesh's traffic across its
// versions.
package route

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmd/mesh"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Segment is one share of a route's traffic. An empty mesh_version_id means
// the draft. In a spec, a label or ordinal can stand in for the id.
type Segment struct {
	MeshVersionID string `json:"mesh_version_id,omitempty"`
	Label         string `json:"label,omitempty"`
	Ordinal       int    `json:"ordinal,omitempty"`
	Weight        int    `json:"weight"`
}

// Route is the view printed by get and read by -f.
type Route struct {
	ID          string    `json:"id,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	MeshID      string    `json:"mesh_id"`
	Segments    []Segment `json:"segments"`
	CreatedAt   string    `json:"created_at,omitempty"`
	UpdatedAt   string    `json:"updated_at,omitempty"`

	meshName string
}

func fromAPI(d gql.RouteView) Route {
	r := Route{
		ID:          d.Id,
		Name:        d.Name,
		Description: cmdutil.Deref(d.Description),
		MeshID:      d.MeshID,
		Segments:    make([]Segment, len(d.Segments)),
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
		meshName:    d.Mesh.Name,
	}
	for i, s := range d.Segments {
		r.Segments[i] = Segment{
			MeshVersionID: cmdutil.Deref(s.MeshVersionID),
			Label:         cmdutil.Deref(s.Label),
			Ordinal:       cmdutil.Deref(s.Ordinal),
			Weight:        s.Weight,
		}
	}
	return r
}

func formatSegments(segs []Segment) string {
	parts := make([]string, len(segs))
	for i, s := range segs {
		name := "draft"
		switch {
		case s.Label != "":
			name = s.Label
		case s.MeshVersionID != "":
			name = fmt.Sprintf("version %d", s.Ordinal)
		}
		parts[i] = fmt.Sprintf("%s %d%%", name, s.Weight)
	}
	return strings.Join(parts, ", ")
}

func listRoutes(ctx context.Context, app *cmdutil.App, meshID string, opts cmdutil.ListOptions) ([]Route, error) {
	c, err := app.GQL()
	if err != nil {
		return nil, err
	}
	return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Route], error) {
		resp, err := gql.RouteList(ctx, c, cmdutil.OptString(meshID), first, after, cmdutil.OptString(opts.Query))
		if err != nil {
			return cmdutil.Page[Route]{}, err
		}
		conn := resp.Routes
		items := make([]Route, len(conn.Edges))
		for i, e := range conn.Edges {
			items[i] = fromAPI(e.Node)
		}
		return cmdutil.Page[Route]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
	})
}

var resource = &cmdutil.Resource[Route]{
	Kind:     "route",
	Command:  "route",
	IDPrefix: "crte",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Route, error) {
		return listRoutes(ctx, app, "", opts)
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Route, error) {
		c, err := app.GQL()
		if err != nil {
			return Route{}, err
		}
		resp, err := gql.RouteGet(ctx, c, id)
		if err != nil {
			return Route{}, err
		}
		if resp.Route == nil {
			return Route{}, cmdutil.NotFound("route", id, "partite route list")
		}
		return fromAPI(*resp.Route), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		_, err = gql.RouteDelete(ctx, c, ids)
		return err
	},
	ID:      func(r Route) string { return r.ID },
	Name:    func(r Route) string { return r.Name },
	Columns: []string{"id", "name", "mesh", "segments", "created"},
	Row: func(r Route) []string {
		return []string{r.ID, r.Name, r.meshName, formatSegments(r.Segments), cmdutil.FormatTime(r.CreatedAt)}
	},
	Fields: func(r Route) []output.Field {
		return []output.Field{
			{Label: "ID", Value: r.ID},
			{Label: "Name", Value: r.Name},
			{Label: "Description", Value: r.Description},
			{Label: "Mesh", Value: fmt.Sprintf("%s (%s)", r.meshName, r.MeshID)},
			{Label: "Segments", Value: formatSegments(r.Segments)},
			{Label: "Created", Value: r.CreatedAt},
			{Label: "Updated", Value: r.UpdatedAt},
		}
	},
}

const longHelp = `A route splits a mesh's traffic across its versions by weight, for canary
releases and A/B tests. A client that starts a conversation with a route's id
(route_id) gets a version picked at random by the weights.

Each segment names a version and its share of the traffic as a percentage.
The shares add up to 100. A share of 0 keeps a version in the route without
sending it traffic. A segment with no version sends traffic to the draft.

Give segments as VERSION=WEIGHT, where VERSION is a label, an ordinal, a
version id or "draft".`

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "route",
		Aliases: []string{"routes"},
		Short:   "Split a mesh's traffic across its versions",
		Long:    longHelp,
	}
	cmd.AddCommand(
		newList(app),
		resource.GetCmd(app),
		newCreate(app),
		newUpdate(app),
		resource.ApplyCmd(app, `  # route.yaml
  name: support-canary
  mesh_id: support            # a mesh name or id
  segments:
    - label: v1
      weight: 90
    - label: v2
      weight: 10

  partite route apply -f route.yaml`, func(ctx context.Context, spec Route, id string) (Route, error) {
			if id == "" {
				return create(ctx, app, spec)
			}
			return update(ctx, app, id, spec)
		}),
		resource.DeleteCmd(app),
	)
	return cmd
}

func newList(app *cmdutil.App) *cobra.Command {
	var opts cmdutil.ListOptions
	var meshRef string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List routes",
		Example: "  partite route list\n  partite route list --mesh support -o json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			meshID := ""
			if meshRef != "" {
				id, err := mesh.Meshes.Resolve(cmd.Context(), app, meshRef)
				if err != nil {
					return err
				}
				meshID = id
			}
			items, err := listRoutes(cmd.Context(), app, meshID, opts)
			if err != nil {
				return err
			}
			rows := make([][]string, len(items))
			for i, r := range items {
				rows[i] = resource.Row(r)
			}
			if items == nil {
				items = []Route{}
			}
			return app.Printer().List(items, resource.Columns, rows)
		},
	}
	cmd.Flags().StringVar(&meshRef, "mesh", "", "only routes of this mesh")
	cmd.Flags().IntVar(&opts.Limit, "limit", 0, "most results to return (default: all)")
	cmd.Flags().StringVar(&opts.Query, "search", "", "only return results matching this text")
	return cmd
}

// parseSegments reads --segment flags in VERSION=WEIGHT form.
func parseSegments(flags []string) ([]Segment, error) {
	segs := make([]Segment, len(flags))
	for i, f := range flags {
		ref, w, ok := strings.Cut(f, "=")
		weight, err := strconv.Atoi(strings.TrimSuffix(w, "%"))
		if !ok || ref == "" || err != nil {
			return nil, cmdutil.UsageError("--segment must be VERSION=WEIGHT, such as v1=90 or draft=10; got %q", f)
		}
		segs[i] = Segment{Weight: weight}
		if ref != "draft" {
			segs[i].MeshVersionID = ref
		}
	}
	return segs, nil
}

// segmentsToAPI resolves each segment's version within the mesh.
func segmentsToAPI(ctx context.Context, app *cmdutil.App, meshID string, segs []Segment) ([]gql.NewRouteSegment, error) {
	if len(segs) == 0 {
		return nil, cmdutil.UsageError("a route needs at least one segment")
	}
	out := make([]gql.NewRouteSegment, len(segs))
	total := 0
	for i, s := range segs {
		out[i].Weight = s.Weight
		total += s.Weight
		ref := s.MeshVersionID
		switch {
		case ref == "" && s.Label != "":
			ref = s.Label
		case ref == "" && s.Ordinal > 0:
			ref = strconv.Itoa(s.Ordinal)
		}
		if ref == "" {
			continue
		}
		id, err := mesh.ResolveVersion(ctx, app, meshID, ref)
		if err != nil {
			return nil, err
		}
		out[i].MeshVersionID = &id
	}
	if total != 100 {
		return nil, cmdutil.UsageError("the segment weights add up to %d; they must add up to 100", total)
	}
	return out, nil
}

func create(ctx context.Context, app *cmdutil.App, spec Route) (Route, error) {
	if spec.MeshID == "" {
		return Route{}, cmdutil.UsageError("a mesh is required; pass --mesh")
	}
	meshID, err := mesh.Meshes.Resolve(ctx, app, spec.MeshID)
	if err != nil {
		return Route{}, err
	}
	segs, err := segmentsToAPI(ctx, app, meshID, spec.Segments)
	if err != nil {
		return Route{}, err
	}
	c, err := app.GQL()
	if err != nil {
		return Route{}, err
	}
	resp, err := gql.RouteCreate(ctx, c, gql.NewRoute{Name: spec.Name, Description: cmdutil.OptString(spec.Description), MeshID: meshID, Segments: segs})
	if err != nil {
		return Route{}, err
	}
	return fromAPI(resp.CreateRoute), nil
}

// update makes the route match spec. A route cannot move to another mesh.
func update(ctx context.Context, app *cmdutil.App, id string, spec Route) (Route, error) {
	cur, err := resource.Get(ctx, app, id)
	if err != nil {
		return Route{}, err
	}
	if spec.MeshID != "" {
		meshID, err := mesh.Meshes.Resolve(ctx, app, spec.MeshID)
		if err != nil {
			return Route{}, err
		}
		if meshID != cur.MeshID {
			return Route{}, cmdutil.UsageError("route %s belongs to mesh %s and cannot move to another mesh; delete it and create a new one", cur.Name, cur.meshName)
		}
	}
	segs, err := segmentsToAPI(ctx, app, cur.MeshID, spec.Segments)
	if err != nil {
		return Route{}, err
	}
	return modify(ctx, app, id, &gql.UpdateRoute{Name: &spec.Name, Description: &spec.Description, Segments: gql.Opt(segs)})
}

func modify(ctx context.Context, app *cmdutil.App, id string, u *gql.UpdateRoute) (Route, error) {
	c, err := app.GQL()
	if err != nil {
		return Route{}, err
	}
	resp, err := gql.RouteUpdate(ctx, c, id, u)
	if err != nil {
		return Route{}, err
	}
	return fromAPI(resp.UpdateRoute), nil
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var spec Route
	var file string
	var segments []string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a route for a mesh",
		Long:  "Create a route.\n\n" + longHelp,
		Example: `  partite route create support-canary --mesh support --segment v1=90 --segment v2=10
  partite route create support-preview --mesh support --segment draft=100`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := spec
			f := cmd.Flags()
			if file != "" {
				if err := app.ReadSpec(file, &spec); err != nil {
					return err
				}
				if f.Changed("mesh") {
					spec.MeshID = flags.MeshID
				}
				if f.Changed("description") {
					spec.Description = flags.Description
				}
			}
			if f.Changed("segment") {
				segs, err := parseSegments(segments)
				if err != nil {
					return err
				}
				spec.Segments = segs
			}
			if len(args) == 1 {
				spec.Name = args[0]
			}
			if spec.Name == "" {
				return cmdutil.UsageError("a name is required")
			}
			r, err := create(cmd.Context(), app, spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Created route %s (%s): %s.", r.Name, r.ID, formatSegments(r.Segments))
			if app.Format() == output.Table {
				return nil
			}
			return resource.Show(app, r)
		},
	}
	cmd.Flags().StringVar(&spec.MeshID, "mesh", "", "mesh name or id (required)")
	cmd.Flags().StringVar(&spec.Description, "description", "", "description")
	cmd.Flags().StringArrayVar(&segments, "segment", nil, "VERSION=WEIGHT; repeat for more (required)")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the route from a YAML or JSON file")
	return cmd
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var name, description string
	var segments []string
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change a route's name, description or split",
		Long: `Change a route. Only the flags you pass change. --segment replaces all the
route's segments, so give every one.`,
		Example: "  partite route update support-canary --segment v1=50 --segment v2=50",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			cur, err := resource.Get(ctx, app, id)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			if !f.Changed("name") && !f.Changed("description") && !f.Changed("segment") {
				return cmdutil.UsageError("nothing to change; pass --name, --description or --segment")
			}
			u := gql.UpdateRoute{}
			if f.Changed("name") {
				u.Name = &name
			}
			if f.Changed("description") {
				u.Description = &description
			}
			if f.Changed("segment") {
				segs, err := parseSegments(segments)
				if err != nil {
					return err
				}
				apiSegs, err := segmentsToAPI(ctx, app, cur.MeshID, segs)
				if err != nil {
					return err
				}
				u.Segments = gql.Opt(apiSegs)
			}
			r, err := modify(ctx, app, id, &u)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated route %s: %s.", r.Name, formatSegments(r.Segments))
			if app.Format() == output.Table {
				return nil
			}
			return resource.Show(app, r)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringArrayVar(&segments, "segment", nil, "VERSION=WEIGHT, replacing all segments; repeat for more")
	return cmd
}

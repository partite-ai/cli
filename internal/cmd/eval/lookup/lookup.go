// Package lookup turns mesh, model set and route names into ids for the
// eval and schedule commands.
package lookup

import (
	"context"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
)

// Mesh returns the id of the mesh with this name or id.
func Mesh(ctx context.Context, app *cmdutil.App, ref string) (string, error) {
	return cmdutil.Resolve(ctx, ref, "cmsh", "mesh", "mesh", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, 0, func(ctx context.Context, first int, after *string) (cmdutil.Page[cmdutil.Named], error) {
			resp, err := gql.EnterpriseFindMeshes(ctx, c, first, after, &name)
			if err != nil {
				return cmdutil.Page[cmdutil.Named]{}, err
			}
			conn := resp.Meshes
			items := make([]cmdutil.Named, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = cmdutil.Named{ID: e.Node.Id, Name: e.Node.Name}
			}
			return cmdutil.Page[cmdutil.Named]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	})
}

// ModelSet returns the id of the model set with this name or id.
func ModelSet(ctx context.Context, app *cmdutil.App, ref string) (string, error) {
	return cmdutil.Resolve(ctx, ref, "cmds", "model set", "model-set", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, 0, func(ctx context.Context, first int, after *string) (cmdutil.Page[cmdutil.Named], error) {
			resp, err := gql.EnterpriseFindModelSets(ctx, c, first, after, &name)
			if err != nil {
				return cmdutil.Page[cmdutil.Named]{}, err
			}
			conn := resp.ModelSets
			items := make([]cmdutil.Named, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = cmdutil.Named{ID: e.Node.Id, Name: e.Node.Name}
			}
			return cmdutil.Page[cmdutil.Named]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	})
}

// Route returns the id of the route of meshID with this name or id.
func Route(ctx context.Context, app *cmdutil.App, meshID, ref string) (string, error) {
	return cmdutil.Resolve(ctx, ref, "crte", "route", "route", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, 0, func(ctx context.Context, first int, after *string) (cmdutil.Page[cmdutil.Named], error) {
			resp, err := gql.EnterpriseFindRoutes(ctx, c, &meshID, first, after, &name)
			if err != nil {
				return cmdutil.Page[cmdutil.Named]{}, err
			}
			conn := resp.Routes
			items := make([]cmdutil.Named, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = cmdutil.Named{ID: e.Node.Id, Name: e.Node.Name}
			}
			return cmdutil.Page[cmdutil.Named]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	})
}

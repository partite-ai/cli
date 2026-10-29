package convui

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convapi"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/spf13/cobra"
)

// TargetFlags pick the version of a mesh that a new conversation runs on.
// A new conversation needs exactly one of them.
type TargetFlags struct {
	Version int
	Label   string
	Route   string
	Draft   bool
}

// TargetHelp says how to pick the version of a new conversation.
const TargetHelp = `A new conversation runs on one version of the mesh. Pick it with --label
(such as prod), --version (a number), --route (a route of the mesh picks the
version) or --draft (the mesh's unpublished draft).`

func (f *TargetFlags) Register(cmd *cobra.Command) {
	cmd.Flags().IntVar(&f.Version, "version", 0, "run this version number of the mesh")
	cmd.Flags().StringVar(&f.Label, "label", "", "run the mesh version with this label, such as prod")
	cmd.Flags().StringVar(&f.Route, "route", "", "let this route of the mesh (name or id) pick the version")
	cmd.Flags().BoolVar(&f.Draft, "draft", false, "run the mesh's draft, its unpublished version")
	cmd.MarkFlagsMutuallyExclusive("version", "label", "route", "draft")
}

// Set is true when any of the flags was passed.
func (f *TargetFlags) Set(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("version") || cmd.Flags().Changed("label") || cmd.Flags().Changed("route") || cmd.Flags().Changed("draft")
}

// ResolveMeshID turns a mesh name or id into an id. An id is used as it is,
// so an API key with only the mesh:converse scope works with ids.
func ResolveMeshID(ctx context.Context, app *cmdutil.App, ref string) (string, error) {
	return cmdutil.Resolve(ctx, ref, "cmsh", "mesh", "mesh", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		meshes, err := cmdutil.ListAll(ctx, 0, func(ctx context.Context, first int, after *string) (cmdutil.Page[cmdutil.Named], error) {
			resp, err := gql.RuntimeFindMeshes(ctx, c, first, after, &name)
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
		if err != nil {
			return nil, fmt.Errorf("find mesh %q: %w", name, err)
		}
		return meshes, nil
	})
}

// ResolveTarget turns a mesh name or id and the version flags into a target.
func ResolveTarget(ctx context.Context, app *cmdutil.App, meshRef string, f TargetFlags) (convapi.Target, error) {
	if f.Version == 0 && f.Label == "" && f.Route == "" && !f.Draft {
		return convapi.Target{}, &cmdutil.Error{
			Code: cmdutil.ExitUsage,
			Msg:  "pick the mesh version for the new conversation with --label, --version, --route or --draft",
			Hint: "See the mesh's labels and routes with: partite mesh get " + meshRef,
		}
	}
	id, err := ResolveMeshID(ctx, app, meshRef)
	if err != nil {
		return convapi.Target{}, err
	}
	if f.Route == "" {
		return convapi.Target{Mesh: &convapi.MeshTarget{ID: id, Version: f.Version, VersionLabel: f.Label, Draft: f.Draft}}, nil
	}
	// A route names its mesh, so the target has only the route.
	t := convapi.Target{}
	if cmdutil.IsID(f.Route, "crte") {
		t.RouteID = f.Route
		return t, nil
	}
	c, err := app.GQL()
	if err != nil {
		return t, err
	}
	resp, err := gql.RuntimeGetMesh(ctx, c, id)
	if err != nil {
		return t, err
	}
	for _, r := range resp.Mesh.Routes {
		if r.Name == f.Route {
			t.RouteID = r.Id
			return t, nil
		}
	}
	return t, cmdutil.NotFound("route", f.Route, "partite mesh get "+meshRef)
}

// CreateError explains an error from starting a conversation: a not-found
// target with a version, label or route, where the server says only that
// the mesh or route was not found, or an external id in use.
func CreateError(err error, meshRef string, body convapi.NewConversation) error {
	var se *cmdutil.StatusError
	if !errors.As(err, &se) {
		return err
	}
	if se.StatusCode == http.StatusConflict && body.ExternalID != "" {
		return &cmdutil.Error{Code: cmdutil.ExitConflict, Msg: fmt.Sprintf("a conversation with external id %q exists already", body.ExternalID), Hint: "See it with: partite conversation get " + body.ExternalID}
	}
	if se.StatusCode != http.StatusNotFound {
		return err
	}
	switch t := body.Target; {
	case t.Mesh != nil && t.Mesh.Version != 0:
		return &cmdutil.Error{Code: cmdutil.ExitNotFound, Msg: fmt.Sprintf("mesh %s has no version %d", meshRef, t.Mesh.Version)}
	case t.Mesh != nil && t.Mesh.VersionLabel != "":
		return &cmdutil.Error{Code: cmdutil.ExitNotFound, Msg: fmt.Sprintf("mesh %s has no version labeled %q", meshRef, t.Mesh.VersionLabel), Hint: "See its labels with: partite mesh get " + meshRef}
	case t.RouteID != "":
		return &cmdutil.Error{Code: cmdutil.ExitNotFound, Msg: fmt.Sprintf("mesh %s has no route %s", meshRef, t.RouteID)}
	}
	return err
}

// ResolveConversation turns a conversation id, or the external id it was
// created with, into its id.
func ResolveConversation(ctx context.Context, c *convapi.Client, ref string) (string, error) {
	if ref == "" {
		return "", cmdutil.UsageError("a conversation id is required")
	}
	if cmdutil.IsID(ref, "rcnv") {
		return ref, nil
	}
	conv, err := c.GetConversation(ctx, convapi.ExternalIDPath(ref))
	if err != nil {
		var se *cmdutil.StatusError
		if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
			return "", &cmdutil.Error{
				Code: cmdutil.ExitNotFound,
				Msg:  fmt.Sprintf("conversation %q not found", ref),
				Hint: "Name a conversation by its id (rcnv_...) or by the external id it was created with.",
			}
		}
		return "", err
	}
	return conv.ID, nil
}

// ResolveRequest returns ref, or the conversation's latest request when ref
// is empty.
func ResolveRequest(ctx context.Context, c *convapi.Client, conversationID, ref string) (string, error) {
	if ref != "" {
		return ref, nil
	}
	reqs, err := c.Requests(ctx, conversationID)
	if err != nil {
		return "", err
	}
	if len(reqs) == 0 {
		return "", cmdutil.Errorf(cmdutil.ExitNotFound, "Start one with: partite run MESH --conversation "+conversationID, "conversation %s has no requests", conversationID)
	}
	return reqs[len(reqs)-1].ID, nil
}

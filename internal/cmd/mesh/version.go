package mesh

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// MeshVersion is one saved state of a mesh's inputs, links and model set.
type MeshVersion struct {
	ID         string  `json:"id"`
	MeshID     string  `json:"mesh_id"`
	Ordinal    int     `json:"ordinal"`
	Label      string  `json:"label,omitempty"`
	Draft      bool    `json:"draft"`
	ModelSetID string  `json:"model_set_id"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	Inputs     []Input `json:"inputs,omitempty"`
	Links      []Link  `json:"links,omitempty"`

	meshName string
}

func versionFromAPI(v gql.MeshVersionView) MeshVersion {
	return MeshVersion{
		ID:         v.Id,
		MeshID:     v.Mesh.Id,
		Ordinal:    v.Ordinal,
		Label:      cmdutil.Deref(v.Label),
		Draft:      v.Mesh.DraftVersion.Id == v.Id,
		ModelSetID: v.ModelSet.Id,
		CreatedAt:  v.CreatedAt,
		UpdatedAt:  v.UpdatedAt,
		meshName:   v.Mesh.Name,
	}
}

func versionState(v MeshVersion) string {
	switch {
	case v.Draft:
		return "draft"
	case v.Label != "":
		return "published"
	}
	return "unlabelled"
}

const versionHelp = `A mesh version is a saved state of a mesh: its inputs, links and model set.

Every mesh has one draft version, where all changes go. Publishing labels the
draft, which freezes it, and opens a new draft copied from it. Clients pick a
version by its label (or ordinal), or use a route to split traffic between
versions. Requests that name neither run on the draft.

A version can be given by id (cmsv_...), by label, by ordinal number, or as
"draft".`

func listVersions(ctx context.Context, app *cmdutil.App, meshID string, opts cmdutil.ListOptions) ([]MeshVersion, error) {
	c, err := app.GQL()
	if err != nil {
		return nil, err
	}
	return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[MeshVersion], error) {
		resp, err := gql.MeshVersionList(ctx, c, meshID, first, after, cmdutil.OptString(opts.Query))
		if err != nil {
			return cmdutil.Page[MeshVersion]{}, err
		}
		if resp.Mesh == nil {
			return cmdutil.Page[MeshVersion]{}, cmdutil.NotFound("mesh", meshID, "partite mesh list")
		}
		conn := resp.Mesh.Versions
		items := make([]MeshVersion, len(conn.Edges))
		for i, e := range conn.Edges {
			items[i] = versionFromAPI(e.Node)
		}
		return cmdutil.Page[MeshVersion]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
	})
}

func getVersion(ctx context.Context, app *cmdutil.App, id string) (MeshVersion, error) {
	c, err := app.GQL()
	if err != nil {
		return MeshVersion{}, err
	}
	resp, err := gql.MeshVersionGet(ctx, c, id)
	if err != nil {
		return MeshVersion{}, err
	}
	if resp.MeshVersion == nil {
		return MeshVersion{}, cmdutil.NotFound("mesh version", id, "partite mesh versions MESH")
	}
	v := versionFromAPI(resp.MeshVersion.MeshVersionView)
	for _, in := range resp.MeshVersion.Inputs {
		v.Inputs = append(v.Inputs, inputFromAPI(in.MeshInputView))
	}
	for _, l := range resp.MeshVersion.Links {
		v.Links = append(v.Links, linkFromAPI(l.MeshLinkView))
	}
	return v, nil
}

// ResolveVersion turns a version reference into a mesh version id. ref is a
// version id, a label, an ordinal or "draft". A label wins over an ordinal.
func ResolveVersion(ctx context.Context, app *cmdutil.App, meshID, ref string) (string, error) {
	if cmdutil.IsID(ref, "cmsv") {
		return ref, nil
	}
	if ref == "draft" {
		c, err := app.GQL()
		if err != nil {
			return "", err
		}
		resp, err := gql.GetMesh(ctx, c, meshID)
		if err != nil {
			return "", err
		}
		if resp.Mesh == nil {
			return "", cmdutil.NotFound("mesh", meshID, "partite mesh list")
		}
		return resp.Mesh.DraftVersion.Id, nil
	}
	versions, err := listVersions(ctx, app, meshID, cmdutil.ListOptions{})
	if err != nil {
		return "", err
	}
	for _, v := range versions {
		if v.Label == ref {
			return v.ID, nil
		}
	}
	if n, err := strconv.Atoi(ref); err == nil {
		for _, v := range versions {
			if v.Ordinal == n {
				return v.ID, nil
			}
		}
	}
	return "", cmdutil.NotFound("mesh version", ref, "partite mesh versions "+meshID)
}

func showVersion(app *cmdutil.App, v MeshVersion) error {
	var inputs, links []string
	for _, in := range v.Inputs {
		inputs = append(inputs, fmt.Sprintf("%s -> %s", in.Name, agentLabel(in.agentName, in.AgentID, in.agentOrdinal)))
	}
	for _, l := range v.Links {
		links = append(links, fmt.Sprintf("%s: %s -> %s", l.Name, orID(l.fromName, l.FromAgentID), orID(l.toName, l.ToAgentID)))
	}
	return app.Printer().Object(v, v.ID, []output.Field{
		{Label: "ID", Value: v.ID},
		{Label: "Mesh", Value: fmt.Sprintf("%s (%s)", v.meshName, v.MeshID)},
		{Label: "Ordinal", Value: fmt.Sprint(v.Ordinal)},
		{Label: "Label", Value: v.Label},
		{Label: "State", Value: versionState(v)},
		{Label: "Model set", Value: v.ModelSetID},
		{Label: "Inputs", Value: strings.Join(inputs, "\n")},
		{Label: "Links", Value: strings.Join(links, "\n")},
		{Label: "Created", Value: v.CreatedAt},
		{Label: "Updated", Value: v.UpdatedAt},
	})
}

func newVersionCmd(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "List, show and delete a mesh's versions",
		Long:  versionHelp,
	}
	cmd.AddCommand(newVersionsList(app, "list"), newVersionGet(app), newVersionDelete(app))
	return cmd
}

func newVersionsList(app *cmdutil.App, use string) *cobra.Command {
	var opts cmdutil.ListOptions
	cmd := &cobra.Command{
		Use:     use + " <mesh>",
		Short:   "List a mesh's versions, newest first",
		Long:    versionHelp,
		Example: "  partite mesh versions support\n  partite mesh versions support -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := Meshes.Resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			items, err := listVersions(cmd.Context(), app, id, opts)
			if err != nil {
				return err
			}
			rows := make([][]string, len(items))
			for i, v := range items {
				rows[i] = []string{v.ID, fmt.Sprint(v.Ordinal), v.Label, versionState(v), cmdutil.FormatTime(v.CreatedAt)}
			}
			if items == nil {
				items = []MeshVersion{}
			}
			return app.Printer().List(items, []string{"id", "ordinal", "label", "state", "created"}, rows)
		},
	}
	if use == "list" {
		cmd.Aliases = []string{"ls"}
	}
	cmd.Flags().IntVar(&opts.Limit, "limit", 0, "most results to return (default: all)")
	cmd.Flags().StringVar(&opts.Query, "search", "", "only return versions whose label or ordinal matches this text")
	return cmd
}

// versionArgs reads either "<version-id>" or "<mesh> <version>".
func versionArgs(ctx context.Context, app *cmdutil.App, args []string) (string, error) {
	if len(args) == 1 {
		if !cmdutil.IsID(args[0], "cmsv") {
			return "", cmdutil.UsageError("give a version id (cmsv_...), or a mesh and a version label or ordinal")
		}
		return args[0], nil
	}
	meshID, err := Meshes.Resolve(ctx, app, args[0])
	if err != nil {
		return "", err
	}
	return ResolveVersion(ctx, app, meshID, args[1])
}

func newVersionGet(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "get <version-id> | <mesh> <version>",
		Aliases: []string{"show"},
		Short:   "Show a mesh version with its inputs and links",
		Long:    versionHelp,
		Example: "  partite mesh version get support v1\n  partite mesh version get support draft -o yaml\n  partite mesh version get cmsv_...",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := versionArgs(cmd.Context(), app, args)
			if err != nil {
				return err
			}
			v, err := getVersion(cmd.Context(), app, id)
			if err != nil {
				return err
			}
			return showVersion(app, v)
		},
	}
}

func newVersionDelete(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <version-id> | <mesh> <version>",
		Aliases: []string{"rm"},
		Short:   "Delete a mesh version",
		Long: `Delete a mesh version. The draft cannot be deleted. Clients that ask for the
deleted version's label stop working.`,
		Example: "  partite mesh version delete support v1\n  partite mesh version delete cmsv_... --yes",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := versionArgs(ctx, app, args)
			if err != nil {
				return err
			}
			v, err := getVersion(ctx, app, id)
			if err != nil {
				return err
			}
			if v.Draft {
				return cmdutil.UsageError("version %d is the draft of mesh %s and cannot be deleted", v.Ordinal, v.meshName)
			}
			what := fmt.Sprintf("version %d of mesh %s", v.Ordinal, v.meshName)
			if v.Label != "" {
				what = fmt.Sprintf("version %d (%s) of mesh %s", v.Ordinal, v.Label, v.meshName)
			}
			if err := app.Confirm(fmt.Sprintf("Delete %s?", what)); err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			if _, err := gql.MeshVersionDelete(ctx, c, []string{id}); err != nil {
				return err
			}
			app.Printer().Done("Deleted %s.", what)
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]any{"deleted": []string{id}})
			}
			return nil
		},
	}
}

func newPublish(app *cmdutil.App) *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "publish <mesh> --label <label>",
		Short: "Save the draft as a labelled version",
		Long: `Publish a mesh's draft: give it a label, which freezes it as a version, and
open a new draft copied from it. Later changes go to the new draft and do
not affect the published version.

Clients ask for a published version by its label, for example with
version_label when they start a conversation, or with a webhook's
mesh_version_label. A label can be used once per mesh.`,
		Example: "  partite mesh publish support --label v1",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if label == "" {
				return cmdutil.UsageError("--label is required")
			}
			meshID, err := Meshes.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			versions, err := listVersions(ctx, app, meshID, cmdutil.ListOptions{})
			if err != nil {
				return err
			}
			draft := ""
			for _, v := range versions {
				if v.Label == label {
					return cmdutil.Errorf(cmdutil.ExitConflict, "Pick another label. See them with: partite mesh versions "+args[0], "mesh %s already has a version labelled %s (version %d)", args[0], label, v.Ordinal)
				}
				if v.Draft {
					draft = v.ID
				}
			}
			if draft == "" {
				return fmt.Errorf("mesh %s has no draft version", args[0])
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			if _, err := gql.MeshVersionLabel(ctx, c, draft, label); err != nil {
				return err
			}
			v, err := getVersion(ctx, app, draft)
			if err != nil {
				return err
			}
			app.Printer().Done("Published mesh %s as version %d, labelled %s. A new draft is open for changes.", v.meshName, v.Ordinal, label)
			if app.Format() == output.Table {
				return nil
			}
			return showVersion(app, v)
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "label for the version, such as v1 (required)")
	return cmd
}

func newDraft(app *cmdutil.App) *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "draft <mesh>",
		Short: "Start a new draft from a published version",
		Long: `Replace a mesh's draft with a copy of another version, to undo changes or
to build on an older version. Without --from-version the copy is of the
newest labelled version.

The old draft is not deleted: it is kept as an unlabelled version, which you
can see with partite mesh versions and delete with partite mesh version delete.`,
		Example: "  partite mesh draft support\n  partite mesh draft support --from-version v1",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			meshID, err := Meshes.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			source := ""
			if from != "" {
				if source, err = ResolveVersion(ctx, app, meshID, from); err != nil {
					return err
				}
			} else {
				versions, err := listVersions(ctx, app, meshID, cmdutil.ListOptions{})
				if err != nil {
					return err
				}
				newest := -1
				for _, v := range versions {
					if v.Label != "" && v.Ordinal > newest {
						source, newest = v.ID, v.Ordinal
					}
				}
				if source == "" {
					return cmdutil.Errorf(cmdutil.ExitUsage, "Publish one with: partite mesh publish "+args[0]+" --label LABEL", "mesh %s has no published versions", args[0])
				}
			}
			oldDraft, err := ResolveVersion(ctx, app, meshID, "draft")
			if err != nil {
				return err
			}
			old, err := getVersion(ctx, app, oldDraft)
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.MeshVersionMakeDraft(ctx, c, source)
			if err != nil {
				return err
			}
			v, err := getVersion(ctx, app, resp.MakeDraftMeshVersion.Id)
			if err != nil {
				return err
			}
			src, err := getVersion(ctx, app, source)
			if err != nil {
				return err
			}
			app.Printer().Done("Mesh %s has a new draft (version %d), copied from version %d. The old draft is kept as version %d.", v.meshName, v.Ordinal, src.Ordinal, old.Ordinal)
			if app.Format() == output.Table {
				return nil
			}
			return showVersion(app, v)
		},
	}
	cmd.Flags().StringVar(&from, "from-version", "", "version to copy: a label, ordinal or id (default: the newest labelled version)")
	return cmd
}

func newSetAgentVersion(app *cmdutil.App) *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "set-agent-version <mesh> <agent>",
		Short: "Move every input and link of an agent to another of its versions",
		Long: `Point every input and link that uses an agent in a mesh's draft at one
version of that agent. Without --version, the agent's latest version is used.
Use it after changing an agent, to take the change into the mesh.`,
		Example: "  partite mesh set-agent-version support triage\n  partite mesh set-agent-version support triage --version cagv_...",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			meshID, err := Meshes.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			m, err := Meshes.Get(ctx, app, meshID)
			if err != nil {
				return err
			}
			refs := newAgentRefs(app)
			agentID, target, err := refs.end(ctx, "agent", args[1], version)
			if err != nil {
				return err
			}
			if target == "" {
				if target, err = refs.latestVersion(ctx, agentID); err != nil {
					return err
				}
			}
			used := map[string]bool{}
			for _, in := range deref(m.Inputs) {
				if in.AgentID == agentID {
					used[in.AgentVersionID] = true
				}
			}
			for _, l := range deref(m.Links) {
				if l.FromAgentID == agentID {
					used[l.FromAgentVersionID] = true
				}
				if l.ToAgentID == agentID {
					used[l.ToAgentVersionID] = true
				}
			}
			if len(used) == 0 {
				return cmdutil.Errorf(cmdutil.ExitNotFound, "", "mesh %s does not use agent %s", args[0], args[1])
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			moved := 0
			for v := range used {
				if v == target {
					continue
				}
				if _, err := gql.MeshSetAgentVersion(ctx, c, meshID, v, target); err != nil {
					return err
				}
				moved++
			}
			if moved == 0 {
				app.Printer().Done("Mesh %s already uses version %s of agent %s.", m.Name, target, args[1])
			} else {
				app.Printer().Done("Mesh %s now uses version %s of agent %s.", m.Name, target, args[1])
			}
			if app.Format() == output.Table {
				return nil
			}
			m, err = Meshes.Get(ctx, app, meshID)
			if err != nil {
				return err
			}
			return Meshes.Show(app, m)
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "agent version id (default: the agent's latest version)")
	return cmd
}

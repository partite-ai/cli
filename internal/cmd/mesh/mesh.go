// Package mesh manages meshes: their agents' wiring (inputs and links) and
// their versions.
package mesh

import (
	"context"
	"fmt"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmd/boundary"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Mesh is the view printed by get and read by -f. Inputs and links are the
// draft's. In a spec, leaving out inputs, links or boundary_ids leaves them as
// they are, and an empty list removes them all.
type Mesh struct {
	ID             string        `json:"id,omitempty"`
	Name           string        `json:"name"`
	Description    string        `json:"description,omitempty"`
	ModelSetID     string        `json:"model_set_id"`
	MemoryStoreID  string        `json:"memory_store_id,omitempty"`
	Tags           []cmdutil.Tag `json:"tags"`
	BoundaryIDs    *[]string     `json:"boundary_ids,omitempty"`
	DraftVersionID string        `json:"draft_version_id,omitempty"`
	CreatedAt      string        `json:"created_at,omitempty"`
	UpdatedAt      string        `json:"updated_at,omitempty"`
	Inputs         *[]Input      `json:"inputs,omitempty"`
	Links          *[]Link       `json:"links,omitempty"`

	modelSetName    string
	memoryStoreName string
	boundaryNames   []string
}

func fromAPI(d gql.MeshView) Mesh {
	m := Mesh{
		ID:             d.Id,
		Name:           d.Name,
		Description:    cmdutil.Deref(d.Description),
		ModelSetID:     d.DraftVersion.ModelSet.Id,
		Tags:           cmdutil.TagsFromAPI(d.Tags),
		DraftVersionID: d.DraftVersion.Id,
		CreatedAt:      d.CreatedAt,
		UpdatedAt:      d.UpdatedAt,
		modelSetName:   d.DraftVersion.ModelSet.Name,
	}
	if d.MemoryStore != nil {
		m.MemoryStoreID = d.MemoryStore.Id
		m.memoryStoreName = d.MemoryStore.Name
	}
	inputs := make([]Input, len(d.DraftVersion.Inputs))
	for i, in := range d.DraftVersion.Inputs {
		inputs[i] = inputFromAPI(in.MeshInputView)
	}
	links := make([]Link, len(d.DraftVersion.Links))
	for i, l := range d.DraftVersion.Links {
		links[i] = linkFromAPI(l.MeshLinkView)
	}
	m.Inputs, m.Links = &inputs, &links
	boundaryIDs := make([]string, len(d.Boundaries))
	for i, b := range d.Boundaries {
		boundaryIDs[i] = b.Id
		m.boundaryNames = append(m.boundaryNames, b.Name)
	}
	m.BoundaryIDs = &boundaryIDs
	return m
}

// Meshes is the mesh resource. Other command groups use it to resolve mesh
// names.
var Meshes = &cmdutil.Resource[Mesh]{
	WebList:  "meshes",
	WebPage:  "mesh/%s/configure",
	WebLink:  "mesh",
	Kind:     "mesh",
	Command:  "mesh",
	IDPrefix: "cmsh",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Mesh, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Mesh], error) {
			resp, err := gql.MeshList(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[Mesh]{}, err
			}
			conn := resp.Meshes
			items := make([]Mesh, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = fromAPI(e.Node)
			}
			return cmdutil.Page[Mesh]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Mesh, error) {
		c, err := app.GQL()
		if err != nil {
			return Mesh{}, err
		}
		resp, err := gql.MeshGet(ctx, c, id)
		if err != nil {
			return Mesh{}, err
		}
		if resp.Mesh == nil {
			return Mesh{}, cmdutil.NotFound("mesh", id, "partite mesh list")
		}
		return fromAPI(*resp.Mesh), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteMesh(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(m Mesh) string { return m.ID },
	Name:    func(m Mesh) string { return m.Name },
	Columns: []string{"id", "name", "model set", "inputs", "links", "created"},
	Row: func(m Mesh) []string {
		return []string{m.ID, m.Name, orID(m.modelSetName, m.ModelSetID), fmt.Sprint(len(deref(m.Inputs))), fmt.Sprint(len(deref(m.Links))), cmdutil.FormatTime(m.CreatedAt)}
	},
	Fields: func(m Mesh) []output.Field {
		var inputs, links []string
		for _, in := range deref(m.Inputs) {
			inputs = append(inputs, fmt.Sprintf("%s -> %s", in.Name, agentLabel(in.agentName, in.AgentID, in.agentOrdinal)))
		}
		for _, l := range deref(m.Links) {
			links = append(links, fmt.Sprintf("%s: %s -> %s", l.Name, orID(l.fromName, l.FromAgentID), orID(l.toName, l.ToAgentID)))
		}
		memory := ""
		if m.MemoryStoreID != "" {
			memory = fmt.Sprintf("%s (%s)", m.memoryStoreName, m.MemoryStoreID)
		}
		return []output.Field{
			{Label: "ID", Value: m.ID},
			{Label: "Name", Value: m.Name},
			{Label: "Description", Value: m.Description},
			{Label: "Model set", Value: fmt.Sprintf("%s (%s)", m.modelSetName, m.ModelSetID)},
			{Label: "Memory store", Value: memory},
			{Label: "Tags", Value: cmdutil.FormatTags(m.Tags)},
			{Label: "Boundaries", Value: strings.Join(m.boundaryNames, ", ")},
			{Label: "Draft version", Value: m.DraftVersionID},
			{Label: "Inputs", Value: strings.Join(inputs, "\n")},
			{Label: "Links", Value: strings.Join(links, "\n")},
			{Label: "Created", Value: m.CreatedAt},
			{Label: "Updated", Value: m.UpdatedAt},
		}
	},
}

func orID(name, id string) string {
	if name == "" {
		return id
	}
	return name
}

const longHelp = `A mesh is a group of agents wired together to handle requests.

Inputs are where requests enter the mesh: each names the agent that takes
the request. Links let one agent hand work to another and say what the other
agent can send back. Manage them with partite mesh input and partite mesh link,
or list them in the mesh's spec and use partite mesh apply.

Every change goes to the mesh's draft version. partite mesh publish saves the
draft as a labelled version that clients can ask for by label, and the draft
stays open for more changes. Requests that name no version or route run on
the draft.`

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "mesh",
		Aliases: []string{"meshes"},
		Short:   "Manage meshes, their inputs and links, and their versions",
		Long:    longHelp,
	}
	// The shared commands add "s" to the kind, which does not suit "mesh".
	list, del := Meshes.ListCmd(app), Meshes.DeleteCmd(app)
	list.Short, del.Short = "List meshes", "Delete meshes"
	cmd.AddCommand(
		list,
		Meshes.GetCmd(app),
		Meshes.ViewCmd(app),
		newCreate(app),
		newUpdate(app),
		newApply(app),
		del,
		newCopy(app),
		newInputCmd(app),
		newLinkCmd(app),
		newVersionsList(app, "versions"),
		newVersionCmd(app),
		newPublish(app),
		newDraft(app),
		newSetAgentVersion(app),
	)
	return cmd
}

const specExample = `  # mesh.yaml
  name: support
  description: Answers customer questions
  model_set_id: default            # a model set name or id
  boundary_ids: [no-refunds]       # boundary names or ids
  inputs:
    - name: chat
      agent_id: triage               # an agent name or id
      responses:
        - name: answer
          description: The reply to the customer
          message_type:
            type: object
            properties:
              text: {type: string, description: The reply text}
  links:
    - name: ask_billing
      description: Send billing questions to the billing agent
      from_agent_id: triage
      to_agent_id: billing
      transfers_control: true
      responses: []`

func modelSetID(ctx context.Context, app *cmdutil.App, ref string) (string, error) {
	c, err := app.GQL()
	if err != nil {
		return "", err
	}
	return cmdutil.Resolve(ctx, ref, "cmds", "model set", "model-set", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
		return cmdutil.ListAll(ctx, 0, func(ctx context.Context, first int, after *string) (cmdutil.Page[cmdutil.Named], error) {
			resp, err := gql.MeshModelSetList(ctx, c, first, after, &name)
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

func memoryStoreID(ctx context.Context, app *cmdutil.App, ref string) (string, error) {
	if cmdutil.IsID(ref, "cmst") {
		return ref, nil
	}
	c, err := app.GQL()
	if err != nil {
		return "", err
	}
	resp, err := gql.MeshMemoryStoreByName(ctx, c, ref)
	if err != nil {
		return "", err
	}
	if resp.MemoryStoreByName == nil {
		return "", cmdutil.NotFound("memory store", ref, "partite memory store list")
	}
	return resp.MemoryStoreByName.Id, nil
}

// boundaryAssociations resolves boundary names or ids.
func boundaryAssociations(ctx context.Context, app *cmdutil.App, refs []string) ([]gql.NewBoundaryMeshAssociation, error) {
	in := make([]gql.NewBoundaryMeshAssociation, len(refs))
	for i, ref := range refs {
		id, err := boundary.Boundaries.Resolve(ctx, app, ref)
		if err != nil {
			return nil, err
		}
		in[i] = gql.NewBoundaryMeshAssociation{BoundaryID: id}
	}
	return in, nil
}

// createMesh creates a mesh from spec, then adds the spec's inputs and links.
func createMesh(ctx context.Context, app *cmdutil.App, spec Mesh) (Mesh, error) {
	if spec.ModelSetID == "" {
		return Mesh{}, cmdutil.UsageError("a model set is required; pass --model-set or set model_set_id")
	}
	c, err := app.GQL()
	if err != nil {
		return Mesh{}, err
	}
	in := &gql.NewMesh{Name: spec.Name, Tags: cmdutil.TagsToAPI(spec.Tags)}
	if in.ModelSetID, err = modelSetID(ctx, app, spec.ModelSetID); err != nil {
		return Mesh{}, err
	}
	if spec.Description != "" {
		in.Description = gql.Opt(spec.Description)
	}
	if spec.MemoryStoreID != "" {
		id, err := memoryStoreID(ctx, app, spec.MemoryStoreID)
		if err != nil {
			return Mesh{}, err
		}
		in.MemoryStoreID = gql.Opt(id)
	}
	if spec.BoundaryIDs != nil {
		if in.Boundaries, err = boundaryAssociations(ctx, app, *spec.BoundaryIDs); err != nil {
			return Mesh{}, err
		}
	}
	resp, err := gql.CreateMesh(ctx, c, in)
	if err != nil {
		return Mesh{}, err
	}
	id := resp.CreateMesh.Mesh.Id
	if spec.Inputs != nil || spec.Links != nil {
		cur := Mesh{ID: id}
		if err := reconcile(ctx, app, cur, spec.Inputs, spec.Links); err != nil {
			return Mesh{}, fmt.Errorf("created mesh %s (%s), but could not wire it: %w", spec.Name, id, err)
		}
	}
	return Meshes.Get(ctx, app, id)
}

// applyMesh makes the mesh with this id match spec in full.
func applyMesh(ctx context.Context, app *cmdutil.App, id string, spec Mesh) (Mesh, error) {
	c, err := app.GQL()
	if err != nil {
		return Mesh{}, err
	}
	if spec.ModelSetID == "" {
		return Mesh{}, cmdutil.UsageError("model_set_id is required")
	}
	u := &gql.UpdateMesh{
		Name:        gql.Opt(spec.Name),
		Description: gql.Opt(spec.Description),
		Tags:        gql.Opt(cmdutil.TagsToAPI(spec.Tags)),
	}
	ms, err := modelSetID(ctx, app, spec.ModelSetID)
	if err != nil {
		return Mesh{}, err
	}
	u.ModelSetID = gql.Opt(ms)
	if spec.MemoryStoreID == "" {
		u.MemoryStoreID = gql.OptNil[string](nil)
	} else {
		store, err := memoryStoreID(ctx, app, spec.MemoryStoreID)
		if err != nil {
			return Mesh{}, err
		}
		u.MemoryStoreID = gql.Opt(store)
	}
	if spec.BoundaryIDs != nil {
		b, err := boundaryAssociations(ctx, app, *spec.BoundaryIDs)
		if err != nil {
			return Mesh{}, err
		}
		u.Boundaries = gql.Opt(b)
	}
	cur, err := Meshes.Get(ctx, app, id)
	if err != nil {
		return Mesh{}, err
	}
	if _, err := gql.ModifyMesh(ctx, c, id, u); err != nil {
		return Mesh{}, err
	}
	if err := reconcile(ctx, app, cur, spec.Inputs, spec.Links); err != nil {
		return Mesh{}, err
	}
	return Meshes.Get(ctx, app, id)
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var spec Mesh
	var file string
	var tags, boundaries []string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a mesh",
		Long: `Create a mesh. A mesh needs a model set, which picks the models its agents
run on. With -f, the file can also list the mesh's inputs and links.`,
		Example: `  partite mesh create support --model-set default
  partite mesh create support --model-set default --memory-store support-memory
  partite mesh create support --model-set default --boundary no-refunds
  partite mesh create -f mesh.yaml

` + specExample,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			flags := spec
			if file != "" {
				if err := app.ReadSpec(file, &spec); err != nil {
					return err
				}
				if f.Changed("model-set") {
					spec.ModelSetID = flags.ModelSetID
				}
				if f.Changed("memory-store") {
					spec.MemoryStoreID = flags.MemoryStoreID
				}
				if f.Changed("description") {
					spec.Description = flags.Description
				}
			}
			if f.Changed("boundary") {
				spec.BoundaryIDs = &boundaries
			}
			if len(args) == 1 {
				spec.Name = args[0]
			}
			if spec.Name == "" {
				return cmdutil.UsageError("a name is required")
			}
			if f.Changed("tag") {
				t, err := cmdutil.ParseTags(tags)
				if err != nil {
					return err
				}
				spec.Tags = t
			}
			m, err := createMesh(cmd.Context(), app, spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Created mesh %s (%s).", m.Name, m.ID)
			if app.Format() == output.Table {
				return nil
			}
			return Meshes.Show(app, m)
		},
	}
	cmd.Flags().StringVar(&spec.ModelSetID, "model-set", "", "model set name or id (required)")
	cmd.Flags().StringVar(&spec.MemoryStoreID, "memory-store", "", "memory store name or id the mesh reads and writes")
	cmd.Flags().StringVar(&spec.Description, "description", "", "description")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tag as name=value; repeat for more")
	cmd.Flags().StringSliceVar(&boundaries, "boundary", nil, "boundary name or id that checks every conversation on the mesh; repeat for more")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the mesh from a YAML or JSON file")
	return cmd
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var name, description, modelSet, memoryStore string
	var noMemory, noBoundaries bool
	var tags, boundaries []string
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change a mesh's name, description, model set, memory store, tags or boundaries",
		Long: `Change a mesh. Only the flags you pass change. To change inputs and links,
use partite mesh input, partite mesh link or partite mesh apply.`,
		Example: `  partite mesh update support --model-set fast
  partite mesh update support --no-memory-store
  partite mesh update support --boundary no-refunds --boundary pii`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := Meshes.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			f := cmd.Flags()
			var u gql.UpdateMesh
			if f.Changed("name") {
				u.Name = gql.Opt(name)
			}
			if f.Changed("description") {
				u.Description = gql.Opt(description)
			}
			if f.Changed("tag") {
				t, err := cmdutil.ParseTags(tags)
				if err != nil {
					return err
				}
				u.Tags = gql.Opt(cmdutil.TagsToAPI(t))
			}
			if f.Changed("model-set") {
				ms, err := modelSetID(ctx, app, modelSet)
				if err != nil {
					return err
				}
				u.ModelSetID = gql.Opt(ms)
			}
			if f.Changed("memory-store") && noMemory {
				return cmdutil.UsageError("pass --memory-store or --no-memory-store, not both")
			}
			if f.Changed("memory-store") {
				store, err := memoryStoreID(ctx, app, memoryStore)
				if err != nil {
					return err
				}
				u.MemoryStoreID = gql.Opt(store)
			}
			if noMemory {
				u.MemoryStoreID = gql.OptNil[string](nil)
			}
			if f.Changed("boundary") && noBoundaries {
				return cmdutil.UsageError("pass --boundary or --no-boundaries, not both")
			}
			if f.Changed("boundary") || noBoundaries {
				b, err := boundaryAssociations(ctx, app, boundaries)
				if err != nil {
					return err
				}
				u.Boundaries = gql.Opt(b)
			}
			if !u.Name.Set && !u.Description.Set && !u.Tags.Set && !u.ModelSetID.Set && !u.MemoryStoreID.Set && !u.Boundaries.Set {
				return cmdutil.UsageError("nothing to change; pass --name, --description, --tag, --model-set, --memory-store, --no-memory-store, --boundary or --no-boundaries")
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			if _, err := gql.ModifyMesh(ctx, c, id, &u); err != nil {
				return err
			}
			m, err := Meshes.Get(ctx, app, id)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated mesh %s.", m.Name)
			if app.Format() == output.Table {
				return nil
			}
			return Meshes.Show(app, m)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringVar(&modelSet, "model-set", "", "model set name or id")
	cmd.Flags().StringVar(&memoryStore, "memory-store", "", "memory store name or id")
	cmd.Flags().BoolVar(&noMemory, "no-memory-store", false, "detach the memory store; its records are kept")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tags as name=value, replacing the current ones; repeat for more")
	cmd.Flags().StringSliceVar(&boundaries, "boundary", nil, "boundary name or id, replacing the current ones; repeat for more")
	cmd.Flags().BoolVar(&noBoundaries, "no-boundaries", false, "detach every boundary")
	return cmd
}

func newApply(app *cmdutil.App) *cobra.Command {
	cmd := Meshes.ApplyCmd(app, `  partite mesh get support -o yaml > mesh.yaml
  # edit mesh.yaml
  partite mesh apply -f mesh.yaml

`+specExample, func(ctx context.Context, spec Mesh, id string) (Mesh, error) {
		if id == "" {
			return createMesh(ctx, app, spec)
		}
		return applyMesh(ctx, app, id, spec)
	})
	cmd.Long = `Create a mesh from a YAML or JSON file, or update the one with the same name
so that it matches the file. Use -f - to read standard input.

The file can list the draft's inputs and links. Apply adds the ones that are
missing, changes the ones that differ and removes the ones the file leaves
out. Leave out the inputs or links key to keep those as they are; an empty
list removes them all. boundary_ids works the same way, and takes boundary
names or ids.

Inputs are matched by name. Links are matched by id, or else by name and
from agent. Agents can be given by name or id. An agent_version_id pins a
version; without one, an existing input or link keeps its version and a new
one uses the agent's latest version.

Fields that the server sets, such as id and created_at, are ignored, so the
output of get -o yaml can be applied as is.`
	return cmd
}

func newCopy(app *cmdutil.App) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "copy <name-or-id>",
		Short: "Copy a mesh, with its draft's inputs and links",
		Long: `Copy a mesh into a new mesh. The copy gets the draft's inputs and links,
the model set, memory store, description and tags. It has no published
versions. Without --name the copy is named after the original.`,
		Example: "  partite mesh copy support --name support-experiment",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := Meshes.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.MeshCopy(ctx, c, id)
			if err != nil {
				return err
			}
			newID := resp.CopyMesh.Mesh.Id
			if name != "" {
				if _, err := gql.ModifyMesh(ctx, c, newID, &gql.UpdateMesh{Name: gql.Opt(name)}); err != nil {
					return fmt.Errorf("copied mesh to %s, but could not rename it: %w", newID, err)
				}
			}
			m, err := Meshes.Get(ctx, app, newID)
			if err != nil {
				return err
			}
			app.Printer().Done("Copied mesh %s to %s (%s).", args[0], m.Name, m.ID)
			if app.Format() == output.Table {
				return nil
			}
			return Meshes.Show(app, m)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "name for the copy")
	return cmd
}

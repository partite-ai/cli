package mesh

import (
	"context"
	"fmt"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

const linkSpecExample = `  # link.yaml
  name: ask_billing
  description: Send billing questions to the billing agent
  from_agent_id: triage            # an agent name or id
  to_agent_id: billing
  transfers_control: false
  responses:
    - name: answer
      description: The billing agent's answer
      message_type:
        type: object
        properties:
          text: {type: string, description: The reply text}`

func linkFields(l Link) []output.Field {
	var rs []string
	for _, r := range l.Responses {
		rs = append(rs, fmt.Sprintf("%s: %s", r.Name, r.Description))
	}
	return []output.Field{
		{Label: "ID", Value: l.ID},
		{Label: "Name", Value: l.Name},
		{Label: "Description", Value: l.Description},
		{Label: "Mesh", Value: l.MeshID},
		{Label: "From", Value: fmt.Sprintf("%s (%s, version %s)", l.fromName, l.FromAgentID, l.FromAgentVersionID)},
		{Label: "To", Value: fmt.Sprintf("%s (%s, version %s)", l.toName, l.ToAgentID, l.ToAgentVersionID)},
		{Label: "Transfers control", Value: fmt.Sprint(l.TransfersControl)},
		{Label: "Responses", Value: strings.Join(rs, "\n")},
		{Label: "Created", Value: l.CreatedAt},
		{Label: "Updated", Value: l.UpdatedAt},
	}
}

// meshLinks returns the mesh's id and the draft's links, each with MeshID set.
func meshLinks(ctx context.Context, app *cmdutil.App, ref string) (string, []Link, error) {
	id, err := Meshes.Resolve(ctx, app, ref)
	if err != nil {
		return "", nil, err
	}
	m, err := Meshes.Get(ctx, app, id)
	if err != nil {
		return "", nil, err
	}
	links := deref(m.Links)
	for i := range links {
		links[i].MeshID = id
	}
	return id, links, nil
}

// findLink finds a link by its id or name. Link ids are not in the usual id
// format, so they are matched directly.
func findLink(ctx context.Context, links []Link, mesh, ref string) (Link, error) {
	for _, l := range links {
		if l.ID == ref {
			return l, nil
		}
	}
	id, err := cmdutil.Resolve(ctx, ref, "clns", "link", "mesh link", func(ctx context.Context, name string) ([]cmdutil.Named, error) {
		named := make([]cmdutil.Named, len(links))
		for i, l := range links {
			named[i] = cmdutil.Named{ID: l.ID, Name: l.Name}
		}
		return named, nil
	})
	if err != nil {
		return Link{}, err
	}
	for _, l := range links {
		if l.ID == id {
			return l, nil
		}
	}
	return Link{}, cmdutil.NotFound("link", ref, "partite mesh link list "+mesh)
}

func showLink(ctx context.Context, app *cmdutil.App, mesh, ref string) error {
	_, links, err := meshLinks(ctx, app, mesh)
	if err != nil {
		return err
	}
	l, err := findLink(ctx, links, mesh, ref)
	if err != nil {
		return err
	}
	return app.Printer().Object(l, l.ID, linkFields(l))
}

func newLinkCmd(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "link",
		Aliases: []string{"links"},
		Short:   "Manage a mesh's links between agents",
		Long: `A link lets one agent of a mesh (from) send work to another (to). The from
agent sees the link as a tool named after it, with the description telling
it when to use the link.

A link that transfers control hands the conversation over: the to agent
carries on with the user. A link that does not acts like a call: the to agent
does the work and replies with one of the link's responses, each with a JSON
schema. Such a link needs at least one response.

A response's message_type is the JSON schema of an object, and each of its
properties needs a description.

Links are given by id (clns_...) or by name. Changes go to the mesh's draft
version.`,
	}
	cmd.AddCommand(newLinkList(app), newLinkGet(app), newLinkAdd(app), newLinkUpdate(app), newLinkRemove(app))
	return cmd
}

func newLinkList(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "list <mesh>",
		Aliases: []string{"ls"},
		Short:   "List a mesh's links",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, links, err := meshLinks(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			rows := make([][]string, len(links))
			for i, l := range links {
				rows[i] = []string{l.ID, l.Name, orID(l.fromName, l.FromAgentID), orID(l.toName, l.ToAgentID), fmt.Sprint(l.TransfersControl), formatResponses(l.Responses)}
			}
			if links == nil {
				links = []Link{}
			}
			return app.Printer().List(links, []string{"id", "name", "from", "to", "transfers", "responses"}, rows)
		},
	}
}

func newLinkGet(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "get <mesh> <link>",
		Aliases: []string{"show"},
		Short:   "Show one of a mesh's links",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return showLink(cmd.Context(), app, args[0], args[1])
		},
	}
}

func newLinkAdd(app *cmdutil.App) *cobra.Command {
	var spec Link
	var file string
	cmd := &cobra.Command{
		Use:   "add <mesh> [name]",
		Short: "Add a link between two agents of a mesh",
		Long: `Add a link to a mesh's draft. Give the agents with --from and --to, by name
or id. Without --from-version or --to-version, the link uses each agent's
latest version. Responses come from -f.`,
		Example: `  partite mesh link add support ask_billing --from triage --to billing --transfers-control \
    --description "Hand billing questions to the billing agent"
  partite mesh link add support -f link.yaml

` + linkSpecExample,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			flags := spec
			f := cmd.Flags()
			if file != "" {
				if err := app.ReadSpec(file, &spec); err != nil {
					return err
				}
				if f.Changed("from") {
					spec.FromAgentID = flags.FromAgentID
				}
				if f.Changed("from-version") {
					spec.FromAgentVersionID = flags.FromAgentVersionID
				}
				if f.Changed("to") {
					spec.ToAgentID = flags.ToAgentID
				}
				if f.Changed("to-version") {
					spec.ToAgentVersionID = flags.ToAgentVersionID
				}
				if f.Changed("description") {
					spec.Description = flags.Description
				}
				if f.Changed("transfers-control") {
					spec.TransfersControl = flags.TransfersControl
				}
			}
			if len(args) == 2 {
				spec.Name = args[1]
			}
			if spec.Name == "" {
				return cmdutil.UsageError("a name is required")
			}
			meshID, _, err := meshLinks(ctx, app, args[0])
			if err != nil {
				return err
			}
			want, err := newAgentRefs(app).resolveLink(ctx, spec)
			if err != nil {
				return err
			}
			id, err := addLink(ctx, app, meshID, want)
			if err != nil {
				return err
			}
			app.Printer().Done("Added link %s (%s) to mesh %s.", spec.Name, id, args[0])
			if app.Format() == output.Table {
				return nil
			}
			return showLink(ctx, app, meshID, id)
		},
	}
	cmd.Flags().StringVar(&spec.FromAgentID, "from", "", "name or id of the agent the link runs from")
	cmd.Flags().StringVar(&spec.FromAgentVersionID, "from-version", "", "version id of the from agent to pin")
	cmd.Flags().StringVar(&spec.ToAgentID, "to", "", "name or id of the agent the link runs to")
	cmd.Flags().StringVar(&spec.ToAgentVersionID, "to-version", "", "version id of the to agent to pin")
	cmd.Flags().StringVar(&spec.Description, "description", "", "when the from agent should use the link")
	cmd.Flags().BoolVar(&spec.TransfersControl, "transfers-control", false, "hand the conversation to the to agent")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the link from a YAML or JSON file")
	return cmd
}

func newLinkUpdate(app *cmdutil.App) *cobra.Command {
	var name, description, from, fromVersion, to, toVersion, file string
	var transfers bool
	cmd := &cobra.Command{
		Use:   "update <mesh> <link>",
		Short: "Change a link's name, description, agents or responses",
		Long: `Change one of a mesh's links. Only the flags you pass change. With -f, the
file's responses and transfers_control replace the link's, and its other
fields replace the link's where they are set.

--from and --to move an end to another agent's latest version; --from-version
and --to-version pin a version.`,
		Example: `  partite mesh link update support ask_billing --description "Billing and refunds"
  partite mesh link update support ask_billing -f link.yaml`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			_, links, err := meshLinks(ctx, app, args[0])
			if err != nil {
				return err
			}
			cur, err := findLink(ctx, links, args[0], args[1])
			if err != nil {
				return err
			}
			want := cur
			want.FromAgentVersionID, want.ToAgentVersionID = "", ""
			if file != "" {
				var spec Link
				if err := app.ReadSpec(file, &spec); err != nil {
					return err
				}
				want.Responses = spec.Responses
				want.TransfersControl = spec.TransfersControl
				if spec.Name != "" {
					want.Name = spec.Name
				}
				if spec.Description != "" {
					want.Description = spec.Description
				}
				if spec.FromAgentID != "" || spec.FromAgentVersionID != "" {
					want.FromAgentID, want.FromAgentVersionID = spec.FromAgentID, spec.FromAgentVersionID
				}
				if spec.ToAgentID != "" || spec.ToAgentVersionID != "" {
					want.ToAgentID, want.ToAgentVersionID = spec.ToAgentID, spec.ToAgentVersionID
				}
			}
			f := cmd.Flags()
			if f.Changed("name") {
				want.Name = name
			}
			if f.Changed("description") {
				want.Description = description
			}
			if f.Changed("transfers-control") {
				want.TransfersControl = transfers
			}
			if f.Changed("from") {
				want.FromAgentID, want.FromAgentVersionID = from, ""
			}
			if f.Changed("from-version") {
				want.FromAgentVersionID = fromVersion
			}
			if f.Changed("to") {
				want.ToAgentID, want.ToAgentVersionID = to, ""
			}
			if f.Changed("to-version") {
				want.ToAgentVersionID = toVersion
			}
			changedAny := false
			for _, n := range []string{"name", "description", "transfers-control", "from", "from-version", "to", "to-version"} {
				changedAny = changedAny || f.Changed(n)
			}
			if file == "" && !changedAny {
				return cmdutil.UsageError("nothing to change; pass a flag such as --description, or -f")
			}
			refs := newAgentRefs(app)
			want, err = refs.resolveLink(ctx, want)
			if err != nil {
				return err
			}
			changed, err := changeLink(ctx, app, refs, cur, want)
			if err != nil {
				return err
			}
			if changed {
				app.Printer().Done("Updated link %s.", want.Name)
			} else {
				app.Printer().Done("Link %s is already up to date.", want.Name)
			}
			if app.Format() == output.Table {
				return nil
			}
			return showLink(ctx, app, args[0], cur.ID)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().BoolVar(&transfers, "transfers-control", false, "hand the conversation to the to agent")
	cmd.Flags().StringVar(&from, "from", "", "name or id of the agent the link runs from")
	cmd.Flags().StringVar(&fromVersion, "from-version", "", "version id of the from agent to pin")
	cmd.Flags().StringVar(&to, "to", "", "name or id of the agent the link runs to")
	cmd.Flags().StringVar(&toVersion, "to-version", "", "version id of the to agent to pin")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the responses and other fields from a YAML or JSON file")
	return cmd
}

func newLinkRemove(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <mesh> <link>...",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove links from a mesh",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			_, links, err := meshLinks(ctx, app, args[0])
			if err != nil {
				return err
			}
			var ids []string
			for _, ref := range args[1:] {
				l, err := findLink(ctx, links, args[0], ref)
				if err != nil {
					return err
				}
				ids = append(ids, l.ID)
			}
			if err := app.Confirm(fmt.Sprintf("Remove %s from mesh %s: %s?", cmdutil.Plural(len(ids), "link"), args[0], strings.Join(args[1:], ", "))); err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			for _, id := range ids {
				if _, err := gql.DeleteLink(ctx, c, id); err != nil {
					return err
				}
			}
			app.Printer().Done("Removed %s from mesh %s.", cmdutil.Plural(len(ids), "link"), args[0])
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]any{"removed": ids})
			}
			return nil
		},
	}
}

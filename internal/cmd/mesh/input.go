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

const inputSpecExample = `  # input.yaml
  name: chat
  agent_id: triage                 # an agent name or id
  responses:
    - name: answer
      description: The reply to the customer
      message_type:
        type: object
        properties:
          text: {type: string, description: The reply text}`

func inputFields(in Input) []output.Field {
	var rs []string
	for _, r := range in.Responses {
		rs = append(rs, fmt.Sprintf("%s: %s", r.Name, r.Description))
	}
	return []output.Field{
		{Label: "Name", Value: in.Name},
		{Label: "Mesh", Value: in.MeshID},
		{Label: "Agent", Value: fmt.Sprintf("%s (%s)", agentLabel(in.agentName, in.AgentID, in.agentOrdinal), in.AgentID)},
		{Label: "Agent version", Value: in.AgentVersionID},
		{Label: "Responses", Value: strings.Join(rs, "\n")},
		{Label: "Created", Value: in.CreatedAt},
		{Label: "Updated", Value: in.UpdatedAt},
	}
}

// meshInputs returns the mesh's id and the draft's inputs, each with MeshID set.
func meshInputs(ctx context.Context, app *cmdutil.App, ref string) (string, []Input, error) {
	id, err := Meshes.Resolve(ctx, app, ref)
	if err != nil {
		return "", nil, err
	}
	m, err := Meshes.Get(ctx, app, id)
	if err != nil {
		return "", nil, err
	}
	inputs := deref(m.Inputs)
	for i := range inputs {
		inputs[i].MeshID = id
	}
	return id, inputs, nil
}

func findInput(inputs []Input, mesh, name string) (Input, error) {
	for _, in := range inputs {
		if in.Name == name {
			return in, nil
		}
	}
	return Input{}, cmdutil.NotFound("input", name, "partite mesh input list "+mesh)
}

func showInput(ctx context.Context, app *cmdutil.App, mesh, name string) error {
	_, inputs, err := meshInputs(ctx, app, mesh)
	if err != nil {
		return err
	}
	in, err := findInput(inputs, mesh, name)
	if err != nil {
		return err
	}
	return app.Printer().Object(in, in.Name, inputFields(in))
}

func newInputCmd(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "input",
		Aliases: []string{"inputs"},
		Short:   "Manage a mesh's inputs, where requests enter it",
		Long: `An input is a named entry point of a mesh. A request names the input it is
for, and the input's agent takes it. The input's responses are the replies
the agent can send back to the caller, each with a JSON schema.

A response's message_type is the JSON schema of an object, and each of its
properties needs a description.

Changes go to the mesh's draft version.`,
	}
	cmd.AddCommand(newInputList(app), newInputGet(app), newInputAdd(app), newInputUpdate(app), newInputRemove(app))
	return cmd
}

func newInputList(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "list <mesh>",
		Aliases: []string{"ls"},
		Short:   "List a mesh's inputs",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, inputs, err := meshInputs(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			rows := make([][]string, len(inputs))
			for i, in := range inputs {
				rows[i] = []string{in.Name, agentLabel(in.agentName, in.AgentID, in.agentOrdinal), formatResponses(in.Responses)}
			}
			if inputs == nil {
				inputs = []Input{}
			}
			return app.Printer().List(inputs, []string{"name", "agent", "responses"}, rows)
		},
	}
}

func newInputGet(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "get <mesh> <input>",
		Aliases: []string{"show"},
		Short:   "Show one of a mesh's inputs",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return showInput(cmd.Context(), app, args[0], args[1])
		},
	}
}

func newInputAdd(app *cmdutil.App) *cobra.Command {
	var spec Input
	var file string
	cmd := &cobra.Command{
		Use:   "add <mesh> [name]",
		Short: "Add an input to a mesh",
		Long: `Add an input to a mesh's draft. Give the agent that takes requests with
--agent, or pin one of its versions with --agent-version; without a version
the input uses the agent's latest version. Responses come from -f.`,
		Example: `  partite mesh input add support chat --agent triage -f input.yaml
  partite mesh input add support -f input.yaml

` + inputSpecExample,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			flags := spec
			if file != "" {
				if err := app.ReadSpec(file, &spec); err != nil {
					return err
				}
			}
			f := cmd.Flags()
			if f.Changed("agent") {
				spec.AgentID = flags.AgentID
			}
			if f.Changed("agent-version") {
				spec.AgentVersionID = flags.AgentVersionID
			}
			if len(args) == 2 {
				spec.Name = args[1]
			}
			if spec.Name == "" {
				return cmdutil.UsageError("a name is required")
			}
			meshID, inputs, err := meshInputs(ctx, app, args[0])
			if err != nil {
				return err
			}
			for _, in := range inputs {
				if in.Name == spec.Name {
					return cmdutil.Errorf(cmdutil.ExitConflict, "Change it with: partite mesh input update", "mesh %s already has an input named %s", args[0], spec.Name)
				}
			}
			want, err := newAgentRefs(app).resolveInput(ctx, spec)
			if err != nil {
				return err
			}
			if err := addInput(ctx, app, meshID, want); err != nil {
				return err
			}
			app.Printer().Done("Added input %s to mesh %s.", spec.Name, args[0])
			if app.Format() == output.Table {
				return nil
			}
			return showInput(ctx, app, meshID, spec.Name)
		},
	}
	cmd.Flags().StringVar(&spec.AgentID, "agent", "", "name or id of the agent that takes requests")
	cmd.Flags().StringVar(&spec.AgentVersionID, "agent-version", "", "agent version id to pin")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the input from a YAML or JSON file")
	return cmd
}

func newInputUpdate(app *cmdutil.App) *cobra.Command {
	var name, agent, agentVersion, file string
	cmd := &cobra.Command{
		Use:   "update <mesh> <input>",
		Short: "Change a mesh input's name, agent or responses",
		Long: `Change one of a mesh's inputs. Only the flags you pass change. With -f, the
file's responses replace the input's.

--agent moves the input to another agent's latest version; --agent-version
pins a version.`,
		Example: `  partite mesh input update support chat --agent triage-v2
  partite mesh input update support chat -f input.yaml`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			meshID, inputs, err := meshInputs(ctx, app, args[0])
			if err != nil {
				return err
			}
			cur, err := findInput(inputs, args[0], args[1])
			if err != nil {
				return err
			}
			want := cur
			want.AgentVersionID = ""
			if file != "" {
				var spec Input
				if err := app.ReadSpec(file, &spec); err != nil {
					return err
				}
				want.Responses = spec.Responses
				if spec.Name != "" {
					want.Name = spec.Name
				}
				if spec.AgentID != "" || spec.AgentVersionID != "" {
					want.AgentID, want.AgentVersionID = spec.AgentID, spec.AgentVersionID
				}
			}
			f := cmd.Flags()
			if f.Changed("name") {
				want.Name = name
			}
			if f.Changed("agent") {
				want.AgentID, want.AgentVersionID = agent, ""
			}
			if f.Changed("agent-version") {
				want.AgentVersionID = agentVersion
			}
			if file == "" && !f.Changed("name") && !f.Changed("agent") && !f.Changed("agent-version") {
				return cmdutil.UsageError("nothing to change; pass --name, --agent, --agent-version or -f")
			}
			refs := newAgentRefs(app)
			want, err = refs.resolveInput(ctx, want)
			if err != nil {
				return err
			}
			changed, err := changeInput(ctx, app, refs, meshID, cur, want)
			if err != nil {
				return err
			}
			if changed {
				app.Printer().Done("Updated input %s.", want.Name)
			} else {
				app.Printer().Done("Input %s is already up to date.", want.Name)
			}
			if app.Format() == output.Table {
				return nil
			}
			return showInput(ctx, app, meshID, want.Name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&agent, "agent", "", "name or id of the agent that takes requests")
	cmd.Flags().StringVar(&agentVersion, "agent-version", "", "agent version id to pin")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the responses (and optionally name and agent) from a YAML or JSON file")
	return cmd
}

func newInputRemove(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <mesh> <input>...",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove inputs from a mesh",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			meshID, inputs, err := meshInputs(ctx, app, args[0])
			if err != nil {
				return err
			}
			names := args[1:]
			for _, n := range names {
				if _, err := findInput(inputs, args[0], n); err != nil {
					return err
				}
			}
			if err := app.Confirm(fmt.Sprintf("Remove %s from mesh %s: %s?", cmdutil.Plural(len(names), "input"), args[0], strings.Join(names, ", "))); err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			for _, n := range names {
				if _, err := gql.DeleteInput(ctx, c, meshID, n); err != nil {
					return err
				}
			}
			app.Printer().Done("Removed %s from mesh %s.", cmdutil.Plural(len(names), "input"), args[0])
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]any{"removed": names})
			}
			return nil
		},
	}
}

// Package clienttaskdef manages client task definitions: tasks an agent can send to a user.
package clienttaskdef

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// ClientTaskDefinition is the view printed by get and read by -f. The schemas and
// form definitions are JSON; in a YAML file they can be written as YAML or as a
// string of JSON.
type ClientTaskDefinition struct {
	ID                string                     `json:"id,omitempty"`
	Name              string                     `json:"name"`
	Description       string                     `json:"description"`
	InputMessageType  json.RawMessage            `json:"input_message_type"`
	OutputMessageType json.RawMessage            `json:"output_message_type"`
	UIForms           map[string]json.RawMessage `json:"ui_forms,omitempty"`
	// Read only when the client task definition is created.
	OverwriteExisting *bool  `json:"overwrite_existing,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

var formTypes = []string{string(gql.ClientTaskUIFormTypeA2ui)}

func fromAPI(d gql.ClientTaskDefinitionDetails) ClientTaskDefinition {
	t := ClientTaskDefinition{
		ID:                d.Id,
		Name:              d.Name,
		Description:       d.Description,
		InputMessageType:  d.InputMessageType,
		OutputMessageType: d.OutputMessageType,
		CreatedAt:         d.CreatedAt,
		UpdatedAt:         d.UpdatedAt,
	}
	if len(d.UiForms) > 0 {
		t.UIForms = make(map[string]json.RawMessage, len(d.UiForms))
		for _, f := range d.UiForms {
			t.UIForms[string(f.Type)] = JSONValue(f.Definition)
		}
	}
	return t
}

// JSONValue turns JSON text from the server into a value for a view, so that
// YAML output shows it as YAML. Text that is not JSON is kept as a string.
func JSONValue(text string) json.RawMessage {
	if text != "" && json.Valid([]byte(text)) {
		return json.RawMessage(text)
	}
	data, _ := json.Marshal(text)
	return data
}

// JSONText turns a JSON value from a spec into JSON text for the server. A
// string value is taken as JSON text itself. field names the value in errors.
func JSONText(field string, v json.RawMessage) (string, error) {
	if len(v) == 0 || string(v) == "null" {
		return "", nil
	}
	text := string(v)
	var s string
	if json.Unmarshal(v, &s) == nil {
		text = s
	}
	if !json.Valid([]byte(text)) {
		return "", cmdutil.UsageError("%s is not valid JSON", field)
	}
	return text, nil
}

// readJSONFlag reads a flag value that takes JSON, @file or @-.
func readJSONFlag(app *cmdutil.App, flag, value string) (string, error) {
	text, err := app.ReadValue(value)
	if err != nil {
		return "", err
	}
	if !json.Valid([]byte(text)) {
		return "", cmdutil.UsageError("--%s is not valid JSON", flag)
	}
	return text, nil
}

func formsToAPI(forms map[string]json.RawMessage) ([]gql.ClientTaskUIFormInput, error) {
	out := make([]gql.ClientTaskUIFormInput, 0, len(forms))
	for typ, def := range forms {
		text, err := JSONText("ui_forms."+typ, def)
		if err != nil {
			return nil, err
		}
		out = append(out, gql.ClientTaskUIFormInput{Type: gql.ClientTaskUIFormType(strings.ToUpper(typ)), Definition: text})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out, nil
}

// Resource is exported so that agents can refer to client task definitions by name.
var Resource = &cmdutil.Resource[ClientTaskDefinition]{
	WebList:  "tools/tasks",
	WebPage:  "tools/tasks/%s",
	WebLink:  "task-definition",
	Kind:     "client task definition",
	Command:  "client-task-definition",
	IDPrefix: "ctsk",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]ClientTaskDefinition, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[ClientTaskDefinition], error) {
			resp, err := gql.AgentListClientTaskDefinitions(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[ClientTaskDefinition]{}, err
			}
			conn := resp.ClientTaskDefinitions
			items := make([]ClientTaskDefinition, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = fromAPI(e.Node)
			}
			return cmdutil.Page[ClientTaskDefinition]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (ClientTaskDefinition, error) {
		c, err := app.GQL()
		if err != nil {
			return ClientTaskDefinition{}, err
		}
		resp, err := gql.GetClientTaskDefinition(ctx, c, id)
		if err != nil {
			return ClientTaskDefinition{}, err
		}
		if resp.ClientTaskDefinition == nil {
			return ClientTaskDefinition{}, cmdutil.NotFound("client task definition", id, "partite client-task-definition list")
		}
		return fromAPI(*resp.ClientTaskDefinition), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteClientTaskDefinition(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(t ClientTaskDefinition) string { return t.ID },
	Name:    func(t ClientTaskDefinition) string { return t.Name },
	Columns: []string{"id", "name", "description", "forms", "updated"},
	Row: func(t ClientTaskDefinition) []string {
		return []string{t.ID, t.Name, t.Description, strings.Join(formNames(t.UIForms), ","), cmdutil.FormatTime(t.UpdatedAt)}
	},
	Fields: func(t ClientTaskDefinition) []output.Field {
		return []output.Field{
			{Label: "ID", Value: t.ID},
			{Label: "Name", Value: t.Name},
			{Label: "Description", Value: t.Description},
			{Label: "Input message type", Value: indentJSON(t.InputMessageType)},
			{Label: "Output message type", Value: indentJSON(t.OutputMessageType)},
			{Label: "UI forms", Value: strings.Join(formNames(t.UIForms), ", ")},
			{Label: "Created", Value: t.CreatedAt},
			{Label: "Updated", Value: t.UpdatedAt},
		}
	},
}

func formNames(forms map[string]json.RawMessage) []string {
	names := make([]string, 0, len(forms))
	for k := range forms {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func indentJSON(v json.RawMessage) string {
	var out strings.Builder
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	var generic any
	if json.Unmarshal(v, &generic) != nil || enc.Encode(generic) != nil {
		return string(v)
	}
	return out.String()
}

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "client-task-definition",
		Aliases: []string{"client-task", "client-tasks", "client-task-definitions"},
		Short:   "Manage client task definitions: tasks an agent can send to a user",
		Long: `A client task definition describes a task an agent can send to a user, such as an
approval or a form to fill in. The agent sees it as a tool. The input message
type is the JSON schema the agent fills in; the output message type is the JSON
schema the user's answer must match. Give a client task definition to an agent with
partite agent update AGENT --client-task NAME.`,
	}
	cmd.AddCommand(
		Resource.ListCmd(app),
		Resource.GetCmd(app),
		Resource.ViewCmd(app),
		newCreate(app),
		newUpdate(app),
		Resource.ApplyCmd(app, "  partite client-task-definition apply -f approve.yaml", func(ctx context.Context, spec ClientTaskDefinition, id string) (ClientTaskDefinition, error) {
			if id == "" {
				return create(ctx, app, spec)
			}
			return replace(ctx, app, id, spec)
		}),
		Resource.DeleteCmd(app),
	)
	return cmd
}

func create(ctx context.Context, app *cmdutil.App, spec ClientTaskDefinition) (ClientTaskDefinition, error) {
	in := &gql.NewClientTaskDefinition{Name: spec.Name, Description: spec.Description, OverwriteExisting: spec.OverwriteExisting}
	var err error
	if in.InputMessageType, err = JSONText("input_message_type", spec.InputMessageType); err != nil {
		return ClientTaskDefinition{}, err
	}
	if in.OutputMessageType, err = JSONText("output_message_type", spec.OutputMessageType); err != nil {
		return ClientTaskDefinition{}, err
	}
	if in.InputMessageType == "" || in.OutputMessageType == "" {
		return ClientTaskDefinition{}, cmdutil.UsageError("input_message_type and output_message_type are required")
	}
	if in.UIForms, err = formsToAPI(spec.UIForms); err != nil {
		return ClientTaskDefinition{}, err
	}
	c, err := app.GQL()
	if err != nil {
		return ClientTaskDefinition{}, err
	}
	resp, err := gql.CreateClientTaskDefinition(ctx, c, in)
	if err != nil {
		return ClientTaskDefinition{}, err
	}
	return fromAPI(resp.CreateClientTaskDefinition), nil
}

// replace sets every field of the client task definition to the spec's.
func replace(ctx context.Context, app *cmdutil.App, id string, spec ClientTaskDefinition) (ClientTaskDefinition, error) {
	in := &gql.UpdateClientTaskDefinition{Name: gql.Opt(spec.Name), Description: gql.Opt(spec.Description)}
	inType, err := JSONText("input_message_type", spec.InputMessageType)
	if err != nil {
		return ClientTaskDefinition{}, err
	}
	outType, err := JSONText("output_message_type", spec.OutputMessageType)
	if err != nil {
		return ClientTaskDefinition{}, err
	}
	if inType != "" {
		in.InputMessageType = gql.Opt(inType)
	}
	if outType != "" {
		in.OutputMessageType = gql.Opt(outType)
	}
	forms, err := formsToAPI(spec.UIForms)
	if err != nil {
		return ClientTaskDefinition{}, err
	}
	in.UIForms = gql.Opt(forms)
	return update(ctx, app, id, in)
}

func update(ctx context.Context, app *cmdutil.App, id string, in *gql.UpdateClientTaskDefinition) (ClientTaskDefinition, error) {
	c, err := app.GQL()
	if err != nil {
		return ClientTaskDefinition{}, err
	}
	resp, err := gql.ModifyClientTaskDefinition(ctx, c, id, in)
	if err != nil {
		return ClientTaskDefinition{}, err
	}
	return fromAPI(resp.UpdateClientTaskDefinition), nil
}

// parseForms reads --ui-form flags in TYPE=JSON form, where JSON may be @file.
func parseForms(app *cmdutil.App, flags []string) (map[string]json.RawMessage, error) {
	forms := make(map[string]json.RawMessage, len(flags))
	for _, f := range flags {
		typ, value, ok := strings.Cut(f, "=")
		if !ok || typ == "" {
			return nil, cmdutil.UsageError("--ui-form must be TYPE=JSON or TYPE=@file, got %q", f)
		}
		text, err := readJSONFlag(app, "ui-form", value)
		if err != nil {
			return nil, err
		}
		forms[strings.ToUpper(typ)] = json.RawMessage(text)
	}
	return forms, nil
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var file, description, inType, outType string
	var forms []string
	var overwrite bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a client task definition",
		Long: `Create a client task definition. The name is the tool name the agent sees, so use
letters, digits and underscores. The message types are JSON schemas; pass them
inline, or as @file to read a file.

UI form types: ` + strings.Join(formTypes, ", "),
		Example: `  partite client-task-definition create approve_refund \
    --description "Ask a person to approve a refund" \
    --input-message-type @refund-input.json \
    --output-message-type '{"type":"object","properties":{"approved":{"type":"boolean"}}}'
  partite client-task-definition create -f approve-refund.yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var spec ClientTaskDefinition
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
			f := cmd.Flags()
			if f.Changed("description") {
				spec.Description = description
			}
			if f.Changed("input-message-type") {
				text, err := readJSONFlag(app, "input-message-type", inType)
				if err != nil {
					return err
				}
				spec.InputMessageType = json.RawMessage(text)
			}
			if f.Changed("output-message-type") {
				text, err := readJSONFlag(app, "output-message-type", outType)
				if err != nil {
					return err
				}
				spec.OutputMessageType = json.RawMessage(text)
			}
			if f.Changed("ui-form") {
				m, err := parseForms(app, forms)
				if err != nil {
					return err
				}
				spec.UIForms = m
			}
			if f.Changed("overwrite-existing") {
				spec.OverwriteExisting = &overwrite
			}
			if spec.Description == "" {
				return cmdutil.UsageError("--description is required; the agent sees it as the tool description")
			}
			if len(spec.InputMessageType) == 0 || len(spec.OutputMessageType) == 0 {
				return cmdutil.UsageError("--input-message-type and --output-message-type are required")
			}
			t, err := create(cmd.Context(), app, spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Created client task definition %s (%s).", t.Name, t.ID)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, t)
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "what the task is for; the agent sees it as the tool description")
	cmd.Flags().StringVar(&inType, "input-message-type", "", "JSON schema the agent fills in, or @file")
	cmd.Flags().StringVar(&outType, "output-message-type", "", "JSON schema the user's answer must match, or @file")
	cmd.Flags().StringArrayVar(&forms, "ui-form", nil, "UI form as TYPE=JSON or TYPE=@file; repeat for more")
	cmd.Flags().BoolVar(&overwrite, "overwrite-existing", false, "take over a client task definition that already has this name instead of failing")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the client task definition from a YAML or JSON file")
	return cmd
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var name, description, inType, outType string
	var forms []string
	var noForms bool
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change a client task definition",
		Example: `  partite client-task-definition update approve_refund --description "Ask a manager to approve a refund"
  partite client-task-definition update approve_refund --ui-form A2UI=@form.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := Resource.Resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			f := cmd.Flags()
			in := &gql.UpdateClientTaskDefinition{}
			changed := false
			if f.Changed("name") {
				in.Name = gql.Opt(name)
				changed = true
			}
			if f.Changed("description") {
				in.Description = gql.Opt(description)
				changed = true
			}
			if f.Changed("input-message-type") {
				text, err := readJSONFlag(app, "input-message-type", inType)
				if err != nil {
					return err
				}
				in.InputMessageType = gql.Opt(text)
				changed = true
			}
			if f.Changed("output-message-type") {
				text, err := readJSONFlag(app, "output-message-type", outType)
				if err != nil {
					return err
				}
				in.OutputMessageType = gql.Opt(text)
				changed = true
			}
			if f.Changed("ui-form") && noForms {
				return cmdutil.UsageError("--ui-form and --no-ui-forms cannot be used together")
			}
			if f.Changed("ui-form") || noForms {
				m, err := parseForms(app, forms)
				if err != nil {
					return err
				}
				list, err := formsToAPI(m)
				if err != nil {
					return err
				}
				in.UIForms = gql.Opt(list)
				changed = true
			}
			if !changed {
				return cmdutil.UsageError("nothing to change; pass --name, --description, --input-message-type, --output-message-type, --ui-form or --no-ui-forms")
			}
			t, err := update(cmd.Context(), app, id, in)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated client task definition %s.", t.Name)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, t)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringVar(&inType, "input-message-type", "", "JSON schema the agent fills in, or @file")
	cmd.Flags().StringVar(&outType, "output-message-type", "", "JSON schema the user's answer must match, or @file")
	cmd.Flags().StringArrayVar(&forms, "ui-form", nil, fmt.Sprintf("UI form as TYPE=JSON or TYPE=@file, replacing all forms; repeat for more (types: %s)", strings.Join(formTypes, ", ")))
	cmd.Flags().BoolVar(&noForms, "no-ui-forms", false, "remove all UI forms")
	return cmd
}

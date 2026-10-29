// Package form checks and builds A2UI task forms for client task definitions.
package form

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/partite-ai/partite-cli/internal/a2ui"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

const example = `open:
  components:
    - id: root
      component: Column
      children: [question, answer, send]
    - id: question
      component: Text
      text: {path: /question}
    - id: answer
      component: TextField
      label: Answer
      value: {path: /answer}
    - id: send
      component: Button
      child: send_label
      action: {event: {name: submit, context: {answer: {path: /answer}}}}
    - id: send_label
      component: Text
      text: Send`

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "form",
		Short: "Check and build A2UI forms for client task definitions",
		Long: `A client task definition can show a form while the task is open. The form is the
A2UI entry of the client task definition's ui_forms. It has an open surface, shown
while the task is open, and an optional completed surface.

Write the form in YAML or JSON, check it, and build it into the JSON that
ui_forms takes. Components come from the A2UI v0.9 basic catalog unless the
form has a catalog of its own.

Example form:

` + example,
	}
	cmd.AddCommand(newCheck(app), newBuild(app), newComponents(app))
	return cmd
}

func readForm(app *cmdutil.App, path string) (*a2ui.Form, error) {
	var f a2ui.Form
	if err := app.ReadSpec(path, &f); err != nil {
		return nil, err
	}
	if err := f.Check(); err != nil {
		return nil, cmdutil.UsageError("%s: %s", path, err)
	}
	return &f, nil
}

func newCheck(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:   "check <file>",
		Short: "Check a form against its catalog",
		Long: `Check a form the way the server does when a client task definition is saved, except
for the checks that need the task's input and output schemas.`,
		Example: "  partite form check approval-form.yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := readForm(app, args[0]); err != nil {
				return err
			}
			app.Printer().Done("%s is a valid form.", args[0])
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]bool{"valid": true})
			}
			return nil
		},
	}
}

func newBuild(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:   "build <file>",
		Short: "Check a form and print it as the JSON ui_forms takes",
		Example: `  partite form build approval-form.yaml > approval-form.json
  partite client-task-definition update approve --ui-form A2UI=@approval-form.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := readForm(app, args[0])
			if err != nil {
				return err
			}
			data, err := json.Marshal(f)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(app.Out, string(data))
			return err
		},
	}
}

type componentView struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Properties  []propertyView `json:"properties"`
}

type propertyView struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Required    bool           `json:"required,omitempty"`
	Enum        []string       `json:"enum,omitempty"`
	Description string         `json:"description,omitempty"`
	Properties  []propertyView `json:"properties,omitempty"`
}

var kindNames = map[a2ui.Kind]string{
	a2ui.KindString:            "string",
	a2ui.KindNumber:            "number",
	a2ui.KindBool:              "boolean",
	a2ui.KindStringList:        "string list",
	a2ui.KindDynamicString:     "string or binding",
	a2ui.KindDynamicNumber:     "number or binding",
	a2ui.KindDynamicBool:       "boolean or binding",
	a2ui.KindDynamicStringList: "string list or binding",
	a2ui.KindChildList:         "component ids or template",
	a2ui.KindIconName:          "icon name or binding",
	a2ui.KindAction:            "action",
	a2ui.KindObject:            "object",
	a2ui.KindObjectList:        "object list",
}

func viewProperties(props []a2ui.Property) []propertyView {
	out := make([]propertyView, len(props))
	for i, p := range props {
		out[i] = propertyView{
			Name:        p.JSONName,
			Kind:        kindNames[p.Kind],
			Required:    p.Required,
			Enum:        p.Enum,
			Description: p.Description,
			Properties:  viewProperties(p.Properties),
		}
	}
	return out
}

func newComponents(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "components [name]",
		Short:   "List the basic catalog's components, or show one",
		Example: "  partite form components\n  partite form components TextField",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			basic, err := a2ui.BasicCatalogComponents()
			if err != nil {
				return err
			}
			var views []componentView
			for _, c := range basic.Components {
				if len(args) == 1 && !strings.EqualFold(args[0], c.JSONName) && args[0] != c.Name {
					continue
				}
				views = append(views, componentView{Name: c.JSONName, Description: c.Description, Properties: viewProperties(c.Properties)})
			}
			if len(args) == 1 {
				if len(views) == 0 {
					return cmdutil.NotFound("component", args[0], "partite form components")
				}
				v := views[0]
				if app.Format() != output.Table {
					return app.Printer().Data(v)
				}
				fmt.Fprintf(app.Out, "%s\n%s\n\nProperties:\n", v.Name, v.Description)
				for _, p := range v.Properties {
					req := ""
					if p.Required {
						req = " (required)"
					}
					fmt.Fprintf(app.Out, "  %s: %s%s\n", p.Name, p.Kind, req)
					if len(p.Enum) > 0 {
						fmt.Fprintf(app.Out, "      one of %s\n", strings.Join(p.Enum, ", "))
					}
					if p.Description != "" {
						fmt.Fprintf(app.Out, "      %s\n", p.Description)
					}
				}
				fmt.Fprintln(app.Out, "\nEvery component also has id, and may have weight and accessibility.")
				return nil
			}
			rows := make([][]string, len(views))
			for i, v := range views {
				rows[i] = []string{v.Name, v.Description}
			}
			return app.Printer().List(views, []string{"component", "description"}, rows)
		},
	}
}

// Package webhook manages webhook destinations: URLs that start a mesh
// request when something POSTs to them.
package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmd/mesh"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Webhook is the view printed by get and read by -f.
type Webhook struct {
	ID               string `json:"id,omitempty"`
	Name             string `json:"name"`
	Description      string `json:"description,omitempty"`
	MeshID           string `json:"mesh_id"`
	InputName        string `json:"input_name"`
	MeshVersionLabel string `json:"mesh_version_label,omitempty"`
	ExternalID       string `json:"external_id,omitempty"`
	InvokeURL        string `json:"invoke_url,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

func fromAPI(app *cmdutil.App, d gql.WebhookDestinationDetails) Webhook {
	return Webhook{
		ID:               d.Id,
		Name:             d.Name,
		Description:      cmdutil.Deref(d.Description),
		MeshID:           d.Mesh.Id,
		InputName:        d.InputName,
		MeshVersionLabel: cmdutil.Deref(d.MeshVersionLabel),
		ExternalID:       d.ExternalID,
		InvokeURL:        app.BaseURL() + "/webhooks/" + d.ExternalID,
		CreatedAt:        d.CreatedAt,
		UpdatedAt:        d.UpdatedAt,
	}
}

var Resource = &cmdutil.Resource[Webhook]{
	WebList:  "webhook-destinations",
	WebPage:  "webhook-destinations/%s",
	WebLink:  "webhook",
	Kind:     "webhook",
	Command:  "webhook",
	IDPrefix: "chkd",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Webhook, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Webhook], error) {
			resp, err := gql.WebhookList(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[Webhook]{}, err
			}
			conn := resp.WebhookDestinations
			items := make([]Webhook, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = fromAPI(app, e.Node)
			}
			return cmdutil.Page[Webhook]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Webhook, error) {
		c, err := app.GQL()
		if err != nil {
			return Webhook{}, err
		}
		resp, err := gql.GetWebhookDestination(ctx, c, id)
		if err != nil {
			return Webhook{}, err
		}
		return fromAPI(app, resp.WebhookDestination), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteWebhookDestination(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(w Webhook) string { return w.ID },
	Name:    func(w Webhook) string { return w.Name },
	Columns: []string{"id", "name", "mesh", "input", "version", "url"},
	Row: func(w Webhook) []string {
		return []string{w.ID, w.Name, w.MeshID, w.InputName, orDraft(w.MeshVersionLabel), w.InvokeURL}
	},
	Fields: func(w Webhook) []output.Field {
		return []output.Field{
			{Label: "ID", Value: w.ID},
			{Label: "Name", Value: w.Name},
			{Label: "Description", Value: w.Description},
			{Label: "Mesh", Value: w.MeshID},
			{Label: "Input", Value: w.InputName},
			{Label: "Mesh version", Value: orDraft(w.MeshVersionLabel)},
			{Label: "URL", Value: w.InvokeURL},
			{Label: "Created", Value: w.CreatedAt},
			{Label: "Updated", Value: w.UpdatedAt},
		}
	},
}

func orDraft(label string) string {
	if label == "" {
		return "draft"
	}
	return label
}

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "webhook",
		Aliases: []string{"webhooks"},
		Short:   "Manage webhooks that start mesh requests",
		Long: `A webhook gives a mesh input a URL. Each JSON object POSTed to the URL
becomes a request to that input, in a new conversation. Other services can
call it to start work, for example when a ticket is filed.

Anyone who has the URL can start requests, so treat it as a secret. A
webhook runs on the mesh version labelled mesh_version_label, or on the
draft when it has none.`,
	}
	cmd.AddCommand(
		Resource.ListCmd(app),
		Resource.GetCmd(app),
		Resource.ViewCmd(app),
		newCreate(app),
		newUpdate(app),
		Resource.ApplyCmd(app, `  # webhook.yaml
  name: new-ticket
  mesh_id: support          # a mesh name or id
  input_name: chat
  mesh_version_label: v1

  partite webhook apply -f webhook.yaml`, func(ctx context.Context, spec Webhook, id string) (Webhook, error) {
			if id == "" {
				return create(ctx, app, spec)
			}
			return update(ctx, app, id, spec)
		}),
		Resource.DeleteCmd(app),
		newSend(app),
	)
	return cmd
}

func create(ctx context.Context, app *cmdutil.App, spec Webhook) (Webhook, error) {
	if spec.MeshID == "" || spec.InputName == "" {
		return Webhook{}, cmdutil.UsageError("a mesh and an input are required; pass --mesh and --input")
	}
	meshID, err := mesh.Meshes.Resolve(ctx, app, spec.MeshID)
	if err != nil {
		return Webhook{}, err
	}
	c, err := app.GQL()
	if err != nil {
		return Webhook{}, err
	}
	in := &gql.NewWebhookDestination{Name: spec.Name, MeshID: meshID, InputName: spec.InputName}
	if spec.Description != "" {
		in.Description = gql.Opt(spec.Description)
	}
	if spec.MeshVersionLabel != "" {
		in.MeshVersionLabel = gql.Opt(spec.MeshVersionLabel)
	}
	resp, err := gql.CreateWebhookDestination(ctx, c, in)
	if err != nil {
		return Webhook{}, err
	}
	return fromAPI(app, resp.CreateWebhookDestination), nil
}

// update makes the webhook match spec in full.
func update(ctx context.Context, app *cmdutil.App, id string, spec Webhook) (Webhook, error) {
	meshID, err := mesh.Meshes.Resolve(ctx, app, spec.MeshID)
	if err != nil {
		return Webhook{}, err
	}
	u := &gql.UpdateWebhookDestination{
		Name:             gql.Opt(spec.Name),
		Description:      gql.Opt(spec.Description),
		MeshID:           gql.Opt(meshID),
		InputName:        gql.Opt(spec.InputName),
		MeshVersionLabel: gql.OptNil(cmdutil.OptString(spec.MeshVersionLabel)),
	}
	return modify(ctx, app, id, u)
}

func modify(ctx context.Context, app *cmdutil.App, id string, u *gql.UpdateWebhookDestination) (Webhook, error) {
	c, err := app.GQL()
	if err != nil {
		return Webhook{}, err
	}
	resp, err := gql.ModifyWebhookDestination(ctx, c, id, u)
	if err != nil {
		return Webhook{}, err
	}
	return fromAPI(app, resp.UpdateWebhookDestination), nil
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var spec Webhook
	var file string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a webhook for a mesh input and print its URL",
		Long: `Create a webhook. It sends what is POSTed to its URL to the mesh's input.
Without --version-label it runs on the mesh's draft.`,
		Example: `  partite webhook create new-ticket --mesh support --input chat --version-label v1
  partite webhook create new-ticket --mesh support --input chat -o json | jq -r .invoke_url`,
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
				if f.Changed("input") {
					spec.InputName = flags.InputName
				}
				if f.Changed("version-label") {
					spec.MeshVersionLabel = flags.MeshVersionLabel
				}
				if f.Changed("description") {
					spec.Description = flags.Description
				}
			}
			if len(args) == 1 {
				spec.Name = args[0]
			}
			if spec.Name == "" {
				return cmdutil.UsageError("a name is required")
			}
			w, err := create(cmd.Context(), app, spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Created webhook %s (%s).\nURL: %s", w.Name, w.ID, w.InvokeURL)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, w)
		},
	}
	cmd.Flags().StringVar(&spec.MeshID, "mesh", "", "mesh name or id (required)")
	cmd.Flags().StringVar(&spec.InputName, "input", "", "name of the mesh input to send to (required)")
	cmd.Flags().StringVar(&spec.MeshVersionLabel, "version-label", "", "label of the mesh version to run (default: the draft)")
	cmd.Flags().StringVar(&spec.Description, "description", "", "description")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the webhook from a YAML or JSON file")
	return cmd
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var name, description, meshRef, input, label string
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change a webhook's name, mesh, input or version",
		Long: `Change a webhook. Only the flags you pass change. Its URL stays the same.
Pass --version-label "" to run on the draft.`,
		Example: "  partite webhook update new-ticket --version-label v2",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := Resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			f := cmd.Flags()
			var u gql.UpdateWebhookDestination
			if f.Changed("name") {
				u.Name = gql.Opt(name)
			}
			if f.Changed("description") {
				u.Description = gql.Opt(description)
			}
			if f.Changed("input") {
				u.InputName = gql.Opt(input)
			}
			if f.Changed("version-label") {
				u.MeshVersionLabel = gql.OptNil(cmdutil.OptString(label))
			}
			if f.Changed("mesh") {
				meshID, err := mesh.Meshes.Resolve(ctx, app, meshRef)
				if err != nil {
					return err
				}
				u.MeshID = gql.Opt(meshID)
			}
			if !u.Name.Set && !u.Description.Set && !u.InputName.Set && !u.MeshVersionLabel.Set && !u.MeshID.Set {
				return cmdutil.UsageError("nothing to change; pass --name, --description, --mesh, --input or --version-label")
			}
			w, err := modify(ctx, app, id, &u)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated webhook %s.", w.Name)
			if app.Format() == output.Table {
				return nil
			}
			return Resource.Show(app, w)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringVar(&meshRef, "mesh", "", "mesh name or id")
	cmd.Flags().StringVar(&input, "input", "", "name of the mesh input to send to")
	cmd.Flags().StringVar(&label, "version-label", "", "label of the mesh version to run; empty for the draft")
	return cmd
}

func newSend(app *cmdutil.App) *cobra.Command {
	var data, contentType string
	var headers []string
	cmd := &cobra.Command{
		Use:   "send <name-or-id>",
		Short: "POST to a webhook, as another service would",
		Long: `POST a body to a webhook's URL and print the response. The body must be a
JSON object; it becomes the request to the mesh input. The request runs in
the background: follow it with the conversation commands.`,
		Example: `  partite webhook send new-ticket --data '{"text": "My invoice is wrong"}'
  partite webhook send new-ticket --data @ticket.json
  cat ticket.json | partite webhook send new-ticket --data @-`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := Resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			w, err := Resource.Get(ctx, app, id)
			if err != nil {
				return err
			}
			body, err := app.ReadValue(data)
			if err != nil {
				return err
			}
			if contentType == "application/json" && !json.Valid([]byte(body)) {
				return cmdutil.UsageError("--data is not valid JSON")
			}
			h := http.Header{"Content-Type": {contentType}}
			for _, kv := range headers {
				k, v, ok := strings.Cut(kv, ":")
				if !ok {
					return cmdutil.UsageError("--header must be Name: value, got %q", kv)
				}
				h.Add(strings.TrimSpace(k), strings.TrimSpace(v))
			}
			rest, err := app.REST()
			if err != nil {
				return err
			}
			resp, err := rest.Do(ctx, http.MethodPost, "/webhooks/"+w.ExternalID, strings.NewReader(body), h)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			out, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			if app.Format() != output.Table {
				result := map[string]any{"status": resp.StatusCode, "body": string(out)}
				if json.Valid(out) {
					result["body"] = json.RawMessage(out)
				}
				return app.Printer().Data(result)
			}
			fmt.Fprintf(app.Out, "Sent to webhook %s: %s\n", w.Name, resp.Status)
			if len(out) > 0 {
				fmt.Fprintln(app.Out, strings.TrimRight(string(out), "\n"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&data, "data", "{}", "body to send; @file reads a file and @- standard input")
	cmd.Flags().StringVar(&contentType, "content-type", "application/json", "Content-Type of the body")
	cmd.Flags().StringArrayVarP(&headers, "header", "H", nil, "extra header as 'Name: value'; repeat for more")
	return cmd
}

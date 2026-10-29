// Package embeddingprofile manages embedding profiles: an embedding model of a
// provider and the size of the vectors it makes.
package embeddingprofile

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmd/modelprovider"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Profile is the view printed by get and read by -f.
type Profile struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// ModelProviderID also takes a provider name on input.
	ModelProviderID   string  `json:"model_provider_id,omitempty"`
	ModelProviderName string  `json:"model_provider_name,omitempty"`
	ModelProviderType string  `json:"model_provider_type,omitempty"`
	Configuration     *Config `json:"configuration,omitempty"`
	// Dimensions is the size of the vectors the profile makes. The server sets it.
	Dimensions int `json:"dimensions,omitempty"`
	// Probe asks the server to embed one text with the provider before saving,
	// and to refuse a profile the provider rejects.
	Probe     bool   `json:"probe,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type Config struct {
	Model          string `json:"model"`
	Dimensions     int    `json:"dimensions"`
	SendDimensions bool   `json:"send_dimensions,omitempty"`
	TaskType       string `json:"task_type,omitempty"`
}

func (c *Config) toInput() (*gql.EmbeddingProfileConfigInput, error) {
	if c == nil || c.Model == "" {
		return nil, cmdutil.UsageError("a model is required; pass --model or set configuration.model")
	}
	if c.Dimensions <= 0 {
		return nil, cmdutil.UsageError("the vector size is required; pass --dimensions or set configuration.dimensions")
	}
	in := &gql.EmbeddingProfileConfigInput{Model: c.Model, Dimensions: c.Dimensions}
	if c.SendDimensions {
		in.SendDimensions = gql.Opt(true)
	}
	if c.TaskType != "" {
		in.TaskType = gql.Opt(c.TaskType)
	}
	return in, nil
}

func fromAPI(d gql.EmbeddingProfileDetails) Profile {
	return Profile{
		ID:              d.Id,
		Name:            d.Name,
		Description:     cmdutil.Deref(d.Description),
		ModelProviderID: d.Provider.Id,
		Configuration: &Config{
			Model:          d.Configuration.Model,
			Dimensions:     d.Configuration.Dimensions,
			SendDimensions: d.Configuration.SendDimensions,
			TaskType:       cmdutil.Deref(d.Configuration.TaskType),
		},
		Dimensions: d.Dimensions,
		CreatedAt:  d.CreatedAt,
		UpdatedAt:  d.UpdatedAt,
	}
}

var profiles = &cmdutil.Resource[Profile]{
	Kind:     "embedding profile",
	Command:  "embedding-profile",
	IDPrefix: "cemp",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Profile, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Profile], error) {
			resp, err := gql.ModelListEmbeddingProfileSummaries(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[Profile]{}, err
			}
			conn := resp.EmbeddingProfileSummaries
			items := make([]Profile, len(conn.Edges))
			for i, e := range conn.Edges {
				n := e.Node
				items[i] = Profile{
					ID:                n.Id,
					Name:              n.Name,
					Description:       cmdutil.Deref(n.Description),
					ModelProviderName: n.ProviderName,
					ModelProviderType: modelprovider.TypeName(n.ProviderType),
					Dimensions:        n.Dimensions,
				}
			}
			return cmdutil.Page[Profile]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Profile, error) {
		c, err := app.GQL()
		if err != nil {
			return Profile{}, err
		}
		resp, err := gql.GetEmbeddingProfile(ctx, c, id)
		if err != nil {
			return Profile{}, err
		}
		return fromAPI(resp.EmbeddingProfile), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteEmbeddingProfile(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(p Profile) string { return p.ID },
	Name:    func(p Profile) string { return p.Name },
	Columns: []string{"id", "name", "provider", "type", "dimensions"},
	Row: func(p Profile) []string {
		return []string{p.ID, p.Name, p.ModelProviderName, p.ModelProviderType, strconv.Itoa(p.Dimensions)}
	},
	Fields: func(p Profile) []output.Field {
		c := p.Configuration
		if c == nil {
			c = &Config{}
		}
		send := "no"
		if c.SendDimensions {
			send = "yes"
		}
		return []output.Field{
			{Label: "ID", Value: p.ID},
			{Label: "Name", Value: p.Name},
			{Label: "Description", Value: p.Description},
			{Label: "Provider", Value: p.ModelProviderID},
			{Label: "Model", Value: c.Model},
			{Label: "Dimensions", Value: strconv.Itoa(p.Dimensions)},
			{Label: "Send dimensions", Value: send},
			{Label: "Task type", Value: c.TaskType},
			{Label: "Created", Value: p.CreatedAt},
			{Label: "Updated", Value: p.UpdatedAt},
		}
	},
}

// resolve turns a reference into an id. A reference is an id, a name, or
// PROVIDER/NAME when two providers have a profile of that name.
func resolve(ctx context.Context, app *cmdutil.App, ref string) (string, error) {
	if providerRef, name, ok := strings.Cut(ref, "/"); ok {
		providerID, err := modelprovider.Providers.Resolve(ctx, app, providerRef)
		if err == nil {
			p, found, err := byProviderAndName(ctx, app, providerID, name)
			if err != nil {
				return "", err
			}
			if !found {
				return "", cmdutil.NotFound("embedding profile", ref, "partite embedding-profile list")
			}
			return p.ID, nil
		}
		if e := app.Classify(err); e.Code != cmdutil.ExitNotFound {
			return "", err
		}
	}
	id, err := profiles.Resolve(ctx, app, ref)
	if e, ok := err.(*cmdutil.Error); ok && e.Code == cmdutil.ExitUsage && strings.Contains(e.Msg, "are named") {
		e.Hint = "Use PROVIDER/" + ref + ", or one of the ids: " + strings.TrimPrefix(e.Hint, "Use the id instead: ")
	}
	return id, err
}

func byProviderAndName(ctx context.Context, app *cmdutil.App, providerID, name string) (Profile, bool, error) {
	c, err := app.GQL()
	if err != nil {
		return Profile{}, false, err
	}
	resp, err := gql.GetEmbeddingProfileByProviderAndName(ctx, c, providerID, name)
	if err != nil {
		if app.Classify(err).Code == cmdutil.ExitNotFound {
			return Profile{}, false, nil
		}
		return Profile{}, false, err
	}
	return fromAPI(resp.EmbeddingProfileByProviderAndName), true, nil
}

const refHelp = `A profile is named by its id, its name, or PROVIDER/NAME when two providers
have a profile with the same name.`

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "embedding-profile",
		Aliases: []string{"embedding-profiles"},
		Short:   "Manage embedding profiles: an embedding model and its vector size",
		Long: `An embedding profile names an embedding model of a model provider, such as
text-embedding-3-small on an OpenAI provider, and the size of the vectors it
makes. Memory stores use an embedding profile to search by meaning.

To see a provider's embedding models, run:
  partite model-provider embedding-models PROVIDER

` + refHelp,
	}
	cmd.AddCommand(
		profiles.ListCmd(app),
		newGet(app),
		newCreate(app),
		newUpdate(app),
		newApply(app),
		newDelete(app),
		newProbe(app),
	)
	return cmd
}

func newGet(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "get <profile>",
		Aliases: []string{"show"},
		Short:   "Show an embedding profile",
		Long: `Show an embedding profile. ` + refHelp + `

With -o yaml or -o json the output is in the same format that -f reads, so
you can save it, edit it and apply it:

  partite embedding-profile get NAME -o yaml > embedding.yaml
  partite embedding-profile apply -f embedding.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			p, err := profiles.Get(cmd.Context(), app, id)
			if err != nil {
				return err
			}
			return profiles.Show(app, p)
		},
	}
}

func newDelete(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <profile>...",
		Aliases: []string{"rm"},
		Short:   "Delete embedding profiles",
		Long:    "Delete embedding profiles. " + refHelp,
		Example: "  partite embedding-profile delete small\n  partite embedding-profile delete my-openai/small --yes",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids := make([]string, len(args))
			for i, ref := range args {
				id, err := resolve(cmd.Context(), app, ref)
				if err != nil {
					return err
				}
				ids[i] = id
			}
			what := "embedding profile " + strings.Join(args, ", ")
			if len(args) > 1 {
				what = cmdutil.Plural(len(args), "embedding profile") + ": " + strings.Join(args, ", ")
			}
			if err := app.Confirm(fmt.Sprintf("Delete %s?", what)); err != nil {
				return err
			}
			if err := profiles.Delete(cmd.Context(), app, ids); err != nil {
				return err
			}
			app.Printer().Done("Deleted %s.", what)
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]any{"deleted": ids})
			}
			return nil
		},
	}
}

func create(ctx context.Context, app *cmdutil.App, providerID string, spec Profile) (Profile, error) {
	cfg, err := spec.Configuration.toInput()
	if err != nil {
		return Profile{}, err
	}
	c, err := app.GQL()
	if err != nil {
		return Profile{}, err
	}
	in := &gql.NewEmbeddingProfile{Name: spec.Name, Configuration: *cfg}
	if spec.Description != "" {
		in.Description = gql.Opt(spec.Description)
	}
	if spec.Probe {
		in.Probe = cmdutil.Ptr(true)
	}
	resp, err := gql.CreateEmbeddingProfile(ctx, c, providerID, in)
	if err != nil {
		return Profile{}, err
	}
	return fromAPI(resp.CreateEmbeddingProfile), nil
}

func update(ctx context.Context, app *cmdutil.App, id string, in *gql.UpdateEmbeddingProfile) (Profile, error) {
	c, err := app.GQL()
	if err != nil {
		return Profile{}, err
	}
	resp, err := gql.ModifyEmbeddingProfile(ctx, c, id, in)
	if err != nil {
		return Profile{}, err
	}
	return fromAPI(resp.UpdateEmbeddingProfile), nil
}

type configFlags struct {
	model          string
	dimensions     int
	sendDimensions bool
	taskType       string
}

func (f *configFlags) add(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.model, "model", "", "the embedding model's id at the provider, such as text-embedding-3-small")
	fl.IntVar(&f.dimensions, "dimensions", 0, "size of the vectors the model makes")
	fl.BoolVar(&f.sendDimensions, "send-dimensions", false, "ask the model for that size with each request; only for a model that can make smaller vectors")
	fl.StringVar(&f.taskType, "task-type", "", "what the vectors are for, such as RETRIEVAL_DOCUMENT (Google only)")
}

// apply writes the flags that were passed into c and reports whether any were.
func (f *configFlags) apply(cmd *cobra.Command, c *Config) bool {
	fl := cmd.Flags()
	if fl.Changed("model") {
		c.Model = f.model
	}
	if fl.Changed("dimensions") {
		c.Dimensions = f.dimensions
	}
	if fl.Changed("send-dimensions") {
		c.SendDimensions = f.sendDimensions
	}
	if fl.Changed("task-type") {
		c.TaskType = f.taskType
	}
	return fl.Changed("model") || fl.Changed("dimensions") || fl.Changed("send-dimensions") || fl.Changed("task-type")
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var spec Profile
	var file string
	var flags configFlags
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an embedding profile",
		Long: `Create an embedding profile on a provider. Pass --provider, --model and
--dimensions, or read the whole profile from a file with -f. Flags override the
file.

With --probe, the server embeds one text with the provider first, and refuses
a profile the provider rejects.`,
		Example: `  partite embedding-profile create small --provider my-openai --model text-embedding-3-small --dimensions 1536
  partite embedding-profile create gemini-docs --provider my-gemini --model gemini-embedding-001 --dimensions 768 --send-dimensions --task-type RETRIEVAL_DOCUMENT`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if spec.Configuration == nil {
				spec.Configuration = &Config{}
			}
			flags.apply(cmd, spec.Configuration)
			if spec.ModelProviderID == "" {
				return cmdutil.UsageError("a provider is required; pass --provider")
			}
			providerID, err := modelprovider.Providers.Resolve(cmd.Context(), app, spec.ModelProviderID)
			if err != nil {
				return err
			}
			p, err := create(cmd.Context(), app, providerID, spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Created embedding profile %s (%s).", p.Name, p.ID)
			if app.Format() == output.Table {
				return nil
			}
			return profiles.Show(app, p)
		},
	}
	flags.add(cmd)
	cmd.Flags().StringVar(&spec.ModelProviderID, "provider", "", "model provider name or id")
	cmd.Flags().StringVar(&spec.Description, "description", "", "description")
	cmd.Flags().BoolVar(&spec.Probe, "probe", false, "check the profile with the provider before saving")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the profile from a YAML or JSON file")
	return cmd
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var name, description string
	var probe bool
	var flags configFlags
	cmd := &cobra.Command{
		Use:   "update <profile>",
		Short: "Change an embedding profile's name, model or vector size",
		Long: `Change an embedding profile. Only the flags you pass change. A profile cannot
move to another provider. Memory stores keep vectors of one size together, so
changing the model or --dimensions of a profile in use affects them.

` + refHelp,
		Example: "  partite embedding-profile update small --description \"Default embeddings\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			f := cmd.Flags()
			in := &gql.UpdateEmbeddingProfile{}
			if f.Changed("name") {
				in.Name = gql.Opt(name)
			}
			if f.Changed("description") {
				in.Description = gql.Opt(description)
			}
			cur, err := profiles.Get(cmd.Context(), app, id)
			if err != nil {
				return err
			}
			if flags.apply(cmd, cur.Configuration) {
				if in.Configuration, err = cur.Configuration.toInput(); err != nil {
					return err
				}
			}
			if !in.Name.Set && !in.Description.Set && in.Configuration == nil {
				return cmdutil.UsageError("nothing to change; see --help for the flags")
			}
			if probe {
				in.Probe = cmdutil.Ptr(true)
			}
			p, err := update(cmd.Context(), app, id, in)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated embedding profile %s.", p.Name)
			if app.Format() == output.Table {
				return nil
			}
			return profiles.Show(app, p)
		},
	}
	flags.add(cmd)
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().BoolVar(&probe, "probe", false, "check the profile with the provider before saving")
	return cmd
}

func newApply(app *cmdutil.App) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "apply -f <file>",
		Short: "Create or update an embedding profile from a file",
		Long: `Create an embedding profile from a YAML or JSON file, or update the profile
with the same name on the same provider. Use -f - to read standard input.
Fields that the server sets, such as id and created_at, are ignored, so the
output of get -o yaml can be applied as is. model_provider_id takes a provider
name too.`,
		Example: `  partite embedding-profile apply -f embedding.yaml

  # embedding.yaml
  name: small
  model_provider_id: my-openai
  configuration:
    model: text-embedding-3-small
    dimensions: 1536`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var spec Profile
			if err := app.ReadSpec(file, &spec); err != nil {
				return err
			}
			if spec.Name == "" {
				return cmdutil.UsageError("%s has no name", file)
			}
			if spec.ModelProviderID == "" {
				return cmdutil.UsageError("%s has no model_provider_id; set it to the provider's name or id", file)
			}
			ctx := cmd.Context()
			providerID, err := modelprovider.Providers.Resolve(ctx, app, spec.ModelProviderID)
			if err != nil {
				return err
			}
			existing, found, err := byProviderAndName(ctx, app, providerID, spec.Name)
			if err != nil {
				return err
			}
			var p Profile
			if found {
				cfg, err := spec.Configuration.toInput()
				if err != nil {
					return err
				}
				in := &gql.UpdateEmbeddingProfile{
					Name:          gql.Opt(spec.Name),
					Description:   gql.Opt(spec.Description),
					Configuration: cfg,
				}
				if spec.Probe {
					in.Probe = cmdutil.Ptr(true)
				}
				if p, err = update(ctx, app, existing.ID, in); err != nil {
					return err
				}
				app.Printer().Done("Updated embedding profile %s (%s).", p.Name, p.ID)
			} else {
				if p, err = create(ctx, app, providerID, spec); err != nil {
					return err
				}
				app.Printer().Done("Created embedding profile %s (%s).", p.Name, p.ID)
			}
			if app.Format() == output.Table {
				return nil
			}
			return profiles.Show(app, p)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "YAML or JSON file, or - for standard input (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newProbe(app *cmdutil.App) *cobra.Command {
	var providerRef string
	var flags configFlags
	cmd := &cobra.Command{
		Use:   "probe [<profile>]",
		Short: "Check an embedding profile with its provider",
		Long: `Embed one text with a profile's model and settings, and report whether the
provider accepts them. Nothing is saved.

Probe a saved profile, optionally with changed settings, or probe settings
before you create a profile with --provider, --model and --dimensions. This
sends a real request to the provider.

The exit code is 0 when the provider accepts the profile or gives no answer,
and 1 when it rejects it. ` + refHelp,
		Example: `  partite embedding-profile probe small
  partite embedding-profile probe --provider my-openai --model text-embedding-3-large --dimensions 1024 --send-dimensions`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg := &Config{}
			var providerID string
			switch {
			case len(args) == 1 && providerRef != "":
				return cmdutil.UsageError("name a saved profile or pass --provider, not both")
			case len(args) == 1:
				id, err := resolve(ctx, app, args[0])
				if err != nil {
					return err
				}
				p, err := profiles.Get(ctx, app, id)
				if err != nil {
					return err
				}
				cfg, providerID = p.Configuration, p.ModelProviderID
			case providerRef == "":
				return cmdutil.UsageError("name a saved profile, or pass --provider, --model and --dimensions")
			default:
				id, err := modelprovider.Providers.Resolve(ctx, app, providerRef)
				if err != nil {
					return err
				}
				providerID = id
			}
			flags.apply(cmd, cfg)
			in, err := cfg.toInput()
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.ModelProbeEmbeddingProfile(ctx, c, gql.ModelProviderCredentials{ProviderID: &providerID}, in)
			if err != nil {
				return err
			}
			r := resp.ProbeEmbeddingProfile
			return modelprovider.ShowProbe(app, "embedding profile", r.Outcome, r.Message)
		},
	}
	flags.add(cmd)
	cmd.Flags().StringVar(&providerRef, "provider", "", "model provider name or id, to probe settings without a saved profile")
	return cmd
}

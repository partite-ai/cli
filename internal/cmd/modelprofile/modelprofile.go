// Package modelprofile manages model profiles: a model of a provider and the
// settings it is called with.
package modelprofile

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
	// Probe asks the server to send one request to the provider before saving,
	// and to refuse a profile the provider rejects.
	Probe     bool          `json:"probe,omitempty"`
	Tags      []cmdutil.Tag `json:"tags"`
	CreatedAt string        `json:"created_at,omitempty"`
	UpdatedAt string        `json:"updated_at,omitempty"`
}

// Config is the model and the settings it is called with. Which settings a
// model accepts is reported by its provider; see partite model-provider models.
type Config struct {
	Model                string   `json:"model"`
	MaxTokens            *int     `json:"max_tokens,omitempty"`
	Temperature          *float64 `json:"temperature,omitempty"`
	TopP                 *float64 `json:"top_p,omitempty"`
	TopK                 *int     `json:"top_k,omitempty"`
	ThinkingBudgetTokens *int     `json:"thinking_budget_tokens,omitempty"`
	ReasoningEffort      string   `json:"reasoning_effort,omitempty"`
	ThinkingType         string   `json:"thinking_type,omitempty"`
	ThinkingDisplay      string   `json:"thinking_display,omitempty"`
}

// enumValue checks v against the values of an enum, ignoring case.
func enumValue[S ~string](field, v string, all []S) (string, error) {
	if v == "" {
		return "", nil
	}
	up := strings.ToUpper(strings.ReplaceAll(v, "-", "_"))
	names := make([]string, len(all))
	for i, a := range all {
		if string(a) == up {
			return up, nil
		}
		names[i] = strings.ToLower(string(a))
	}
	return "", cmdutil.UsageError("%s %q is not one of %s", field, v, strings.Join(names, ", "))
}

func optInt(p *int) gql.Optional[int64] {
	if p == nil {
		return gql.Optional[int64]{}
	}
	return gql.Opt(int64(*p))
}

func (c *Config) toInput() (*gql.ModelProfileConfigInput, error) {
	if c == nil || c.Model == "" {
		return nil, cmdutil.UsageError("a model is required; pass --model or set configuration.model")
	}
	effort, err := enumValue("reasoning_effort", c.ReasoningEffort, gql.AllReasoningEffort)
	if err != nil {
		return nil, err
	}
	thinkingType, err := enumValue("thinking_type", c.ThinkingType, gql.AllThinkingType)
	if err != nil {
		return nil, err
	}
	display, err := enumValue("thinking_display", c.ThinkingDisplay, gql.AllThinkingDisplay)
	if err != nil {
		return nil, err
	}
	in := &gql.ModelProfileConfigInput{
		Model:                c.Model,
		MaxTokens:            optInt(c.MaxTokens),
		Temperature:          gql.OptPtr(c.Temperature),
		TopP:                 gql.OptPtr(c.TopP),
		TopK:                 optInt(c.TopK),
		ThinkingBudgetTokens: optInt(c.ThinkingBudgetTokens),
	}
	if effort != "" {
		in.ReasoningEffort = gql.Opt(effort)
	}
	if thinkingType != "" {
		in.ThinkingType = gql.Opt(thinkingType)
	}
	if display != "" {
		in.ThinkingDisplay = gql.Opt(display)
	}
	return in, nil
}

func fromAPI(d gql.ModelProfileFields) Profile {
	c := d.Configuration
	cfg := &Config{
		Model:                c.Model,
		MaxTokens:            c.MaxTokens,
		Temperature:          c.Temperature,
		TopP:                 c.TopP,
		TopK:                 c.TopK,
		ThinkingBudgetTokens: c.ThinkingBudgetTokens,
		ReasoningEffort:      string(cmdutil.Deref(c.ReasoningEffort)),
		ThinkingType:         string(cmdutil.Deref(c.ThinkingType)),
		ThinkingDisplay:      string(cmdutil.Deref(c.ThinkingDisplay)),
	}
	return Profile{
		ID:                d.Id,
		Name:              d.Name,
		Description:       cmdutil.Deref(d.Description),
		ModelProviderID:   d.Provider.Id,
		ModelProviderName: d.Provider.Name,
		ModelProviderType: modelprovider.TypeName(d.Provider.ProviderType),
		Configuration:     cfg,
		Tags:              cmdutil.TagsFromAPI(d.Tags),
		CreatedAt:         d.CreatedAt,
		UpdatedAt:         d.UpdatedAt,
	}
}

func formatInt(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

func formatFloat(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'g', -1, 64)
}

var profiles = &cmdutil.Resource[Profile]{
	Kind:     "model profile",
	Command:  "model-profile",
	IDPrefix: "cmpr",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Profile, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Profile], error) {
			resp, err := gql.ModelListProfileSummaries(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[Profile]{}, err
			}
			conn := resp.ModelProfileSummaries
			items := make([]Profile, len(conn.Edges))
			for i, e := range conn.Edges {
				n := e.Node
				items[i] = Profile{
					ID:                n.Id,
					Name:              n.Name,
					Description:       cmdutil.Deref(n.Description),
					ModelProviderName: n.ProviderName,
					ModelProviderType: modelprovider.TypeName(n.ProviderType),
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
		resp, err := gql.ModelGetProfile(ctx, c, id)
		if err != nil {
			return Profile{}, err
		}
		return fromAPI(resp.ModelProfile), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteModelProfile(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(p Profile) string { return p.ID },
	Name:    func(p Profile) string { return p.Name },
	Columns: []string{"id", "name", "provider", "type"},
	Row: func(p Profile) []string {
		return []string{p.ID, p.Name, p.ModelProviderName, p.ModelProviderType}
	},
	Fields: func(p Profile) []output.Field {
		c := p.Configuration
		if c == nil {
			c = &Config{}
		}
		return []output.Field{
			{Label: "ID", Value: p.ID},
			{Label: "Name", Value: p.Name},
			{Label: "Description", Value: p.Description},
			{Label: "Provider", Value: fmt.Sprintf("%s (%s, %s)", p.ModelProviderName, p.ModelProviderType, p.ModelProviderID)},
			{Label: "Model", Value: c.Model},
			{Label: "Max tokens", Value: formatInt(c.MaxTokens)},
			{Label: "Temperature", Value: formatFloat(c.Temperature)},
			{Label: "Top p", Value: formatFloat(c.TopP)},
			{Label: "Top k", Value: formatInt(c.TopK)},
			{Label: "Thinking budget tokens", Value: formatInt(c.ThinkingBudgetTokens)},
			{Label: "Reasoning effort", Value: c.ReasoningEffort},
			{Label: "Thinking type", Value: c.ThinkingType},
			{Label: "Thinking display", Value: c.ThinkingDisplay},
			{Label: "Tags", Value: cmdutil.FormatTags(p.Tags)},
			{Label: "Created", Value: p.CreatedAt},
			{Label: "Updated", Value: p.UpdatedAt},
		}
	},
}

// Resolve turns a model profile reference into an id. A reference is an id, a
// profile name, or PROVIDER/NAME when two providers have a profile of that name.
func Resolve(ctx context.Context, app *cmdutil.App, ref string) (string, error) {
	if providerRef, name, ok := strings.Cut(ref, "/"); ok {
		providerID, err := modelprovider.Providers.Resolve(ctx, app, providerRef)
		if err == nil {
			p, found, err := byProviderAndName(ctx, app, providerID, name)
			if err != nil {
				return "", err
			}
			if !found {
				return "", cmdutil.NotFound("model profile", ref, "partite model-profile list")
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
	resp, err := gql.ModelGetProfileByProviderAndName(ctx, c, providerID, name)
	if err != nil {
		if app.Classify(err).Code == cmdutil.ExitNotFound {
			return Profile{}, false, nil
		}
		return Profile{}, false, err
	}
	return fromAPI(resp.ModelProfileByProviderAndName), true, nil
}

const refHelp = `A profile is named by its id, its name, or PROVIDER/NAME when two providers
have a profile with the same name.`

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "model-profile",
		Aliases: []string{"model-profiles", "profile", "profiles"},
		Short:   "Manage model profiles: a model and the settings it is called with",
		Long: `A model profile names a model of a model provider, such as claude-sonnet-4-5
on an Anthropic provider, and the settings it is called with: max tokens,
temperature, reasoning effort and so on. Model sets choose among profiles.

Which settings a model takes depends on the model. To see them, run:
  partite model-provider models PROVIDER

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
		Short:   "Show a model profile",
		Long: `Show a model profile. ` + refHelp + `

With -o yaml or -o json the output is in the same format that -f reads, so
you can save it, edit it and apply it:

  partite model-profile get NAME -o yaml > profile.yaml
  partite model-profile apply -f profile.yaml`,
		Example: "  partite model-profile get sonnet\n  partite model-profile get my-anthropic/sonnet -o yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := Resolve(cmd.Context(), app, args[0])
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
		Short:   "Delete model profiles",
		Long:    "Delete model profiles. " + refHelp,
		Example: "  partite model-profile delete sonnet\n  partite model-profile delete my-anthropic/sonnet --yes",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids := make([]string, len(args))
			for i, ref := range args {
				id, err := Resolve(cmd.Context(), app, ref)
				if err != nil {
					return err
				}
				ids[i] = id
			}
			what := "model profile " + strings.Join(args, ", ")
			if len(args) > 1 {
				what = cmdutil.Plural(len(args), "model profile") + ": " + strings.Join(args, ", ")
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
	in := &gql.NewModelProfile{
		Name:          spec.Name,
		Configuration: *cfg,
		Tags:          cmdutil.TagsToAPI(spec.Tags),
	}
	if spec.Description != "" {
		in.Description = gql.Opt(spec.Description)
	}
	if spec.Probe {
		in.Probe = cmdutil.Ptr(true)
	}
	resp, err := gql.ModelCreateProfile(ctx, c, providerID, in)
	if err != nil {
		return Profile{}, err
	}
	return fromAPI(resp.CreateModelProfile), nil
}

func update(ctx context.Context, app *cmdutil.App, id string, in *gql.UpdateModelProfile) (Profile, error) {
	c, err := app.GQL()
	if err != nil {
		return Profile{}, err
	}
	resp, err := gql.ModelUpdateProfile(ctx, c, id, in)
	if err != nil {
		return Profile{}, err
	}
	return fromAPI(resp.UpdateModelProfile), nil
}

// configFlags are the flags for the common configuration fields.
type configFlags struct {
	model                       string
	maxTokens, topK, thinking   int
	temperature, topP           float64
	effort, thinkingType, shown string
}

var configFlagNames = []string{"model", "max-tokens", "temperature", "top-p", "top-k", "thinking-budget-tokens", "reasoning-effort", "thinking-type", "thinking-display"}

func lower[S ~string](all []S) []string {
	out := make([]string, len(all))
	for i, a := range all {
		out[i] = strings.ToLower(string(a))
	}
	return out
}

func (f *configFlags) add(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.model, "model", "", "the model's id at the provider, such as claude-sonnet-4-5")
	fl.IntVar(&f.maxTokens, "max-tokens", 0, "most tokens the model may produce in one response")
	fl.Float64Var(&f.temperature, "temperature", 0, "how much the output may vary")
	fl.Float64Var(&f.topP, "top-p", 0, "nucleus sampling cutoff")
	fl.IntVar(&f.topK, "top-k", 0, "how many candidate tokens the model considers")
	fl.IntVar(&f.thinking, "thinking-budget-tokens", 0, "tokens the model may spend thinking before it answers")
	fl.StringVar(&f.effort, "reasoning-effort", "", "how hard the model thinks: "+strings.Join(lower(gql.AllReasoningEffort), ", "))
	fl.StringVar(&f.thinkingType, "thinking-type", "", "how the model thinks: "+strings.Join(lower(gql.AllThinkingType), ", "))
	fl.StringVar(&f.shown, "thinking-display", "", "what the model returns of its thinking: "+strings.Join(lower(gql.AllThinkingDisplay), ", "))
	for flag, all := range map[string][]string{
		"reasoning-effort": lower(gql.AllReasoningEffort),
		"thinking-type":    lower(gql.AllThinkingType),
		"thinking-display": lower(gql.AllThinkingDisplay),
	} {
		_ = cmd.RegisterFlagCompletionFunc(flag, cobra.FixedCompletions(all, cobra.ShellCompDirectiveNoFileComp))
	}
}

// apply writes the flags that were passed into c and reports whether any were.
func (f *configFlags) apply(cmd *cobra.Command, c *Config) bool {
	fl := cmd.Flags()
	if fl.Changed("model") {
		c.Model = f.model
	}
	if fl.Changed("max-tokens") {
		c.MaxTokens = cmdutil.Ptr(f.maxTokens)
	}
	if fl.Changed("temperature") {
		c.Temperature = cmdutil.Ptr(f.temperature)
	}
	if fl.Changed("top-p") {
		c.TopP = cmdutil.Ptr(f.topP)
	}
	if fl.Changed("top-k") {
		c.TopK = cmdutil.Ptr(f.topK)
	}
	if fl.Changed("thinking-budget-tokens") {
		c.ThinkingBudgetTokens = cmdutil.Ptr(f.thinking)
	}
	if fl.Changed("reasoning-effort") {
		c.ReasoningEffort = f.effort
	}
	if fl.Changed("thinking-type") {
		c.ThinkingType = f.thinkingType
	}
	if fl.Changed("thinking-display") {
		c.ThinkingDisplay = f.shown
	}
	for _, name := range configFlagNames {
		if fl.Changed(name) {
			return true
		}
	}
	return false
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var spec Profile
	var file string
	var tags []string
	var flags configFlags
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a model profile",
		Long: `Create a model profile on a provider. Pass --provider and --model, and any
settings, or read the whole profile from a file with -f. Flags override the
file.

With --probe, the server sends one request to the provider first, and refuses
a profile the provider rejects.`,
		Example: `  partite model-profile create sonnet --provider my-anthropic --model claude-sonnet-4-5 --max-tokens 8192
  partite model-profile create gpt-think --provider my-openai --model gpt-5 --reasoning-effort high --probe
  partite model-profile create sonnet -f profile.yaml`,
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
			if cmd.Flags().Changed("tag") {
				t, err := cmdutil.ParseTags(tags)
				if err != nil {
					return err
				}
				spec.Tags = t
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
			app.Printer().Done("Created model profile %s (%s).", p.Name, p.ID)
			if app.Format() == output.Table {
				return nil
			}
			return profiles.Show(app, p)
		},
	}
	flags.add(cmd)
	cmd.Flags().StringVar(&spec.ModelProviderID, "provider", "", "model provider name or id")
	cmd.Flags().StringVar(&spec.Description, "description", "", "description")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tag as name=value; repeat for more")
	cmd.Flags().BoolVar(&spec.Probe, "probe", false, "check the profile with the provider before saving")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the profile from a YAML or JSON file")
	return cmd
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var name, description string
	var tags []string
	var probe bool
	var flags configFlags
	cmd := &cobra.Command{
		Use:   "update <profile>",
		Short: "Change a model profile's name, model or settings",
		Long: `Change a model profile. Only the flags you pass change. To remove a setting,
edit the output of get -o yaml and apply it. A profile cannot move to another
provider.

` + refHelp,
		Example: `  partite model-profile update sonnet --max-tokens 16000 --temperature 0.2
  partite model-profile update my-openai/gpt-think --reasoning-effort medium --probe`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := Resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			f := cmd.Flags()
			in := &gql.UpdateModelProfile{}
			if f.Changed("name") {
				in.Name = gql.Opt(name)
			}
			if f.Changed("description") {
				in.Description = gql.Opt(description)
			}
			if f.Changed("tag") {
				t, err := cmdutil.ParseTags(tags)
				if err != nil {
					return err
				}
				in.Tags = gql.Opt(cmdutil.TagsToAPI(t))
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
			if !in.Name.Set && !in.Description.Set && !in.Tags.Set && in.Configuration == nil {
				return cmdutil.UsageError("nothing to change; see --help for the flags")
			}
			if probe {
				in.Probe = cmdutil.Ptr(true)
			}
			p, err := update(cmd.Context(), app, id, in)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated model profile %s.", p.Name)
			if app.Format() == output.Table {
				return nil
			}
			return profiles.Show(app, p)
		},
	}
	flags.add(cmd)
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tags as name=value, replacing the current ones; repeat for more")
	cmd.Flags().BoolVar(&probe, "probe", false, "check the profile with the provider before saving")
	return cmd
}

func newApply(app *cmdutil.App) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "apply -f <file>",
		Short: "Create or update a model profile from a file",
		Long: `Create a model profile from a YAML or JSON file, or update the profile with
the same name on the same provider. Use -f - to read standard input. Fields
that the server sets, such as id and created_at, are ignored, so the output of
get -o yaml can be applied as is. model_provider_id takes a provider name too.`,
		Example: `  partite model-profile apply -f profile.yaml

  # profile.yaml
  name: sonnet
  model_provider_id: my-anthropic
  configuration:
    model: claude-sonnet-4-5
    max_tokens: 8192
    thinking_type: adaptive`,
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
				in := &gql.UpdateModelProfile{
					Name:          gql.Opt(spec.Name),
					Description:   gql.Opt(spec.Description),
					Configuration: cfg,
					Tags:          gql.Opt(cmdutil.TagsToAPI(spec.Tags)),
				}
				if spec.Probe {
					in.Probe = cmdutil.Ptr(true)
				}
				if p, err = update(ctx, app, existing.ID, in); err != nil {
					return err
				}
				app.Printer().Done("Updated model profile %s (%s).", p.Name, p.ID)
			} else {
				if p, err = create(ctx, app, providerID, spec); err != nil {
					return err
				}
				app.Printer().Done("Created model profile %s (%s).", p.Name, p.ID)
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
		Short: "Check a model profile with its provider",
		Long: `Send one request to the provider with a profile's model and settings, and
report whether the provider accepts them. Nothing is saved.

Probe a saved profile, optionally with changed settings, or probe settings
before you create a profile with --provider and --model. This sends a real
request to the provider, which may cost a few tokens.

The exit code is 0 when the provider accepts the profile or gives no answer,
and 1 when it rejects it. ` + refHelp,
		Example: `  partite model-profile probe sonnet
  partite model-profile probe sonnet --temperature 1.5
  partite model-profile probe --provider my-openai --model gpt-5 --reasoning-effort high`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg := &Config{}
			var providerID string
			switch {
			case len(args) == 1 && providerRef != "":
				return cmdutil.UsageError("name a saved profile or pass --provider, not both")
			case len(args) == 1:
				id, err := Resolve(ctx, app, args[0])
				if err != nil {
					return err
				}
				p, err := profiles.Get(ctx, app, id)
				if err != nil {
					return err
				}
				cfg, providerID = p.Configuration, p.ModelProviderID
			case providerRef == "":
				return cmdutil.UsageError("name a saved profile, or pass --provider and --model")
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
			resp, err := gql.ModelProbeProfile(ctx, c, gql.ModelProviderCredentials{ProviderID: &providerID}, in)
			if err != nil {
				return err
			}
			r := resp.ProbeModelProfile
			return modelprovider.ShowProbe(app, "model profile", r.Outcome, r.Message)
		},
	}
	flags.add(cmd)
	cmd.Flags().StringVar(&providerRef, "provider", "", "model provider name or id, to probe settings without a saved profile")
	return cmd
}

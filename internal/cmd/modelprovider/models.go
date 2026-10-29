package modelprovider

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// ListedModel is one model a provider serves.
type ListedModel struct {
	ID                  string   `json:"id"`
	DisplayName         string   `json:"display_name"`
	Flagship            bool     `json:"flagship"`
	SupportedParameters []string `json:"supported_parameters"`
	RequiredParameters  []string `json:"required_parameters"`
	Bounds              []Bound  `json:"bounds"`
	// A nil list means unknown; an empty one means the model accepts none.
	ReasoningEfforts  []string  `json:"reasoning_efforts"`
	ThinkingTypes     []string  `json:"thinking_types"`
	ThinkingDisplays  []string  `json:"thinking_displays"`
	Defaults          *Defaults `json:"defaults,omitempty"`
	DefaultDimensions *int      `json:"default_dimensions,omitempty"`
}

type Bound struct {
	Parameter string  `json:"parameter"`
	Min       float64 `json:"min"`
	Max       float64 `json:"max"`
	Step      float64 `json:"step"`
}

type Defaults struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`
}

// Capabilities is what every provider of one type supports.
type Capabilities struct {
	Type                         string   `json:"type"`
	SupportsModelProfiles        bool     `json:"supports_model_profiles"`
	SupportsEmbeddingProfiles    bool     `json:"supports_embedding_profiles"`
	SupportedParameters          []string `json:"supported_parameters"`
	RequiredParameters           []string `json:"required_parameters"`
	Bounds                       []Bound  `json:"bounds"`
	ReasoningEfforts             []string `json:"reasoning_efforts"`
	ThinkingTypes                []string `json:"thinking_types"`
	ThinkingDisplays             []string `json:"thinking_displays"`
	SupportedEmbeddingParameters []string `json:"supported_embedding_parameters"`
}

// lowerStrings turns enum values such as MAX_TOKENS into max_tokens, the names
// profiles use. A nil slice stays nil.
func lowerStrings[S ~string](in []S) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(string(s))
	}
	return out
}

func enumStrings[S ~string](in []S) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = string(s)
	}
	return out
}

func listedFromAPI(m gql.ModelListedModelFields) ListedModel {
	l := ListedModel{
		ID:                  m.Id,
		DisplayName:         m.DisplayName,
		Flagship:            m.Flagship,
		SupportedParameters: lowerStrings(m.SupportedParameters),
		RequiredParameters:  lowerStrings(m.RequiredParameters),
		ReasoningEfforts:    enumStrings(m.ReasoningEfforts),
		ThinkingTypes:       enumStrings(m.ThinkingTypes),
		ThinkingDisplays:    enumStrings(m.ThinkingDisplays),
	}
	for _, b := range m.Bounds {
		l.Bounds = append(l.Bounds, Bound{Parameter: strings.ToLower(string(b.Parameter)), Min: b.Min, Max: b.Max, Step: b.Step})
	}
	if d := m.Defaults; d.Temperature != nil || d.TopP != nil || d.TopK != nil {
		l.Defaults = &Defaults{Temperature: d.Temperature, TopP: d.TopP, TopK: d.TopK}
	}
	if m.Embedding != nil {
		l.DefaultDimensions = m.Embedding.DefaultDimensions
	}
	return l
}

func newModels(app *cmdutil.App, embedding bool) *cobra.Command {
	var flags configFlags
	use, short, what := "models", "List the models a provider serves", "models"
	if embedding {
		use, short, what = "embedding-models", "List the embedding models a provider serves", "embedding models"
	}
	cmd := &cobra.Command{
		Use:   use + " [<provider>]",
		Short: short,
		Long: fmt.Sprintf(`List the %s a provider serves. The server asks the provider's API
each time, so the list is current.

Name a saved provider, or give a type and a key to look before you create one.
The key is used for this request only and is not saved.

`, what) + secretHelp,
		Example: fmt.Sprintf(`  partite model-provider %s my-anthropic
  partite model-provider %s --type openai --api-key-env OPENAI_API_KEY -o json`, use, use),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			creds, err := credentials(cmd, app, args, &flags)
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			var models []gql.ModelListedModelFields
			if embedding {
				resp, err := gql.ModelListProviderEmbeddingModels(cmd.Context(), c, creds)
				if err != nil {
					return err
				}
				models = resp.ModelProviderEmbeddingModels
			} else {
				resp, err := gql.ModelListProviderModels(cmd.Context(), c, creds)
				if err != nil {
					return err
				}
				models = resp.ModelProviderModels
			}
			items := make([]ListedModel, len(models))
			rows := make([][]string, len(models))
			for i, m := range models {
				items[i] = listedFromAPI(m)
				if embedding {
					dims := ""
					if d := items[i].DefaultDimensions; d != nil {
						dims = strconv.Itoa(*d)
					}
					rows[i] = []string{m.Id, m.DisplayName, dims}
				} else {
					flagship := ""
					if m.Flagship {
						flagship = "yes"
					}
					rows[i] = []string{m.Id, m.DisplayName, flagship, strings.Join(items[i].SupportedParameters, ",")}
				}
			}
			if embedding {
				return app.Printer().List(items, []string{"id", "name", "dimensions"}, rows)
			}
			return app.Printer().List(items, []string{"id", "name", "flagship", "parameters"}, rows)
		},
	}
	flags.add(cmd, true)
	return cmd
}

// credentials names a saved provider when args has one, or else builds
// unsaved credentials from the flags.
func credentials(cmd *cobra.Command, app *cmdutil.App, args []string, flags *configFlags) (gql.ModelProviderCredentials, error) {
	if len(args) == 1 {
		if flags.kind != "" {
			return gql.ModelProviderCredentials{}, cmdutil.UsageError("name a saved provider or pass --type, not both")
		}
		id, err := Providers.Resolve(cmd.Context(), app, args[0])
		if err != nil {
			return gql.ModelProviderCredentials{}, err
		}
		return gql.ModelProviderCredentials{ProviderID: &id}, nil
	}
	if flags.kind == "" {
		return gql.ModelProviderCredentials{}, cmdutil.UsageError("name a saved provider, or pass --type and a key")
	}
	var spec Provider
	if err := flags.apply(app, cmd, &spec); err != nil {
		return gql.ModelProviderCredentials{}, err
	}
	typ, cfg, err := providerConfig(app, spec, nil)
	if err != nil {
		return gql.ModelProviderCredentials{}, err
	}
	return gql.ModelProviderCredentials{Unsaved: &gql.UnsavedModelProviderCredentials{ProviderType: typ, ProviderConfig: cfg}}, nil
}

func newCapabilities(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:   "capabilities <type-or-provider>",
		Short: "Show what providers of a type support",
		Long: `Show what every provider of a type supports: whether it serves model and
embedding profiles, and which profile settings it takes. Give a type, such as
anthropic, or the name of a saved provider. This makes no request to the
provider.

Types: ` + strings.Join(kinds, ", "),
		Example: "  partite model-provider capabilities anthropic\n  partite model-provider capabilities my-openai -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			typ, ok := kindTypes[normalizeKind(args[0])]
			if !ok && normalizeKind(args[0]) == "google-genai" {
				typ, ok = gql.ModelProviderTypeGoogleGenAi, true
			}
			if !ok {
				id, err := Providers.Resolve(cmd.Context(), app, args[0])
				if err != nil {
					return err
				}
				p, err := Providers.Get(cmd.Context(), app, id)
				if err != nil {
					return err
				}
				k, err := p.kind()
				if err != nil {
					return err
				}
				typ = kindTypes[k]
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.ModelGetProviderCapabilities(cmd.Context(), c, typ)
			if err != nil {
				return err
			}
			r := resp.ModelProviderCapabilities
			caps := Capabilities{
				Type:                         TypeName(typ),
				SupportsModelProfiles:        r.SupportsModelProfiles,
				SupportsEmbeddingProfiles:    r.SupportsEmbeddingProfiles,
				SupportedParameters:          lowerStrings(r.SupportedParameters),
				RequiredParameters:           lowerStrings(r.RequiredParameters),
				ReasoningEfforts:             enumStrings(r.ReasoningEfforts),
				ThinkingTypes:                enumStrings(r.ThinkingTypes),
				ThinkingDisplays:             enumStrings(r.ThinkingDisplays),
				SupportedEmbeddingParameters: lowerStrings(r.SupportedEmbeddingParameters),
			}
			for _, b := range r.Bounds {
				caps.Bounds = append(caps.Bounds, Bound{Parameter: strings.ToLower(string(b.Parameter)), Min: b.Min, Max: b.Max, Step: b.Step})
			}
			var bounds []string
			for _, b := range caps.Bounds {
				bounds = append(bounds, fmt.Sprintf("%s %g to %g, step %g", b.Parameter, b.Min, b.Max, b.Step))
			}
			yesNo := map[bool]string{true: "yes", false: "no"}
			unknown := func(s []string) string {
				if s == nil {
					return "unknown"
				}
				if len(s) == 0 {
					return "none"
				}
				return strings.Join(s, ", ")
			}
			return app.Printer().Object(caps, caps.Type, []output.Field{
				{Label: "Type", Value: caps.Type},
				{Label: "Model profiles", Value: yesNo[caps.SupportsModelProfiles]},
				{Label: "Embedding profiles", Value: yesNo[caps.SupportsEmbeddingProfiles]},
				{Label: "Parameters", Value: strings.Join(caps.SupportedParameters, ", ")},
				{Label: "Required", Value: strings.Join(caps.RequiredParameters, ", ")},
				{Label: "Bounds", Value: strings.Join(bounds, "\n")},
				{Label: "Reasoning efforts", Value: unknown(caps.ReasoningEfforts)},
				{Label: "Thinking types", Value: unknown(caps.ThinkingTypes)},
				{Label: "Thinking displays", Value: unknown(caps.ThinkingDisplays)},
				{Label: "Embedding parameters", Value: strings.Join(caps.SupportedEmbeddingParameters, ", ")},
			})
		},
	}
}

// ProbeResult is the provider's answer to a probe of a profile.
type ProbeResult struct {
	Outcome string `json:"outcome"`
	Message string `json:"message,omitempty"`
}

// ShowProbe prints a probe result. what is the thing probed, such as "model
// profile". A rejected profile is an error, so scripts can check the exit code.
func ShowProbe(app *cmdutil.App, what string, outcome gql.ProbeOutcome, message *string) error {
	r := ProbeResult{Outcome: string(outcome), Message: cmdutil.Deref(message)}
	if app.Format() != output.Table {
		if err := app.Printer().Data(r); err != nil {
			return err
		}
	}
	switch outcome {
	case gql.ProbeOutcomeAccepted:
		app.Printer().Done("The provider accepted the %s.", what)
	case gql.ProbeOutcomeUnverified:
		app.Printer().Done("The provider gave no answer, so the %s is not verified: %s", what, r.Message)
	default:
		return &cmdutil.Error{Code: cmdutil.ExitError, Msg: fmt.Sprintf("the provider rejected the %s: %s", what, r.Message)}
	}
	return nil
}

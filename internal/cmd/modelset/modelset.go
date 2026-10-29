// Package modelset manages model sets: rules that choose a model profile for
// each request of a mesh.
package modelset

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmd/modelprofile"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Set is the view printed by get and read by -f.
type Set struct {
	ID                  string        `json:"id,omitempty"`
	Name                string        `json:"name"`
	Description         string        `json:"description,omitempty"`
	DefaultModelProfile ProfileRef    `json:"default_model_profile"`
	Rules               []Rule        `json:"rules"`
	Tags                []cmdutil.Tag `json:"tags"`
	CreatedAt           string        `json:"created_at,omitempty"`
	UpdatedAt           string        `json:"updated_at,omitempty"`
}

type Rule struct {
	Complexity   string        `json:"complexity,omitempty"`
	MatchTags    []cmdutil.Tag `json:"match_tags,omitempty"`
	ModelProfile ProfileRef    `json:"model_profile"`
}

// ProfileRef names a model profile. On input it may also be a plain string: a
// profile name, PROVIDER/NAME, or an id. When name is set it wins over id, so
// a spec can be edited by name or applied to another organization.
type ProfileRef struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
}

func (r *ProfileRef) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*r = ProfileRef{Name: s}
		return nil
	}
	type plain ProfileRef
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode((*plain)(r))
}

func (r ProfileRef) resolve(ctx context.Context, app *cmdutil.App) (string, error) {
	switch {
	case r.Name != "" && r.ProviderID != "":
		return modelprofile.Resolve(ctx, app, r.ProviderID+"/"+r.Name)
	case r.Name != "":
		return modelprofile.Resolve(ctx, app, r.Name)
	case r.ID != "":
		return r.ID, nil
	}
	return "", cmdutil.UsageError("a model profile is missing its name or id")
}

func (r ProfileRef) String() string {
	if r.Name == "" {
		return r.ID
	}
	return r.Name
}

func fromAPI(d gql.ModelSetDetails) Set {
	s := Set{
		ID:          d.Id,
		Name:        d.Name,
		Description: cmdutil.Deref(d.Description),
		DefaultModelProfile: ProfileRef{
			ID:         d.DefaultModelProfile.Id,
			Name:       d.DefaultModelProfile.Name,
			ProviderID: d.DefaultModelProfile.Provider.Id,
		},
		Rules:     make([]Rule, len(d.Rules)),
		Tags:      cmdutil.TagsFromAPI(d.Tags),
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}
	for i, r := range d.Rules {
		s.Rules[i] = Rule{
			Complexity:   strings.ToLower(string(cmdutil.Deref(r.Complexity))),
			MatchTags:    cmdutil.TagsFromAPI(r.MatchTags),
			ModelProfile: ProfileRef{ID: r.ModelProfileID, Name: r.ModelProfileName, ProviderID: r.ModelProviderID},
		}
	}
	return s
}

func complexity(v string) (string, error) {
	up := strings.ToUpper(v)
	for _, c := range gql.AllModelComplexity {
		if string(c) == up {
			return up, nil
		}
	}
	return "", cmdutil.UsageError("complexity %q is not one of low, medium, high", v)
}

// toInput resolves the profile names in spec into ids.
func toInput(ctx context.Context, app *cmdutil.App, spec Set) (string, []gql.ModelSetRuleInput, error) {
	defaultID, err := spec.DefaultModelProfile.resolve(ctx, app)
	if err != nil {
		return "", nil, err
	}
	rules := make([]gql.ModelSetRuleInput, len(spec.Rules))
	for i, r := range spec.Rules {
		id, err := r.ModelProfile.resolve(ctx, app)
		if err != nil {
			return "", nil, err
		}
		rules[i] = gql.ModelSetRuleInput{ModelProfileID: id}
		if r.Complexity != "" {
			c, err := complexity(r.Complexity)
			if err != nil {
				return "", nil, err
			}
			rules[i].Complexity = gql.Opt(c)
		}
		if len(r.MatchTags) > 0 {
			rules[i].MatchTags = gql.Opt(cmdutil.TagsToAPI(r.MatchTags))
		}
	}
	return defaultID, rules, nil
}

func formatRules(rules []Rule) string {
	lines := make([]string, len(rules))
	for i, r := range rules {
		var when []string
		if r.Complexity != "" {
			when = append(when, r.Complexity+" complexity")
		}
		if len(r.MatchTags) > 0 {
			when = append(when, "tags "+cmdutil.FormatTags(r.MatchTags))
		}
		if len(when) == 0 {
			when = append(when, "always")
		}
		lines[i] = fmt.Sprintf("%s: %s", strings.Join(when, " and "), r.ModelProfile)
	}
	return strings.Join(lines, "\n")
}

var Sets = &cmdutil.Resource[Set]{
	WebList:  "models/model-sets",
	WebPage:  "models/model-sets/%s",
	WebLink:  "model-set",
	Kind:     "model set",
	Command:  "model-set",
	IDPrefix: "cmds",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Set, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Set], error) {
			resp, err := gql.ModelListSets(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[Set]{}, err
			}
			conn := resp.ModelSets
			items := make([]Set, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = fromAPI(e.Node)
			}
			return cmdutil.Page[Set]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Set, error) {
		c, err := app.GQL()
		if err != nil {
			return Set{}, err
		}
		resp, err := gql.GetModelSet(ctx, c, id)
		if err != nil {
			return Set{}, err
		}
		return fromAPI(resp.ModelSet), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteModelSet(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(s Set) string { return s.ID },
	Name:    func(s Set) string { return s.Name },
	Columns: []string{"id", "name", "default", "rules", "created"},
	Row: func(s Set) []string {
		return []string{s.ID, s.Name, s.DefaultModelProfile.String(), fmt.Sprint(len(s.Rules)), cmdutil.FormatTime(s.CreatedAt)}
	},
	Fields: func(s Set) []output.Field {
		return []output.Field{
			{Label: "ID", Value: s.ID},
			{Label: "Name", Value: s.Name},
			{Label: "Description", Value: s.Description},
			{Label: "Default", Value: fmt.Sprintf("%s (%s)", s.DefaultModelProfile.Name, s.DefaultModelProfile.ID)},
			{Label: "Rules", Value: formatRules(s.Rules)},
			{Label: "Tags", Value: cmdutil.FormatTags(s.Tags)},
			{Label: "Created", Value: s.CreatedAt},
			{Label: "Updated", Value: s.UpdatedAt},
		}
	},
}

const profileHelp = `A profile is named by its name, PROVIDER/NAME, or its id. In a file,
default_model_profile and model_profile take that string, or an object with
id, or name and provider_id, as get -o yaml prints it.`

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "model-set",
		Aliases: []string{"model-sets"},
		Short:   "Manage model sets: rules that choose a model profile",
		Long: `A model set chooses the model profile for each request of a mesh. Each rule
names a profile and when to use it: for agents of a complexity (low, medium or
high), for requests with matching tags, or both. The first rule that matches wins, and
the default profile is used when none does.

` + profileHelp,
	}
	cmd.AddCommand(
		Sets.ListCmd(app),
		Sets.GetCmd(app),
		Sets.ViewCmd(app),
		newCreate(app),
		newUpdate(app),
		Sets.ApplyCmd(app, `  partite model-set apply -f models.yaml

  # models.yaml
  name: default
  default_model_profile: my-anthropic/sonnet
  rules:
    - complexity: high
      model_profile: my-anthropic/opus
    - match_tags:
        - name: team
          value: support
      model_profile: haiku`, func(ctx context.Context, spec Set, id string) (Set, error) {
			if id == "" {
				return create(ctx, app, spec)
			}
			return update(ctx, app, id, spec)
		}),
		Sets.DeleteCmd(app),
	)
	return cmd
}

func create(ctx context.Context, app *cmdutil.App, spec Set) (Set, error) {
	defaultID, rules, err := toInput(ctx, app, spec)
	if err != nil {
		return Set{}, err
	}
	c, err := app.GQL()
	if err != nil {
		return Set{}, err
	}
	in := &gql.NewModelSet{
		Name:                  spec.Name,
		Tags:                  cmdutil.TagsToAPI(spec.Tags),
		DefaultModelProfileID: defaultID,
		Rules:                 rules,
	}
	if spec.Description != "" {
		in.Description = gql.Opt(spec.Description)
	}
	resp, err := gql.CreateModelSet(ctx, c, in)
	if err != nil {
		return Set{}, err
	}
	return fromAPI(resp.CreateModelSet), nil
}

// update replaces every field of the set with spec.
func update(ctx context.Context, app *cmdutil.App, id string, spec Set) (Set, error) {
	defaultID, rules, err := toInput(ctx, app, spec)
	if err != nil {
		return Set{}, err
	}
	c, err := app.GQL()
	if err != nil {
		return Set{}, err
	}
	resp, err := gql.ModifyModelSet(ctx, c, id, &gql.UpdateModelSet{
		Name:                  gql.Opt(spec.Name),
		Description:           gql.Opt(spec.Description),
		Tags:                  gql.Opt(cmdutil.TagsToAPI(spec.Tags)),
		DefaultModelProfileID: gql.Opt(defaultID),
		Rules:                 gql.Opt(rules),
	})
	if err != nil {
		return Set{}, err
	}
	return fromAPI(resp.UpdateModelSet), nil
}

// parseRules reads --rule flags in COMPLEXITY=PROFILE form.
func parseRules(flags []string) ([]Rule, error) {
	rules := make([]Rule, len(flags))
	for i, f := range flags {
		c, profile, ok := strings.Cut(f, "=")
		if !ok || profile == "" {
			return nil, cmdutil.UsageError("--rule must be COMPLEXITY=PROFILE, such as high=opus; got %q", f)
		}
		if _, err := complexity(c); err != nil {
			return nil, err
		}
		rules[i] = Rule{Complexity: strings.ToLower(c), ModelProfile: ProfileRef{Name: profile}}
	}
	return rules, nil
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var spec Set
	var file, defaultProfile string
	var tags, rules []string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a model set",
		Long: `Create a model set. Pass --default and any --rule flags, or read the whole set
from a file with -f. Flags override the file. Rules that match tags can only
be given in a file.

` + profileHelp,
		Example: `  partite model-set create default --default sonnet --rule high=opus --rule low=haiku
  partite model-set create default -f models.yaml`,
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
			f := cmd.Flags()
			if f.Changed("default") {
				spec.DefaultModelProfile = ProfileRef{Name: defaultProfile}
			}
			if spec.DefaultModelProfile == (ProfileRef{}) {
				return cmdutil.UsageError("a default model profile is required; pass --default")
			}
			if f.Changed("rule") {
				r, err := parseRules(rules)
				if err != nil {
					return err
				}
				spec.Rules = r
			}
			if f.Changed("tag") {
				t, err := cmdutil.ParseTags(tags)
				if err != nil {
					return err
				}
				spec.Tags = t
			}
			s, err := create(cmd.Context(), app, spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Created model set %s (%s).", s.Name, s.ID)
			if app.Format() == output.Table {
				return nil
			}
			return Sets.Show(app, s)
		},
	}
	cmd.Flags().StringVar(&defaultProfile, "default", "", "model profile to use when no rule matches")
	cmd.Flags().StringArrayVar(&rules, "rule", nil, "rule as COMPLEXITY=PROFILE, such as high=opus; repeat for more, in order")
	cmd.Flags().StringVar(&spec.Description, "description", "", "description")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tag as name=value; repeat for more")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the set from a YAML or JSON file")
	return cmd
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var name, description, defaultProfile string
	var tags, rules []string
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change a model set's name, default profile or rules",
		Long: `Change a model set. Only the flags you pass change. --rule replaces all the
rules, including rules that match tags; to edit those, use get -o yaml and
apply.

` + profileHelp,
		Example: `  partite model-set update default --default my-anthropic/sonnet
  partite model-set update default --rule high=opus --rule medium=sonnet`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := Sets.Resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			f := cmd.Flags()
			if !f.Changed("name") && !f.Changed("description") && !f.Changed("tag") && !f.Changed("default") && !f.Changed("rule") {
				return cmdutil.UsageError("nothing to change; pass --name, --description, --tag, --default or --rule")
			}
			spec, err := Sets.Get(cmd.Context(), app, id)
			if err != nil {
				return err
			}
			if f.Changed("name") {
				spec.Name = name
			}
			if f.Changed("description") {
				spec.Description = description
			}
			if f.Changed("tag") {
				if spec.Tags, err = cmdutil.ParseTags(tags); err != nil {
					return err
				}
			}
			if f.Changed("default") {
				spec.DefaultModelProfile = ProfileRef{Name: defaultProfile}
			}
			if f.Changed("rule") {
				if spec.Rules, err = parseRules(rules); err != nil {
					return err
				}
			}
			s, err := update(cmd.Context(), app, id, spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated model set %s.", s.Name)
			if app.Format() == output.Table {
				return nil
			}
			return Sets.Show(app, s)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringVar(&defaultProfile, "default", "", "model profile to use when no rule matches")
	cmd.Flags().StringArrayVar(&rules, "rule", nil, "rule as COMPLEXITY=PROFILE, replacing all rules; repeat for more, in order")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tags as name=value, replacing the current ones; repeat for more")
	return cmd
}

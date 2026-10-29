package eval

import (
	"context"
	"fmt"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// JudgeMetric is the view printed by get and read by -f.
type JudgeMetric struct {
	ID          string         `json:"id,omitempty"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Category    string         `json:"category"`
	Complexity  string         `json:"complexity"`
	Scope       string         `json:"scope"`
	Prompt      string         `json:"prompt"`
	Examples    []ScoreExample `json:"examples,omitempty"`
	InputFields []InputField   `json:"input_fields,omitempty"`
	CreatedAt   string         `json:"created_at,omitempty"`
	UpdatedAt   string         `json:"updated_at,omitempty"`
}

type ScoreExample struct {
	Rating   int    `json:"rating"`
	Scenario string `json:"scenario"`
	Judgment string `json:"judgment"`
}

type InputField struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
}

func judgeMetricFromAPI(d gql.JudgeMetricDetails) JudgeMetric {
	m := JudgeMetric{
		ID:          d.Id,
		Name:        d.Name,
		Description: cmdutil.Deref(d.Description),
		Category:    string(d.Category),
		Complexity:  string(d.Complexity),
		Scope:       string(d.Scope),
		Prompt:      d.Prompt,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
	for _, e := range d.Examples {
		m.Examples = append(m.Examples, ScoreExample{Rating: e.Rating, Scenario: e.Scenario, Judgment: e.Judgment})
	}
	for _, f := range d.InputFields {
		m.InputFields = append(m.InputFields, InputField{Name: f.Name, Label: f.Label, Description: f.Description, Type: string(f.Type), Required: f.Required})
	}
	return m
}

// judgeMetricInput checks a view and turns it into the create input. The
// update input carries the same fields.
func judgeMetricInput(m JudgeMetric) (gql.NewJudgeMetric, error) {
	if m.Name == "" {
		return gql.NewJudgeMetric{}, cmdutil.UsageError("a name is required")
	}
	if strings.TrimSpace(m.Prompt) == "" {
		return gql.NewJudgeMetric{}, cmdutil.UsageError("a prompt is required; pass --prompt or set prompt in the file")
	}
	category, err := upperChoice("category", m.Category, gql.AllMetricCategory)
	if err != nil {
		return gql.NewJudgeMetric{}, err
	}
	scope, err := upperChoice("scope", m.Scope, gql.AllJudgeScope)
	if err != nil {
		return gql.NewJudgeMetric{}, err
	}
	complexity, err := upperChoice("complexity", m.Complexity, gql.AllModelComplexity)
	if err != nil {
		return gql.NewJudgeMetric{}, err
	}
	in := gql.NewJudgeMetric{
		Name:        m.Name,
		Description: cmdutil.OptString(m.Description),
		Category:    category,
		Scope:       scope,
		Prompt:      m.Prompt,
		Complexity:  complexity,
		Examples:    []gql.NewScoreExample{},
		InputFields: []gql.NewJudgeInputField{},
	}
	for _, e := range m.Examples {
		in.Examples = append(in.Examples, gql.NewScoreExample{Rating: e.Rating, Scenario: e.Scenario, Judgment: e.Judgment})
	}
	for _, f := range m.InputFields {
		t, err := upperChoice(fmt.Sprintf("input field %q type", f.Name), f.Type, gql.AllJudgeInputFieldType)
		if err != nil {
			return gql.NewJudgeMetric{}, err
		}
		in.InputFields = append(in.InputFields, gql.NewJudgeInputField{Name: f.Name, Label: f.Label, Description: f.Description, Type: t, Required: f.Required})
	}
	return in, nil
}

var judgeMetricResource = &cmdutil.Resource[JudgeMetric]{
	WebList:    "observability/metrics",
	WebPage:    "observability/metrics/%s",
	WebLink:    "eval-judge-metric",
	Kind:       "judge metric",
	Command:    "eval judge-metric",
	IDPrefix:   "eumt",
	Enterprise: true,
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]JudgeMetric, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[JudgeMetric], error) {
			resp, err := gql.EnterpriseListJudgeMetrics(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[JudgeMetric]{}, err
			}
			conn := resp.Metrics
			var items []JudgeMetric
			for _, e := range conn.Edges {
				if n, ok := e.Node.(*gql.EnterpriseListJudgeMetricsMetricsMetricConnectionEdgesMetricEdgeNodeJudgeMetric); ok {
					items = append(items, judgeMetricFromAPI(n.JudgeMetricDetails))
				}
			}
			return cmdutil.Page[JudgeMetric]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (JudgeMetric, error) {
		c, err := app.GQL()
		if err != nil {
			return JudgeMetric{}, err
		}
		resp, err := gql.GetJudgeMetric(ctx, c, id)
		if err != nil {
			return JudgeMetric{}, err
		}
		if resp.JudgeMetric == nil {
			return JudgeMetric{}, cmdutil.NotFound("judge metric", id, "partite eval judge-metric list")
		}
		return judgeMetricFromAPI(*resp.JudgeMetric), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		_, err = gql.EnterpriseDeleteJudgeMetrics(ctx, c, ids)
		return err
	},
	ID:      func(m JudgeMetric) string { return m.ID },
	Name:    func(m JudgeMetric) string { return m.Name },
	Columns: []string{"id", "name", "category", "scope", "complexity", "description"},
	Row: func(m JudgeMetric) []string {
		return []string{m.ID, m.Name, m.Category, m.Scope, m.Complexity, m.Description}
	},
	Fields: func(m JudgeMetric) []output.Field {
		var examples, inputs []string
		for _, e := range m.Examples {
			examples = append(examples, fmt.Sprintf("%d: %s\n  -> %s", e.Rating, e.Scenario, e.Judgment))
		}
		for _, f := range m.InputFields {
			req := "optional"
			if f.Required {
				req = "required"
			}
			inputs = append(inputs, fmt.Sprintf("%s (%s, %s): %s", f.Name, f.Type, req, f.Description))
		}
		return []output.Field{
			{Label: "ID", Value: m.ID},
			{Label: "Name", Value: m.Name},
			{Label: "Description", Value: m.Description},
			{Label: "Category", Value: m.Category},
			{Label: "Scope", Value: m.Scope},
			{Label: "Complexity", Value: m.Complexity},
			{Label: "Prompt", Value: m.Prompt + "\n"},
			{Label: "Examples", Value: multiline(examples)},
			{Label: "Input fields", Value: multiline(inputs)},
			{Label: "Created", Value: m.CreatedAt},
			{Label: "Updated", Value: m.UpdatedAt},
		}
	},
}

// multiline joins lines so that the Object printer shows them as a block.
func multiline(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

const judgeMetricLong = `A judge metric is a question a model answers about a conversation, scored as
a whole number rating. Its prompt tells the judging model what to look for;
examples anchor the scale. Put judge metrics in a rubric to use them.

Every organization starts with built-in judge metrics, which can be edited
like any other.

Fields:
  category     ` + "QUALITY, SAFETY, SECURITY, RELIABILITY, EFFICIENCY or COST" + `
  scope        REQUEST judges the whole request; HOP judges each hop between agents
  complexity   LOW, MEDIUM or HIGH: how capable a model judges it
  prompt       what the judging model is asked
  examples     worked examples: rating, scenario, judgment (file only)
  input_fields values a rubric fills in when it uses the metric: name, label,
               description, type (STRING, NUMBER, BOOLEAN, STRING_LIST,
               JSON_OBJECT), required (file only)`

func newJudgeMetric(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "judge-metric",
		Aliases: []string{"judge-metrics", "metric", "metrics"},
		Short:   "Manage metrics a model judges",
		Long:    judgeMetricLong,
	}
	cmd.AddCommand(
		judgeMetricResource.ListCmd(app),
		judgeMetricResource.GetCmd(app),
		judgeMetricResource.ViewCmd(app),
		newJudgeMetricCreate(app),
		newJudgeMetricUpdate(app),
		judgeMetricResource.ApplyCmd(app, "  partite eval judge-metric apply -f helpfulness.yaml", func(ctx context.Context, spec JudgeMetric, id string) (JudgeMetric, error) {
			return saveJudgeMetric(ctx, app, id, spec)
		}),
		judgeMetricResource.DeleteCmd(app),
	)
	return cmd
}

// saveJudgeMetric creates the metric when id is "", otherwise replaces every
// field of the one with that id.
func saveJudgeMetric(ctx context.Context, app *cmdutil.App, id string, m JudgeMetric) (JudgeMetric, error) {
	in, err := judgeMetricInput(m)
	if err != nil {
		return JudgeMetric{}, err
	}
	c, err := app.GQL()
	if err != nil {
		return JudgeMetric{}, err
	}
	if id == "" {
		resp, err := gql.CreateJudgeMetric(ctx, c, in)
		if err != nil {
			return JudgeMetric{}, err
		}
		return judgeMetricFromAPI(resp.CreateJudgeMetric), nil
	}
	// Every field goes, so that a field left out of a file is cleared.
	resp, err := gql.ModifyJudgeMetric(ctx, c, id, &gql.UpdateJudgeMetric{
		Name:        &in.Name,
		Description: in.Description,
		Category:    &in.Category,
		Scope:       &in.Scope,
		Prompt:      &in.Prompt,
		Complexity:  &in.Complexity,
		Examples:    in.Examples,
		InputFields: in.InputFields,
	})
	if err != nil {
		return JudgeMetric{}, err
	}
	return judgeMetricFromAPI(resp.UpdateJudgeMetric), nil
}

func addJudgeMetricFlags(cmd *cobra.Command, m *JudgeMetric, prompt *string) {
	f := cmd.Flags()
	f.StringVar(&m.Description, "description", "", "description")
	f.StringVar(&m.Category, "category", "", "QUALITY, SAFETY, SECURITY, RELIABILITY, EFFICIENCY or COST")
	f.StringVar(&m.Scope, "scope", "", "REQUEST (the whole request) or HOP (each hop between agents)")
	f.StringVar(&m.Complexity, "complexity", "", "LOW, MEDIUM or HIGH")
	f.StringVar(prompt, "prompt", "", "what the judging model is asked; @file reads a file")
	_ = cmd.RegisterFlagCompletionFunc("category", cobra.FixedCompletions(choiceNames(gql.AllMetricCategory), cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("scope", cobra.FixedCompletions(choiceNames(gql.AllJudgeScope), cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("complexity", cobra.FixedCompletions(choiceNames(gql.AllModelComplexity), cobra.ShellCompDirectiveNoFileComp))
}

func newJudgeMetricCreate(app *cmdutil.App) *cobra.Command {
	var flags JudgeMetric
	var prompt, file string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a judge metric",
		Long: `Create a judge metric. Pass the fields as flags, or put them in a file with
-f; flags override the file. Examples and input fields can only come from a
file. Scope defaults to REQUEST and complexity to MEDIUM.`,
		Example: `  partite eval judge-metric create politeness --category QUALITY --prompt @politeness.md
  partite eval judge-metric create -f politeness.yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := judgeMetricResource.RequireEnterprise(app); err != nil {
				return err
			}
			var spec JudgeMetric
			if file != "" {
				if err := app.ReadSpec(file, &spec); err != nil {
					return err
				}
			}
			if len(args) == 1 {
				spec.Name = args[0]
			}
			f := cmd.Flags()
			if f.Changed("description") {
				spec.Description = flags.Description
			}
			if f.Changed("category") {
				spec.Category = flags.Category
			}
			if f.Changed("scope") {
				spec.Scope = flags.Scope
			}
			if f.Changed("complexity") {
				spec.Complexity = flags.Complexity
			}
			if f.Changed("prompt") {
				p, err := app.ReadValue(prompt)
				if err != nil {
					return err
				}
				spec.Prompt = p
			}
			if spec.Scope == "" {
				spec.Scope = string(gql.JudgeScopeRequest)
			}
			if spec.Complexity == "" {
				spec.Complexity = string(gql.ModelComplexityMedium)
			}
			m, err := saveJudgeMetric(cmd.Context(), app, "", spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Created judge metric %s (%s).", m.Name, m.ID)
			if app.Format() == output.Table {
				return nil
			}
			return judgeMetricResource.Show(app, m)
		},
	}
	addJudgeMetricFlags(cmd, &flags, &prompt)
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the metric from a YAML or JSON file")
	return cmd
}

func newJudgeMetricUpdate(app *cmdutil.App) *cobra.Command {
	var flags JudgeMetric
	var name, prompt string
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change a judge metric",
		Long: `Change the fields of a judge metric that are passed as flags. To change
examples or input fields, use get -o yaml, edit the file, and apply it.`,
		Example: `  partite eval judge-metric update politeness --complexity HIGH
  partite eval judge-metric update politeness --prompt @politeness.md`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := judgeMetricResource.Resolve(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			m, err := judgeMetricResource.Get(cmd.Context(), app, id)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			changed := false
			for _, n := range []string{"name", "description", "category", "scope", "complexity", "prompt"} {
				changed = changed || f.Changed(n)
			}
			if !changed {
				return cmdutil.UsageError("nothing to change; pass --name, --description, --category, --scope, --complexity or --prompt")
			}
			if f.Changed("name") {
				m.Name = name
			}
			if f.Changed("description") {
				m.Description = flags.Description
			}
			if f.Changed("category") {
				m.Category = flags.Category
			}
			if f.Changed("scope") {
				m.Scope = flags.Scope
			}
			if f.Changed("complexity") {
				m.Complexity = flags.Complexity
			}
			if f.Changed("prompt") {
				p, err := app.ReadValue(prompt)
				if err != nil {
					return err
				}
				m.Prompt = p
			}
			m, err = saveJudgeMetric(cmd.Context(), app, id, m)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated judge metric %s.", m.Name)
			if app.Format() == output.Table {
				return nil
			}
			return judgeMetricResource.Show(app, m)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	addJudgeMetricFlags(cmd, &flags, &prompt)
	return cmd
}

// Package boundary manages boundaries: rules that refuse things in
// conversations, and modifications that set things on them.
package boundary

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

type Rule struct {
	Name       string `json:"name"`
	Action     string `json:"action"`
	When       string `json:"when,omitempty"`
	Effect     string `json:"effect"`
	Message    string `json:"message,omitempty"`
	ReportOnly bool   `json:"report_only,omitempty"`
}

type Modification struct {
	Name         string            `json:"name"`
	Action       string            `json:"action"`
	When         string            `json:"when,omitempty"`
	SetTags      map[string]string `json:"set_tags,omitempty"`
	SetTracing   string            `json:"set_tracing,omitempty"`
	SetArguments map[string]string `json:"set_arguments,omitempty"`
}

// Boundary is the view printed by get and read by -f.
type Boundary struct {
	ID            string         `json:"id,omitempty"`
	Name          string         `json:"name"`
	Description   string         `json:"description,omitempty"`
	ReportOnly    bool           `json:"report_only"`
	Rules         []Rule         `json:"rules"`
	Modifications []Modification `json:"modifications"`
	Warnings      []string       `json:"warnings,omitempty"`
	CreatedAt     string         `json:"created_at,omitempty"`
	UpdatedAt     string         `json:"updated_at,omitempty"`

	meshNames        []string
	accessPointNames []string
}

func fromAPI(d gql.BoundaryView) Boundary {
	b := Boundary{
		ID:            d.Id,
		Name:          d.Name,
		Description:   cmdutil.Deref(d.Description),
		ReportOnly:    d.ReportOnly,
		Rules:         make([]Rule, len(d.Rules)),
		Modifications: make([]Modification, len(d.Modifications)),
		Warnings:      d.Warnings,
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
	}
	for i, r := range d.Rules {
		b.Rules[i] = Rule{
			Name:       r.Name,
			Action:     string(r.Action),
			When:       cmdutil.Deref(r.When),
			Effect:     string(r.Effect),
			Message:    cmdutil.Deref(r.Message),
			ReportOnly: r.ReportOnly,
		}
	}
	for i, m := range d.Modifications {
		mod := Modification{
			Name:       m.Name,
			Action:     string(m.Action),
			When:       cmdutil.Deref(m.When),
			SetTracing: cmdutil.Deref(m.SetTracing),
		}
		for _, t := range m.SetTags {
			if mod.SetTags == nil {
				mod.SetTags = map[string]string{}
			}
			mod.SetTags[t.Name] = t.Expression
		}
		for _, a := range m.SetArguments {
			if mod.SetArguments == nil {
				mod.SetArguments = map[string]string{}
			}
			mod.SetArguments[a.Name] = a.Expression
		}
		b.Modifications[i] = mod
	}
	for _, m := range d.Meshes {
		b.meshNames = append(b.meshNames, m.Name)
	}
	for _, ap := range d.AccessPoints {
		b.accessPointNames = append(b.accessPointNames, ap.Name)
	}
	return b
}

func ruleInputs(rules []Rule) []gql.BoundaryRuleInput {
	in := make([]gql.BoundaryRuleInput, len(rules))
	for i, r := range rules {
		in[i] = gql.BoundaryRuleInput{
			Name:       r.Name,
			Action:     gql.BoundaryAction(strings.ToUpper(r.Action)),
			When:       cmdutil.OptString(r.When),
			Effect:     gql.BoundaryEffect(strings.ToUpper(r.Effect)),
			Message:    cmdutil.OptString(r.Message),
			ReportOnly: r.ReportOnly,
		}
	}
	return in
}

func modificationInputs(mods []Modification) []gql.BoundaryModificationInput {
	in := make([]gql.BoundaryModificationInput, len(mods))
	for i, m := range mods {
		in[i] = gql.BoundaryModificationInput{
			Name:         m.Name,
			Action:       gql.BoundaryAction(strings.ToUpper(m.Action)),
			When:         cmdutil.OptString(m.When),
			SetTags:      namedExpressions(m.SetTags),
			SetTracing:   cmdutil.OptString(m.SetTracing),
			SetArguments: namedExpressions(m.SetArguments),
		}
	}
	return in
}

// namedExpressions sorts by name so that the same map always gives the same
// input.
func namedExpressions(m map[string]string) []gql.BoundaryNamedExpressionInput {
	in := make([]gql.BoundaryNamedExpressionInput, 0, len(m))
	for name, expr := range m {
		in = append(in, gql.BoundaryNamedExpressionInput{Name: name, Expression: expr})
	}
	slices.SortFunc(in, func(a, b gql.BoundaryNamedExpressionInput) int { return strings.Compare(a.Name, b.Name) })
	return in
}

func formatRule(r Rule) string {
	s := fmt.Sprintf("%s: %s %s", r.Name, r.Effect, r.Action)
	if r.When != "" {
		s += " when " + r.When
	}
	if r.ReportOnly {
		s += " (report only)"
	}
	return s
}

func formatModification(m Modification) string {
	var sets []string
	for _, e := range namedExpressions(m.SetTags) {
		sets = append(sets, fmt.Sprintf("tag %s = %s", e.Name, e.Expression))
	}
	if m.SetTracing != "" {
		sets = append(sets, "tracing = "+m.SetTracing)
	}
	for _, e := range namedExpressions(m.SetArguments) {
		sets = append(sets, fmt.Sprintf("argument %s = %s", e.Name, e.Expression))
	}
	s := fmt.Sprintf("%s: %s sets %s", m.Name, m.Action, strings.Join(sets, ", "))
	if m.When != "" {
		s += " when " + m.When
	}
	return s
}

// Boundaries is the boundary resource. The mesh and access point commands use
// it to resolve boundary names.
var Boundaries = &cmdutil.Resource[Boundary]{
	Kind:     "boundary",
	Command:  "boundary",
	IDPrefix: "cbnd",
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Boundary, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Boundary], error) {
			resp, err := gql.BoundaryList(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[Boundary]{}, err
			}
			conn := resp.Boundaries
			items := make([]Boundary, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = fromAPI(e.Node)
			}
			return cmdutil.Page[Boundary]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Boundary, error) {
		c, err := app.GQL()
		if err != nil {
			return Boundary{}, err
		}
		resp, err := gql.BoundaryGet(ctx, c, id)
		if err != nil {
			return Boundary{}, err
		}
		if resp.Boundary == nil {
			return Boundary{}, cmdutil.NotFound("boundary", id, "partite boundary list")
		}
		return fromAPI(*resp.Boundary), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		_, err = gql.BoundaryDelete(ctx, c, ids)
		return err
	},
	ID:      func(b Boundary) string { return b.ID },
	Name:    func(b Boundary) string { return b.Name },
	Columns: []string{"id", "name", "rules", "modifications", "report only", "created"},
	Row: func(b Boundary) []string {
		return []string{b.ID, b.Name, fmt.Sprint(len(b.Rules)), fmt.Sprint(len(b.Modifications)), fmt.Sprint(b.ReportOnly), cmdutil.FormatTime(b.CreatedAt)}
	},
	Fields: func(b Boundary) []output.Field {
		rules := make([]string, len(b.Rules))
		for i, r := range b.Rules {
			rules[i] = formatRule(r)
		}
		mods := make([]string, len(b.Modifications))
		for i, m := range b.Modifications {
			mods[i] = formatModification(m)
		}
		return []output.Field{
			{Label: "ID", Value: b.ID},
			{Label: "Name", Value: b.Name},
			{Label: "Description", Value: b.Description},
			{Label: "Report only", Value: fmt.Sprint(b.ReportOnly)},
			{Label: "Rules", Value: strings.Join(rules, "\n")},
			{Label: "Modifications", Value: strings.Join(mods, "\n")},
			{Label: "Warnings", Value: strings.Join(b.Warnings, "\n")},
			{Label: "Meshes", Value: strings.Join(b.meshNames, ", ")},
			{Label: "Access points", Value: strings.Join(b.accessPointNames, ", ")},
			{Label: "Created", Value: b.CreatedAt},
			{Label: "Updated", Value: b.UpdatedAt},
		}
	},
}

var longHelp = `A boundary limits what conversations can do. Attach it to a mesh to check
every conversation on the mesh, or to an access point to check every
conversation created through it.

Rules refuse an action when their CEL expression (when) is true. A DENY rule
refuses it. An ASK rule makes a tool call wait for the user to approve it.
Modifications set tags, tracing or tool call arguments on what an action does.

Actions: ` + actionList + `

A refused caller gets a decision id. See why with partite boundary decision.`

var actionList = func() string {
	names := make([]string, len(gql.AllBoundaryAction))
	for i, a := range gql.AllBoundaryAction {
		names[i] = string(a)
	}
	return strings.Join(names, ", ")
}()

const specExample = `  # boundary.yaml
  name: no-refunds
  description: Keep agents away from refunds
  rules:
    - name: no-refund-tool
      action: TOOL_PROVIDE
      when: tool.name == "refund"
      effect: DENY
    - name: approve-big-orders
      action: TOOL_CALL
      when: tool.name == "order" && has(arguments.amount) && arguments.amount > 100
      effect: ASK
      message: Orders over 100 need approval.
  modifications:
    - name: tag-source
      action: CONVERSATION_CREATE
      set_tags:
        source: '"web"'`

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "boundary",
		Aliases: []string{"boundaries"},
		Short:   "Manage boundaries, the rules that limit what conversations can do",
		Long:    longHelp,
	}
	list, del := Boundaries.ListCmd(app), Boundaries.DeleteCmd(app)
	list.Short, del.Short = "List boundaries", "Delete boundaries"
	del.Long = "Delete boundaries. They are detached from their meshes and access points."
	cmd.AddCommand(
		list,
		Boundaries.GetCmd(app),
		newCreate(app),
		newUpdate(app),
		Boundaries.ApplyCmd(app, `  partite boundary get no-refunds -o yaml > boundary.yaml
  # edit boundary.yaml
  partite boundary apply -f boundary.yaml

`+specExample, func(ctx context.Context, spec Boundary, id string) (Boundary, error) {
			if id == "" {
				return create(ctx, app, spec)
			}
			return replace(ctx, app, id, spec)
		}),
		del,
		newDecision(app),
	)
	return cmd
}

func create(ctx context.Context, app *cmdutil.App, spec Boundary) (Boundary, error) {
	c, err := app.GQL()
	if err != nil {
		return Boundary{}, err
	}
	resp, err := gql.BoundaryCreate(ctx, c, &gql.NewBoundary{
		Name:          spec.Name,
		Description:   cmdutil.OptString(spec.Description),
		ReportOnly:    spec.ReportOnly,
		Rules:         ruleInputs(spec.Rules),
		Modifications: modificationInputs(spec.Modifications),
	})
	if err != nil {
		return Boundary{}, err
	}
	return fromAPI(resp.CreateBoundary), nil
}

// replace makes the boundary with this id match spec in full.
func replace(ctx context.Context, app *cmdutil.App, id string, spec Boundary) (Boundary, error) {
	return update(ctx, app, id, &gql.UpdateBoundary{
		Name:          gql.Opt(spec.Name),
		Description:   gql.Opt(spec.Description),
		ReportOnly:    gql.Opt(spec.ReportOnly),
		Rules:         gql.Opt(ruleInputs(spec.Rules)),
		Modifications: gql.Opt(modificationInputs(spec.Modifications)),
	})
}

func update(ctx context.Context, app *cmdutil.App, id string, u *gql.UpdateBoundary) (Boundary, error) {
	c, err := app.GQL()
	if err != nil {
		return Boundary{}, err
	}
	resp, err := gql.BoundaryUpdate(ctx, c, id, u)
	if err != nil {
		return Boundary{}, err
	}
	return fromAPI(resp.UpdateBoundary), nil
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var spec Boundary
	var file string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a boundary",
		Long: `Create a boundary. Its rules and modifications come from -f. The server
refuses a boundary with an expression that does not compile, and lists every
problem it found.`,
		Example: `  partite boundary create -f boundary.yaml
  partite boundary create trial --report-only -f boundary.yaml

` + specExample,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			flags := spec
			if file != "" {
				if err := app.ReadSpec(file, &spec); err != nil {
					return err
				}
				if f.Changed("description") {
					spec.Description = flags.Description
				}
				if f.Changed("report-only") {
					spec.ReportOnly = flags.ReportOnly
				}
			}
			if len(args) == 1 {
				spec.Name = args[0]
			}
			if spec.Name == "" {
				return cmdutil.UsageError("a name is required")
			}
			b, err := create(cmd.Context(), app, spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Created boundary %s (%s).", b.Name, b.ID)
			for _, w := range b.Warnings {
				fmt.Fprintf(app.Err, "Warning: %s\n", w)
			}
			if app.Format() == output.Table {
				return nil
			}
			return Boundaries.Show(app, b)
		},
	}
	cmd.Flags().StringVar(&spec.Description, "description", "", "description")
	cmd.Flags().BoolVar(&spec.ReportOnly, "report-only", false, "only report what the rules would refuse, and make no modifications")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the boundary from a YAML or JSON file")
	return cmd
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var name, description string
	var reportOnly bool
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change a boundary's name, description or report-only setting",
		Long: `Change a boundary. Only the flags you pass change. To change rules and
modifications, use partite boundary apply.`,
		Example: `  partite boundary update no-refunds --report-only=false`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := Boundaries.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			f := cmd.Flags()
			var u gql.UpdateBoundary
			if f.Changed("name") {
				u.Name = gql.Opt(name)
			}
			if f.Changed("description") {
				u.Description = gql.Opt(description)
			}
			if f.Changed("report-only") {
				u.ReportOnly = gql.Opt(reportOnly)
			}
			if !u.Name.Set && !u.Description.Set && !u.ReportOnly.Set {
				return cmdutil.UsageError("nothing to change; pass --name, --description or --report-only")
			}
			b, err := update(ctx, app, id, &u)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated boundary %s.", b.Name)
			if app.Format() == output.Table {
				return nil
			}
			return Boundaries.Show(app, b)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().BoolVar(&reportOnly, "report-only", false, "only report what the rules would refuse, and make no modifications")
	return cmd
}

type RuleEvaluation struct {
	Boundary   string `json:"boundary"`
	Name       string `json:"name"`
	Effect     string `json:"effect"`
	Applies    bool   `json:"applies"`
	Error      string `json:"error,omitempty"`
	ReportOnly bool   `json:"report_only,omitempty"`
}

type ModificationEvaluation struct {
	Boundary string `json:"boundary"`
	Name     string `json:"name"`
	Applied  bool   `json:"applied"`
	Error    string `json:"error,omitempty"`
}

// Decision is the trace of one boundary decision.
type Decision struct {
	ID            string                   `json:"id"`
	Action        string                   `json:"action"`
	Outcome       string                   `json:"outcome"`
	Reason        string                   `json:"reason,omitempty"`
	Boundary      string                   `json:"boundary,omitempty"`
	Rule          string                   `json:"rule,omitempty"`
	Message       string                   `json:"message,omitempty"`
	Tags          []cmdutil.Tag            `json:"tags,omitempty"`
	Tracing       *bool                    `json:"tracing,omitempty"`
	Arguments     json.RawMessage          `json:"arguments,omitempty"`
	Boundaries    []string                 `json:"boundaries"`
	Rules         []RuleEvaluation         `json:"rules"`
	Modifications []ModificationEvaluation `json:"modifications"`
	Variables     json.RawMessage          `json:"variables"`
	DecidedAt     string                   `json:"decided_at"`
}

func decisionFromAPI(d gql.BoundaryDecisionGetBoundaryDecision) Decision {
	v := Decision{
		ID:            d.Id,
		Action:        string(d.Action),
		Outcome:       string(d.Outcome),
		Boundary:      cmdutil.Deref(d.Boundary),
		Rule:          cmdutil.Deref(d.Rule),
		Message:       cmdutil.Deref(d.Message),
		Tags:          cmdutil.TagsFromAPI(d.Tags),
		Tracing:       d.Tracing,
		Boundaries:    make([]string, len(d.Boundaries)),
		Rules:         make([]RuleEvaluation, len(d.Rules)),
		Modifications: make([]ModificationEvaluation, len(d.Modifications)),
		Variables:     d.Variables,
		DecidedAt:     d.DecidedAt,
	}
	if d.Reason != nil {
		v.Reason = string(*d.Reason)
	}
	if d.Arguments != nil {
		v.Arguments = *d.Arguments
	}
	for i, b := range d.Boundaries {
		v.Boundaries[i] = b.Name
	}
	for i, r := range d.Rules {
		v.Rules[i] = RuleEvaluation{Boundary: r.Boundary, Name: r.Name, Effect: string(r.Effect), Applies: r.Applies, Error: cmdutil.Deref(r.Error), ReportOnly: r.ReportOnly}
	}
	for i, m := range d.Modifications {
		v.Modifications[i] = ModificationEvaluation{Boundary: m.Boundary, Name: m.Name, Applied: m.Applied, Error: cmdutil.Deref(m.Error)}
	}
	return v
}

func newDecision(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:   "decision <decision-id>",
		Short: "Show why a boundary refused something",
		Long: `Show a recorded boundary decision: the boundaries that were checked, each
rule and whether it applied, the modifications, and the values the
expressions read. A refused caller is given the decision id. Refusals are
recorded, and so is what report-only rules would have refused.

With -o json the output has the values the expressions read, under variables.`,
		Example: `  partite boundary decision "$DECISION_ID"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.BoundaryDecisionGet(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			if resp.BoundaryDecision == nil {
				return &cmdutil.Error{Code: cmdutil.ExitNotFound, Msg: fmt.Sprintf("no boundary decision has the id %q", args[0]), Hint: "Only refusals, and what report-only rules would have refused, are recorded."}
			}
			d := decisionFromAPI(*resp.BoundaryDecision)
			rules := make([]string, len(d.Rules))
			for i, r := range d.Rules {
				s := fmt.Sprintf("%s/%s (%s): ", r.Boundary, r.Name, r.Effect)
				switch {
				case r.Error != "":
					s += "failed: " + r.Error
				case r.Applies:
					s += "applies"
				default:
					s += "does not apply"
				}
				if r.ReportOnly {
					s += ", report only"
				}
				rules[i] = s
			}
			mods := make([]string, len(d.Modifications))
			for i, m := range d.Modifications {
				s := fmt.Sprintf("%s/%s: ", m.Boundary, m.Name)
				switch {
				case m.Error != "":
					s += "failed: " + m.Error
				case m.Applied:
					s += "applied"
				default:
					s += "did not apply"
				}
				mods[i] = s
			}
			decidedBy := d.Boundary
			if d.Rule != "" {
				decidedBy += "/" + d.Rule
			}
			tracing := ""
			if d.Tracing != nil {
				tracing = fmt.Sprint(*d.Tracing)
			}
			return app.Printer().Object(d, d.ID, []output.Field{
				{Label: "ID", Value: d.ID},
				{Label: "Action", Value: d.Action},
				{Label: "Outcome", Value: d.Outcome},
				{Label: "Reason", Value: d.Reason},
				{Label: "Decided by", Value: decidedBy},
				{Label: "Message", Value: d.Message},
				{Label: "Tags", Value: cmdutil.FormatTags(d.Tags)},
				{Label: "Tracing", Value: tracing},
				{Label: "Arguments", Value: string(d.Arguments)},
				{Label: "Boundaries", Value: strings.Join(d.Boundaries, ", ")},
				{Label: "Rules", Value: strings.Join(rules, "\n")},
				{Label: "Modifications", Value: strings.Join(mods, "\n")},
				{Label: "Decided", Value: d.DecidedAt},
			})
		},
	}
}

package eval

import (
	"context"
	"fmt"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmd/eval/lookup"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Evaluation is one request scored against a rubric. It is read-only.
type Evaluation struct {
	ID             string             `json:"id"`
	Status         string             `json:"status"`
	RubricScore    *float64           `json:"rubric_score"`
	TraceRequestID string             `json:"trace_request_id"`
	MeshID         string             `json:"mesh_id"`
	MeshName       string             `json:"mesh_name,omitempty"`
	MeshVersionID  string             `json:"mesh_version_id"`
	MeshVersion    *int               `json:"mesh_version,omitempty"`
	RubricID       string             `json:"rubric_id"`
	RubricName     string             `json:"rubric_name,omitempty"`
	ModelSetID     string             `json:"model_set_id,omitempty"`
	ModelSetName   string             `json:"model_set_name,omitempty"`
	MonitorID      string             `json:"monitor_id,omitempty"`
	TraceRunID     string             `json:"trace_run_id,omitempty"`
	Results        []EvaluationResult `json:"results,omitempty"`
	CreatedAt      string             `json:"created_at"`
}

// EvaluationResult is one metric's score within an evaluation.
type EvaluationResult struct {
	MetricName        string   `json:"metric_name"`
	MetricType        string   `json:"metric_type"`
	Category          string   `json:"category"`
	Score             *float64 `json:"score"`
	RawValue          *float64 `json:"raw_value,omitempty"`
	Unit              string   `json:"unit,omitempty"`
	TrialCount        int      `json:"trial_count"`
	MeasurementStatus string   `json:"measurement_status,omitempty"`
	MeasurementReason string   `json:"measurement_reason,omitempty"`
	Label             string   `json:"label,omitempty"`
	Feedback          string   `json:"feedback,omitempty"`
	Error             string   `json:"error,omitempty"`
}

func evaluationFromAPI(d gql.EnterpriseEvaluationSummary) Evaluation {
	return Evaluation{
		ID:             d.Id,
		Status:         string(d.Status),
		RubricScore:    d.RubricScore,
		TraceRequestID: d.TraceRequestID,
		MeshID:         d.MeshID,
		MeshName:       cmdutil.Deref(d.MeshName),
		MeshVersionID:  d.MeshVersionID,
		MeshVersion:    d.MeshOrdinal,
		RubricID:       d.RubricID,
		RubricName:     cmdutil.Deref(d.RubricName),
		ModelSetID:     cmdutil.Deref(d.ModelSetID),
		ModelSetName:   cmdutil.Deref(d.ModelSetName),
		MonitorID:      cmdutil.Deref(d.MonitorID),
		TraceRunID:     cmdutil.Deref(d.TraceRunID),
		CreatedAt:      d.CreatedAt,
	}
}

func evaluationResultFromAPI(d gql.EnterpriseEvaluationResult) EvaluationResult {
	r := EvaluationResult{
		MetricName:        cmdutil.Deref(d.MetricName),
		MetricType:        string(d.RubricMetricType),
		Category:          string(d.Category),
		Score:             d.Score,
		RawValue:          d.RawValue,
		TrialCount:        d.TrialCount,
		MeasurementReason: cmdutil.Deref(d.MeasurementReason),
		Label:             cmdutil.Deref(d.Label),
		Feedback:          cmdutil.Deref(d.Feedback),
		Error:             cmdutil.Deref(d.Error),
	}
	if d.Unit != nil {
		r.Unit = string(*d.Unit)
	}
	if d.MeasurementStatus != nil {
		r.MeasurementStatus = string(*d.MeasurementStatus)
	}
	if r.MetricName == "" {
		r.MetricName = d.RubricMetricID
	}
	return r
}

func versionLabel(ordinal *int, id string) string {
	if ordinal == nil {
		return id
	}
	return fmt.Sprintf("v%d", *ordinal)
}

var evaluationResource = &cmdutil.Resource[Evaluation]{
	WebList:    "observability/evaluations",
	ReadOnly:   true,
	Kind:       "evaluation",
	Command:    "eval evaluation",
	IDPrefix:   "evrn",
	Enterprise: true,
	// Evaluations have no names, so a name search finds nothing.
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Evaluation, error) {
		if opts.Query != "" {
			return nil, nil
		}
		return listEvaluations(ctx, app, nil, opts.Limit)
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Evaluation, error) {
		c, err := app.GQL()
		if err != nil {
			return Evaluation{}, err
		}
		resp, err := gql.EnterpriseGetEvaluation(ctx, c, id)
		if err != nil {
			return Evaluation{}, err
		}
		if resp.Evaluation == nil {
			return Evaluation{}, cmdutil.NotFound("evaluation", id, "partite eval evaluation list")
		}
		e := evaluationFromAPI(resp.Evaluation.EnterpriseEvaluationSummary)
		for _, r := range resp.Evaluation.Results {
			e.Results = append(e.Results, evaluationResultFromAPI(r.EnterpriseEvaluationResult))
		}
		return e, nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		_, err = gql.EnterpriseDeleteEvaluations(ctx, c, ids)
		return err
	},
	ID:      func(e Evaluation) string { return e.ID },
	Name:    func(e Evaluation) string { return "" },
	Columns: []string{"id", "status", "score", "mesh", "version", "rubric", "created"},
	Row: func(e Evaluation) []string {
		return []string{e.ID, e.Status, formatScore(e.RubricScore), e.MeshName, versionLabel(e.MeshVersion, e.MeshVersionID), e.RubricName, cmdutil.FormatTime(e.CreatedAt)}
	},
	Fields: func(e Evaluation) []output.Field {
		lines := make([]string, len(e.Results))
		for i, r := range e.Results {
			lines[i] = describeResult(r)
		}
		return []output.Field{
			{Label: "ID", Value: e.ID},
			{Label: "Status", Value: e.Status},
			{Label: "Rubric score", Value: formatScore(e.RubricScore)},
			{Label: "Mesh", Value: fmt.Sprintf("%s (%s) %s", e.MeshName, e.MeshID, versionLabel(e.MeshVersion, e.MeshVersionID))},
			{Label: "Rubric", Value: fmt.Sprintf("%s (%s)", e.RubricName, e.RubricID)},
			{Label: "Model set", Value: e.ModelSetName},
			{Label: "Request", Value: e.TraceRequestID},
			{Label: "Monitor", Value: e.MonitorID},
			{Label: "Trace run", Value: e.TraceRunID},
			{Label: "Created", Value: e.CreatedAt},
			{Label: "Results", Value: multiline(lines)},
		}
	},
}

func describeResult(r EvaluationResult) string {
	s := fmt.Sprintf("%s: %s", r.MetricName, formatScore(r.Score))
	if r.RawValue != nil {
		s += fmt.Sprintf(" (%s %s)", formatScore(r.RawValue), strings.ToLower(r.Unit))
	}
	if r.TrialCount > 1 {
		s += fmt.Sprintf(", %s", cmdutil.Plural(r.TrialCount, "trial"))
	}
	if r.MeasurementStatus != "" && r.MeasurementStatus != "COMPLETE" {
		s += ", " + strings.ToLower(strings.ReplaceAll(r.MeasurementStatus, "_", " "))
	}
	if r.Error != "" {
		s += "\n  error: " + r.Error
	}
	if r.Feedback != "" {
		s += "\n  " + strings.ReplaceAll(strings.TrimSpace(r.Feedback), "\n", "\n  ")
	}
	return s
}

func listEvaluations(ctx context.Context, app *cmdutil.App, filter *gql.EvaluationsFilter, limit int) ([]Evaluation, error) {
	c, err := app.GQL()
	if err != nil {
		return nil, err
	}
	return cmdutil.ListAll(ctx, limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Evaluation], error) {
		resp, err := gql.EnterpriseListEvaluations(ctx, c, filter, first, after)
		if err != nil {
			return cmdutil.Page[Evaluation]{}, err
		}
		conn := resp.Evaluations
		items := make([]Evaluation, len(conn.Edges))
		for i, e := range conn.Edges {
			items[i] = evaluationFromAPI(e.Node)
		}
		return cmdutil.Page[Evaluation]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
	})
}

func newEvaluation(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "evaluation",
		Aliases: []string{"evaluations"},
		Short:   "Look at and delete evaluations",
		Long: `An evaluation is one conversation request scored against a rubric. Monitors
and trace runs create them; each has a score per metric and an overall rubric
score. Evaluations have no names; refer to them by id.`,
	}
	cmd.AddCommand(newEvaluationList(app), evaluationResource.GetCmd(app),
		evaluationResource.ViewCmd(app), evaluationResource.DeleteCmd(app))
	return cmd
}

func newEvaluationList(app *cmdutil.App) *cobra.Command {
	var mesh, rubric, monitor, request, traceRun string
	var limit int
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List evaluations, newest first",
		Example: `  partite eval evaluation list --mesh support
  partite eval evaluation list --monitor support/support-quality --limit 10
  partite eval evaluation list --request treq_... -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := evaluationResource.RequireEnterprise(app); err != nil {
				return err
			}
			var filter gql.EvaluationsFilter
			if mesh != "" {
				id, err := lookup.Mesh(ctx, app, mesh)
				if err != nil {
					return err
				}
				filter.MeshID = &id
			}
			if rubric != "" {
				id, err := rubricResource.Resolve(ctx, app, rubric)
				if err != nil {
					return err
				}
				filter.RubricID = &id
			}
			if monitor != "" {
				id, err := monitorResource.Resolve(ctx, app, monitor)
				if err != nil {
					return err
				}
				filter.MonitorID = &id
			}
			filter.TraceRequestID = cmdutil.OptString(request)
			filter.TraceRunID = cmdutil.OptString(traceRun)
			items, err := listEvaluations(ctx, app, &filter, limit)
			if err != nil {
				return err
			}
			rows := make([][]string, len(items))
			for i, e := range items {
				rows[i] = evaluationResource.Row(e)
			}
			if items == nil {
				items = []Evaluation{}
			}
			return app.Printer().List(items, evaluationResource.Columns, rows)
		},
	}
	f := cmd.Flags()
	f.StringVar(&mesh, "mesh", "", "only evaluations of this mesh")
	f.StringVar(&rubric, "rubric", "", "only evaluations against this rubric")
	f.StringVar(&monitor, "monitor", "", "only evaluations by this monitor (MESH/RUBRIC or id)")
	f.StringVar(&request, "request", "", "only evaluations of this trace request id")
	f.StringVar(&traceRun, "trace-run", "", "only evaluations made by this trace run id")
	f.IntVar(&limit, "limit", 50, "most results to return; 0 for all")
	return cmd
}

// TraceRun is an on-demand evaluation of a trace. It is read-only.
type TraceRun struct {
	ID              string           `json:"id"`
	TraceID         string           `json:"trace_id"`
	Scope           string           `json:"scope"`
	Status          string           `json:"status"`
	RubricScore     *float64         `json:"rubric_score"`
	MeshID          string           `json:"mesh_id"`
	MeshVersionID   string           `json:"mesh_version_id"`
	RubricID        string           `json:"rubric_id"`
	ModelSetID      string           `json:"model_set_id,omitempty"`
	EvaluationCount int              `json:"evaluation_count"`
	CompletedCount  int              `json:"completed_count"`
	ErroredCount    int              `json:"errored_count"`
	PendingCount    int              `json:"pending_count"`
	MetricScores    []TraceRunMetric `json:"metric_scores"`
	StartedAt       string           `json:"started_at,omitempty"`
	CompletedAt     string           `json:"completed_at,omitempty"`
	CreatedAt       string           `json:"created_at"`
}

// TraceRunMetric is one metric's mean score over a trace run's evaluations.
type TraceRunMetric struct {
	MetricName        string   `json:"metric_name"`
	MetricType        string   `json:"metric_type"`
	Category          string   `json:"category"`
	MeanScore         *float64 `json:"mean_score"`
	ScoredEvaluations int      `json:"scored_evaluations"`
}

func traceRunFromAPI(d gql.EnterpriseTraceRun) TraceRun {
	t := TraceRun{
		ID:              d.Id,
		TraceID:         d.TraceID,
		Scope:           string(d.Scope),
		Status:          string(d.Status),
		RubricScore:     d.RubricScore,
		MeshID:          d.MeshID,
		MeshVersionID:   d.MeshVersionID,
		RubricID:        d.RubricID,
		ModelSetID:      cmdutil.Deref(d.ModelSetID),
		EvaluationCount: d.EvaluationCount,
		CompletedCount:  d.CompletedCount,
		ErroredCount:    d.ErroredCount,
		PendingCount:    d.PendingCount,
		MetricScores:    []TraceRunMetric{},
		StartedAt:       cmdutil.Deref(d.StartedAt),
		CompletedAt:     cmdutil.Deref(d.CompletedAt),
		CreatedAt:       d.CreatedAt,
	}
	for _, m := range d.MetricScores {
		t.MetricScores = append(t.MetricScores, TraceRunMetric{
			MetricName:        m.MetricName,
			MetricType:        string(m.RubricMetricType),
			Category:          string(m.Category),
			MeanScore:         m.MeanScore,
			ScoredEvaluations: m.ScoredEvaluationCount,
		})
	}
	return t
}

func listTraceRuns(ctx context.Context, app *cmdutil.App, filter *gql.EvaluationTraceRunsFilter, limit int) ([]TraceRun, error) {
	c, err := app.GQL()
	if err != nil {
		return nil, err
	}
	return cmdutil.ListAll(ctx, limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[TraceRun], error) {
		resp, err := gql.EnterpriseListTraceRuns(ctx, c, filter, first, after)
		if err != nil {
			return cmdutil.Page[TraceRun]{}, err
		}
		conn := resp.EvaluationTraceRuns
		items := make([]TraceRun, len(conn.Edges))
		for i, e := range conn.Edges {
			items[i] = traceRunFromAPI(e.Node)
		}
		return cmdutil.Page[TraceRun]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
	})
}

var traceRunResource = &cmdutil.Resource[TraceRun]{
	ReadOnly:   true,
	Kind:       "evaluation trace run",
	Command:    "eval trace-run",
	IDPrefix:   "evtr",
	Enterprise: true,
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]TraceRun, error) {
		if opts.Query != "" {
			return nil, nil
		}
		return listTraceRuns(ctx, app, nil, opts.Limit)
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (TraceRun, error) {
		c, err := app.GQL()
		if err != nil {
			return TraceRun{}, err
		}
		resp, err := gql.EnterpriseGetTraceRun(ctx, c, id)
		if err != nil {
			return TraceRun{}, err
		}
		if resp.EvaluationTraceRun == nil {
			return TraceRun{}, cmdutil.NotFound("evaluation trace run", id, "partite eval trace-run list")
		}
		return traceRunFromAPI(*resp.EvaluationTraceRun), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		_, err = gql.EnterpriseDeleteTraceRuns(ctx, c, ids)
		return err
	},
	ID:      func(t TraceRun) string { return t.ID },
	Name:    func(t TraceRun) string { return "" },
	Columns: []string{"id", "trace", "scope", "status", "score", "evaluations", "created"},
	Row: func(t TraceRun) []string {
		return []string{t.ID, t.TraceID, t.Scope, t.Status, formatScore(t.RubricScore), fmt.Sprintf("%d/%d", t.CompletedCount, t.EvaluationCount), cmdutil.FormatTime(t.CreatedAt)}
	},
	Fields: func(t TraceRun) []output.Field {
		lines := make([]string, len(t.MetricScores))
		for i, m := range t.MetricScores {
			lines[i] = fmt.Sprintf("%s: %s (%s)", m.MetricName, formatScore(m.MeanScore), cmdutil.Plural(m.ScoredEvaluations, "evaluation"))
		}
		return []output.Field{
			{Label: "ID", Value: t.ID},
			{Label: "Trace", Value: t.TraceID},
			{Label: "Scope", Value: t.Scope},
			{Label: "Status", Value: t.Status},
			{Label: "Rubric score", Value: formatScore(t.RubricScore)},
			{Label: "Mesh", Value: t.MeshID + " " + t.MeshVersionID},
			{Label: "Rubric", Value: t.RubricID},
			{Label: "Model set", Value: t.ModelSetID},
			{Label: "Evaluations", Value: fmt.Sprintf("%d: %d completed, %d errored, %d pending", t.EvaluationCount, t.CompletedCount, t.ErroredCount, t.PendingCount)},
			{Label: "Metric scores", Value: multiline(lines)},
			{Label: "Started", Value: t.StartedAt},
			{Label: "Completed", Value: t.CompletedAt},
			{Label: "Created", Value: t.CreatedAt},
		}
	},
}

func newTraceRun(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "trace-run",
		Aliases: []string{"trace-runs"},
		Short:   "Look at and delete on-demand evaluations of traces",
		Long: `A trace run scores one request of a trace, or every request of it, against a
rubric when someone asks, rather than as a monitor samples them. It makes an
evaluation per request and rolls their scores up. Deleting a trace run
deletes its evaluations too. Trace runs have no names; refer to them by id.

To see a trace run's evaluations: partite eval evaluation list --trace-run ID`,
	}
	cmd.AddCommand(newTraceRunList(app), traceRunResource.GetCmd(app), traceRunResource.DeleteCmd(app))
	return cmd
}

func newTraceRunList(app *cmdutil.App) *cobra.Command {
	var trace, mesh, rubric string
	var limit int
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List evaluation trace runs, newest first",
		Example: "  partite eval trace-run list --trace ttrc_...\n  partite eval trace-run list --mesh support --rubric support-quality",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := traceRunResource.RequireEnterprise(app); err != nil {
				return err
			}
			filter := gql.EvaluationTraceRunsFilter{TraceID: cmdutil.OptString(trace)}
			if mesh != "" {
				id, err := lookup.Mesh(ctx, app, mesh)
				if err != nil {
					return err
				}
				filter.MeshID = &id
			}
			if rubric != "" {
				id, err := rubricResource.Resolve(ctx, app, rubric)
				if err != nil {
					return err
				}
				filter.RubricID = &id
			}
			items, err := listTraceRuns(ctx, app, &filter, limit)
			if err != nil {
				return err
			}
			rows := make([][]string, len(items))
			for i, t := range items {
				rows[i] = traceRunResource.Row(t)
			}
			if items == nil {
				items = []TraceRun{}
			}
			return app.Printer().List(items, traceRunResource.Columns, rows)
		},
	}
	f := cmd.Flags()
	f.StringVar(&trace, "trace", "", "only runs of this trace id")
	f.StringVar(&mesh, "mesh", "", "only runs of this mesh")
	f.StringVar(&rubric, "rubric", "", "only runs against this rubric")
	f.IntVar(&limit, "limit", 50, "most results to return; 0 for all")
	return cmd
}

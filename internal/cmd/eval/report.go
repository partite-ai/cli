package eval

import (
	"fmt"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmd/eval/lookup"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Report is how a mesh scored against a rubric over a time range, by version.
type Report struct {
	MeshID    string          `json:"mesh_id"`
	RubricID  string          `json:"rubric_id"`
	StartTime string          `json:"start_time"`
	EndTime   string          `json:"end_time"`
	Interval  string          `json:"interval"`
	Metrics   []ReportMetric  `json:"metrics"`
	Versions  []ReportVersion `json:"versions"`
}

type ReportMetric struct {
	RubricMetricID string   `json:"rubric_metric_id"`
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	Category       string   `json:"category"`
	Unit           string   `json:"unit"`
	Aggregation    string   `json:"aggregation"`
	Weight         *float64 `json:"weight"`
}

type ReportVersion struct {
	MeshVersionID string          `json:"mesh_version_id"`
	Version       *int            `json:"version,omitempty"`
	Label         string          `json:"label,omitempty"`
	Summary       ReportAggregate `json:"summary"`
	Points        []ReportPoint   `json:"points"`
}

type ReportPoint struct {
	StartTime string          `json:"start_time"`
	EndTime   string          `json:"end_time"`
	Aggregate ReportAggregate `json:"aggregate"`
}

type ReportAggregate struct {
	TotalRequests      int                 `json:"total_requests"`
	SelectedRequests   int                 `json:"selected_requests"`
	SuccessfulRequests int                 `json:"successful_requests"`
	PendingRequests    int                 `json:"pending_requests"`
	FailedRequests     int                 `json:"failed_requests"`
	Coverage           *float64            `json:"coverage"`
	RubricScore        *float64            `json:"rubric_score"`
	Metrics            []ReportMetricValue `json:"metrics"`
}

type ReportMetricValue struct {
	RubricMetricID       string   `json:"rubric_metric_id"`
	Value                *float64 `json:"value"`
	Score                *float64 `json:"score"`
	ContributingRequests int      `json:"contributing_requests"`
}

func reportAggregateFromAPI(d gql.EnterpriseReportAggregate) ReportAggregate {
	a := ReportAggregate{
		TotalRequests:      d.TotalRequestCount,
		SelectedRequests:   d.SelectedRequestCount,
		SuccessfulRequests: d.SuccessfulRequestCount,
		PendingRequests:    d.LatestPendingRequestCount,
		FailedRequests:     d.LatestFailedRequestCount,
		Coverage:           d.CoverageRatio,
		RubricScore:        d.RubricScore,
		Metrics:            []ReportMetricValue{},
	}
	for _, m := range d.Metrics {
		a.Metrics = append(a.Metrics, ReportMetricValue{RubricMetricID: m.RubricMetricID, Value: m.Value, Score: m.Score, ContributingRequests: m.ContributingRequestCount})
	}
	return a
}

func reportFromAPI(d gql.EnterpriseEvaluationReportEvaluationReport) Report {
	r := Report{
		MeshID:    d.MeshID,
		RubricID:  d.RubricID,
		StartTime: d.StartTime,
		EndTime:   d.EndTime,
		Interval:  string(d.Interval),
		Metrics:   []ReportMetric{},
		Versions:  []ReportVersion{},
	}
	for _, m := range d.Metrics {
		r.Metrics = append(r.Metrics, ReportMetric{
			RubricMetricID: m.RubricMetricID,
			Name:           m.Name,
			Type:           string(m.Type),
			Category:       string(m.Category),
			Unit:           string(m.Unit),
			Aggregation:    string(m.Aggregation),
			Weight:         m.Weight,
		})
	}
	for _, v := range d.Versions {
		rv := ReportVersion{
			MeshVersionID: v.MeshVersionID,
			Version:       v.Ordinal,
			Label:         cmdutil.Deref(v.Label),
			Summary:       reportAggregateFromAPI(v.Summary.EnterpriseReportAggregate),
			Points:        []ReportPoint{},
		}
		for _, p := range v.Points {
			rv.Points = append(rv.Points, ReportPoint{StartTime: p.StartTime, EndTime: p.EndTime, Aggregate: reportAggregateFromAPI(p.Aggregate.EnterpriseReportAggregate)})
		}
		r.Versions = append(r.Versions, rv)
	}
	return r
}

// reportTable lays out a report as rows: one per version, or with overTime
// one per version and time bucket. Each metric gets a column.
func reportTable(r Report, overTime bool) ([]string, [][]string) {
	headers := []string{"version"}
	if overTime {
		headers = append(headers, "from")
	}
	headers = append(headers, "requests", "scored", "coverage", "score")
	for _, m := range r.Metrics {
		headers = append(headers, m.Name)
	}
	row := func(v ReportVersion, from string, a ReportAggregate) []string {
		name := versionLabel(v.Version, v.MeshVersionID)
		if v.Label != "" {
			name += " " + v.Label
		}
		cells := []string{name}
		if overTime {
			cells = append(cells, from)
		}
		cells = append(cells, fmt.Sprint(a.TotalRequests), fmt.Sprint(a.SuccessfulRequests), formatRatio(a.Coverage), formatScore(a.RubricScore))
		values := make(map[string]ReportMetricValue, len(a.Metrics))
		for _, mv := range a.Metrics {
			values[mv.RubricMetricID] = mv
		}
		for _, m := range r.Metrics {
			cells = append(cells, formatMetricCell(m, values[m.RubricMetricID]))
		}
		return cells
	}
	var rows [][]string
	for _, v := range r.Versions {
		if !overTime {
			rows = append(rows, row(v, "", v.Summary))
			continue
		}
		for _, p := range v.Points {
			rows = append(rows, row(v, cmdutil.FormatTime(p.StartTime), p.Aggregate))
		}
	}
	return headers, rows
}

// formatMetricCell prints a metric's score, with its measured value for
// computed metrics.
func formatMetricCell(m ReportMetric, v ReportMetricValue) string {
	s := formatScore(v.Score)
	if m.Type == string(gql.MetricTypeComputed) && v.Value != nil {
		s += fmt.Sprintf(" (%s %s)", formatScore(v.Value), strings.ToLower(m.Unit))
	}
	return s
}

func newReport(app *cmdutil.App) *cobra.Command {
	var since, from, to, interval string
	var overTime bool
	cmd := &cobra.Command{
		Use:   "report <mesh> [rubric]",
		Short: "Show how a mesh scored against a rubric over time",
		Long: `Show how a mesh scored against a rubric over a time range, one row per mesh
version, with a column per metric. Computed metrics also show their measured
value. With --over-time, there is a row per version and time bucket.

Without a rubric, list the rubrics the mesh was scored against in the range,
with how many requests each scored.

The range is the last --since (default 24h), or --from and --to.`,
		Example: `  partite eval report support
  partite eval report support support-quality --since 7d
  partite eval report support support-quality --over-time --interval ONE_HOUR
  partite eval report support support-quality --from 2026-10-01T00:00:00Z --to 2026-10-08T00:00:00Z -o json`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := evaluationResource.RequireEnterprise(app); err != nil {
				return err
			}
			start, end, err := reportRange(since, from, to, time.Now())
			if err != nil {
				return err
			}
			meshID, err := lookup.Mesh(ctx, app, args[0])
			if err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			startS, endS := start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)
			if len(args) == 1 {
				resp, err := gql.EnterpriseEvaluationReportRubrics(ctx, c, gql.EvaluationReportRangeInput{MeshID: meshID, StartTime: startS, EndTime: endS})
				if err != nil {
					return err
				}
				type rubricRow struct {
					RubricID         string `json:"rubric_id"`
					Name             string `json:"name"`
					Description      string `json:"description,omitempty"`
					SelectedRequests int    `json:"selected_requests"`
				}
				items := []rubricRow{}
				var rows [][]string
				for _, r := range resp.EvaluationReportRubrics {
					items = append(items, rubricRow{r.RubricID, r.Name, cmdutil.Deref(r.Description), r.SelectedRequestCount})
					rows = append(rows, []string{r.RubricID, r.Name, fmt.Sprint(r.SelectedRequestCount), cmdutil.Deref(r.Description)})
				}
				return app.Printer().List(items, []string{"id", "rubric", "requests", "description"}, rows)
			}
			rubricID, err := rubricResource.Resolve(ctx, app, args[1])
			if err != nil {
				return err
			}
			in := gql.EvaluationReportInput{MeshID: meshID, RubricID: rubricID, StartTime: startS, EndTime: endS}
			if interval != "" {
				iv, err := upperChoice("--interval", interval, gql.AllEvaluationReportInterval)
				if err != nil {
					return err
				}
				in.Interval = &iv
			}
			resp, err := gql.EnterpriseEvaluationReport(ctx, c, in)
			if err != nil {
				return err
			}
			report := reportFromAPI(resp.EvaluationReport)
			if app.Format() != output.Table {
				return app.Printer().Data(report)
			}
			fmt.Fprintf(app.Out, "%s to %s\n\n", cmdutil.FormatTime(report.StartTime), cmdutil.FormatTime(report.EndTime))
			headers, rows := reportTable(report, overTime)
			return app.Printer().List(report, headers, rows)
		},
	}
	f := cmd.Flags()
	f.StringVar(&since, "since", "24h", "how far back to report, such as 30m, 24h or 7d")
	f.StringVar(&from, "from", "", "start of the range, in RFC 3339 form (overrides --since)")
	f.StringVar(&to, "to", "", "end of the range, in RFC 3339 form (default: now)")
	f.StringVar(&interval, "interval", "", "time bucket size: "+strings.Join(choiceNames(gql.AllEvaluationReportInterval), ", ")+" (default: chosen by the server)")
	f.BoolVar(&overTime, "over-time", false, "show a row per time bucket")
	_ = cmd.RegisterFlagCompletionFunc("interval", cobra.FixedCompletions(choiceNames(gql.AllEvaluationReportInterval), cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// reportRange works out the report's start and end from the flags.
func reportRange(since, from, to string, now time.Time) (time.Time, time.Time, error) {
	end := now
	if to != "" {
		t, err := time.Parse(time.RFC3339, to)
		if err != nil {
			return time.Time{}, time.Time{}, cmdutil.UsageError("--to must be a time such as 2026-10-01T00:00:00Z, got %q", to)
		}
		end = t
	}
	var start time.Time
	if from != "" {
		t, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return time.Time{}, time.Time{}, cmdutil.UsageError("--from must be a time such as 2026-10-01T00:00:00Z, got %q", from)
		}
		start = t
	} else {
		d, err := parseSince(since)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		start = end.Add(-d)
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, cmdutil.UsageError("the start of the range must be before its end")
	}
	return start, end, nil
}

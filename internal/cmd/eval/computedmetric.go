package eval

import (
	"context"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// ComputedMetric is a metric Partite measures itself. It is read-only.
type ComputedMetric struct {
	Key             string `json:"key"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Category        string `json:"category"`
	CollectionScope string `json:"collection_scope"`
	SummarySource   string `json:"summary_source"`
	Aggregation     string `json:"aggregation"`
	Unit            string `json:"unit"`
}

func computedMetricFromAPI(d gql.ComputedMetricDetails) ComputedMetric {
	return ComputedMetric{
		Key:             d.Key,
		Name:            d.Name,
		Description:     cmdutil.Deref(d.Description),
		Category:        string(d.Category),
		CollectionScope: string(d.CollectionScope),
		SummarySource:   string(d.SummarySource),
		Aggregation:     string(d.Aggregation),
		Unit:            string(d.Unit),
	}
}

// computedMetricResource is used for list and its row layout. Computed
// metrics are found by key, not id, so get is built separately.
var computedMetricResource = &cmdutil.Resource[ComputedMetric]{
	Kind:       "computed metric",
	Command:    "eval computed-metric",
	Enterprise: true,
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]ComputedMetric, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		all, err := cmdutil.ListAll(ctx, 0, func(ctx context.Context, first int, after *string) (cmdutil.Page[ComputedMetric], error) {
			resp, err := gql.EnterpriseListComputedMetrics(ctx, c, first, after)
			if err != nil {
				return cmdutil.Page[ComputedMetric]{}, err
			}
			conn := resp.Metrics
			var items []ComputedMetric
			for _, e := range conn.Edges {
				if n, ok := e.Node.(*gql.EnterpriseListComputedMetricsMetricsMetricConnectionEdgesMetricEdgeNodeComputedMetric); ok {
					items = append(items, computedMetricFromAPI(n.ComputedMetricDetails))
				}
			}
			return cmdutil.Page[ComputedMetric]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
		if err != nil {
			return nil, err
		}
		// The catalog is small, so the search is done here, on key and name.
		var out []ComputedMetric
		q := strings.ToLower(opts.Query)
		for _, m := range all {
			if q == "" || strings.Contains(strings.ToLower(m.Key), q) || strings.Contains(strings.ToLower(m.Name), q) {
				out = append(out, m)
			}
		}
		if opts.Limit > 0 && len(out) > opts.Limit {
			out = out[:opts.Limit]
		}
		return out, nil
	},
	ID:      func(m ComputedMetric) string { return m.Key },
	Name:    func(m ComputedMetric) string { return m.Name },
	Columns: []string{"key", "name", "category", "unit", "aggregation"},
	Row: func(m ComputedMetric) []string {
		return []string{m.Key, m.Name, m.Category, m.Unit, m.Aggregation}
	},
	Fields: func(m ComputedMetric) []output.Field {
		return []output.Field{
			{Label: "Key", Value: m.Key},
			{Label: "Name", Value: m.Name},
			{Label: "Description", Value: m.Description},
			{Label: "Category", Value: m.Category},
			{Label: "Unit", Value: m.Unit},
			{Label: "Collected from", Value: m.CollectionScope},
			{Label: "Request value", Value: m.SummarySource},
			{Label: "Aggregation", Value: m.Aggregation},
		}
	},
}

func newComputedMetric(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "computed-metric",
		Aliases: []string{"computed-metrics"},
		Short:   "List metrics Partite measures itself, such as latency",
		Long: `Computed metrics are numbers Partite measures from a conversation, such as
latency, token use or cost. They are built in and cannot be changed. Add one
to a rubric by its key.

Fields:
  collection_scope  ROOT_ONLY reads the first hop; ALL_HOPS reads every hop
  summary_source    ROOT_VALUE uses the first hop's value; AGGREGATE combines
                    the hops' values with the aggregation`,
	}
	cmd.AddCommand(computedMetricResource.ListCmd(app), newComputedMetricGet(app))
	return cmd
}

func newComputedMetricGet(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "get <key-or-name>",
		Aliases: []string{"show"},
		Short:   "Show a computed metric",
		Example: "  partite eval computed-metric get latency_ms",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := computedMetricResource.RequireEnterprise(app); err != nil {
				return err
			}
			all, err := computedMetricResource.List(cmd.Context(), app, cmdutil.ListOptions{})
			if err != nil {
				return err
			}
			m, err := findComputedMetric(all, args[0])
			if err != nil {
				return err
			}
			return computedMetricResource.Show(app, m)
		},
	}
}

// findComputedMetric matches ref against keys first, then names.
func findComputedMetric(all []ComputedMetric, ref string) (ComputedMetric, error) {
	for _, m := range all {
		if m.Key == ref {
			return m, nil
		}
	}
	for _, m := range all {
		if strings.EqualFold(m.Name, ref) || strings.EqualFold(m.Key, ref) {
			return m, nil
		}
	}
	return ComputedMetric{}, cmdutil.NotFound("computed metric", ref, "partite eval computed-metric list")
}

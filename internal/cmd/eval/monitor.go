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

// Monitor is the view printed by get and read by -f. On input, mesh_id,
// rubric_id and model_set_id may be names.
type Monitor struct {
	ID           string   `json:"id,omitempty"`
	MeshID       string   `json:"mesh_id"`
	RubricID     string   `json:"rubric_id"`
	ModelSetID   string   `json:"model_set_id"`
	SampleRate   *float64 `json:"sample_rate"`
	Enabled      *bool    `json:"enabled,omitempty"`
	MeshName     string   `json:"mesh_name,omitempty"`
	RubricName   string   `json:"rubric_name,omitempty"`
	ModelSetName string   `json:"model_set_name,omitempty"`
	CreatedAt    string   `json:"created_at,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
}

func monitorFromAPI(d gql.EnterpriseMonitor) Monitor {
	return Monitor{
		ID:           d.Id,
		MeshID:       d.MeshID,
		RubricID:     d.RubricID,
		ModelSetID:   d.ModelSetID,
		SampleRate:   &d.SampleRate,
		Enabled:      &d.Enabled,
		MeshName:     cmdutil.Deref(d.MeshName),
		RubricName:   cmdutil.Deref(d.RubricName),
		ModelSetName: cmdutil.Deref(d.ModelSetName),
		CreatedAt:    d.CreatedAt,
		UpdatedAt:    d.UpdatedAt,
	}
}

// monitorName is how a monitor is named on the command line: it has no name
// of its own, and a mesh has at most one monitor per rubric.
func monitorName(m Monitor) string {
	return m.MeshName + "/" + m.RubricName
}

func listMonitors(ctx context.Context, app *cmdutil.App, meshID *string, limit int) ([]Monitor, error) {
	c, err := app.GQL()
	if err != nil {
		return nil, err
	}
	return cmdutil.ListAll(ctx, limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Monitor], error) {
		resp, err := gql.EnterpriseListMonitors(ctx, c, meshID, first, after)
		if err != nil {
			return cmdutil.Page[Monitor]{}, err
		}
		conn := resp.EvalMeshMonitors
		items := make([]Monitor, len(conn.Edges))
		for i, e := range conn.Edges {
			items[i] = monitorFromAPI(e.Node)
		}
		return cmdutil.Page[Monitor]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
	})
}

var monitorResource = &cmdutil.Resource[Monitor]{
	Kind:       "eval monitor",
	Command:    "eval monitor",
	IDPrefix:   "emon",
	Enterprise: true,
	// The server has no search for monitors, so a search is matched here
	// against MESH/RUBRIC.
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Monitor, error) {
		limit := opts.Limit
		if opts.Query != "" {
			limit = 0
		}
		all, err := listMonitors(ctx, app, nil, limit)
		if err != nil || opts.Query == "" {
			return all, err
		}
		var out []Monitor
		q := strings.ToLower(opts.Query)
		for _, m := range all {
			if strings.Contains(strings.ToLower(monitorName(m)), q) {
				out = append(out, m)
			}
		}
		if opts.Limit > 0 && len(out) > opts.Limit {
			out = out[:opts.Limit]
		}
		return out, nil
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Monitor, error) {
		c, err := app.GQL()
		if err != nil {
			return Monitor{}, err
		}
		resp, err := gql.EnterpriseGetMonitor(ctx, c, id)
		if err != nil {
			return Monitor{}, err
		}
		if resp.EvalMeshMonitor == nil {
			return Monitor{}, cmdutil.NotFound("eval monitor", id, "partite eval monitor list")
		}
		return monitorFromAPI(*resp.EvalMeshMonitor), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := gql.DeleteEvalMeshMonitor(ctx, c, id); err != nil {
				return err
			}
		}
		return nil
	},
	ID:      func(m Monitor) string { return m.ID },
	Name:    monitorName,
	Columns: []string{"id", "mesh", "rubric", "model set", "sample rate", "enabled"},
	Row: func(m Monitor) []string {
		return []string{m.ID, m.MeshName, m.RubricName, m.ModelSetName, formatRatio(m.SampleRate), fmt.Sprint(cmdutil.Deref(m.Enabled))}
	},
	Fields: func(m Monitor) []output.Field {
		return []output.Field{
			{Label: "ID", Value: m.ID},
			{Label: "Mesh", Value: fmt.Sprintf("%s (%s)", m.MeshName, m.MeshID)},
			{Label: "Rubric", Value: fmt.Sprintf("%s (%s)", m.RubricName, m.RubricID)},
			{Label: "Model set", Value: fmt.Sprintf("%s (%s)", m.ModelSetName, m.ModelSetID)},
			{Label: "Sample rate", Value: formatRatio(m.SampleRate)},
			{Label: "Enabled", Value: fmt.Sprint(cmdutil.Deref(m.Enabled))},
			{Label: "Created", Value: m.CreatedAt},
			{Label: "Updated", Value: m.UpdatedAt},
		}
	},
}

const monitorLong = `A monitor scores a share of one mesh's conversations against a rubric as
they happen. The judge metrics run on the monitor's model set. A mesh has at
most one monitor per rubric.

Monitors have no names. Refer to one by its id, or as MESH/RUBRIC using the
mesh's and rubric's names, such as support/support-quality.

A file for apply looks like this; the ids may be names:

  mesh_id: support
  rubric_id: support-quality
  model_set_id: judges
  sample_rate: 0.1     # score 10% of conversations
  enabled: true`

func newMonitor(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "monitor",
		Aliases: []string{"monitors"},
		Short:   "Score a share of a mesh's conversations against a rubric",
		Long:    monitorLong,
	}
	cmd.AddCommand(
		newMonitorList(app),
		monitorResource.GetCmd(app),
		newMonitorCreate(app),
		newMonitorUpdate(app),
		newMonitorApply(app),
		monitorResource.DeleteCmd(app),
		newMonitorEnable(app, true),
		newMonitorEnable(app, false),
	)
	return cmd
}

func newMonitorList(app *cmdutil.App) *cobra.Command {
	var mesh string
	var limit int
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List eval monitors",
		Example: "  partite eval monitor list\n  partite eval monitor list --mesh support -o json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := monitorResource.RequireEnterprise(app); err != nil {
				return err
			}
			var meshID *string
			if mesh != "" {
				id, err := lookup.Mesh(cmd.Context(), app, mesh)
				if err != nil {
					return err
				}
				meshID = &id
			}
			items, err := listMonitors(cmd.Context(), app, meshID, limit)
			if err != nil {
				return err
			}
			rows := make([][]string, len(items))
			for i, m := range items {
				rows[i] = monitorResource.Row(m)
			}
			if items == nil {
				items = []Monitor{}
			}
			return app.Printer().List(items, monitorResource.Columns, rows)
		},
	}
	cmd.Flags().StringVar(&mesh, "mesh", "", "only monitors of this mesh")
	cmd.Flags().IntVar(&limit, "limit", 0, "most results to return (default: all)")
	return cmd
}

// resolveMonitorRefs turns the names in a monitor spec into ids.
func resolveMonitorRefs(ctx context.Context, app *cmdutil.App, m Monitor) (Monitor, error) {
	var err error
	if m.MeshID == "" || m.RubricID == "" || m.ModelSetID == "" {
		return m, cmdutil.UsageError("a monitor needs a mesh, a rubric and a model set")
	}
	if m.SampleRate == nil {
		return m, cmdutil.UsageError("a monitor needs a sample rate from 0 to 1")
	}
	if *m.SampleRate < 0 || *m.SampleRate > 1 {
		return m, cmdutil.UsageError("the sample rate must be from 0 to 1, got %v", *m.SampleRate)
	}
	if m.MeshID, err = lookup.Mesh(ctx, app, m.MeshID); err != nil {
		return m, err
	}
	if m.RubricID, err = rubricResource.Resolve(ctx, app, m.RubricID); err != nil {
		return m, err
	}
	if m.ModelSetID, err = lookup.ModelSet(ctx, app, m.ModelSetID); err != nil {
		return m, err
	}
	return m, nil
}

func createMonitor(ctx context.Context, app *cmdutil.App, m Monitor) (Monitor, error) {
	c, err := app.GQL()
	if err != nil {
		return Monitor{}, err
	}
	enabled := true
	if m.Enabled != nil {
		enabled = *m.Enabled
	}
	resp, err := gql.EnterpriseCreateMonitor(ctx, c, gql.NewEvalMeshMonitor{
		MeshID:     m.MeshID,
		RubricID:   m.RubricID,
		ModelSetID: m.ModelSetID,
		Enabled:    enabled,
		SampleRate: *m.SampleRate,
	})
	if err != nil {
		return Monitor{}, err
	}
	return monitorFromAPI(resp.CreateEvalMeshMonitor), nil
}

func updateMonitor(ctx context.Context, app *cmdutil.App, id string, in gql.UpdateEvalMeshMonitor) (Monitor, error) {
	c, err := app.GQL()
	if err != nil {
		return Monitor{}, err
	}
	resp, err := gql.EnterpriseModifyMonitor(ctx, c, id, in)
	if err != nil {
		return Monitor{}, err
	}
	return monitorFromAPI(resp.UpdateEvalMeshMonitor), nil
}

func newMonitorCreate(app *cmdutil.App) *cobra.Command {
	var spec Monitor
	var rate float64
	var disabled bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Start scoring a mesh's conversations against a rubric",
		Example: `  partite eval monitor create --mesh support --rubric support-quality --model-set judges --sample-rate 0.1
  partite eval monitor create --mesh support --rubric latency --model-set judges --sample-rate 1 --disabled`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := monitorResource.RequireEnterprise(app); err != nil {
				return err
			}
			if cmd.Flags().Changed("sample-rate") {
				spec.SampleRate = &rate
			}
			enabled := !disabled
			spec.Enabled = &enabled
			m, err := resolveMonitorRefs(cmd.Context(), app, spec)
			if err != nil {
				return err
			}
			m, err = createMonitor(cmd.Context(), app, m)
			if err != nil {
				return err
			}
			app.Printer().Done("Created eval monitor %s (%s).", monitorName(m), m.ID)
			if app.Format() == output.Table {
				return nil
			}
			return monitorResource.Show(app, m)
		},
	}
	f := cmd.Flags()
	f.StringVar(&spec.MeshID, "mesh", "", "mesh whose conversations are scored (required)")
	f.StringVar(&spec.RubricID, "rubric", "", "rubric to score against (required)")
	f.StringVar(&spec.ModelSetID, "model-set", "", "model set the judge metrics run on (required)")
	f.Float64Var(&rate, "sample-rate", 0, "share of conversations to score, from 0 to 1 (required)")
	f.BoolVar(&disabled, "disabled", false, "create the monitor without starting it")
	for _, n := range []string{"mesh", "rubric", "model-set", "sample-rate"} {
		_ = cmd.MarkFlagRequired(n)
	}
	return cmd
}

func newMonitorUpdate(app *cmdutil.App) *cobra.Command {
	var modelSet string
	var rate float64
	var enabled bool
	cmd := &cobra.Command{
		Use:   "update <mesh/rubric-or-id>",
		Short: "Change a monitor's model set, sample rate or whether it is enabled",
		Long: `Change a monitor's model set, sample rate or whether it is enabled. The mesh
and rubric cannot change; delete the monitor and create another.`,
		Example: "  partite eval monitor update support/support-quality --sample-rate 0.25",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := monitorResource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			f := cmd.Flags()
			var in gql.UpdateEvalMeshMonitor
			if f.Changed("model-set") {
				msID, err := lookup.ModelSet(ctx, app, modelSet)
				if err != nil {
					return err
				}
				in.ModelSetID = &msID
			}
			if f.Changed("sample-rate") {
				if rate < 0 || rate > 1 {
					return cmdutil.UsageError("--sample-rate must be from 0 to 1, got %v", rate)
				}
				in.SampleRate = &rate
			}
			if f.Changed("enabled") {
				in.Enabled = &enabled
			}
			if in == (gql.UpdateEvalMeshMonitor{}) {
				return cmdutil.UsageError("nothing to change; pass --model-set, --sample-rate or --enabled")
			}
			m, err := updateMonitor(ctx, app, id, in)
			if err != nil {
				return err
			}
			app.Printer().Done("Updated eval monitor %s.", monitorName(m))
			if app.Format() == output.Table {
				return nil
			}
			return monitorResource.Show(app, m)
		},
	}
	cmd.Flags().StringVar(&modelSet, "model-set", "", "model set the judge metrics run on")
	cmd.Flags().Float64Var(&rate, "sample-rate", 0, "share of conversations to score, from 0 to 1")
	cmd.Flags().BoolVar(&enabled, "enabled", false, "whether the monitor scores conversations")
	return cmd
}

func newMonitorEnable(app *cmdutil.App, enable bool) *cobra.Command {
	use, short, done := "enable", "Start a monitor scoring conversations", "Enabled"
	if !enable {
		use, short, done = "disable", "Stop a monitor; what it has scored is kept", "Disabled"
	}
	return &cobra.Command{
		Use:     use + " <mesh/rubric-or-id>...",
		Short:   short,
		Example: fmt.Sprintf("  partite eval monitor %s support/support-quality", use),
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var out []Monitor
			for _, ref := range args {
				id, err := monitorResource.Resolve(cmd.Context(), app, ref)
				if err != nil {
					return err
				}
				m, err := updateMonitor(cmd.Context(), app, id, gql.UpdateEvalMeshMonitor{Enabled: &enable})
				if err != nil {
					return err
				}
				app.Printer().Done("%s eval monitor %s.", done, monitorName(m))
				out = append(out, m)
			}
			if app.Format() == output.Table {
				return nil
			}
			if len(out) == 1 {
				return monitorResource.Show(app, out[0])
			}
			return app.Printer().Data(out)
		},
	}
}

func newMonitorApply(app *cmdutil.App) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "apply -f <file>",
		Short: "Create or update an eval monitor from a file",
		Long: `Create the monitor in a YAML or JSON file, or update the one the mesh already
has for that rubric. Use -f - to read standard input. The output of get -o yaml
can be applied as is.`,
		Example: "  partite eval monitor apply -f support-quality-monitor.yaml",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := monitorResource.RequireEnterprise(app); err != nil {
				return err
			}
			var spec Monitor
			if err := app.ReadSpec(file, &spec); err != nil {
				return err
			}
			spec, err := resolveMonitorRefs(ctx, app, spec)
			if err != nil {
				return err
			}
			existing, err := listMonitors(ctx, app, &spec.MeshID, 0)
			if err != nil {
				return err
			}
			var m Monitor
			found := false
			for _, e := range existing {
				if e.RubricID == spec.RubricID {
					found = true
					m, err = updateMonitor(ctx, app, e.ID, gql.UpdateEvalMeshMonitor{
						ModelSetID: &spec.ModelSetID,
						SampleRate: spec.SampleRate,
						Enabled:    spec.Enabled,
					})
					break
				}
			}
			if !found {
				m, err = createMonitor(ctx, app, spec)
			}
			if err != nil {
				return err
			}
			if found {
				app.Printer().Done("Updated eval monitor %s (%s).", monitorName(m), m.ID)
			} else {
				app.Printer().Done("Created eval monitor %s (%s).", monitorName(m), m.ID)
			}
			if app.Format() == output.Table {
				return nil
			}
			return monitorResource.Show(app, m)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "YAML or JSON file, or - for standard input (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

// Package schedule manages schedules, which start a conversation with a mesh
// on a cron timetable. It needs an enterprise server.
package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmd/eval/lookup"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// Schedule is the view printed by get and read by -f. On input, mesh_id and
// route_id may be names.
type Schedule struct {
	ID                    string          `json:"id,omitempty"`
	Name                  string          `json:"name"`
	Description           string          `json:"description,omitempty"`
	MeshID                string          `json:"mesh_id"`
	MeshVersionLabel      string          `json:"mesh_version_label,omitempty"`
	RouteID               string          `json:"route_id,omitempty"`
	InputName             string          `json:"input_name"`
	Input                 json.RawMessage `json:"input"`
	ConversationTags      []cmdutil.Tag   `json:"conversation_tags,omitempty"`
	NotificationChannelID string          `json:"notification_channel_id,omitempty"`
	CronExpression        string          `json:"cron_expression"`
	Enabled               *bool           `json:"enabled,omitempty"`

	MeshName              string `json:"mesh_name,omitempty"`
	RouteName             string `json:"route_name,omitempty"`
	NextRunAt             string `json:"next_run_at,omitempty"`
	LastRunAt             string `json:"last_run_at,omitempty"`
	LastRunConversationID string `json:"last_run_conversation_id,omitempty"`
	LastRunError          string `json:"last_run_error,omitempty"`
	CreatedAt             string `json:"created_at,omitempty"`
	UpdatedAt             string `json:"updated_at,omitempty"`
}

func fromAPI(d gql.EnterpriseSchedule) Schedule {
	s := Schedule{
		ID:                    d.Id,
		Name:                  d.Name,
		Description:           cmdutil.Deref(d.Description),
		MeshID:                d.Mesh.Id,
		MeshName:              d.Mesh.Name,
		MeshVersionLabel:      cmdutil.Deref(d.MeshVersionLabel),
		InputName:             d.InputName,
		Input:                 d.Input,
		ConversationTags:      cmdutil.TagsFromAPI(d.ConversationTags),
		NotificationChannelID: cmdutil.Deref(d.NotificationChannelID),
		CronExpression:        d.CronExpression,
		Enabled:               &d.Enabled,
		NextRunAt:             cmdutil.Deref(d.NextRunAt),
		LastRunAt:             cmdutil.Deref(d.LastRunAt),
		LastRunConversationID: cmdutil.Deref(d.LastRunConversationID),
		LastRunError:          cmdutil.Deref(d.LastRunError),
		CreatedAt:             d.CreatedAt,
		UpdatedAt:             d.UpdatedAt,
	}
	if d.Route != nil {
		s.RouteID = d.Route.Id
		s.RouteName = d.Route.Name
	}
	return s
}

// target says what a schedule runs, for tables.
func target(s Schedule) string {
	switch {
	case s.RouteName != "":
		return s.MeshName + " route " + s.RouteName
	case s.MeshVersionLabel != "":
		return s.MeshName + "@" + s.MeshVersionLabel
	}
	return s.MeshName + " (draft)"
}

var resource = &cmdutil.Resource[Schedule]{
	WebList:    "triggers/schedules",
	WebPage:    "triggers/schedules/%s",
	WebLink:    "schedule",
	Kind:       "schedule",
	Command:    "schedule",
	IDPrefix:   "esch",
	Enterprise: true,
	List: func(ctx context.Context, app *cmdutil.App, opts cmdutil.ListOptions) ([]Schedule, error) {
		c, err := app.GQL()
		if err != nil {
			return nil, err
		}
		return cmdutil.ListAll(ctx, opts.Limit, func(ctx context.Context, first int, after *string) (cmdutil.Page[Schedule], error) {
			resp, err := gql.EnterpriseListSchedules(ctx, c, first, after, cmdutil.OptString(opts.Query))
			if err != nil {
				return cmdutil.Page[Schedule]{}, err
			}
			conn := resp.Schedules
			items := make([]Schedule, len(conn.Edges))
			for i, e := range conn.Edges {
				items[i] = fromAPI(e.Node)
			}
			return cmdutil.Page[Schedule]{Items: items, EndCursor: conn.PageInfo.EndCursor, HasNext: conn.PageInfo.HasNextPage}, nil
		})
	},
	Get: func(ctx context.Context, app *cmdutil.App, id string) (Schedule, error) {
		c, err := app.GQL()
		if err != nil {
			return Schedule{}, err
		}
		resp, err := gql.EnterpriseGetSchedule(ctx, c, id)
		if err != nil {
			return Schedule{}, err
		}
		return fromAPI(resp.Schedule), nil
	},
	Delete: func(ctx context.Context, app *cmdutil.App, ids []string) error {
		c, err := app.GQL()
		if err != nil {
			return err
		}
		_, err = gql.EnterpriseDeleteSchedules(ctx, c, ids)
		return err
	},
	ID:      func(s Schedule) string { return s.ID },
	Name:    func(s Schedule) string { return s.Name },
	Columns: []string{"id", "name", "cron (utc)", "runs", "enabled", "next run", "last run"},
	Row: func(s Schedule) []string {
		last := cmdutil.FormatTime(s.LastRunAt)
		if s.LastRunError != "" {
			last += " (failed)"
		}
		return []string{s.ID, s.Name, s.CronExpression, target(s), fmt.Sprint(cmdutil.Deref(s.Enabled)), cmdutil.FormatTime(s.NextRunAt), last}
	},
	Fields: func(s Schedule) []output.Field {
		next := cmdutil.FormatTime(s.NextRunAt)
		if !cmdutil.Deref(s.Enabled) {
			next = "never (disabled)"
		}
		return []output.Field{
			{Label: "ID", Value: s.ID},
			{Label: "Name", Value: s.Name},
			{Label: "Description", Value: s.Description},
			{Label: "Cron (UTC)", Value: s.CronExpression},
			{Label: "Enabled", Value: fmt.Sprint(cmdutil.Deref(s.Enabled))},
			{Label: "Mesh", Value: fmt.Sprintf("%s (%s)", s.MeshName, s.MeshID)},
			{Label: "Version label", Value: s.MeshVersionLabel},
			{Label: "Route", Value: s.RouteName},
			{Label: "Input name", Value: s.InputName},
			{Label: "Input", Value: string(s.Input)},
			{Label: "Tags", Value: cmdutil.FormatTags(s.ConversationTags)},
			{Label: "Notification channel", Value: s.NotificationChannelID},
			{Label: "Next run", Value: next},
			{Label: "Last run", Value: cmdutil.FormatTime(s.LastRunAt)},
			{Label: "Last conversation", Value: s.LastRunConversationID},
			{Label: "Last error", Value: s.LastRunError},
			{Label: "Created", Value: s.CreatedAt},
			{Label: "Updated", Value: s.UpdatedAt},
		}
	},
}

const longHelp = `A schedule starts a conversation with a mesh on a cron timetable, as if a
client had sent the same request each time. It stores what starting a
conversation takes:

  mesh_id                  the mesh to talk to (a name or id)
  mesh_version_label       run the version with this label; without it, the
                           draft runs
  route_id                 or run through a route of the mesh, which splits
                           traffic across versions (not with a label)
  input_name               the mesh input the request enters at
  input                    the request, a JSON object matching that input
  conversation_tags        tags put on each conversation it starts
  notification_channel_id  where the conversation's notifications go
  cron_expression          when it runs: five fields in UTC (minute, hour,
                           day of month, month, day of week). If both day
                           fields are set, either one matching is enough.

Each conversation also gets the tags schedule_id and scheduled_for, so you can
find what a schedule started. If a run fails, the error is kept in
last_run_error and the schedule tries again at its next time.

Preview a cron expression with: partite schedule next "0 9 * * 1-5"

This needs an enterprise server.`

func New(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "schedule",
		Aliases: []string{"schedules"},
		Short:   "Start conversations with a mesh on a cron timetable (enterprise)",
		Long:    longHelp,
	}
	cmd.AddCommand(
		resource.ListCmd(app),
		resource.GetCmd(app),
		resource.ViewCmd(app),
		newCreate(app),
		newUpdate(app),
		resource.ApplyCmd(app, "  partite schedule apply -f nightly-report.yaml", func(ctx context.Context, spec Schedule, id string) (Schedule, error) {
			spec, err := resolveRefs(ctx, app, spec)
			if err != nil {
				return Schedule{}, err
			}
			if id == "" {
				return create(ctx, app, spec)
			}
			return update(ctx, app, id, replaceAll(spec))
		}),
		resource.DeleteCmd(app),
		newEnable(app, true),
		newEnable(app, false),
		newNext(app),
	)
	return cmd
}

// resolveRefs checks a spec and turns its mesh and route names into ids.
func resolveRefs(ctx context.Context, app *cmdutil.App, s Schedule) (Schedule, error) {
	switch {
	case s.Name == "":
		return s, cmdutil.UsageError("a name is required")
	case s.MeshID == "":
		return s, cmdutil.UsageError("a mesh is required")
	case s.InputName == "":
		return s, cmdutil.UsageError("an input name is required: the mesh input the request enters at")
	case s.CronExpression == "":
		return s, cmdutil.UsageError("a cron expression is required, such as \"0 9 * * 1-5\"")
	case s.RouteID != "" && s.MeshVersionLabel != "":
		return s, cmdutil.UsageError("give a route or a version label, not both")
	}
	input, err := checkInput(s.Input)
	if err != nil {
		return s, err
	}
	s.Input = input
	if s.MeshID, err = lookup.Mesh(ctx, app, s.MeshID); err != nil {
		return s, err
	}
	if s.RouteID != "" {
		if s.RouteID, err = lookup.Route(ctx, app, s.MeshID, s.RouteID); err != nil {
			return s, err
		}
	}
	return s, nil
}

// checkInput checks that input is a JSON object. An empty input is {}.
func checkInput(raw json.RawMessage) (json.RawMessage, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return json.RawMessage("{}"), nil
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, cmdutil.UsageError("the input must be a JSON object, such as {\"message\": \"Write the daily report\"}")
	}
	return raw, nil
}

func create(ctx context.Context, app *cmdutil.App, s Schedule) (Schedule, error) {
	c, err := app.GQL()
	if err != nil {
		return Schedule{}, err
	}
	enabled := true
	if s.Enabled != nil {
		enabled = *s.Enabled
	}
	resp, err := gql.EnterpriseCreateSchedule(ctx, c, gql.NewSchedule{
		Name:                  s.Name,
		Description:           cmdutil.OptString(s.Description),
		MeshID:                s.MeshID,
		MeshVersionLabel:      cmdutil.OptString(s.MeshVersionLabel),
		RouteID:               cmdutil.OptString(s.RouteID),
		InputName:             s.InputName,
		Input:                 s.Input,
		ConversationTags:      cmdutil.TagsToAPI(s.ConversationTags),
		NotificationChannelID: cmdutil.OptString(s.NotificationChannelID),
		CronExpression:        s.CronExpression,
		Enabled:               enabled,
	})
	if err != nil {
		return Schedule{}, err
	}
	return fromAPI(resp.CreateSchedule), nil
}

// replaceAll is the update that makes a schedule match s, clearing the
// optional fields s leaves empty. Enabled is kept when s does not set it.
func replaceAll(s Schedule) gql.UpdateSchedule {
	in := gql.UpdateSchedule{
		Name:                  gql.Opt(s.Name),
		Description:           gql.OptNil(cmdutil.OptString(s.Description)),
		MeshID:                gql.Opt(s.MeshID),
		MeshVersionLabel:      gql.OptNil(cmdutil.OptString(s.MeshVersionLabel)),
		RouteID:               gql.OptNil(cmdutil.OptString(s.RouteID)),
		InputName:             gql.Opt(s.InputName),
		Input:                 gql.Opt(s.Input),
		ConversationTags:      gql.Opt(cmdutil.TagsToAPI(s.ConversationTags)),
		NotificationChannelID: gql.OptNil(cmdutil.OptString(s.NotificationChannelID)),
		CronExpression:        gql.Opt(s.CronExpression),
		Enabled:               gql.OptPtr(s.Enabled),
	}
	return in
}

func update(ctx context.Context, app *cmdutil.App, id string, in gql.UpdateSchedule) (Schedule, error) {
	c, err := app.GQL()
	if err != nil {
		return Schedule{}, err
	}
	resp, err := gql.EnterpriseModifySchedule(ctx, c, id, &in)
	if err != nil {
		return Schedule{}, err
	}
	return fromAPI(resp.UpdateSchedule), nil
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var flags Schedule
	var input, file string
	var tags []string
	var disabled bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a schedule",
		Long: `Create a schedule from flags or a file; flags override the file. The
schedule starts enabled unless --disabled is passed.`,
		Example: `  partite schedule create daily-report --mesh reports --input-name prompt \
      --input '{"message": "Write the daily report"}' --cron "0 9 * * 1-5"
  partite schedule create weekly --mesh reports --label stable --input-name prompt --input @weekly.json --cron "0 6 * * 1"
  partite schedule create -f daily-report.yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := resource.RequireEnterprise(app); err != nil {
				return err
			}
			var spec Schedule
			if file != "" {
				if err := app.ReadSpec(file, &spec); err != nil {
					return err
				}
			}
			if len(args) == 1 {
				spec.Name = args[0]
			}
			if err := applyFlags(cmd, app, &spec, flags, input, tags); err != nil {
				return err
			}
			if cmd.Flags().Changed("disabled") {
				enabled := !disabled
				spec.Enabled = &enabled
			}
			spec, err := resolveRefs(ctx, app, spec)
			if err != nil {
				return err
			}
			s, err := create(ctx, app, spec)
			if err != nil {
				return err
			}
			app.Printer().Done("Created schedule %s (%s). Next run: %s.", s.Name, s.ID, nextRunText(s))
			if app.Format() == output.Table {
				return nil
			}
			return resource.Show(app, s)
		},
	}
	addFlags(cmd, &flags, &input, &tags)
	cmd.Flags().BoolVar(&disabled, "disabled", false, "create the schedule without starting it")
	cmd.Flags().StringVarP(&file, "file", "f", "", "read the schedule from a YAML or JSON file")
	return cmd
}

func nextRunText(s Schedule) string {
	if s.NextRunAt == "" {
		return "never (disabled)"
	}
	return cmdutil.FormatTime(s.NextRunAt)
}

func addFlags(cmd *cobra.Command, s *Schedule, input *string, tags *[]string) {
	f := cmd.Flags()
	f.StringVar(&s.Description, "description", "", "description")
	f.StringVar(&s.MeshID, "mesh", "", "mesh to start conversations with")
	f.StringVar(&s.MeshVersionLabel, "label", "", "run the mesh version with this label (default: the draft)")
	f.StringVar(&s.RouteID, "route", "", "run through this route of the mesh instead of a label")
	f.StringVar(&s.InputName, "input-name", "", "the mesh input the request enters at")
	f.StringVar(input, "input", "", "the request as a JSON object; @file reads a file")
	f.StringArrayVar(tags, "tag", nil, "tag for each conversation, as name=value; repeat for more")
	f.StringVar(&s.NotificationChannelID, "notification-channel", "", "id of the notification channel for the conversations")
	f.StringVar(&s.CronExpression, "cron", "", "when to run: five cron fields in UTC, such as \"0 9 * * 1-5\"")
}

// applyFlags copies the flags that were passed onto s.
func applyFlags(cmd *cobra.Command, app *cmdutil.App, s *Schedule, flags Schedule, input string, tags []string) error {
	f := cmd.Flags()
	if f.Changed("description") {
		s.Description = flags.Description
	}
	if f.Changed("mesh") {
		s.MeshID = flags.MeshID
	}
	if f.Changed("label") {
		s.MeshVersionLabel = flags.MeshVersionLabel
	}
	if f.Changed("route") {
		s.RouteID = flags.RouteID
	}
	if f.Changed("input-name") {
		s.InputName = flags.InputName
	}
	if f.Changed("input") {
		v, err := app.ReadValue(input)
		if err != nil {
			return err
		}
		s.Input = json.RawMessage(v)
	}
	if f.Changed("tag") {
		t, err := cmdutil.ParseTags(tags)
		if err != nil {
			return err
		}
		s.ConversationTags = t
	}
	if f.Changed("notification-channel") {
		s.NotificationChannelID = flags.NotificationChannelID
	}
	if f.Changed("cron") {
		s.CronExpression = flags.CronExpression
	}
	return nil
}

func newUpdate(app *cmdutil.App) *cobra.Command {
	var flags Schedule
	var name, input string
	var tags []string
	var enabled bool
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Change a schedule",
		Long: `Change the fields of a schedule that are passed as flags. Pass an empty value,
such as --label "", to clear --description, --label, --route or
--notification-channel. --tag replaces all the tags.`,
		Example: `  partite schedule update daily-report --cron "30 8 * * *"
  partite schedule update daily-report --route canary
  partite schedule update daily-report --label ""`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := resource.Resolve(ctx, app, args[0])
			if err != nil {
				return err
			}
			cur, err := resource.Get(ctx, app, id)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			changed := false
			for _, n := range []string{"name", "description", "mesh", "label", "route", "input-name", "input", "tag", "notification-channel", "cron", "enabled"} {
				changed = changed || f.Changed(n)
			}
			if !changed {
				return cmdutil.UsageError("nothing to change; see partite schedule update --help")
			}
			if f.Changed("name") {
				cur.Name = name
			}
			if err := applyFlags(cmd, app, &cur, flags, input, tags); err != nil {
				return err
			}
			// A new route replaces the label and the other way round.
			if f.Changed("route") && !f.Changed("label") && cur.RouteID != "" {
				cur.MeshVersionLabel = ""
			}
			if f.Changed("label") && !f.Changed("route") && cur.MeshVersionLabel != "" {
				cur.RouteID = ""
			}
			if f.Changed("mesh") && !f.Changed("route") {
				cur.RouteID = ""
			}
			if f.Changed("enabled") {
				cur.Enabled = &enabled
			}
			cur, err = resolveRefs(ctx, app, cur)
			if err != nil {
				return err
			}
			s, err := update(ctx, app, id, replaceAll(cur))
			if err != nil {
				return err
			}
			app.Printer().Done("Updated schedule %s. Next run: %s.", s.Name, nextRunText(s))
			if app.Format() == output.Table {
				return nil
			}
			return resource.Show(app, s)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	addFlags(cmd, &flags, &input, &tags)
	cmd.Flags().BoolVar(&enabled, "enabled", false, "whether the schedule runs; --enabled=false stops it")
	return cmd
}

func newEnable(app *cmdutil.App, enable bool) *cobra.Command {
	use, short, done := "enable", "Start running a schedule again", "Enabled"
	if !enable {
		use, short, done = "disable", "Stop running a schedule", "Disabled"
	}
	return &cobra.Command{
		Use:     use + " <name-or-id>...",
		Short:   short,
		Example: fmt.Sprintf("  partite schedule %s daily-report", use),
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var out []Schedule
			for _, ref := range args {
				id, err := resource.Resolve(cmd.Context(), app, ref)
				if err != nil {
					return err
				}
				s, err := update(cmd.Context(), app, id, gql.UpdateSchedule{Enabled: gql.Opt(enable)})
				if err != nil {
					return err
				}
				if enable {
					app.Printer().Done("%s schedule %s. Next run: %s.", done, s.Name, nextRunText(s))
				} else {
					app.Printer().Done("%s schedule %s.", done, s.Name)
				}
				out = append(out, s)
			}
			if app.Format() == output.Table {
				return nil
			}
			if len(out) == 1 {
				return resource.Show(app, out[0])
			}
			return app.Printer().Data(out)
		},
	}
}

func newNext(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:   "next <cron-expression>",
		Short: "Show when a cron expression next runs",
		Long: `Show the next time a cron expression runs, as the server reads it, without
saving anything. Cron expressions have five fields in UTC: minute, hour, day
of month, month and day of week. Quote the expression.`,
		Example: `  partite schedule next "0 9 * * 1-5"
  partite schedule next "*/15 * * * *" -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := resource.RequireEnterprise(app); err != nil {
				return err
			}
			c, err := app.GQL()
			if err != nil {
				return err
			}
			resp, err := gql.EnterpriseCronNextRun(cmd.Context(), c, args[0])
			if err != nil {
				// The server rejects a bad expression with a plain error; it
				// is the user's input, so it is a usage error.
				if e := app.Classify(err); e.Code == cmdutil.ExitError {
					e.Code = cmdutil.ExitUsage
					return e
				}
				return err
			}
			next := resp.CronNextRun
			if app.Format() != output.Table {
				return app.Printer().Data(map[string]string{"cron_expression": args[0], "next_run_at": next})
			}
			t, err := time.Parse(time.RFC3339Nano, next)
			if err != nil {
				fmt.Fprintln(app.Out, next)
				return nil
			}
			fmt.Fprintf(app.Out, "%s UTC (%s local, in %s)\n", t.UTC().Format("2006-01-02 15:04"), t.Local().Format("2006-01-02 15:04"), time.Until(t).Round(time.Minute))
			return nil
		},
	}
}

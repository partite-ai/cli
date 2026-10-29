// Package conversation looks at and manages conversations: their requests,
// events, responses and tasks.
package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/partite-ai/partite-cli/internal/cmd/accesspoint"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convapi"
	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convui"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

func New(app *cmdutil.App) *cobra.Command {
	var caller accesspoint.CallerFlags
	cmd := &cobra.Command{
		Use:     "conversation",
		Aliases: []string{"conversations", "conv"},
		GroupID: "runtime",
		Short:   "Look at and manage conversations, requests, events and tasks",
		Long: `A conversation is a series of requests to one mesh version. Each request
has events: messages, thinking, tasks, and at the end a response, an error or
a cancellation. Start requests with partite run or partite chat.

A conversation is named by its id (rcnv_...) or by the external id it was
created with. Where a request (rreq_...) is optional, the conversation's
latest request is used.

` + accesspoint.CallerHelp + ` A conversation made through an access
point belongs to the caller who made it, so read it back with the same
--access-point and --subject.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if caller.AccessPoint != "" && cmd.Name() == "notifications" {
				return cmdutil.UsageError("notification channels cannot be read through an access point")
			}
			return caller.Apply(cmd, app)
		},
	}
	caller.Register(cmd, true)
	cmd.AddCommand(
		newCreate(app),
		newGet(app),
		newLookup(app),
		newIntents(app),
		newRequests(app),
		newEvents(app),
		newResponse(app),
		newCancel(app),
		newTask(app),
		newAttach(app),
		newResources(app),
		newNotifications(app),
	)
	return cmd
}

func client(app *cmdutil.App) (*convapi.Client, error) {
	rest, err := app.REST()
	if err != nil {
		return nil, err
	}
	return &convapi.Client{REST: rest}, nil
}

// target resolves the <conversation> [request] arguments.
func target(ctx context.Context, c *convapi.Client, args []string) (string, string, error) {
	conv, err := convui.ResolveConversation(ctx, c, args[0])
	if err != nil {
		return "", "", err
	}
	ref := ""
	if len(args) > 1 {
		ref = args[1]
	}
	req, err := convui.ResolveRequest(ctx, c, conv, ref)
	return conv, req, err
}

func showConversation(app *cmdutil.App, conv convapi.Conversation) error {
	if conv.Tags == nil {
		conv.Tags = []cmdutil.Tag{}
	}
	return app.Printer().Object(conv, conv.ID, []output.Field{
		{Label: "ID", Value: conv.ID},
		{Label: "External id", Value: conv.ExternalID},
		{Label: "Mesh", Value: conv.Mesh.ID},
		{Label: "Mesh version", Value: conv.Mesh.VersionID},
		{Label: "Tags", Value: cmdutil.FormatTags(conv.Tags)},
		{Label: "Created", Value: cmdutil.FormatTime(conv.CreatedAt)},
		{Label: "Updated", Value: cmdutil.FormatTime(conv.UpdatedAt)},
	})
}

func newCreate(app *cmdutil.App) *cobra.Command {
	var tf convui.TargetFlags
	var body convapi.NewConversation
	var tags []string
	var noTrace bool
	cmd := &cobra.Command{
		Use:   "create <mesh>",
		Short: "Start an empty conversation, to attach files before the first request",
		Long: `Start a conversation with no request. Most of the time partite run starts
conversations; create one first to attach files to it, to read its intents,
or to set it up before anyone sends a request. Send requests to it with:
partite run <mesh> --conversation <id>.

` + convui.TargetHelp,
		Example: `  partite conversation create support --label prod --external-id ticket-4411
  id=$(partite conversation create support --draft -o id)
  partite conversation attach $id contract.pdf`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := client(app)
			if err != nil {
				return err
			}
			if body.Target, err = convui.ResolveTarget(ctx, app, args[0], tf); err != nil {
				return err
			}
			if body.Tags, err = cmdutil.ParseTags(tags); err != nil {
				return err
			}
			if noTrace {
				body.Tracing = cmdutil.Ptr(false)
			}
			if body.NotificationChannel != "" {
				if err := c.CreateChannel(ctx, body.NotificationChannel); err != nil {
					return err
				}
			}
			conv, err := c.CreateConversation(ctx, body, nil)
			if err != nil {
				return convui.CreateError(err, args[0], body)
			}
			app.Printer().Done("Created conversation %s.", conv.ID)
			if app.Format() == output.Table {
				return nil
			}
			return showConversation(app, conv)
		},
	}
	tf.Register(cmd)
	f := cmd.Flags()
	f.StringVar(&body.ExternalID, "external-id", "", "your own unique id for the conversation, to find it again")
	f.StringArrayVar(&tags, "tag", nil, "tag the conversation, as name=value; repeat for more")
	f.StringVar(&body.NotificationChannel, "notification-channel", "", "also send the conversation's events to this notification channel")
	f.BoolVar(&noTrace, "no-trace", false, "record no trace for the conversation")
	return cmd
}

func newGet(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "get <conversation>",
		Aliases: []string{"show"},
		Short:   "Show a conversation",
		Example: "  partite conversation get rcnv_8Xk2\n  partite conversation get ticket-4411",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client(app)
			if err != nil {
				return err
			}
			path := convapi.ConversationPath(args[0])
			if !cmdutil.IsID(args[0], "rcnv") {
				path = convapi.ExternalIDPath(args[0])
			}
			conv, err := c.GetConversation(cmd.Context(), path)
			if err != nil {
				return err
			}
			return showConversation(app, conv)
		},
	}
}

func newLookup(app *cmdutil.App) *cobra.Command {
	var externalID string
	cmd := &cobra.Command{
		Use:     "lookup --external-id <id>",
		Short:   "Find a conversation by the external id it was created with",
		Example: "  partite conversation lookup --external-id ticket-4411 -o id",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client(app)
			if err != nil {
				return err
			}
			conv, err := c.GetConversation(cmd.Context(), convapi.ExternalIDPath(externalID))
			if err != nil {
				return err
			}
			return showConversation(app, conv)
		},
	}
	cmd.Flags().StringVar(&externalID, "external-id", "", "the external id (required)")
	_ = cmd.MarkFlagRequired("external-id")
	return cmd
}

func newIntents(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:   "intents <conversation>",
		Short: "List the intents a conversation takes, with their input fields",
		Long: `List the intents of the mesh version a conversation runs on: what each one
does, the fields of its input and the responses it can give. To see the
intents of a mesh version, create a conversation on it first with
partite conversation create.

With -o json the output has each intent's full input schema ("schema") and
response schemas, as JSON Schema. Use it to build the input for
partite run --input.`,
		Example: `  partite conversation intents rcnv_8Xk2
  partite conversation intents $(partite conversation create support --label prod -o id) -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := client(app)
			if err != nil {
				return err
			}
			id, err := convui.ResolveConversation(ctx, c, args[0])
			if err != nil {
				return err
			}
			intents, err := c.Intents(ctx, id)
			if err != nil {
				return err
			}
			if intents == nil {
				intents = []convapi.Intent{}
			}
			switch app.Format() {
			case output.Table:
				convui.WriteIntents(app.Out, convui.NewStyle(app.Out), intents)
			case output.ID:
				for _, in := range intents {
					fmt.Fprintln(app.Out, in.Name)
				}
			default:
				return app.Printer().Data(intents)
			}
			return nil
		},
	}
}

func oneLineJSON(data json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, data) != nil {
		return string(data)
	}
	return buf.String()
}

func newRequests(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:   "requests <conversation>",
		Short: "List a conversation's requests and how each ended",
		Long: `List a conversation's requests, oldest first. With -o json each request has
its status, input, and its response, error or open tasks. See a request's
events with partite conversation events.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := client(app)
			if err != nil {
				return err
			}
			id, err := convui.ResolveConversation(ctx, c, args[0])
			if err != nil {
				return err
			}
			reqs, err := c.Requests(ctx, id)
			if err != nil {
				return err
			}
			if reqs == nil {
				reqs = []convapi.Request{}
			}
			rows := make([][]string, len(reqs))
			for i, r := range reqs {
				res := convapi.ResultOf(id, r)
				outcome := res.Status
				switch res.Status {
				case convapi.StatusComplete:
					outcome = convui.ResponseText(res.Response)
				case convapi.StatusFailed:
					outcome = "failed: " + res.Error
				case convapi.StatusTask:
					outcome = "waiting for task " + res.Task.TaskID
				}
				rows[i] = []string{r.ID, res.Status, oneLineJSON(r.Input), outcome}
			}
			return app.Printer().List(reqs, []string{"request", "status", "input", "result"}, rows)
		},
	}
}

func newEvents(app *cmdutil.App) *cobra.Command {
	var follow, thinking bool
	var after string
	cmd := &cobra.Command{
		Use:   "events <conversation> [request]",
		Short: "Show a request's events, or follow them as they come",
		Long: `Show the events of a request: messages, thinking, tasks and their answers,
and the response, error or cancellation that ends it.

With --follow, show events as they come until the request ends, like
partite run does. Without a terminal, --follow prints each event as a line of
JSON and the result as the last line, and exits with the same codes as run.
Pick up after an event you already have with --after.

` + convui.ExitCodesHelp,
		Example: `  partite conversation events rcnv_8Xk2
  partite conversation events rcnv_8Xk2 rreq_3Fd9 --follow
  partite conversation events rcnv_8Xk2 --follow --after rout_77Qa -o json`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := client(app)
			if err != nil {
				return err
			}
			conv, req, err := target(ctx, c, args)
			if err != nil {
				return err
			}
			if follow {
				_, err := convui.Follow(ctx, app, c, convui.NewLineReader(app.In), conv, req, after, convui.FollowOptions{
					Thinking: thinking,
					Events:   app.Format() != output.Table,
					Answer:   app.Interactive(),
				})
				return err
			}
			events, err := c.Events(ctx, conv, req, after)
			if err != nil {
				return err
			}
			r, err := c.GetRequest(ctx, conv, req, 0)
			if err != nil {
				return err
			}
			if app.Format() == output.Table {
				rd := &convui.Renderer{Out: app.Out, Style: convui.NewStyle(app.Out), Thinking: thinking, DrawForms: output.IsTerminal(app.Out)}
				for _, ev := range events {
					if err := rd.Event(ev); err != nil {
						return err
					}
				}
				if res := convapi.ResultOf(conv, r); res.Status != convapi.StatusComplete {
					fmt.Fprintln(app.Out, convui.NewStyle(app.Out).Dim("\n"+describeStatus(res)))
				}
				return nil
			}
			rows := make([][]string, len(events))
			for i, ev := range events {
				rows[i] = []string{ev.ID}
			}
			return app.Printer().List(events, []string{"id"}, rows)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep showing events until the request ends")
	cmd.Flags().BoolVar(&thinking, "thinking", false, "show the model's thinking, dimmed")
	cmd.Flags().StringVar(&after, "after", "", "start after the event with this id")
	return cmd
}

func describeStatus(res convapi.Result) string {
	switch res.Status {
	case convapi.StatusFailed:
		return "The request failed: " + res.Error
	case convapi.StatusCancelled:
		return "The request was cancelled."
	case convapi.StatusTask:
		return "The request waits for task " + res.Task.TaskID + " to be completed."
	}
	return "The request is still running."
}

func newResponse(app *cmdutil.App) *cobra.Command {
	var wait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "response <conversation> [request]",
		Short: "Show a request's result, or wait for it",
		Long: `Show where a request stands: its response, or that it failed, was cancelled,
waits for a task or is still running. With --wait, wait until it ends or
stops at a task. At a terminal, --wait asks for the answers to tasks and
goes on waiting.

` + convui.ResultHelp + `

` + convui.ExitCodesHelp,
		Example: `  partite conversation response rcnv_8Xk2
  partite conversation response rcnv_8Xk2 rreq_3Fd9 --wait -o json | jq .response`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := client(app)
			if err != nil {
				return err
			}
			conv, req, err := target(ctx, c, args)
			if err != nil {
				return err
			}
			if !wait {
				r, err := c.GetRequest(ctx, conv, req, 0)
				if err != nil {
					return err
				}
				return convui.Report(app, convapi.ResultOf(conv, r), false)
			}
			waitCtx := ctx
			if timeout > 0 {
				var cancel context.CancelFunc
				waitCtx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			lr := convui.NewLineReader(app.In)
			for {
				r, err := c.WaitRequest(waitCtx, conv, req)
				if err != nil {
					if waitCtx.Err() != nil && ctx.Err() == nil {
						return convui.Report(app, convapi.Result{ConversationID: conv, RequestID: req, Status: convapi.StatusRunning}, false)
					}
					return err
				}
				res := convapi.ResultOf(conv, r)
				if res.Status != convapi.StatusTask || !app.Interactive() {
					return convui.Report(app, res, false)
				}
				fmt.Fprintln(app.Err, convui.NewStyle(app.Err).Question(convui.TaskQuestion(*res.Task)))
				if err := convui.AnswerTask(ctx, app, c, lr, conv, req, *res.Task); err != nil {
					return err
				}
			}
		},
	}
	cmd.Flags().BoolVarP(&wait, "wait", "w", false, "wait for the request to end")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "with --wait, stop waiting after this long, such as 5m")
	return cmd
}

func newCancel(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:     "cancel <conversation> [request]",
		Short:   "Cancel a running request",
		Long:    "Cancel a request. The mesh stops at its next checkpoint and the request ends with a cancellation.",
		Example: "  partite conversation cancel rcnv_8Xk2\n  partite conversation cancel rcnv_8Xk2 rreq_3Fd9",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := client(app)
			if err != nil {
				return err
			}
			conv, req, err := target(ctx, c, args)
			if err != nil {
				return err
			}
			r, err := c.Cancel(ctx, conv, req)
			if err != nil {
				return err
			}
			if r.Status != convapi.RequestCancelled {
				return &cmdutil.Error{Code: cmdutil.ExitConflict, Msg: fmt.Sprintf("request %s has ended already, so it cannot be cancelled", req), Hint: "See how it ended with: partite conversation response " + conv + " " + req}
			}
			app.Printer().Done("Cancelled request %s.", req)
			if app.Format() == output.Table {
				return nil
			}
			return app.Printer().Object(map[string]any{"conversation_id": conv, "request_id": req, "cancelled": true}, req, nil)
		},
	}
}

func newAttach(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:   "attach <conversation> <file>...",
		Short: "Upload files to a conversation",
		Long: `Upload files to a conversation, so that its agents can read them. Each file
gets a reference, such as {"$ref": "resource://r3"}, that a later request's
input can hold to point at it. To attach files to a new request, use
partite run --attach instead.`,
		Example: "  partite conversation attach rcnv_8Xk2 contract.pdf notes.md",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := client(app)
			if err != nil {
				return err
			}
			conv, err := convui.ResolveConversation(ctx, c, args[0])
			if err != nil {
				return err
			}
			files := make([]convapi.Attachment, 0, len(args)-1)
			for _, path := range args[1:] {
				f, err := convapi.ReadAttachment(path)
				if err != nil {
					return err
				}
				files = append(files, f)
			}
			added, err := c.AddResources(ctx, conv, files)
			if err != nil {
				return err
			}
			return listResources(app, added)
		},
	}
}

func listResources(app *cmdutil.App, resources []convapi.Resource) error {
	if resources == nil {
		resources = []convapi.Resource{}
	}
	rows := make([][]string, len(resources))
	for i, r := range resources {
		rows[i] = []string{r.Name, r.MimeType, oneLineJSON(r.Reference)}
	}
	return app.Printer().List(resources, []string{"name", "type", "reference"}, rows)
}

func newResources(app *cmdutil.App) *cobra.Command {
	return &cobra.Command{
		Use:   "resources <conversation>",
		Short: "List the files and other things a conversation holds",
		Long: `List a conversation's resources, in the order they came: attached files, and
what tools and MCP servers added. Each has a reference, such as
{"$ref": "resource://r3"}, that a request's input can hold to point at it.`,
		Example: "  partite conversation resources rcnv_8Xk2 -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := client(app)
			if err != nil {
				return err
			}
			conv, err := convui.ResolveConversation(ctx, c, args[0])
			if err != nil {
				return err
			}
			resources, err := c.Resources(ctx, conv)
			if err != nil {
				return err
			}
			return listResources(app, resources)
		},
	}
}

func newNotifications(app *cmdutil.App) *cobra.Command {
	var follow, create bool
	var after string
	var limit int
	cmd := &cobra.Command{
		Use:   "notifications <channel>",
		Short: "Read a notification channel: the events of many conversations",
		Long: `Read the events of a notification channel. Conversations created with
--notification-channel send every event of every request to the channel, so
one reader can follow them all. Events are read in order; resume after one
you have with --after.

Without --follow, print the events there are and stop. With -o json the
output is {"events": [...], "next": "rnco_..."}; pass next to --after to read
on. With --follow, wait for new events until Ctrl-C; with -o json each event
is a line of JSON.`,
		Example: `  partite conversation notifications support-inbox
  partite conversation notifications support-inbox --follow -o json
  partite conversation notifications support-inbox --after rnco_5tPq`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := client(app)
			if err != nil {
				return err
			}
			channel := args[0]
			if create {
				if err := c.CreateChannel(ctx, channel); err != nil {
					return err
				}
			}
			style := convui.NewStyle(app.Out)
			if follow {
				err := c.FollowChannel(ctx, channel, after, func(n convapi.Notification) error {
					if app.Format() == output.Table {
						fmt.Fprintln(app.Out, notificationLine(style, n))
						return nil
					}
					data, err := json.Marshal(n)
					if err != nil {
						return err
					}
					_, err = fmt.Fprintf(app.Out, "%s\n", data)
					return err
				})
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			var all []convapi.Notification
			next := after
			for limit <= 0 || len(all) < limit {
				size := 1000
				if limit > 0 {
					size = min(size, limit-len(all))
				}
				page, n, err := c.ChannelPage(ctx, channel, next, size)
				if err != nil {
					return err
				}
				all = append(all, page...)
				next = n
				if len(page) < size {
					break
				}
			}
			if all == nil {
				all = []convapi.Notification{}
			}
			switch app.Format() {
			case output.Table:
				if len(all) == 0 {
					fmt.Fprintln(app.Out, "No events.")
					return nil
				}
				for _, n := range all {
					fmt.Fprintln(app.Out, notificationLine(style, n))
				}
				fmt.Fprintln(app.Err, style.Dim("Read on with: --after "+next))
			case output.ID:
				for _, n := range all {
					fmt.Fprintln(app.Out, n.ID)
				}
			default:
				return app.Printer().Data(map[string]any{"events": all, "next": next})
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "wait for new events until Ctrl-C")
	cmd.Flags().BoolVar(&create, "create", false, "create the channel first if it does not exist")
	cmd.Flags().StringVar(&after, "after", "", "start after the event with this id (rnco_...)")
	cmd.Flags().IntVar(&limit, "limit", 100, "most events to read without --follow; 0 for all")
	return cmd
}

func notificationLine(style convui.Style, n convapi.Notification) string {
	text := ""
	switch n.Event.Type {
	case "response":
		text = convui.ResponseText(n.Event.Data)
	case "task":
		if t, err := convapi.TaskFromEvent(n.Event); err == nil {
			text = convui.TaskQuestion(t)
		}
	case "task_response":
		text = convui.AnswerText(n.Event.Data)
	default:
		text = convapi.EventText(n.Event.Data)
	}
	conv := n.ConversationID
	if n.ConversationExternalID != "" {
		conv += " (" + n.ConversationExternalID + ")"
	}
	return fmt.Sprintf("%s %s %s  %s", style.Dim(n.ID), style.Dim(conv+" "+n.RequestID), style.Bold(n.Event.Type), output.Truncate(strings.ReplaceAll(text, "\n", " "), 200))
}

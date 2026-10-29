// Package run has the commands that talk to a mesh: run sends one request
// and chat holds a conversation.
package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmd/accesspoint"
	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convapi"
	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convui"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

// requestFlags are what run and chat share for starting requests.
type requestFlags struct {
	intent       string
	conversation string
	target       convui.TargetFlags
	caller       accesspoint.CallerFlags
}

func (f *requestFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.intent, "intent", "", "intent to call (default: the mesh's only intent, or the only one that takes text)")
	cmd.Flags().StringVar(&f.conversation, "conversation", "", "continue this conversation (id or external id) instead of starting one")
	f.target.Register(cmd)
	f.caller.Register(cmd, false)
}

// session is where requests go: an existing conversation, or a new one on
// a mesh version.
type session struct {
	client         *convapi.Client
	meshRef        string
	target         convapi.Target
	conversationID string
	// intents are the conversation's. They are nil before there is one.
	intents []convapi.Intent
}

// openSession resolves the mesh, and the conversation with its intents when
// there is one.
func openSession(ctx context.Context, app *cmdutil.App, cmd *cobra.Command, meshRef string, f *requestFlags) (*session, error) {
	rest, err := app.REST()
	if err != nil {
		return nil, err
	}
	s := &session{client: &convapi.Client{REST: rest}, meshRef: meshRef}
	if f.conversation == "" {
		s.target, err = convui.ResolveTarget(ctx, app, meshRef, f.target)
		return s, err
	}
	if f.target.Set(cmd) {
		return nil, cmdutil.UsageError("--version, --label, --route and --draft pick the version of a new conversation; --conversation already has one")
	}
	if s.conversationID, err = convui.ResolveConversation(ctx, s.client, f.conversation); err != nil {
		return nil, err
	}
	conv, err := s.client.GetConversation(ctx, convapi.ConversationPath(s.conversationID))
	if err != nil {
		return nil, err
	}
	meshID, err := convui.ResolveMeshID(ctx, app, meshRef)
	if err != nil {
		return nil, err
	}
	if meshID != conv.Mesh.ID {
		return nil, cmdutil.UsageError("conversation %s is on mesh %s, not %s", s.conversationID, conv.Mesh.ID, meshRef)
	}
	s.intents, err = s.client.Intents(ctx, s.conversationID)
	return s, err
}

// create starts the session's conversation with no message, so that its
// intents can be read.
func (s *session) create(ctx context.Context, opts convapi.NewConversation) error {
	opts.Target = s.target
	conv, err := s.client.CreateConversation(ctx, opts, nil)
	if err != nil {
		return convui.CreateError(err, s.meshRef, opts)
	}
	s.conversationID = conv.ID
	s.intents, err = s.client.Intents(ctx, s.conversationID)
	return err
}

func (s *session) intentsCmd() string {
	return "partite conversation intents " + s.conversationID
}

// started reports a request that run started without following it.
func started(app *cmdutil.App, events bool, conversationID, requestID string) error {
	if app.Format() == output.Table && !events {
		fmt.Fprintf(app.Out, "Started request %s in conversation %s.\n", requestID, conversationID)
		fmt.Fprintf(app.Out, "Follow it with: partite conversation events %s %s --follow\n", conversationID, requestID)
		return nil
	}
	res := convapi.Result{ConversationID: conversationID, RequestID: requestID, Status: convapi.StatusRunning}
	if events {
		res.Type = "result"
	}
	// The request is running, which is what --no-wait asked for, so this
	// is not an error.
	_ = convui.Report(app, res, false)
	return nil
}

// createdHint adds to an error about the input that run created the
// conversation already, so that the next try can use it.
func createdHint(err error, s *session, created bool) error {
	var e *cmdutil.Error
	if !created || !errors.As(err, &e) {
		return err
	}
	e.Hint = strings.TrimSpace(e.Hint + "\nConversation " + s.conversationID + " was created with no request. Send to it with: partite run " + s.meshRef + " --conversation " + s.conversationID)
	return e
}

func New(app *cmdutil.App) *cobra.Command {
	var rf requestFlags
	var input, externalID, channel string
	var attach, tags, requestTags []string
	var noTrace, events, noWait, cancelOnInterrupt, thinking bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:     "run <mesh> [text]",
		GroupID: "runtime",
		Short:   "Send a request to a mesh and show its answer",
		Long: `Send a request to a mesh and follow it until it answers.

The request starts a new conversation on the mesh, or continues one with
--conversation. A new conversation needs the mesh version: --label,
--version, --route or --draft.

Give the input as text, which fills the intent's one text field, or as a
whole JSON or YAML object with --input. Use - as the text to read it from
standard input. With text, run creates the conversation first, to read the
fields of its intents; see them with partite conversation intents
<conversation>. With --input, the conversation and its first request are
created together, and the server checks the input.

At a terminal the agents' messages show as they come, and the response at the
end. When the mesh asks you something (a task), you answer at the prompt.
Without a terminal, run stops at the task with exit code 9, and prints it.
Answer it with partite conversation task complete.

Ctrl-C stops following. It asks whether to cancel the request on the server,
or cancels it without asking with --cancel-on-interrupt.

` + convui.ResultHelp + `

With --events, every event is printed as a line of JSON as it comes, and the
result is the last line, with "type": "result". Event types are message,
thinking, task, task_response, response, error and cancelled.

` + convui.ExitCodesHelp + `

` + accesspoint.CallerHelp,
		Example: `  partite run support --label prod "How do I reset my password?"
  partite run support --draft --intent triage --input '{"ticket": "T-12", "priority": "high"}'
  partite run support --label prod --input @request.json --attach invoice.pdf -o json
  partite run support --conversation rcnv_8Xk2 "And for an admin account?"
  partite run support --label prod --intent review --input '{"file": {"$ref": "attachment://notes.txt"}}' --attach notes.txt
  echo "hello" | partite run support --version 3 - --events
  partite run support --label prod --access-point partners --subject alice --claim tier=gold "Refund order 12"`,
		Args: cobra.RangeArgs(1, 2),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return rf.caller.Apply(cmd, app)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			text := ""
			if len(args) == 2 {
				text = args[1]
				if text == "-" {
					data, err := io.ReadAll(app.In)
					if err != nil {
						return err
					}
					text = strings.TrimRight(string(data), "\n")
				}
			}
			if input != "" {
				v, err := app.ReadValue(input)
				if err != nil {
					return err
				}
				input = v
			}
			if text != "" && input != "" {
				return cmdutil.UsageError("pass the input as text or with --input, not both")
			}
			if rf.conversation != "" && (externalID != "" || channel != "" || noTrace || len(tags) > 0) {
				return cmdutil.UsageError("--external-id, --tag, --notification-channel and --no-trace set up a new conversation; they cannot change one with --conversation")
			}
			files := make([]convapi.Attachment, 0, len(attach))
			for _, path := range attach {
				f, err := convapi.ReadAttachment(path)
				if err != nil {
					return err
				}
				files = append(files, f)
			}
			convTags, err := cmdutil.ParseTags(tags)
			if err != nil {
				return err
			}
			reqTags, err := cmdutil.ParseTags(requestTags)
			if err != nil {
				return err
			}

			s, err := openSession(ctx, app, cmd, args[0], &rf)
			if err != nil {
				return err
			}
			newConv := convapi.NewConversation{Tags: convTags, ExternalID: externalID, NotificationChannel: channel}
			if noTrace {
				newConv.Tracing = cmdutil.Ptr(false)
			}
			if channel != "" {
				if err := s.client.CreateChannel(ctx, channel); err != nil {
					return err
				}
			}
			// Text fills a field of the intent's input, so it needs the
			// intents, which only a conversation has. Request tags can only
			// go with a request to a conversation. Other input goes to the
			// server as it is, with the new conversation.
			if s.conversationID == "" && (text != "" || len(reqTags) > 0) {
				if err := s.create(ctx, newConv); err != nil {
					return err
				}
			}
			var send func(context.Context, convapi.Handler) (convapi.Result, error)
			if s.conversationID == "" {
				msg := json.RawMessage(`{}`)
				if input != "" {
					if msg, err = convui.BuildInput(convapi.Intent{}, "", input, ""); err != nil {
						return err
					}
				}
				newConv.Target, newConv.Intent, newConv.Input = s.target, rf.intent, msg
				send = func(ctx context.Context, h convapi.Handler) (convapi.Result, error) {
					res, err := s.client.StartConversation(ctx, newConv, files, h)
					return res, convui.CreateError(err, args[0], newConv)
				}
				if noWait {
					conv, err := s.client.CreateConversation(ctx, newConv, files)
					if err != nil {
						return convui.CreateError(err, args[0], newConv)
					}
					return started(app, events, conv.ID, conv.Request.ID)
				}
			} else {
				intent, err := convui.PickIntent(s.intents, rf.intent, text != "", s.intentsCmd())
				if err != nil {
					return createdHint(err, s, rf.conversation == "")
				}
				msg, err := convui.BuildInput(intent, text, input, s.intentsCmd())
				if err != nil {
					return createdHint(err, s, rf.conversation == "")
				}
				body := convapi.NewRequest{Intent: intent.Name, Input: msg, Tags: reqTags}
				send = func(ctx context.Context, h convapi.Handler) (convapi.Result, error) {
					return s.client.StartRequest(ctx, s.conversationID, body, files, h)
				}
				if noWait {
					req, err := s.client.SendRequest(ctx, s.conversationID, body, files)
					if err != nil {
						return err
					}
					return started(app, events, s.conversationID, req.ID)
				}
			}
			lr := convui.NewLineReader(app.In)
			res, err := convui.Start(ctx, app, s.client, lr, convui.FollowOptions{
				Thinking:          thinking,
				Events:            events,
				Answer:            app.Interactive(),
				Timeout:           timeout,
				CancelOnInterrupt: cancelOnInterrupt,
				OfferCancel:       true,
				ShowIDs:           true,
			}, send)
			if app.Format() == output.Table && !events && res.Status == convapi.StatusComplete {
				style := convui.NewStyle(app.Err)
				fmt.Fprintln(app.Err, style.Dim(fmt.Sprintf("\nContinue with: partite run %s --conversation %s TEXT", args[0], res.ConversationID)))
			}
			return err
		},
	}
	rf.register(cmd)
	f := cmd.Flags()
	f.StringVar(&input, "input", "", "the whole input as a JSON or YAML object, @file or @- for standard input")
	f.StringArrayVar(&attach, "attach", nil, `attach a file; --input can refer to it as {"$ref": "attachment://NAME"}; repeat for more`)
	f.StringVar(&externalID, "external-id", "", "your own unique id for the new conversation, to find it again")
	f.StringArrayVar(&tags, "tag", nil, "tag the new conversation, as name=value; repeat for more")
	f.StringArrayVar(&requestTags, "request-tag", nil, "tag the request, as name=value; repeat for more")
	f.StringVar(&channel, "notification-channel", "", "also send the new conversation's events to this notification channel")
	f.BoolVar(&noTrace, "no-trace", false, "record no trace for the new conversation")
	f.BoolVar(&thinking, "thinking", false, "show the model's thinking, dimmed")
	f.BoolVar(&events, "events", false, "print every event as a line of JSON, then the result")
	f.DurationVar(&timeout, "timeout", 0, "stop following after this long, such as 5m; the request keeps running (exit code 10)")
	f.BoolVar(&noWait, "no-wait", false, "start the request and print its ids without following it")
	f.BoolVar(&cancelOnInterrupt, "cancel-on-interrupt", false, "on Ctrl-C, cancel the request on the server without asking")
	return cmd
}

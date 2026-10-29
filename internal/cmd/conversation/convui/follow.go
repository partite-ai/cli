package convui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convapi"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	"github.com/partite-ai/partite-cli/internal/output"
)

// ExitCodesHelp documents the exit codes of the commands that report a
// request's result.
const ExitCodesHelp = `Exit codes:
  0   the request finished with a response
  7   the request failed; the error is in the result
  8   the request was cancelled
  9   the request waits for a task to be completed (see the result's task)
  10  the request is still running when --timeout passed
  130 interrupted with Ctrl-C
Other codes are the usual ones; see partite --help.`

// ResultHelp documents the result that -o json prints.
const ResultHelp = `With -o json the result is one JSON object:
  {"conversation_id": "rcnv_...", "request_id": "rreq_...",
   "status": "complete" | "failed" | "cancelled" | "task_pending" | "running",
   "response": {...},  // when complete
   "error": "...",     // when failed or cancelled
   "task": {"task_id": "rout_...", "kind": "client_task", "response_schema": {...}, "client_task": {"name": "...", "input": {...}}}}`

// FollowOptions say how to show a request while following it.
type FollowOptions struct {
	// Thinking shows the model's thinking, dimmed.
	Thinking bool
	// Events prints every event as a line of JSON, and the result last.
	Events bool
	// Quiet prints no events, only the result.
	Quiet bool
	// Answer asks at the terminal for the answers to tasks. Without it,
	// following stops at an open task.
	Answer bool
	// Timeout stops following after this long. The request keeps running.
	Timeout time.Duration
	// CancelOnInterrupt cancels the request on the server on Ctrl-C without
	// asking.
	CancelOnInterrupt bool
	// OfferCancel asks on Ctrl-C, at a terminal, whether to cancel the
	// request on the server. Commands that only watch a request leave it
	// off.
	OfferCancel bool
	// AnsweredTask is a task that was opened before the events followed, so
	// that its answer can be shown with its form.
	AnsweredTask *convapi.Task
	// ShowIDs prints the conversation and request ids, dimmed, once the
	// server names them.
	ShowIDs bool
}

type handler struct {
	conversationID, requestID string
	started                   func(conversationID, requestID string) error
	event                     func(convapi.Event) error
	task                      func(context.Context, convapi.Task) (bool, error)
}

func (h *handler) Started(conversationID, requestID string) error {
	h.conversationID, h.requestID = conversationID, requestID
	return h.started(conversationID, requestID)
}

func (h *handler) Event(ev convapi.Event) error { return h.event(ev) }

func (h *handler) Task(ctx context.Context, t convapi.Task) (bool, error) { return h.task(ctx, t) }

// Follow follows a request, shows it the way opts and the output format
// say, and prints the result. The error is nil only when the request ended
// with a response; otherwise it carries the exit code for the status.
func Follow(ctx context.Context, app *cmdutil.App, c *convapi.Client, lr *LineReader, conversationID, requestID, after string, opts FollowOptions) (convapi.Result, error) {
	return Start(ctx, app, c, lr, opts, func(ctx context.Context, h convapi.Handler) (convapi.Result, error) {
		if err := h.Started(conversationID, requestID); err != nil {
			return convapi.Result{}, err
		}
		return c.Follow(ctx, conversationID, requestID, after, h)
	})
}

// Start is Follow for a request that start sends: start passes h to the
// client's StartConversation or StartRequest, which name the request to h.
func Start(ctx context.Context, app *cmdutil.App, c *convapi.Client, lr *LineReader, opts FollowOptions, start func(context.Context, convapi.Handler) (convapi.Result, error)) (convapi.Result, error) {
	style := NewStyle(app.Out)
	human := app.Format() == output.Table && !opts.Events
	var spin *Spinner
	if human {
		spin = StartSpinner(app.Err, "working")
	}
	rendered := false
	h := &handler{
		started: func(string, string) error { return nil },
		event:   func(convapi.Event) error { return nil },
		task:    func(context.Context, convapi.Task) (bool, error) { return false, nil },
	}
	if human && opts.ShowIDs {
		h.started = func(conversationID, requestID string) error {
			spin.Clear()
			fmt.Fprintln(app.Err, NewStyle(app.Err).Dim(fmt.Sprintf("conversation %s · request %s", conversationID, requestID)))
			return nil
		}
	}
	switch {
	case opts.Events:
		h.event = EventLines{Out: app.Out}.Event
	case human && !opts.Quiet:
		r := &Renderer{Out: app.Out, Style: style, Thinking: opts.Thinking, Spinner: spin, DrawForms: output.IsTerminal(app.Out)}
		if t := opts.AnsweredTask; t != nil && t.TaskID != "" {
			r.tasks = map[string]convapi.Task{t.TaskID: *t}
		}
		h.event = r.Event
		rendered = true
	}
	if opts.Answer {
		h.task = func(ctx context.Context, t convapi.Task) (bool, error) {
			spin.Pause()
			defer spin.Resume()
			if !rendered {
				fmt.Fprintln(app.Err, NewStyle(app.Err).Question(TaskQuestion(t)))
			}
			return true, AnswerTask(ctx, app, c, lr, h.conversationID, h.requestID, t)
		}
	}

	runCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	res, err := start(runCtx, h)
	spin.Stop()
	switch {
	case err != nil && ctx.Err() != nil:
		if opts.Events {
			res.Type = "result"
		}
		return res, interrupted(app, c, lr, res, opts)
	case err != nil && runCtx.Err() != nil && res.RequestID != "":
		res.Status = convapi.StatusRunning
	case err != nil:
		return res, err
	}
	if opts.Events {
		res.Type = "result"
	}
	return res, Report(app, res, rendered)
}

// AnswerTask asks for a task's answer at the terminal and completes the task.
// When the server rejects the answer, it says why and asks again.
func AnswerTask(ctx context.Context, app *cmdutil.App, c *convapi.Client, lr *LineReader, conversationID, requestID string, t convapi.Task) error {
	style := NewStyle(app.Err)
	for {
		answer, err := PromptTask(ctx, lr, app.Err, style, t)
		if err != nil {
			return err
		}
		err = c.CompleteTask(ctx, conversationID, requestID, t.TaskID, answer)
		var se *cmdutil.StatusError
		switch {
		case errors.As(err, &se) && se.StatusCode == http.StatusBadRequest:
			fmt.Fprintln(app.Err, style.Red("The answer was not accepted: "+app.Classify(err).Msg))
			continue
		case errors.As(err, &se) && se.StatusCode == http.StatusConflict:
			// Someone else answered it first, or the request ended.
			fmt.Fprintln(app.Err, style.Dim("The task is not open any more."))
			return nil
		}
		return err
	}
}

// Report prints a result and returns the error that gives its exit code.
// rendered is true when the events were shown already, response included.
func Report(app *cmdutil.App, res convapi.Result, rendered bool) error {
	if res.Type != "" {
		data, err := json.Marshal(res)
		if err != nil {
			return err
		}
		fmt.Fprintf(app.Out, "%s\n", data)
		return ResultError(res)
	}
	switch app.Format() {
	case output.Table:
		switch {
		case res.Status == convapi.StatusComplete && !rendered:
			fmt.Fprintln(app.Out, ResponseText(res.Response))
		case res.Status == convapi.StatusTask:
			DescribeTask(app.Out, NewStyle(app.Out), res.ConversationID, res.RequestID, *res.Task, !rendered)
		}
	case output.ID:
		fmt.Fprintln(app.Out, res.ConversationID)
	default:
		if err := app.Printer().Data(res); err != nil {
			return err
		}
	}
	return ResultError(res)
}

// ResultError is the error for a request that did not end with a response.
func ResultError(res convapi.Result) error {
	c, r := res.ConversationID, res.RequestID
	switch res.Status {
	case convapi.StatusFailed:
		return &cmdutil.Error{Code: cmdutil.ExitRequestFailed, Msg: "the request failed: " + res.Error, Hint: "See what happened with: partite trace get " + c}
	case convapi.StatusCancelled:
		return &cmdutil.Error{Code: cmdutil.ExitRequestCancelled, Msg: "the request was cancelled"}
	case convapi.StatusTask:
		return &cmdutil.Error{
			Code: cmdutil.ExitTaskPending,
			Msg:  fmt.Sprintf("the request waits for task %s to be completed", res.Task.TaskID),
			Hint: fmt.Sprintf("Answer it with: partite conversation task complete %s %s --data JSON --follow\nOr decline it with: partite conversation task cancel %s %s --follow", c, r, c, r),
		}
	case convapi.StatusRunning:
		return &cmdutil.Error{
			Code: cmdutil.ExitStillRunning,
			Msg:  "the request is still running",
			Hint: fmt.Sprintf("Wait for it with: partite conversation response %s %s --wait", c, r),
		}
	}
	return nil
}

// interrupted handles Ctrl-C while following: it cancels the request on the
// server when asked to, says how to pick the request up again otherwise, and
// prints the result so far for scripts.
func interrupted(app *cmdutil.App, c *convapi.Client, lr *LineReader, res convapi.Result, opts FollowOptions) error {
	fmt.Fprintln(app.Err)
	if res.RequestID == "" {
		// Stopped before the server named the request.
		return context.Canceled
	}
	cancel := opts.CancelOnInterrupt
	if !cancel && opts.OfferCancel && app.Interactive() && lr != nil {
		// The command's context is done, so a second Ctrl-C needs its own.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		fmt.Fprint(app.Err, "Cancel the request on the server too? [y/N] ")
		line, err := lr.ReadLine(ctx)
		stop()
		if err != nil {
			fmt.Fprintln(app.Err)
		}
		cancel = strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes")
	}
	if cancel {
		ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		r, err := c.Cancel(ctx, res.ConversationID, res.RequestID)
		switch {
		case err != nil:
			fmt.Fprintf(app.Err, "Could not cancel request %s: %s\n", res.RequestID, app.Classify(err).Msg)
		default:
			if r.Status == convapi.RequestCancelled {
				fmt.Fprintf(app.Err, "Cancelled request %s.\n", res.RequestID)
			} else {
				fmt.Fprintf(app.Err, "Request %s had ended already.\n", res.RequestID)
			}
			typ := res.Type
			res = convapi.ResultOf(res.ConversationID, r)
			res.Type = typ
		}
	} else {
		fmt.Fprintf(app.Err, "The request is still running. Follow it with:\n  partite conversation events %s %s --follow\n", res.ConversationID, res.RequestID)
	}
	switch {
	case res.Type != "":
		if data, err := json.Marshal(res); err == nil {
			fmt.Fprintf(app.Out, "%s\n", data)
		}
	case app.Format() == output.JSON || app.Format() == output.YAML:
		_ = app.Printer().Data(res)
	}
	return context.Canceled
}

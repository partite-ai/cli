package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convapi"
	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convui"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
	"github.com/partite-ai/partite-cli/internal/output"
	"github.com/spf13/cobra"
)

func newTask(app *cmdutil.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Show and answer the tasks a mesh asks people to do",
		Long: `When an agent needs something from a person, such as an answer to a
question or a choice, its request stops at a task until the task is completed.
partite run asks at the terminal; without one, it exits with code 9 and the
task is answered with partite conversation task complete, or declined with
partite conversation task cancel.`,
	}
	cmd.AddCommand(newTaskGet(app), newTaskComplete(app), newTaskCancel(app))
	return cmd
}

// openTask returns the request's open task, the one with id taskID when it
// is set. With wait, it waits while the request runs. When there is none it
// says why.
func openTask(ctx context.Context, c *convapi.Client, conv, req, taskID string, wait bool) (convapi.Task, error) {
	var r convapi.Request
	var err error
	if wait {
		r, err = c.WaitRequest(ctx, conv, req)
	} else {
		r, err = c.GetRequest(ctx, conv, req, 0)
	}
	if err != nil {
		return convapi.Task{}, err
	}
	tasks := r.OpenTasks()
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		if taskID == "" || t.TaskID == taskID {
			return t, nil
		}
		ids[i] = t.TaskID
	}
	if len(tasks) > 0 {
		return convapi.Task{}, cmdutil.Errorf(cmdutil.ExitConflict, "", "task %s is not open; the open tasks are %s", taskID, strings.Join(ids, ", "))
	}
	res := convapi.ResultOf(conv, r)
	hint := ""
	if res.Status == convapi.StatusRunning {
		hint = "Wait for one with --wait."
	}
	return convapi.Task{}, &cmdutil.Error{
		Code: cmdutil.ExitNotFound,
		Msg:  fmt.Sprintf("request %s has no open task. %s", req, describeStatus(res)),
		Hint: hint,
	}
}

type taskView struct {
	ConversationID string `json:"conversation_id"`
	RequestID      string `json:"request_id"`
	convapi.Task
}

func newTaskGet(app *cmdutil.App) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "get <conversation> [request]",
		Short: "Show the task a request waits on",
		Long: `Show the open task of a request: what it asks, and the fields of the answer.
With -o json the task has its kind, its response_schema, the JSON Schema of
the answer that task complete takes, and its details under its kind: a
client_task has its name and input, and a tool_call_approval has the tool and
the arguments of the call it asks about.`,
		Example: `  partite conversation task get rcnv_8Xk2
  partite conversation task get rcnv_8Xk2 rreq_3Fd9 --wait -o json`,
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
			t, err := openTask(ctx, c, conv, req, "", wait)
			if err != nil {
				return err
			}
			if app.Format() == output.Table {
				convui.DescribeTask(app.Out, convui.NewStyle(app.Out), conv, req, t, true)
				return nil
			}
			return app.Printer().Object(taskView{conv, req, t}, t.TaskID, nil)
		},
	}
	cmd.Flags().BoolVarP(&wait, "wait", "w", false, "wait for the request to ask for a task")
	return cmd
}

func newTaskComplete(app *cmdutil.App) *cobra.Command {
	var taskID, data, answer string
	var follow, thinking bool
	cmd := &cobra.Command{
		Use:   "complete <conversation> [request]",
		Short: "Answer a task, so the request goes on",
		Long: `Complete the open task of a request with an answer. The request then goes on.

Give the answer as JSON with --data, matching the task's response_schema (see
partite conversation task get). For a task whose answer has one field, such
as ask_question's "answer" or pick_one's "selected", --answer takes just the
value; a list takes values separated by commas, and a number picks one of
the task's options. At a terminal with neither, you are asked for each field.

With --follow, keep following the request after the answer, as partite run
does, with the same output and exit codes.

` + convui.ExitCodesHelp,
		Example: `  partite conversation task complete rcnv_8Xk2 --answer "Blue"
  partite conversation task complete rcnv_8Xk2 rreq_3Fd9 --data '{"selected": ["a", "c"]}' --follow -o json
  partite conversation task complete rcnv_8Xk2 --task rout_77Qa --data @answer.json`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if data != "" && answer != "" {
				return cmdutil.UsageError("pass --data or --answer, not both")
			}
			c, err := client(app)
			if err != nil {
				return err
			}
			conv, req, err := target(ctx, c, args)
			if err != nil {
				return err
			}
			var answerData json.RawMessage
			if data != "" {
				v, err := app.ReadValue(data)
				if err != nil {
					return err
				}
				var obj map[string]any
				if err := json.Unmarshal([]byte(v), &obj); err != nil {
					if err := cmdutil.DecodeSpec("--data", []byte(v), &obj); err != nil {
						return cmdutil.UsageError("--data must be a JSON or YAML object: %s", err)
					}
				}
				if answerData, err = json.Marshal(obj); err != nil {
					return err
				}
			}
			// The open task is needed to check --task, to build --answer and
			// to ask for the answer. Only --task with --data can do without.
			var task convapi.Task
			if taskID == "" || answerData == nil {
				if task, err = openTask(ctx, c, conv, req, taskID, false); err != nil {
					return err
				}
				taskID = task.TaskID
			}
			lr := convui.NewLineReader(app.In)
			switch {
			case answer != "":
				if answerData, err = convui.AnswerFromText(task, answer); err != nil {
					return err
				}
			case answerData == nil && app.Interactive():
				style := convui.NewStyle(app.Err)
				fmt.Fprintln(app.Err, style.Question(convui.TaskQuestion(task)))
				if err := convui.AnswerTask(ctx, app, c, lr, conv, req, task); err != nil {
					return err
				}
			case answerData == nil:
				return &cmdutil.Error{Code: cmdutil.ExitUsage, Msg: "pass the answer with --data or --answer", Hint: "See the fields of the answer with: partite conversation task get " + conv + " " + req}
			}
			if answerData != nil {
				if err := c.CompleteTask(ctx, conv, req, taskID, answerData); err != nil {
					return err
				}
			}
			if follow {
				_, err := convui.Follow(ctx, app, c, lr, conv, req, taskID, convui.FollowOptions{
					Thinking:     thinking,
					Events:       app.Format() != output.Table,
					Answer:       app.Interactive(),
					OfferCancel:  true,
					AnsweredTask: &task,
				})
				return err
			}
			app.Printer().Done("Completed task %s. Follow the request with: partite conversation events %s %s --follow", taskID, conv, req)
			if app.Format() == output.Table {
				return nil
			}
			return app.Printer().Object(map[string]any{"conversation_id": conv, "request_id": req, "task_id": taskID, "completed": true}, taskID, nil)
		},
	}
	f := cmd.Flags()
	f.StringVar(&taskID, "task", "", "the task to complete (default: the request's open task)")
	f.StringVar(&data, "data", "", "the answer as a JSON or YAML object, @file or @- for standard input")
	f.StringVar(&answer, "answer", "", "the answer's value, for a task whose answer has one field")
	f.BoolVarP(&follow, "follow", "f", false, "keep following the request after the answer")
	f.BoolVar(&thinking, "thinking", false, "with --follow, show the model's thinking, dimmed")
	return cmd
}

func newTaskCancel(app *cmdutil.App) *cobra.Command {
	var taskID string
	var follow, thinking bool
	cmd := &cobra.Command{
		Use:   "cancel <conversation> [request]",
		Short: "Decline a task, so the request goes on without an answer",
		Long: `Cancel the open task of a request: decline to do it. The agent that asked
is told the task failed, and the request goes on.

With --follow, keep following the request after, as partite run does, with
the same output and exit codes.

` + convui.ExitCodesHelp,
		Example: `  partite conversation task cancel rcnv_8Xk2
  partite conversation task cancel rcnv_8Xk2 rreq_3Fd9 --task rout_77Qa --follow`,
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
			task, err := openTask(ctx, c, conv, req, taskID, false)
			if err != nil {
				return err
			}
			if err := c.CancelTask(ctx, conv, req, task.TaskID); err != nil {
				return err
			}
			if follow {
				_, err := convui.Follow(ctx, app, c, convui.NewLineReader(app.In), conv, req, task.TaskID, convui.FollowOptions{
					Thinking:    thinking,
					Events:      app.Format() != output.Table,
					Answer:      app.Interactive(),
					OfferCancel: true,
				})
				return err
			}
			app.Printer().Done("Cancelled task %s. Follow the request with: partite conversation events %s %s --follow", task.TaskID, conv, req)
			if app.Format() == output.Table {
				return nil
			}
			return app.Printer().Object(map[string]any{"conversation_id": conv, "request_id": req, "task_id": task.TaskID, "cancelled": true}, task.TaskID, nil)
		},
	}
	f := cmd.Flags()
	f.StringVar(&taskID, "task", "", "the task to cancel (default: the request's open task)")
	f.BoolVarP(&follow, "follow", "f", false, "keep following the request after")
	f.BoolVar(&thinking, "thinking", false, "with --follow, show the model's thinking, dimmed")
	return cmd
}

// Package convui shows conversations to people and to scripts: it renders
// event streams, asks people to complete tasks, prints results and turns them
// into exit codes. run, chat and conversation share it.
package convui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convapi"
	"github.com/partite-ai/partite-cli/internal/output"
)

// Style adds terminal colors when they are on.
type Style struct {
	Color bool
}

// NewStyle turns colors on when w is a terminal, unless NO_COLOR is set.
func NewStyle(w io.Writer) Style {
	return Style{Color: output.IsTerminal(w) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"}
}

func (s Style) wrap(code, text string) string {
	if !s.Color || text == "" {
		return text
	}
	return "\033[" + code + "m" + text + "\033[0m"
}

func (s Style) Dim(text string) string   { return s.wrap("2", text) }
func (s Style) Bold(text string) string  { return s.wrap("1", text) }
func (s Style) Cyan(text string) string  { return s.wrap("36", text) }
func (s Style) Red(text string) string   { return s.wrap("31", text) }
func (s Style) Green(text string) string { return s.wrap("32", text) }

// Question styles a task's question.
func (s Style) Question(text string) string { return s.Cyan("? ") + s.Bold(text) }

// Renderer prints a request's events for people: messages as text, thinking
// dimmed, tasks as questions, and the response.
type Renderer struct {
	Out      io.Writer
	Style    Style
	Thinking bool
	Spinner  *Spinner
	// DrawForms draws the completed form of a task that has one in place of
	// its answer. It is for terminals only.
	DrawForms bool

	wrote bool
	// tasks are the tasks seen so far, by event ID, so that an answer can be
	// shown with its task's form.
	tasks map[string]convapi.Task
}

func (r *Renderer) Event(ev convapi.Event) error {
	r.Spinner.Clear()
	switch ev.Type {
	case "message":
		r.block(convapi.EventText(ev.Data))
	case "thinking":
		if r.Thinking {
			r.block(r.Style.Dim(convapi.EventText(ev.Data)))
		}
	case "task":
		t, err := convapi.TaskFromEvent(ev)
		if err != nil {
			return err
		}
		if r.tasks == nil {
			r.tasks = map[string]convapi.Task{}
		}
		r.tasks[ev.ID] = t
		r.block(r.Style.Question(TaskQuestion(t)))
	case "task_response":
		if _, completed, ok := taskForm(r.tasks[ev.InReplyTo]); r.DrawForms && ok && completed != nil {
			var buf bytes.Buffer
			drawForm(&buf, r.Style, completed, ev.Data, "  ")
			fmt.Fprint(r.Out, buf.String())
			return nil
		}
		fmt.Fprintln(r.Out, r.Style.Dim("  ↳ "+AnswerText(ev.Data)))
	case "response":
		r.block(ResponseText(ev.Data))
	}
	return nil
}

// block prints text with a blank line before it, unless it is the first.
func (r *Renderer) block(text string) {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}
	if r.wrote {
		fmt.Fprintln(r.Out)
	}
	fmt.Fprintln(r.Out, text)
	r.wrote = true
}

// ResponseText is how a response reads as text: the value itself when it is
// a string or an object with one string property, and indented JSON
// otherwise.
func ResponseText(data json.RawMessage) string {
	var s string
	if json.Unmarshal(data, &s) == nil {
		return s
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) == nil && len(m) == 1 {
		for _, v := range m {
			if json.Unmarshal(v, &s) == nil {
				return s
			}
		}
	}
	var buf bytes.Buffer
	if json.Indent(&buf, data, "", "  ") != nil {
		return string(data)
	}
	return buf.String()
}

// AnswerText is a short form of a task's answer: the value of its only
// property, or compact JSON.
func AnswerText(data json.RawMessage) string {
	var s string
	if json.Unmarshal(data, &s) == nil {
		return s
	}
	var m map[string]any
	if json.Unmarshal(data, &m) == nil && len(m) == 1 {
		for _, v := range m {
			switch v := v.(type) {
			case string:
				return v
			case []any:
				parts := make([]string, len(v))
				for i, e := range v {
					parts[i] = fmt.Sprint(e)
				}
				return strings.Join(parts, ", ")
			}
		}
	}
	var buf bytes.Buffer
	if json.Compact(&buf, data) != nil {
		return string(data)
	}
	return buf.String()
}

// ToolLabel names a tool and, when it has one, the MCP connection, particle
// or memory profile it comes from.
func ToolLabel(t convapi.ApprovalTool) string {
	if t.Source.Name == "" {
		return t.Name
	}
	return fmt.Sprintf("%s (from %s)", t.Name, t.Source.Name)
}

// TaskQuestion is the question a task asks. A tool call approval asks whether
// the call may be made. A client task asks its input's question, or names
// itself when it has none.
func TaskQuestion(t convapi.Task) string {
	if a := t.ToolCallApproval; a != nil {
		question := fmt.Sprintf("May the agent call %s with %s?", ToolLabel(a.Tool), string(a.Arguments))
		if a.Message != "" {
			return a.Message + " " + question
		}
		return question
	}
	var in struct {
		Question string `json:"question"`
	}
	_ = json.Unmarshal(t.Input(), &in)
	if in.Question != "" {
		return in.Question
	}
	return fmt.Sprintf("The mesh asks you to complete a %s task.", t.Name())
}

// EventLines writes each event as one line of JSON.
type EventLines struct {
	Out io.Writer
}

func (e EventLines) Event(ev convapi.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.Out, "%s\n", data)
	return err
}

// Spinner shows that a request is still working, on one line that it
// redraws. Its methods do nothing on a nil Spinner, so callers that print to
// something other than a terminal pass nil.
type Spinner struct {
	mu     sync.Mutex
	w      io.Writer
	label  string
	start  time.Time
	shown  bool
	paused bool
	stop   chan struct{}
	done   chan struct{}
}

// StartSpinner starts a spinner on w when w is a terminal, and returns nil
// otherwise.
func StartSpinner(w io.Writer, label string) *Spinner {
	if !output.IsTerminal(w) {
		return nil
	}
	s := &Spinner{w: w, label: label, start: time.Now(), stop: make(chan struct{}), done: make(chan struct{})}
	go s.loop()
	return s
}

func (s *Spinner) loop() {
	defer close(s.done)
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for i := 0; ; i++ {
		select {
		case <-s.stop:
			return
		case <-tick.C:
		}
		s.mu.Lock()
		// Wait a moment before showing, so quick replies do not flash it.
		if !s.paused && time.Since(s.start) > 500*time.Millisecond {
			secs := int(time.Since(s.start).Seconds())
			fmt.Fprintf(s.w, "\r\033[2K\033[2m%s %s %ds\033[0m", frames[i%len(frames)], s.label, secs)
			s.shown = true
		}
		s.mu.Unlock()
	}
}

// Clear erases the spinner so that other output can be printed. It comes
// back on the next tick.
func (s *Spinner) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clear()
}

func (s *Spinner) clear() {
	if s.shown {
		fmt.Fprint(s.w, "\r\033[2K")
		s.shown = false
	}
}

// Pause hides the spinner until Resume, such as while asking a question.
func (s *Spinner) Pause() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clear()
	s.paused = true
}

func (s *Spinner) Resume() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = false
}

func (s *Spinner) Stop() {
	if s == nil {
		return
	}
	close(s.stop)
	<-s.done
	s.Clear()
}

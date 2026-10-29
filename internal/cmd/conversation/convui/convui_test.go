package convui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convapi"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
)

func events(t *testing.T, lines ...string) []convapi.Event {
	t.Helper()
	out := make([]convapi.Event, len(lines))
	for i, l := range lines {
		if err := json.Unmarshal([]byte(l), &out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

const pickOne = `{"id":"rout_t","type":"task","data":{"kind":"client_task","response_schema":{"type":"object","properties":{"selected":{"type":"string","description":"The option the user selected"}},"required":["selected"]},"client_task":{"name":"pick_one","input":{"question":"Which color?","options":["red","green","blue"]}}}}`

// A stream as the server sent it, from a request that asked a question.
func recorded(t *testing.T) []convapi.Event {
	return events(t,
		`{"id":"rout_1","type":"thinking","data":"Ask with pick_one."}`,
		`{"id":"rout_2","type":"message","data":"I need to know your favorite color."}`,
		pickOne,
		`{"id":"rout_3","type":"task_response","data":{"selected":"green"},"in_reply_to":"rout_t"}`,
		`{"id":"rout_4","type":"message","data":"Thanks, wrapping up."}`,
		`{"id":"rout_5","type":"response","data":{"answer":"You picked green."}}`,
	)
}

func TestRenderer(t *testing.T) {
	tests := []struct {
		thinking bool
		want     string
	}{
		{false, `I need to know your favorite color.

? Which color?
  ↳ green

Thanks, wrapping up.

You picked green.
`},
		{true, `Ask with pick_one.

I need to know your favorite color.

? Which color?
  ↳ green

Thanks, wrapping up.

You picked green.
`},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		r := &Renderer{Out: &buf, Thinking: tt.thinking}
		for _, ev := range recorded(t) {
			if err := r.Event(ev); err != nil {
				t.Fatal(err)
			}
		}
		if buf.String() != tt.want {
			t.Errorf("thinking=%v rendered:\n%s\nwant:\n%s", tt.thinking, buf.String(), tt.want)
		}
	}
}

func TestRendererColors(t *testing.T) {
	var buf bytes.Buffer
	r := &Renderer{Out: &buf, Style: Style{Color: true}, Thinking: true}
	for _, ev := range recorded(t)[:1] {
		if err := r.Event(ev); err != nil {
			t.Fatal(err)
		}
	}
	if got := buf.String(); got != "\033[2mAsk with pick_one.\033[0m\n" {
		t.Errorf("thinking rendered as %q, want it dimmed", got)
	}
}

func TestEventLines(t *testing.T) {
	var buf bytes.Buffer
	for _, ev := range recorded(t)[:2] {
		if err := (EventLines{Out: &buf}).Event(ev); err != nil {
			t.Fatal(err)
		}
	}
	want := `{"id":"rout_1","type":"thinking","data":"Ask with pick_one."}
{"id":"rout_2","type":"message","data":"I need to know your favorite color."}
`
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestResponseText(t *testing.T) {
	tests := map[string]string{
		`"plain"`:              "plain",
		`{"answer":"one"}`:     "one",
		`{"a":1}`:              "{\n  \"a\": 1\n}",
		`{"a":"x","b":"y"}`:    "{\n  \"a\": \"x\",\n  \"b\": \"y\"\n}",
		`{"answer":["x","y"]}`: "{\n  \"answer\": [\n    \"x\",\n    \"y\"\n  ]\n}",
	}
	for in, want := range tests {
		if got := ResponseText(json.RawMessage(in)); got != want {
			t.Errorf("ResponseText(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestTextProperty(t *testing.T) {
	tests := []struct {
		schema string
		want   string
	}{
		{`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`, "text"},
		{`{"type":"object","properties":{"message":{"type":"string"}}}`, "message"},
		{`{"type":"object","properties":{"text":{"type":"string"},"lang":{"type":"string"}},"required":["text"]}`, "text"},
		{`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a","b"]}`, ""},
		{`{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}`, ""},
		{`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}}`, ""},
	}
	for _, tt := range tests {
		got, _ := TextProperty(json.RawMessage(tt.schema))
		if got != tt.want {
			t.Errorf("TextProperty(%s) = %q, want %q", tt.schema, got, tt.want)
		}
	}
}

var (
	chat   = convapi.Intent{Name: "chat", Schema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)}
	triage = convapi.Intent{Name: "triage", Schema: json.RawMessage(`{"type":"object","properties":{"ticket":{"type":"string","description":"Ticket id."},"priority":{"type":"integer"}},"required":["ticket","priority"]}`)}
)

func TestPickIntent(t *testing.T) {
	both := []convapi.Intent{chat, triage}
	tests := []struct {
		intents []convapi.Intent
		name    string
		text    bool
		want    string
		code    int
	}{
		{[]convapi.Intent{triage}, "", false, "triage", 0},
		{both, "triage", true, "triage", 0},
		{both, "", true, "chat", 0},
		{both, "", false, "", cmdutil.ExitUsage},
		{both, "nope", false, "", cmdutil.ExitNotFound},
		{nil, "", true, "", cmdutil.ExitUsage},
	}
	for _, tt := range tests {
		got, err := PickIntent(tt.intents, tt.name, tt.text, "partite conversation intents m")
		var e *cmdutil.Error
		switch {
		case tt.code != 0 && (!errors.As(err, &e) || e.Code != tt.code):
			t.Errorf("PickIntent(%q, text=%v) err = %v, want code %d", tt.name, tt.text, err, tt.code)
		case tt.code == 0 && (err != nil || got.Name != tt.want):
			t.Errorf("PickIntent(%q, text=%v) = %q, %v; want %q", tt.name, tt.text, got.Name, err, tt.want)
		}
	}
}

func TestBuildInput(t *testing.T) {
	tests := []struct {
		intent convapi.Intent
		text   string
		input  string
		want   string
		errHas string
	}{
		{chat, "hello", "", `{"text":"hello"}`, ""},
		{chat, "", `{"text":"hi","extra":1}`, `{"extra":1,"text":"hi"}`, ""},
		{chat, "", "text: from yaml", `{"text":"from yaml"}`, ""},
		{triage, "", `{"ticket":"T-1","priority":2}`, `{"priority":2,"ticket":"T-1"}`, ""},
		{triage, "hello", "", "", "takes structured input"},
		{triage, "", `{"ticket":"T-1"}`, "", "needs priority"},
		{chat, "", "", "", "needs text; pass the text as an argument"},
		{chat, "x", `{"text":"y"}`, "", "not both"},
		{chat, "", `[1,2]`, "", "must be a JSON or YAML object"},
	}
	for _, tt := range tests {
		got, err := BuildInput(tt.intent, tt.text, tt.input, "partite conversation intents m")
		if tt.errHas != "" {
			if err == nil || !strings.Contains(err.Error(), tt.errHas) {
				t.Errorf("BuildInput(%s, %q, %q) err = %v, want one with %q", tt.intent.Name, tt.text, tt.input, err, tt.errHas)
			}
			continue
		}
		if err != nil || string(got) != tt.want {
			t.Errorf("BuildInput(%s, %q, %q) = %s, %v; want %s", tt.intent.Name, tt.text, tt.input, got, err, tt.want)
		}
	}
}

func task(t *testing.T, ev string) convapi.Task {
	tk, err := convapi.TaskFromEvent(events(t, ev)[0])
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

const multiSelect = `{"id":"rout_m","type":"task","data":{"kind":"client_task","response_schema":{"type":"object","properties":{"selected":{"type":"array","items":{"type":"string"}}},"required":["selected"]},"client_task":{"name":"multi_select","input":{"question":"Fruits?","options":["apple","pear","plum"]}}}}`

const form = `{"id":"rout_f","type":"task","data":{"kind":"client_task","response_schema":{"type":"object","properties":{"city":{"type":"string","description":"Where to."},"express":{"type":"boolean"},"count":{"type":"integer"},"size":{"type":"string","enum":["S","M","L"]},"note":{"type":"string"}},"required":["city","express","count","size"]},"client_task":{"name":"shipping","input":{}}}}`

func TestAnswerFromText(t *testing.T) {
	tests := []struct {
		task   string
		text   string
		want   string
		errHas string
	}{
		{pickOne, "2", `{"selected":"green"}`, ""},
		{pickOne, "purple", `{"selected":"purple"}`, ""},
		{pickOne, "7", "", "not a choice"},
		{multiSelect, "1, plum", `{"selected":["apple","plum"]}`, ""},
		{form, "x", "", "more than one field"},
	}
	for _, tt := range tests {
		got, err := AnswerFromText(task(t, tt.task), tt.text)
		if tt.errHas != "" {
			if err == nil || !strings.Contains(err.Error(), tt.errHas) {
				t.Errorf("AnswerFromText(%q) err = %v, want one with %q", tt.text, err, tt.errHas)
			}
			continue
		}
		if err != nil || string(got) != tt.want {
			t.Errorf("AnswerFromText(%q) = %s, %v; want %s", tt.text, got, err, tt.want)
		}
	}
}

func TestPromptTask(t *testing.T) {
	// Bad answers are asked again; the optional note is skipped.
	in := strings.Join([]string{"", "Paris", "maybe", "y", "two", "3", "XL", "2", ""}, "\n") + "\n"
	var out bytes.Buffer
	got, err := PromptTask(context.Background(), NewLineReader(strings.NewReader(in)), &out, Style{}, task(t, form))
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if want := `{"city":"Paris","count":3,"express":true,"size":"M"}`; string(got) != want {
		t.Errorf("answer = %s, want %s", got, want)
	}
	for _, msg := range []string{"An answer is required.", "answer yes or no", `"two" is not a whole number`, "XL is not one of the choices", "city Where to.", "  2) M"} {
		if !strings.Contains(out.String(), msg) {
			t.Errorf("prompt output has no %q:\n%s", msg, out.String())
		}
	}
}

func TestPromptTaskInputEnds(t *testing.T) {
	_, err := PromptTask(context.Background(), NewLineReader(strings.NewReader("")), &bytes.Buffer{}, Style{}, task(t, pickOne))
	if err == nil || !strings.Contains(err.Error(), "input ended") {
		t.Errorf("err = %v, want the input to have ended", err)
	}
}

func TestPromptTaskStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A reader that never returns a line: only the context can end the read.
	r, _ := io.Pipe()
	_, err := PromptTask(ctx, NewLineReader(r), &bytes.Buffer{}, Style{}, task(t, pickOne))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestDescribeTask(t *testing.T) {
	var buf bytes.Buffer
	DescribeTask(&buf, Style{}, "rcnv_1", "rreq_1", task(t, pickOne), true)
	want := `? Which color?

Task rout_t, of type pick_one, with this input:
  {
    "question": "Which color?",
    "options": [
      "red",
      "green",
      "blue"
    ]
  }

Its answer must match this JSON schema:
  {
    "type": "object",
    "properties": {
      "selected": {
        "type": "string",
        "description": "The option the user selected"
      }
    },
    "required": [
      "selected"
    ]
  }

Answer it with:
  partite conversation task complete rcnv_1 rreq_1 --answer red
  partite conversation task complete rcnv_1 rreq_1 --data '{"selected": "..."}'
Or decline it with:
  partite conversation task cancel rcnv_1 rreq_1
`
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestResultError(t *testing.T) {
	tk := task(t, pickOne)
	tests := []struct {
		res  convapi.Result
		code int
	}{
		{convapi.Result{Status: convapi.StatusComplete}, 0},
		{convapi.Result{Status: convapi.StatusFailed, Error: "boom"}, cmdutil.ExitRequestFailed},
		{convapi.Result{Status: convapi.StatusCancelled}, cmdutil.ExitRequestCancelled},
		{convapi.Result{Status: convapi.StatusTask, Task: &tk}, cmdutil.ExitTaskPending},
		{convapi.Result{Status: convapi.StatusRunning}, cmdutil.ExitStillRunning},
	}
	for _, tt := range tests {
		err := ResultError(tt.res)
		var e *cmdutil.Error
		switch {
		case tt.code == 0 && err != nil:
			t.Errorf("%s: err = %v, want none", tt.res.Status, err)
		case tt.code != 0 && (!errors.As(err, &e) || e.Code != tt.code):
			t.Errorf("%s: err = %v, want code %d", tt.res.Status, err, tt.code)
		}
	}
}

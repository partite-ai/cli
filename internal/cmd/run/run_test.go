package run_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/partite-ai/partite-cli/internal/cmd/root"
	"github.com/partite-ai/partite-cli/internal/cmd/run"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
)

const meshID = "cmsh_11AL552WdT69LrwqHzj3GPTb"

const intents = `{"intents":[
 {"name":"chat","schema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]},"responses":[]},
 {"name":"triage","schema":{"type":"object","properties":{"ticket":{"type":"string"},"priority":{"type":"integer"}},"required":["ticket","priority"]},"responses":[]}]}`

const taskData = `{"kind":"client_task","response_schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]},"client_task":{"name":"ask_question","input":{"question":"Name?"}}}`

// fakeMesh serves the conversation API for one request whose events are
// given. It records the path and body of each POST.
func fakeMesh(t *testing.T, events ...string) (*httptest.Server, *[]string) {
	var posts []string
	stream := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, "event: started\ndata: {\"type\":\"started\",\"conversation_id\":\"rcnv_1\",\"request_id\":\"rreq_1\"}\n\n")
		for _, e := range events {
			var ev struct{ ID, Type string }
			_ = json.Unmarshal([]byte(e), &ev)
			fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, e)
		}
	}
	problem := func(w http.ResponseWriter, status int, detail, param string) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"type":"about:blank","title":%q,"status":%d,"detail":%q,"param":%q}`, http.StatusText(status), status, detail, param)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /conversations", func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		posts = append(posts, r.URL.Path+" "+string(data))
		var body struct {
			Mesh struct {
				ID string `json:"id"`
			} `json:"mesh"`
			Intent string          `json:"intent"`
			Input  json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Error(err)
		}
		switch {
		case body.Mesh.ID != meshID:
			problem(w, http.StatusNotFound, "mesh or route not found", "")
		case body.Input == nil:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":"rcnv_1","mesh":{"id":%q,"version_id":"cmv_1"},"tags":[]}`, meshID)
		case body.Intent == "":
			problem(w, http.StatusBadRequest, "intent is required: the mesh has more than one", "intent")
		default:
			stream(w)
		}
	})
	mux.HandleFunc("GET /conversations/rcnv_1", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"rcnv_1","mesh":{"id":%q,"version_id":"cmv_1"},"tags":[]}`, meshID)
	})
	mux.HandleFunc("GET /conversations/rcnv_1/intents", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, intents)
	})
	mux.HandleFunc("POST /conversations/rcnv_1/requests", func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		posts = append(posts, r.URL.Path+" "+string(data))
		stream(w)
	})
	mux.HandleFunc("GET /conversations/rcnv_1/requests/rreq_1", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"rreq_1","status":"waiting","input":{},"tags":[],"tasks":[{"id":"rout_t","kind":"client_task","response_schema":{},"client_task":{"name":"ask_question","input":{"question":"Name?"}}}]}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Logf("not served: %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &posts
}

func partite(t *testing.T, srv *httptest.Server, args ...string) (string, string, int) {
	t.Setenv("PARTITE_CONFIG_DIR", t.TempDir())
	t.Setenv("PARTITE_NO_KEYRING", "1")
	t.Setenv("PARTITE_CONTEXT", "local")
	t.Setenv("PARTITE_URL", srv.URL)
	t.Setenv("PARTITE_API_TOKEN", "")
	t.Setenv("PARTITE_API_KEY", "")
	t.Setenv("PARTITE_TOKEN", "")
	var out, errOut bytes.Buffer
	app := cmdutil.NewApp()
	app.In, app.Out, app.Err = strings.NewReader(""), &out, &errOut
	cmd := root.New(app, run.New)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	code := 0
	if err != nil {
		e := app.Classify(err)
		app.PrintError(&errOut, e)
		code = e.Code
	}
	return out.String(), errOut.String(), code
}

func TestRunComplete(t *testing.T) {
	srv, posts := fakeMesh(t,
		`{"id":"rout_1","type":"message","data":"Looking."}`,
		`{"id":"rout_2","type":"response","data":{"answer":"Hi!"}}`,
	)
	out, errOut, code := partite(t, srv, "run", meshID, "--label", "prod", "hello")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	want := `/conversations {"mesh":{"id":"` + meshID + `","version_label":"prod"}}` + "\n" +
		`/conversations/rcnv_1/requests {"intent":"chat","input":{"text":"hello"}}`
	if got := strings.Join(*posts, "\n"); got != want {
		t.Errorf("sent:\n%s\nwant:\n%s", got, want)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if res["status"] != "complete" || res["conversation_id"] != "rcnv_1" || res["request_id"] != "rreq_1" {
		t.Errorf("result = %v", res)
	}
	if r, _ := res["response"].(map[string]any); r["answer"] != "Hi!" {
		t.Errorf("response = %v", res["response"])
	}
}

func TestRunHuman(t *testing.T) {
	srv, _ := fakeMesh(t,
		`{"id":"rout_1","type":"thinking","data":"Hmm."}`,
		`{"id":"rout_2","type":"message","data":"Looking."}`,
		`{"id":"rout_3","type":"response","data":{"answer":"Hi!"}}`,
	)
	out, errOut, code := partite(t, srv, "run", meshID, "--draft", "hello", "-o", "table")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if out != "Looking.\n\nHi!\n" {
		t.Errorf("output:\n%s", out)
	}
	if !strings.Contains(errOut, "--conversation rcnv_1") {
		t.Errorf("stderr has no hint to continue:\n%s", errOut)
	}
}

func TestRunStopsAtTask(t *testing.T) {
	srv, _ := fakeMesh(t,
		`{"id":"rout_1","type":"message","data":"I must ask."}`,
		`{"id":"rout_t","type":"task","data":`+taskData+`}`,
	)
	out, errOut, code := partite(t, srv, "run", meshID, "--draft", "hello", "--events")
	if code != cmdutil.ExitTaskPending {
		t.Fatalf("exit %d, want %d: %s", code, cmdutil.ExitTaskPending, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 2 events and the result, got:\n%s", out)
	}
	var res struct {
		Type   string
		Status string
		Task   struct {
			TaskID         string          `json:"task_id"`
			ResponseSchema json.RawMessage `json:"response_schema"`
		}
	}
	if err := json.Unmarshal([]byte(lines[2]), &res); err != nil {
		t.Fatal(err)
	}
	if res.Type != "result" || res.Status != "task_pending" || res.Task.TaskID != "rout_t" || len(res.Task.ResponseSchema) == 0 {
		t.Errorf("result = %s", lines[2])
	}
	if !strings.Contains(errOut, "partite conversation task complete rcnv_1 rreq_1") {
		t.Errorf("stderr does not say how to answer:\n%s", errOut)
	}
}

func TestRunFailed(t *testing.T) {
	srv, _ := fakeMesh(t, `{"id":"rout_1","type":"error","data":"the model is unavailable"}`)
	out, _, code := partite(t, srv, "run", meshID, "--draft", "hello")
	if code != cmdutil.ExitRequestFailed {
		t.Fatalf("exit %d, want %d", code, cmdutil.ExitRequestFailed)
	}
	if !strings.Contains(out, `"error": "the model is unavailable"`) {
		t.Errorf("output:\n%s", out)
	}
}

func TestRunUsageErrors(t *testing.T) {
	srv, _ := fakeMesh(t)
	tests := []struct {
		args []string
		has  string
	}{
		{[]string{"run", meshID, "hello"}, "pick the mesh version"},
		{[]string{"run", meshID, "--draft", "--label", "prod", "hello"}, "if any flags in the group"},
		{[]string{"run", meshID, "--draft", "--input", `{"ticket":"T-1","priority":1}`}, "The problem is with: intent"},
		{[]string{"run", meshID, "--draft", "--intent", "triage", "hello"}, "takes structured input"},
		{[]string{"run", meshID, "--draft", "--intent", "triage", "hello"}, "Send to it with: partite run " + meshID + " --conversation rcnv_1"},
		{[]string{"run", meshID, "--draft", "--intent", "nope", "x"}, `no intent`},
		{[]string{"run", meshID, "--draft", "--input", "{}", "x"}, "not both"},
		{[]string{"run", meshID, "--conversation", "rcnv_1", "--label", "prod", "x"}, "--conversation already has one"},
		{[]string{"run", meshID, "--conversation", "rcnv_1", "--input", "{}"}, "several intents"},
		{[]string{"run", meshID, "--conversation", "rcnv_1", "--external-id", "e", "x"}, "cannot change one"},
	}
	for _, tt := range tests {
		_, errOut, code := partite(t, srv, tt.args...)
		if code == 0 || !strings.Contains(errOut, tt.has) {
			t.Errorf("%v: exit %d, stderr %s; want %q", tt.args, code, errOut, tt.has)
		}
	}
}

func TestRunWithInputStartsTheConversationInOnePost(t *testing.T) {
	srv, posts := fakeMesh(t, `{"id":"rout_1","type":"response","data":{"answer":"Done"}}`)
	out, errOut, code := partite(t, srv, "run", meshID, "--version", "3", "--intent", "triage", "--input", `{"ticket":"T-1","priority":1}`, "--external-id", "t-1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	want := `/conversations {"mesh":{"id":"` + meshID + `","version":3},"external_id":"t-1","intent":"triage","input":{"priority":1,"ticket":"T-1"}}`
	if got := strings.Join(*posts, "\n"); got != want {
		t.Errorf("sent:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(out, `"status": "complete"`) {
		t.Errorf("output:\n%s", out)
	}
}

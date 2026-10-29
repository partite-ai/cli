package convapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
)

func TestSSEReader(t *testing.T) {
	stream := ": ping\n\n" +
		"id: 1\r\nevent: message\r\ndata: {\"a\":\r\ndata: 1}\r\n\r\n" +
		": ping\n" +
		"data: no id\n\n" +
		"id: 3\ndata: cut off"
	r := NewSSEReader(strings.NewReader(stream))
	var got []SSEEvent
	for {
		ev, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}
	want := []SSEEvent{
		{ID: "1", Event: "message", Data: "{\"a\":\n1}"},
		{Data: "no id"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// server answers the client's requests in memory. Each call to events, or
// POST of a message, returns the next scripted body for the events stream.
type server struct {
	mu      sync.Mutex
	streams []string
	// lastIDs has the Last-Event-ID of each events request.
	lastIDs []string
	// request is the reply to GET .../requests/rreq_1.
	request string
	// status, when set, is the status of every events request.
	status int
	// posts has the path and body of each POST.
	posts []string
}

func (s *server) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply := func(status int, body string) *http.Response {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}
	}
	switch {
	case r.Method == http.MethodPost:
		data, _ := io.ReadAll(r.Body)
		s.posts = append(s.posts, r.URL.Path+" "+string(data))
		if r.Header.Get("Accept") != "text/event-stream" || len(s.streams) == 0 {
			return reply(http.StatusBadRequest, `{"type":"about:blank","title":"Bad Request","status":400,"detail":"no"}`), nil
		}
		body := s.streams[0]
		s.streams = s.streams[1:]
		return reply(http.StatusCreated, body), nil
	case strings.HasSuffix(r.URL.Path, "/events"):
		s.lastIDs = append(s.lastIDs, r.Header.Get("Last-Event-ID"))
		if s.status != 0 {
			return reply(s.status, `{"error":"unavailable"}`), nil
		}
		if len(s.streams) == 0 {
			return reply(http.StatusInternalServerError, `{"error":"no more streams"}`), nil
		}
		body := s.streams[0]
		s.streams = s.streams[1:]
		return reply(http.StatusOK, body), nil
	case strings.HasSuffix(r.URL.Path, "/requests/rreq_1"):
		return reply(http.StatusOK, s.request), nil
	}
	return reply(http.StatusNotFound, `{"error":"not found"}`), nil
}

func newClient(s *server) *Client {
	return &Client{REST: &cmdutil.REST{BaseURL: "http://mesh", HTTP: &http.Client{Transport: s}}}
}

// recorder is a Handler that keeps the events it gets and answers tasks
// with answer.
type recorder struct {
	started string
	events  []Event
	tasks   []Task
	answer  bool
}

func (r *recorder) Started(conversationID, requestID string) error {
	r.started = conversationID + " " + requestID
	return nil
}

func (r *recorder) Event(ev Event) error {
	r.events = append(r.events, ev)
	return nil
}

func (r *recorder) Task(_ context.Context, t Task) (bool, error) {
	r.tasks = append(r.tasks, t)
	return r.answer, nil
}

func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		var ev Event
		if err := json.Unmarshal([]byte(e), &ev); err != nil {
			panic(err)
		}
		name := ev.Type
		if name == "error" {
			name = "request_error"
		}
		b.WriteString("id: " + ev.ID + "\nevent: " + name + "\ndata: " + e + "\n\n: ping\n\n")
	}
	return b.String()
}

func TestFollowRecordedStream(t *testing.T) {
	data, err := os.ReadFile("testdata/task_stream.sse")
	if err != nil {
		t.Fatal(err)
	}
	// The task in the stream was answered, so the server has no open task.
	s := &server{streams: []string{string(data)}, request: `{"id":"rreq_1","status":"running"}`}
	var rec recorder
	res, err := newClient(s).Follow(context.Background(), "rcnv_1", "rreq_1", "", &rec)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, ev := range rec.events {
		types = append(types, ev.Type)
	}
	if got := strings.Join(types, ","); got != "thinking,message,task,task_response,message,response" {
		t.Errorf("events = %s", got)
	}
	if len(rec.tasks) != 0 {
		t.Errorf("asked to answer %d tasks that were answered already", len(rec.tasks))
	}
	if res.Status != StatusComplete || string(res.Response) != `{"answer":"You picked a color."}` {
		t.Errorf("result = %+v", res)
	}
	if res.ConversationID != "rcnv_1" || res.RequestID != "rreq_1" {
		t.Errorf("result ids = %s %s", res.ConversationID, res.RequestID)
	}
}

const openRequest = `{"id":"rreq_1","status":"waiting","tasks":[{"id":"rout_t","kind":"client_task","response_schema":{},"client_task":{"name":"ask_question","input":{"question":"Name?"}}}]}`

const taskEvent = `{"id":"rout_t","type":"task","data":{"kind":"client_task","response_schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]},"client_task":{"name":"ask_question","input":{"question":"Name?"}}}}`

func TestFollowStopsAtOpenTask(t *testing.T) {
	s := &server{
		streams: []string{sse(`{"id":"rout_m","type":"message","data":"hi"}`, taskEvent)},
		request: openRequest,
	}
	var rec recorder
	res, err := newClient(s).Follow(context.Background(), "rcnv_1", "rreq_1", "", &rec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusTask || res.Task == nil || res.Task.TaskID != "rout_t" || res.Task.Name() != "ask_question" {
		t.Fatalf("result = %+v", res)
	}
	if len(rec.tasks) != 1 {
		t.Errorf("handler got %d tasks, want 1", len(rec.tasks))
	}
}

func TestFollowGoesOnAfterAnsweredTask(t *testing.T) {
	s := &server{
		streams: []string{sse(taskEvent,
			`{"id":"rout_a","type":"task_response","data":{"answer":"Bob"},"in_reply_to":"rout_t"}`,
			`{"id":"rout_r","type":"response","data":{"answer":"Hi Bob"}}`)},
		request: openRequest,
	}
	rec := recorder{answer: true}
	res, err := newClient(s).Follow(context.Background(), "rcnv_1", "rreq_1", "", &rec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusComplete || len(rec.events) != 3 {
		t.Fatalf("result = %+v after %d events", res, len(rec.events))
	}
}

func TestFollowReconnectsWhereItLeftOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &server{streams: []string{
			// The connection drops after two events.
			sse(`{"id":"rout_1","type":"message","data":"one"}`, `{"id":"rout_2","type":"message","data":"two"}`),
			// The server reports a stream error.
			"event: server_error\ndata: {\"type\":\"server_error\",\"error\":\"error reading stream\"}\n\n",
			sse(`{"id":"rout_3","type":"error","data":"the model is unavailable"}`),
		}}
		var rec recorder
		res, err := newClient(s).Follow(context.Background(), "rcnv_1", "rreq_1", "rout_0", &rec)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(s.lastIDs, ","); got != "rout_0,rout_2,rout_2" {
			t.Errorf("Last-Event-ID of each connection = %s", got)
		}
		if len(rec.events) != 3 {
			t.Errorf("got %d events, want 3 with none repeated", len(rec.events))
		}
		if res.Status != StatusFailed || res.Error != "the model is unavailable" {
			t.Errorf("result = %+v", res)
		}
	})
}

func TestFollowGivesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &server{status: http.StatusBadGateway}
		_, err := newClient(s).Follow(context.Background(), "rcnv_1", "rreq_1", "", &recorder{})
		if !errors.Is(err, errStreamBroken) {
			t.Fatalf("err = %v, want a broken stream", err)
		}
		if len(s.lastIDs) != maxStreamFailures+1 {
			t.Errorf("connected %d times, want %d", len(s.lastIDs), maxStreamFailures+1)
		}
	})
}

func TestFollowDoesNotRetryClientErrors(t *testing.T) {
	s := &server{status: http.StatusNotFound}
	_, err := newClient(s).Follow(context.Background(), "rcnv_1", "rreq_1", "", &recorder{})
	var se *cmdutil.StatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusNotFound {
		t.Fatalf("err = %v, want a 404", err)
	}
	if len(s.lastIDs) != 1 {
		t.Errorf("connected %d times, want 1", len(s.lastIDs))
	}
}

func TestFollowStopsWhenContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &server{status: http.StatusServiceUnavailable}
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			// Cancel while Follow waits to reconnect.
			synctest.Wait()
			cancel()
		}()
		_, err := newClient(s).Follow(ctx, "rcnv_1", "rreq_1", "", &recorder{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

func TestResultOf(t *testing.T) {
	tests := []struct {
		request string
		want    string
	}{
		{`{"id":"rreq_1","status":"running"}`, StatusRunning},
		{openRequest, StatusTask},
		{`{"id":"rreq_1","status":"completed","response":{"answer":"hi"}}`, StatusComplete},
		{`{"id":"rreq_1","status":"failed","error":"boom"}`, StatusFailed},
		{`{"id":"rreq_1","status":"cancelled"}`, StatusCancelled},
	}
	for _, tt := range tests {
		var r Request
		if err := json.Unmarshal([]byte(tt.request), &r); err != nil {
			t.Fatal(err)
		}
		res := ResultOf("rcnv_1", r)
		if res.Status != tt.want || res.RequestID != "rreq_1" {
			t.Errorf("%s: result = %+v, want %s", tt.request, res, tt.want)
		}
		if (res.Task != nil) != (tt.want == StatusTask) {
			t.Errorf("%s: task = %+v", tt.request, res.Task)
		}
	}
	if res := ResultOf("rcnv_1", Request{ID: "rreq_1", Status: RequestWaiting, Tasks: []RequestTask{{ID: "rout_t", TaskData: TaskData{Kind: TaskKindClientTask, ClientTask: &ClientTask{Name: "pick_one"}}}}}); res.Task.TaskID != "rout_t" || res.Task.Name() != "pick_one" {
		t.Errorf("task = %+v", res.Task)
	}
}

const started = "event: started\ndata: {\"type\":\"started\",\"conversation_id\":\"rcnv_1\",\"request_id\":\"rreq_1\"}\n\n"

func TestStartRequestFollowsThePostAndResumes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &server{
			streams: []string{
				// The POST's stream drops after one event.
				started + sse(`{"id":"rout_1","type":"message","data":"one"}`),
				started + sse(`{"id":"rout_2","type":"response","data":{"answer":"done"}}`),
			},
		}
		var rec recorder
		res, err := newClient(s).StartRequest(context.Background(), "rcnv_1", NewRequest{Input: json.RawMessage(`{"text":"hi"}`)}, nil, &rec)
		if err != nil {
			t.Fatal(err)
		}
		if rec.started != "rcnv_1 rreq_1" {
			t.Errorf("started = %q", rec.started)
		}
		if got := strings.Join(s.posts, ","); got != `/conversations/rcnv_1/requests {"input":{"text":"hi"}}` {
			t.Errorf("posts = %s", got)
		}
		if got := strings.Join(s.lastIDs, ","); got != "rout_1" {
			t.Errorf("Last-Event-ID of each resume = %s", got)
		}
		if res.Status != StatusComplete || res.ConversationID != "rcnv_1" || res.RequestID != "rreq_1" || len(rec.events) != 2 {
			t.Errorf("result = %+v after %d events", res, len(rec.events))
		}
	})
}

func TestStartConversationCannotResumeBeforeStarted(t *testing.T) {
	s := &server{streams: []string{""}}
	body := NewConversation{Target: Target{Mesh: &MeshTarget{ID: "cmsh_1", Draft: true}}, Input: json.RawMessage(`{}`)}
	_, err := newClient(s).StartConversation(context.Background(), body, nil, &recorder{})
	if !errors.Is(err, errCannotResume) {
		t.Fatalf("err = %v, want errCannotResume", err)
	}
	if got := strings.Join(s.posts, ","); got != `/conversations {"mesh":{"id":"cmsh_1","draft":true},"input":{}}` {
		t.Errorf("posts = %s", got)
	}
}

func TestEventsStopsWhenQuiet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pr, pw := io.Pipe()
		c := &Client{REST: &cmdutil.REST{BaseURL: "http://mesh", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Last-Event-ID") != "rout_0" {
				t.Errorf("Last-Event-ID = %q", r.Header.Get("Last-Event-ID"))
			}
			go func() {
				_, _ = io.WriteString(pw, started+sse(`{"id":"rout_1","type":"message","data":"one"}`))
				// As net/http does, end the body when the request's context ends.
				<-r.Context().Done()
				pw.CloseWithError(r.Context().Err())
			}()
			return &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}, Request: r}, nil
		})}}}
		events, err := c.Events(context.Background(), "rcnv_1", "rreq_1", "rout_0")
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 || events[0].ID != "rout_1" {
			t.Errorf("events = %+v", events)
		}
	})
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

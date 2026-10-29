package convapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"
)

// Status of a request, as the CLI reports it.
const (
	StatusComplete  = "complete"     // it has a response
	StatusFailed    = "failed"       // it ended with an error
	StatusCancelled = "cancelled"    // it was cancelled
	StatusTask      = "task_pending" // it waits for a task to be completed
	StatusRunning   = "running"      // it is still working
)

// Result is where a request stands. It is what run, response and the other
// commands print for scripts, so its fields are a stable contract.
type Result struct {
	// Type is "result" when the result ends a stream of events, so that it can
	// be told apart from them.
	Type           string          `json:"type,omitempty"`
	ConversationID string          `json:"conversation_id"`
	RequestID      string          `json:"request_id"`
	Status         string          `json:"status"`
	Response       json.RawMessage `json:"response,omitempty"`
	Error          string          `json:"error,omitempty"`
	Task           *Task           `json:"task,omitempty"`
}

// EventText returns event data that is a JSON string as that string, and any
// other data as compact JSON.
func EventText(data json.RawMessage) string {
	var s string
	if json.Unmarshal(data, &s) == nil {
		return s
	}
	return string(data)
}

// Handler receives a request's events as Follow reads them.
type Handler interface {
	// Started is called once the server names the request that a message
	// started.
	Started(conversationID, requestID string) error
	Event(ev Event) error
	// Task is called for a task that is still open. It returns true when it
	// completed the task, so that following goes on.
	Task(ctx context.Context, t Task) (bool, error)
}

// errCannotResume is a stream from a POST that broke before it named the
// request, so there is nothing to resume.
var errCannotResume = errors.New("the server's event stream ended before it named the request; see the conversation's requests to find it")

// Follow reads a request's events, from after the event with id after, until
// the request ends or waits on a task that h does not complete. It
// reconnects when the stream drops, without repeating events.
func (c *Client) Follow(ctx context.Context, conversationID, requestID, after string, h Handler) (Result, error) {
	return c.follow(ctx, nil, conversationID, requestID, after, h)
}

// StartConversation starts a conversation with its first message, and
// follows the request it starts as Follow does.
func (c *Client) StartConversation(ctx context.Context, body NewConversation, files []Attachment, h Handler) (Result, error) {
	resp, err := c.postBody(ctx, "/conversations", body, files, http.Header{"Accept": {"text/event-stream"}})
	if err != nil {
		return Result{}, err
	}
	return c.follow(ctx, resp, "", "", "", h)
}

// StartRequest sends a message to a conversation, and follows the request it
// starts as Follow does.
func (c *Client) StartRequest(ctx context.Context, conversationID string, body NewRequest, files []Attachment, h Handler) (Result, error) {
	resp, err := c.postBody(ctx, ConversationPath(conversationID)+"/requests", body, files, http.Header{"Accept": {"text/event-stream"}})
	if err != nil {
		return Result{}, err
	}
	return c.follow(ctx, resp, conversationID, "", "", h)
}

// follow reads first, when it is not nil, and then the request's events
// stream from after the last event read. A stream from a POST names the
// request in its started event.
func (c *Client) follow(ctx context.Context, first *http.Response, conversationID, requestID, after string, h Handler) (Result, error) {
	res := Result{ConversationID: conversationID, RequestID: requestID, Status: StatusRunning}
	handle := func(sse SSEEvent) (bool, error) {
		if sse.ID != "" {
			after = sse.ID
		}
		if sse.Event == "started" {
			if res.RequestID != "" {
				return false, nil
			}
			var s struct {
				ConversationID string `json:"conversation_id"`
				RequestID      string `json:"request_id"`
			}
			if err := json.Unmarshal([]byte(sse.Data), &s); err != nil {
				return false, fmt.Errorf("read event %q: %w", sse.Data, err)
			}
			res.ConversationID, res.RequestID = s.ConversationID, s.RequestID
			return false, h.Started(s.ConversationID, s.RequestID)
		}
		var ev Event
		if err := json.Unmarshal([]byte(sse.Data), &ev); err != nil {
			return false, fmt.Errorf("read event %q: %w", sse.Data, err)
		}
		if ev.ID == "" {
			ev.ID = sse.ID
		}
		if err := h.Event(ev); err != nil {
			return false, err
		}
		if ev.Final() {
			res = finalResult(res, ev)
			return true, nil
		}
		if ev.Type != "task" {
			return false, nil
		}
		t, err := TaskFromEvent(ev)
		if err != nil {
			return false, err
		}
		// A task that was answered before this stream started reads like
		// an open one, so ask the server.
		r, err := c.GetRequest(ctx, res.ConversationID, res.RequestID, 0)
		if err != nil || !slices.ContainsFunc(r.Tasks, func(o RequestTask) bool { return o.ID == t.TaskID }) {
			return false, err
		}
		answered, err := h.Task(ctx, t)
		if err != nil || answered {
			return false, err
		}
		res.Status = StatusTask
		res.Task = &t
		return true, nil
	}
	if first != nil {
		done, _, err := readStream(ctx, first, handle)
		if done || (err != nil && !errors.Is(err, errStreamBroken)) {
			return res, err
		}
		if res.RequestID == "" {
			return res, fmt.Errorf("%w: %w", errCannotResume, err)
		}
	}
	connect := func(after string) (*http.Response, error) {
		return c.getStream(ctx, requestPath(res.ConversationID, res.RequestID)+"/events", after)
	}
	err := c.stream(ctx, connect, after, handle)
	return res, err
}
func finalResult(res Result, ev Event) Result {
	switch ev.Type {
	case "response":
		res.Status = StatusComplete
		res.Response = ev.Data
	case "error":
		res.Status = StatusFailed
		res.Error = EventText(ev.Data)
	case "cancelled":
		res.Status = StatusCancelled
		res.Error = EventText(ev.Data)
	}
	return res
}

// ResultOf is where a request stands, from what the server says of it.
func ResultOf(conversationID string, r Request) Result {
	res := Result{ConversationID: conversationID, RequestID: r.ID, Status: StatusRunning}
	switch r.Status {
	case RequestCompleted:
		res.Status = StatusComplete
		res.Response = r.Response
	case RequestFailed:
		res.Status = StatusFailed
		res.Error = r.Error
	case RequestCancelled:
		res.Status = StatusCancelled
	case RequestWaiting:
		if tasks := r.OpenTasks(); len(tasks) > 0 {
			res.Status = StatusTask
			res.Task = &tasks[0]
		}
	}
	return res
}

// eventsQuiet is how long Events waits for another event before it stops.
const eventsQuiet = time.Second

// Events reads the events a request has so far, after the event with id
// after when it is set. The stream of a request that has not ended stays
// open for the events to come, so reading stops at the final event or once
// no event has come for eventsQuiet.
func (c *Client) Events(ctx context.Context, conversationID, requestID, after string) ([]Event, error) {
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, err := c.getStream(readCtx, requestPath(conversationID, requestID)+"/events", after)
	if err != nil {
		return nil, err
	}
	timer := time.AfterFunc(eventsQuiet, cancel)
	defer timer.Stop()
	events := []Event{}
	_, _, err = readStream(readCtx, resp, func(sse SSEEvent) (bool, error) {
		timer.Reset(eventsQuiet)
		if sse.Event == "started" {
			return false, nil
		}
		var ev Event
		if err := json.Unmarshal([]byte(sse.Data), &ev); err != nil {
			return false, fmt.Errorf("read event %q: %w", sse.Data, err)
		}
		events = append(events, ev)
		return ev.Final(), nil
	})
	if err != nil && readCtx.Err() != nil && ctx.Err() == nil {
		// No event came for eventsQuiet.
		err = nil
	}
	return events, err
}

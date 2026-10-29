// Package convapi is a client for the conversation and notification channel
// REST APIs: starting requests, following their events, answering tasks.
package convapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
)

type Client struct {
	REST *cmdutil.REST
}

// Target picks the mesh version of a new conversation: a mesh or a route.
type Target struct {
	Mesh    *MeshTarget `json:"mesh,omitempty"`
	RouteID string      `json:"route_id,omitempty"`
}

// MeshTarget is a mesh with exactly one of Version, VersionLabel and Draft.
type MeshTarget struct {
	ID           string `json:"id"`
	Version      int    `json:"version,omitempty"`
	VersionLabel string `json:"version_label,omitempty"`
	Draft        bool   `json:"draft,omitempty"`
}

type Intent struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Schema      json.RawMessage  `json:"schema"`
	Responses   []IntentResponse `json:"responses"`
}

type IntentResponse struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema"`
}

// ConversationMesh is the mesh a conversation runs on, and its version.
type ConversationMesh struct {
	ID        string `json:"id"`
	VersionID string `json:"version_id"`
}

type Conversation struct {
	ID         string           `json:"id"`
	ExternalID string           `json:"external_id,omitempty"`
	Mesh       ConversationMesh `json:"mesh"`
	Tags       []cmdutil.Tag    `json:"tags"`
	CreatedAt  string           `json:"created_at,omitempty"`
	UpdatedAt  string           `json:"updated_at,omitempty"`
	// Request is the request the conversation's first message started, when
	// it was created with one.
	Request *Request `json:"request,omitempty"`
}

// NewConversation is the body that starts a conversation. With Input, it also
// sends the first message.
type NewConversation struct {
	Target
	ExternalID          string          `json:"external_id,omitempty"`
	Tags                []cmdutil.Tag   `json:"tags,omitempty"`
	NotificationChannel string          `json:"notification_channel,omitempty"`
	Tracing             *bool           `json:"tracing,omitempty"`
	Intent              string          `json:"intent,omitempty"`
	Input               json.RawMessage `json:"input,omitempty"`
}

// NewRequest is the body of a message to a conversation. Intent may be empty
// when the mesh has one intent.
type NewRequest struct {
	Intent string          `json:"intent,omitempty"`
	Input  json.RawMessage `json:"input"`
	Tags   []cmdutil.Tag   `json:"tags,omitempty"`
}

// Attachment is a file sent with a message. The input can refer to it as
// {"$ref": "attachment://<Filename>"}.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// ReadAttachment reads a file to attach. The content type comes from the
// file's extension, or from its first bytes.
func ReadAttachment(path string) (Attachment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Attachment{}, cmdutil.UsageError("read %s: %s", path, err)
	}
	ct := mime.TypeByExtension(filepath.Ext(path))
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	return Attachment{Filename: filepath.Base(path), ContentType: ct, Data: data}, nil
}

// Event is one output of a request. Type is message, thinking, task,
// task_response, response, error or cancelled.
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	InReplyTo string          `json:"in_reply_to,omitempty"`
}

// Final is true for the event that ends a request.
func (e Event) Final() bool {
	return e.Type == "response" || e.Type == "error" || e.Type == "cancelled"
}

// The statuses of a request, as the server reports them.
const (
	RequestRunning   = "running"
	RequestWaiting   = "waiting"
	RequestCompleted = "completed"
	RequestFailed    = "failed"
	RequestCancelled = "cancelled"
)

// Request is a request of a conversation and where it stands.
type Request struct {
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	Input     json.RawMessage `json:"input"`
	Tags      []cmdutil.Tag   `json:"tags"`
	CreatedAt string          `json:"created_at,omitempty"`
	Response  json.RawMessage `json:"response,omitempty"`
	Tasks     []RequestTask   `json:"tasks,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// RequestTask is an open task as a request lists it.
type RequestTask struct {
	ID string `json:"id"`
	TaskData
}

// TaskData is what a task holds: its kind, the JSON schema of what completes
// it, and its details under the name of its kind.
type TaskData struct {
	Kind             string            `json:"kind"`
	ResponseSchema   json.RawMessage   `json:"response_schema"`
	ClientTask       *ClientTask       `json:"client_task,omitempty"`
	ToolCallApproval *ToolCallApproval `json:"tool_call_approval,omitempty"`
}

// The kinds of task.
const (
	TaskKindClientTask       = "client_task"
	TaskKindToolCallApproval = "tool_call_approval"
)

// ClientTask is a task an agent sends with a client task definition.
type ClientTask struct {
	Name          string          `json:"name"`
	Input         json.RawMessage `json:"input"`
	Presentations json.RawMessage `json:"presentations,omitempty"`
}

// ToolCallApproval asks whether a tool call may be made.
type ToolCallApproval struct {
	Tool       ApprovalTool    `json:"tool"`
	Arguments  json.RawMessage `json:"arguments"`
	Message    string          `json:"message,omitempty"`
	DecisionID string          `json:"decision_id"`
}

// ApprovalTool is the tool a tool call approval is for.
type ApprovalTool struct {
	Name   string             `json:"name"`
	Source ApprovalToolSource `json:"source"`
	Memory *struct {
		Operation string `json:"operation"`
	} `json:"memory,omitempty"`
}

// ApprovalToolSource is where a tool comes from: an MCP connection, installed
// particle or memory interaction profile, named by ID and Name, or a kind
// with none behind it.
type ApprovalToolSource struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// Task is a task that waits for a person (or a program) to answer it.
type Task struct {
	TaskID string `json:"task_id"`
	TaskData
}

// Name is the client task's name, or the task's kind for any other task.
func (t Task) Name() string {
	if t.ClientTask != nil {
		return t.ClientTask.Name
	}
	return t.Kind
}

// Input is the input a client task was sent with. Other tasks have none.
func (t Task) Input() json.RawMessage {
	if t.ClientTask != nil {
		return t.ClientTask.Input
	}
	return nil
}

// OpenTasks returns the request's open tasks.
func (r Request) OpenTasks() []Task {
	out := make([]Task, len(r.Tasks))
	for i, t := range r.Tasks {
		out[i] = Task{TaskID: t.ID, TaskData: t.TaskData}
	}
	return out
}

// TaskFromEvent reads the task of a task event.
func TaskFromEvent(ev Event) (Task, error) {
	var d TaskData
	if err := json.Unmarshal(ev.Data, &d); err != nil {
		return Task{}, fmt.Errorf("read task %s: %w", ev.ID, err)
	}
	return Task{TaskID: ev.ID, TaskData: d}, nil
}

// ConversationPath is the path of a conversation, named by its id.
func ConversationPath(id string) string {
	return "/conversations/" + url.PathEscape(id)
}

// ExternalIDPath is the path of a conversation, named by its external id.
func ExternalIDPath(externalID string) string {
	return "/conversations/extid=" + url.PathEscape(externalID)
}

func requestPath(conversationID, requestID string) string {
	return ConversationPath(conversationID) + "/requests/" + url.PathEscape(requestID)
}

func taskPath(conversationID, requestID, taskID string) string {
	return requestPath(conversationID, requestID) + "/tasks/" + url.PathEscape(taskID)
}

func (c *Client) Intents(ctx context.Context, conversationID string) ([]Intent, error) {
	var out struct {
		Intents []Intent `json:"intents"`
	}
	err := c.REST.JSON(ctx, http.MethodGet, ConversationPath(conversationID)+"/intents", nil, &out)
	// The server lists intents in no set order.
	sort.Slice(out.Intents, func(i, j int) bool { return out.Intents[i].Name < out.Intents[j].Name })
	return out.Intents, err
}

// GetConversation reads a conversation at path, which is ConversationPath or
// ExternalIDPath.
func (c *Client) GetConversation(ctx context.Context, path string) (Conversation, error) {
	var out Conversation
	err := c.REST.JSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// CreateConversation starts a conversation, and sends its first message when
// body has an input. The request it starts is in the conversation.
func (c *Client) CreateConversation(ctx context.Context, body NewConversation, files []Attachment) (Conversation, error) {
	var out Conversation
	err := c.post(ctx, "/conversations", body, files, http.Header{"Accept": {"application/json"}}, &out)
	return out, err
}

// SendRequest sends a message to a conversation and returns the request it
// started.
func (c *Client) SendRequest(ctx context.Context, conversationID string, body NewRequest, files []Attachment) (Request, error) {
	var out Request
	err := c.post(ctx, ConversationPath(conversationID)+"/requests", body, files, http.Header{"Accept": {"application/json"}}, &out)
	return out, err
}

// post sends body as JSON, or as a multipart form with the files when there
// are any, and decodes the JSON reply into out.
func (c *Client) post(ctx context.Context, path string, body any, files []Attachment, header http.Header, out any) error {
	resp, err := c.postBody(ctx, path, body, files, header)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("read the server's reply: %w", err)
	}
	return nil
}

// postBody sends body as post does and returns the response, which the
// caller closes.
func (c *Client) postBody(ctx context.Context, path string, body any, files []Attachment, header http.Header) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		header.Set("Content-Type", "application/json")
		return c.REST.Do(ctx, http.MethodPost, path, bytes.NewReader(data), header)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part := func(name, filename, contentType string, data []byte) error {
		h := textproto.MIMEHeader{}
		if filename != "" {
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, name, filename))
		} else {
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q`, name))
		}
		h.Set("Content-Type", contentType)
		p, err := w.CreatePart(h)
		if err != nil {
			return err
		}
		_, err = p.Write(data)
		return err
	}
	if err := part("params", "", "application/json", data); err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := part("attachments", f.Filename, f.ContentType, f.Data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	header.Set("Content-Type", w.FormDataContentType())
	return c.REST.Do(ctx, http.MethodPost, path, &buf, header)
}

func (c *Client) Requests(ctx context.Context, conversationID string) ([]Request, error) {
	var out struct {
		Requests []Request `json:"requests"`
	}
	err := c.REST.JSON(ctx, http.MethodGet, ConversationPath(conversationID)+"/requests", nil, &out)
	return out.Requests, err
}

// MaxWait is the longest the server holds a request's answer.
const MaxWait = 50 * time.Second

// GetRequest reads a request. With wait, the server answers once the request
// leaves running, or when wait ends, whichever comes first.
func (c *Client) GetRequest(ctx context.Context, conversationID, requestID string, wait time.Duration) (Request, error) {
	path := requestPath(conversationID, requestID)
	if secs := int(min(wait, MaxWait).Seconds()); secs > 0 {
		path += "?wait=" + strconv.Itoa(secs)
	}
	header := http.Header{"Accept": {"application/json"}}
	resp, err := c.REST.Do(ctx, http.MethodGet, path, nil, header)
	if err != nil {
		return Request{}, err
	}
	defer resp.Body.Close()
	var out Request
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Request{}, fmt.Errorf("read the server's reply: %w", err)
	}
	return out, nil
}

// WaitRequest reads a request once it leaves running. It asks again while
// the server's wait ends with the request still running, until ctx ends.
func (c *Client) WaitRequest(ctx context.Context, conversationID, requestID string) (Request, error) {
	for {
		r, err := c.GetRequest(ctx, conversationID, requestID, MaxWait)
		if err != nil || r.Status != RequestRunning {
			return r, err
		}
	}
}

// Cancel cancels a request and returns its state after. A request that had
// ended keeps its final state.
func (c *Client) Cancel(ctx context.Context, conversationID, requestID string) (Request, error) {
	var out Request
	err := c.REST.JSON(ctx, http.MethodPost, requestPath(conversationID, requestID)+"/cancel", nil, &out)
	return out, err
}

func (c *Client) CompleteTask(ctx context.Context, conversationID, requestID, taskID string, output json.RawMessage) error {
	return c.REST.JSON(ctx, http.MethodPost, taskPath(conversationID, requestID, taskID)+"/complete", output, nil)
}

// CancelTask declines a task. The agent that asked gets an error, and the
// request goes on.
func (c *Client) CancelTask(ctx context.Context, conversationID, requestID, taskID string) error {
	return c.REST.JSON(ctx, http.MethodPost, taskPath(conversationID, requestID, taskID)+"/cancel", nil, nil)
}

// Resource is something a conversation holds, such as a file. Reference is
// what an input puts where it refers to it, such as {"$ref": "resource://r3"}.
type Resource struct {
	Reference   json.RawMessage `json:"reference"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	MimeType    string          `json:"mime_type"`
}

// AddResources adds files to a conversation.
func (c *Client) AddResources(ctx context.Context, conversationID string, files []Attachment) ([]Resource, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files"; filename=%q`, f.Filename))
		h.Set("Content-Type", f.ContentType)
		p, err := w.CreatePart(h)
		if err != nil {
			return nil, err
		}
		if _, err := p.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	header := http.Header{"Content-Type": {w.FormDataContentType()}, "Accept": {"application/json"}}
	resp, err := c.REST.Do(ctx, http.MethodPost, ConversationPath(conversationID)+"/resources", &buf, header)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Resources []Resource `json:"resources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("read the server's reply: %w", err)
	}
	return out.Resources, nil
}

func (c *Client) Resources(ctx context.Context, conversationID string) ([]Resource, error) {
	var out struct {
		Resources []Resource `json:"resources"`
	}
	err := c.REST.JSON(ctx, http.MethodGet, ConversationPath(conversationID)+"/resources", nil, &out)
	return out.Resources, err
}

// Notification is one event of a notification channel: a request's event,
// with the ids of the conversation and request that wrote it.
type Notification struct {
	ID                     string `json:"id"`
	ConversationID         string `json:"conversation_id"`
	ConversationExternalID string `json:"conversation_external_id,omitempty"`
	RequestID              string `json:"request_id"`
	Event                  Event  `json:"event"`
}

func channelPath(id string) string {
	return "/notification-channels/" + url.PathEscape(id)
}

// CreateChannel creates a notification channel. It is not an error when the
// channel exists.
func (c *Client) CreateChannel(ctx context.Context, id string) error {
	return c.REST.JSON(ctx, http.MethodPut, channelPath(id), nil, nil)
}

// ChannelPage reads up to limit events of a channel, after the event with id
// after when it is set. next is where to resume.
func (c *Client) ChannelPage(ctx context.Context, id, after string, limit int) ([]Notification, string, error) {
	path := channelPath(id) + "/events"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	header := http.Header{"Accept": {"application/json"}}
	if after != "" {
		header.Set("Last-Event-ID", after)
	}
	resp, err := c.REST.Do(ctx, http.MethodGet, path, nil, header)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	var out struct {
		Events []Notification `json:"events"`
		Next   string         `json:"next"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, "", fmt.Errorf("read the server's reply: %w", err)
	}
	return out.Events, out.Next, nil
}

// FollowChannel streams a channel's events to fn until ctx ends or fn
// returns an error. It reconnects when the stream drops.
func (c *Client) FollowChannel(ctx context.Context, id, after string, fn func(Notification) error) error {
	connect := func(after string) (*http.Response, error) {
		return c.getStream(ctx, channelPath(id)+"/events", after)
	}
	return c.stream(ctx, connect, after, func(sse SSEEvent) (bool, error) {
		var n Notification
		if err := json.Unmarshal([]byte(sse.Data), &n); err != nil {
			return false, fmt.Errorf("read notification %q: %w", sse.Data, err)
		}
		return false, fn(n)
	})
}

// errStreamBroken is a stream that ended or failed in a way that a new
// connection can fix.
var errStreamBroken = errors.New("the event stream closed early")

// Most consecutive failed connections before stream gives up.
const maxStreamFailures = 5

// sleep waits for d, or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) getStream(ctx context.Context, path, after string) (*http.Response, error) {
	header := http.Header{"Accept": {"text/event-stream"}}
	if after != "" {
		header.Set("Last-Event-ID", after)
	}
	return c.REST.Do(ctx, http.MethodGet, path, nil, header)
}

// stream connects with connect, reads SSE events and passes each to fn,
// until fn reports done or fails. When a connection drops or the server
// sends a server_error event, it connects again and resumes after the last event
// with an id.
func (c *Client) stream(ctx context.Context, connect func(after string) (*http.Response, error), after string, fn func(SSEEvent) (done bool, err error)) error {
	failures := 0
	for {
		var done, read bool
		resp, err := connect(after)
		var se *cmdutil.StatusError
		switch {
		case err == nil:
			done, read, err = readStream(ctx, resp, func(sse SSEEvent) (bool, error) {
				if sse.ID != "" {
					after = sse.ID
				}
				return fn(sse)
			})
		case ctx.Err() != nil:
			return ctx.Err()
		case !errors.As(err, &se) || se.StatusCode >= 500:
			err = fmt.Errorf("%w: %w", errStreamBroken, err)
		}
		if done || (err != nil && !errors.Is(err, errStreamBroken)) {
			return err
		}
		if read {
			failures = 0
		}
		failures++
		if failures > maxStreamFailures {
			return fmt.Errorf("lost the event stream from the server %d times in a row: %w", maxStreamFailures, err)
		}
		if err := sleep(ctx, min(time.Second<<(failures-1), 10*time.Second)); err != nil {
			return err
		}
	}
}

// readStream reads one connection and closes it. read is true when it got at
// least one event. A dropped connection, or a server_error event, which the
// server sends when it cannot read the stream further, is errStreamBroken.
func readStream(ctx context.Context, resp *http.Response, fn func(SSEEvent) (bool, error)) (done, read bool, err error) {
	defer resp.Body.Close()
	r := NewSSEReader(resp.Body)
	for {
		sse, err := r.Next()
		if err != nil {
			if ctx.Err() != nil {
				return false, read, ctx.Err()
			}
			if err == io.EOF {
				return false, read, errStreamBroken
			}
			return false, read, fmt.Errorf("%w: %w", errStreamBroken, err)
		}
		if sse.Event == "server_error" {
			var se struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal([]byte(sse.Data), &se)
			return false, read, fmt.Errorf("%w: the server said: %s", errStreamBroken, se.Error)
		}
		read = true
		done, err := fn(sse)
		if done || err != nil {
			return done, read, err
		}
	}
}

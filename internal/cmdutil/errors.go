package cmdutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/partite-ai/partite-cli/internal/auth"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// Exit codes. Scripts and agents can rely on these.
const (
	ExitOK        = 0
	ExitError     = 1 // anything not listed below
	ExitUsage     = 2 // bad flags or arguments, or a confirmation was needed
	ExitAuth      = 3 // not logged in, or the login expired
	ExitForbidden = 4 // logged in, but missing a permission
	ExitNotFound  = 5 // the named resource does not exist
	ExitConflict  = 6 // the name is taken, or the change conflicts with current state

	// A mesh request that did not end with a response.
	ExitRequestFailed    = 7
	ExitRequestCancelled = 8
	ExitTaskPending      = 9  // waiting for a task to be completed
	ExitStillRunning     = 10 // still running when --timeout passed
	ExitCancelled        = 130
)

// Error is an error with an exit code and an optional hint for what to do next.
// Param names the input the server found the problem with, when it said.
// DecisionID is set when a boundary refused the action.
type Error struct {
	Code       int
	Msg        string
	Hint       string
	Param      string
	DecisionID string
}

func (e *Error) Error() string { return e.Msg }

func Errorf(code int, hint string, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...), Hint: hint}
}

func UsageError(format string, args ...any) *Error {
	return &Error{Code: ExitUsage, Msg: fmt.Sprintf(format, args...)}
}

func NotFound(kind, ref, listCmd string) *Error {
	return &Error{
		Code: ExitNotFound,
		Msg:  fmt.Sprintf("%s %q not found", kind, ref),
		Hint: "List them with: " + listCmd,
	}
}

// Classify turns any error into an *Error with the right exit code and hint.
func (a *App) Classify(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Code: ExitCancelled, Msg: "cancelled"}
	}
	if errors.Is(err, auth.ErrLoginExpired) {
		return &Error{Code: ExitAuth, Msg: fmt.Sprintf("your login to %s has expired", a.ContextName), Hint: a.loginHint()}
	}

	var he *graphql.HTTPError
	if errors.As(err, &he) {
		return a.classifyStatus(he.StatusCode, gqlBody(he))
	}
	var se *StatusError
	if errors.As(err, &se) {
		return a.classifyStatus(se.StatusCode, se.Body)
	}

	var gl gqlerror.List
	if errors.As(err, &gl) && len(gl) > 0 {
		msgs := make([]string, len(gl))
		for i, g := range gl {
			msgs[i] = g.Message
		}
		return classifyMessage(strings.Join(msgs, "; "))
	}
	return classifyMessage(err.Error())
}

func (a *App) classifyStatus(status int, body string) *Error {
	e := a.statusError(status, body)
	var p struct {
		Param      string `json:"param"`
		DecisionID string `json:"decision_id"`
	}
	_ = json.Unmarshal([]byte(body), &p)
	e.Param, e.DecisionID = p.Param, p.DecisionID
	switch {
	case e.DecisionID != "":
		e.Hint = "A boundary refused this. An admin can see why with: partite boundary decision " + e.DecisionID
	case e.Param != "" && e.Hint == "":
		e.Hint = "The problem is with: " + e.Param
	}
	return e
}

func (a *App) statusError(status int, body string) *Error {
	msg := serverMessage(body)
	switch status {
	case http.StatusUnauthorized:
		return &Error{Code: ExitAuth, Msg: fmt.Sprintf("not logged in to %s (%s)", a.ContextName, a.Context.URL), Hint: a.loginHint()}
	case http.StatusForbidden:
		return &Error{Code: ExitForbidden, Msg: orDefault(msg, "permission denied"), Hint: "Ask an admin of your organization for the permission, or use an API key that has it."}
	case http.StatusNotFound:
		return &Error{Code: ExitNotFound, Msg: orDefault(msg, "not found")}
	case http.StatusConflict:
		return &Error{Code: ExitConflict, Msg: orDefault(msg, "conflict")}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return &Error{Code: ExitUsage, Msg: orDefault(msg, "the server rejected the request")}
	}
	return &Error{Code: ExitError, Msg: fmt.Sprintf("server returned %d: %s", status, orDefault(msg, http.StatusText(status)))}
}

func classifyMessage(msg string) *Error {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "missing required scope"):
		return &Error{Code: ExitForbidden, Msg: msg, Hint: "Ask an admin of your organization for the permission, or use an API key that has it."}
	case strings.HasPrefix(lower, "unknown command") || strings.HasPrefix(lower, "unknown flag") ||
		strings.HasPrefix(lower, "unknown shorthand flag") || strings.HasPrefix(lower, "accepts ") ||
		strings.HasPrefix(lower, "requires at least") || strings.HasPrefix(lower, "required flag") ||
		strings.HasPrefix(lower, "if any flags in the group"):
		return &Error{Code: ExitUsage, Msg: msg, Hint: "See --help for usage."}
	case strings.Contains(lower, "invalid id"):
		return &Error{Code: ExitNotFound, Msg: msg, Hint: "Check the id, or use the name instead."}
	case strings.Contains(lower, "not found") || strings.Contains(lower, "no rows"):
		return &Error{Code: ExitNotFound, Msg: msg}
	case strings.Contains(lower, "already exists") || strings.Contains(lower, "duplicate") || strings.Contains(lower, "unique constraint") || strings.Contains(lower, "conflict"):
		return &Error{Code: ExitConflict, Msg: msg}
	}
	return &Error{Code: ExitError, Msg: msg}
}

func (a *App) loginHint() string {
	if a.Context == nil {
		return "Run: partite login"
	}
	if a.envCredentials() {
		return "Check PARTITE_API_TOKEN, or PARTITE_TOKEN."
	}
	if a.Context.Auth == "api-key" {
		return fmt.Sprintf(`Store an API key with: echo "$PARTITE_API_TOKEN" | partite --context %s login --with-api-key`, a.ContextName)
	}
	if a.ContextName == defaultContextName(a) {
		return "Run: partite login"
	}
	return "Run: partite login --context " + a.ContextName
}

func defaultContextName(a *App) string {
	if a.Config == nil {
		return ""
	}
	return a.Config.Current
}

func serverMessage(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	var parsed struct {
		Detail  string `json:"detail"`
		Title   string `json:"title"`
		Error   any    `json:"error"`
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal([]byte(body), &parsed) == nil {
		switch {
		case parsed.Detail != "":
			return parsed.Detail
		case parsed.Title != "":
			return parsed.Title
		case parsed.Message != "":
			return parsed.Message
		case len(parsed.Errors) > 0:
			return parsed.Errors[0].Message
		case parsed.Error != nil:
			if s, ok := parsed.Error.(string); ok {
				return s
			}
			if m, ok := parsed.Error.(map[string]any); ok {
				if s, ok := m["message"].(string); ok {
					return s
				}
			}
		}
	}
	if len(body) > 300 {
		body = body[:300] + "…"
	}
	return body
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// PrintError writes err to w, as JSON when the user asked for JSON output.
func (a *App) PrintError(w io.Writer, e *Error) {
	if a.jsonErrors() {
		fields := map[string]any{"message": e.Msg, "code": e.Code, "hint": e.Hint}
		if e.Param != "" {
			fields["param"] = e.Param
		}
		if e.DecisionID != "" {
			fields["decision_id"] = e.DecisionID
		}
		out := map[string]any{"error": fields}
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(out)
		return
	}
	fmt.Fprintf(w, "Error: %s\n", e.Msg)
	if e.Hint != "" {
		fmt.Fprintf(w, "%s\n", e.Hint)
	}
}

func gqlBody(he *graphql.HTTPError) string {
	if len(he.Response.Errors) > 0 {
		return he.Response.Errors[0].Message
	}
	return ""
}

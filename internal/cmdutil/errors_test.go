package cmdutil

import (
	"bytes"
	"strings"
	"testing"

	"github.com/partite-ai/partite-cli/internal/output"
)

func TestClassifyProblem(t *testing.T) {
	app := NewApp()
	body := `{"type":"about:blank","title":"Bad Request","status":400,"detail":"intent is required: the mesh has more than one","param":"intent"}`
	e := app.Classify(&StatusError{StatusCode: 400, Body: body})
	if e.Code != ExitUsage || e.Msg != "intent is required: the mesh has more than one" || e.Param != "intent" {
		t.Errorf("error = %+v", e)
	}
	if !strings.Contains(e.Hint, "intent") {
		t.Errorf("hint = %q", e.Hint)
	}

	e = app.Classify(&StatusError{StatusCode: 404, Body: `{"type":"about:blank","title":"Not Found","status":404,"detail":"conversation not found"}`})
	if e.Code != ExitNotFound || e.Msg != "conversation not found" || e.Param != "" || e.Hint != "" {
		t.Errorf("error = %+v", e)
	}

	var buf bytes.Buffer
	app.format = output.JSON
	app.PrintError(&buf, &Error{Code: ExitUsage, Msg: "bad", Param: "input"})
	if !strings.Contains(buf.String(), `"param":"input"`) {
		t.Errorf("JSON error = %s", buf.String())
	}
}

func TestClassifyBoundaryRefusal(t *testing.T) {
	app := NewApp()
	body := `{"type":"about:blank","title":"Forbidden","status":403,"detail":"Refunds need a person.","decision_id":"d_1","boundary":"no-refunds","rule":"deny-refunds"}`
	e := app.Classify(&StatusError{StatusCode: 403, Body: body})
	if e.Code != ExitForbidden || e.Msg != "Refunds need a person." || e.DecisionID != "d_1" {
		t.Errorf("error = %+v", e)
	}
	if !strings.Contains(e.Hint, "partite boundary decision d_1") {
		t.Errorf("hint = %q", e.Hint)
	}

	var buf bytes.Buffer
	app.format = output.JSON
	app.PrintError(&buf, e)
	if !strings.Contains(buf.String(), `"decision_id":"d_1"`) {
		t.Errorf("JSON error = %s", buf.String())
	}
}

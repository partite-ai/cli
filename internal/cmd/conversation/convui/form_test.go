package convui

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convapi"
)

// formTask is a task event with an a2ui presentation.
func formTask(t *testing.T, input, open, completed string) convapi.Task {
	t.Helper()
	content := `{"catalogId":"` + basicCatalogID + `","open":` + open
	if completed != "" {
		content += `,"completed":` + completed
	}
	content += "}"
	ev := `{"id":"rout_f","type":"task","data":{"kind":"client_task","response_schema":{"type":"object"},"client_task":{"name":"confirm","input":` + input +
		`,"presentations":[{"type":"a2ui","content":` + content + `}]}}}`
	return task(t, ev)
}

const confirmSurface = `{"components": [
  {"id": "root", "component": "Column", "children": ["question", "reason", "buttons"]},
  {"id": "question", "component": "Text", "text": {"path": "/question"}, "variant": "h3"},
  {"id": "reason", "component": "TextField", "label": "Reason", "value": {"path": "/reason"}},
  {"id": "buttons", "component": "Row", "children": ["yes", "no"]},
  {"id": "yes", "component": "Button", "child": "yes_label", "variant": "primary",
   "action": {"event": {"name": "confirm", "context": {"confirmed": true, "reason": {"path": "/reason"}}}}},
  {"id": "yes_label", "component": "Text", "text": "Yes"},
  {"id": "no", "component": "Button", "child": "no_label",
   "action": {"event": {"name": "reject", "context": {"confirmed": false}}}},
  {"id": "no_label", "component": "Text", "text": "No"}
]}`

func TestPromptTaskFillsInAForm(t *testing.T) {
	tk := formTask(t, `{"question":"Delete the old reports?"}`, confirmSurface, "")
	var out bytes.Buffer
	got, err := PromptTask(context.Background(), NewLineReader(strings.NewReader("Too old\n\n")), &out, Style{}, tk)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if want := `{"confirmed":true,"reason":"Too old"}`; string(got) != want {
		t.Errorf("answer = %s, want %s", got, want)
	}
	// A terminal echoes what is typed; here the answers do not show.
	want := "Delete the old reports?\nReason\n>   1) Yes  (Enter)\n  2) No\nnumber > "
	if out.String() != want {
		t.Errorf("prompt output:\n%s\nwant:\n%s", out.String(), want)
	}
}

const orderSurface = `{"components": [
  {"id": "root", "component": "Column", "children": ["title", "items", "size", "gift", "qty", "when", "send", "cancel"]},
  {"id": "title", "component": "Text", "variant": "h2",
   "text": {"call": "formatString", "args": {"value": "Order ${/order/id}"}, "returnType": "string"}},
  {"id": "items", "component": "List", "children": {"componentId": "item", "path": "/order/items"}},
  {"id": "item", "component": "Text",
   "text": {"call": "formatString", "args": {"value": "${name} x${count}"}, "returnType": "string"}},
  {"id": "size", "component": "ChoicePicker", "label": "Size", "value": {"path": "/size"},
   "options": [{"label": "Small", "value": "S"}, {"label": "Medium", "value": "M"}, {"label": "Large", "value": "L"}],
   "checks": [{"condition": {"call": "required", "args": {"value": {"path": "/size"}}}, "message": "Pick a size."}]},
  {"id": "gift", "component": "CheckBox", "label": "Gift wrap", "value": {"path": "/gift"}},
  {"id": "qty", "component": "Slider", "label": "Quantity", "min": 1, "max": 10, "value": {"path": "/qty"}},
  {"id": "when", "component": "DateTimeInput", "label": "Deliver on", "enableDate": true, "value": {"path": "/when"}},
  {"id": "send", "component": "Button", "child": "send_label", "variant": "primary",
   "checks": [{"condition": {"call": "required", "args": {"value": {"path": "/when"}}}, "message": "Pick a delivery date."}],
   "action": {"event": {"name": "order", "context": {"size": {"path": "/size"}, "gift": {"path": "/gift"}, "qty": {"path": "/qty"}, "when": {"path": "/when"}}}}},
  {"id": "send_label", "component": "Text", "text": "Order"},
  {"id": "cancel", "component": "Button", "child": "cancel_label", "action": {"event": {"name": "cancel", "context": {"cancelled": true}}}},
  {"id": "cancel_label", "component": "Text", "text": "Cancel"}
]}`

const orderInput = `{"order": {"id": "A-17", "items": [{"name": "Mug", "count": 2}, {"name": "Shirt", "count": 1}]}, "qty": 1, "gift": false}`

func TestPromptFormAsksAgainUntilTheChecksPass(t *testing.T) {
	tk := formTask(t, orderInput, orderSurface, "")
	lines := []string{
		"",                        // size: required
		"XL",                      // size: not a choice
		"2",                       // size: Medium
		"maybe",                   // gift: not yes or no
		"y",                       // gift
		"11",                      // quantity: out of range
		"",                        // quantity: keep 1
		"tomorrow",                // date: not a date
		"",                        // date: left empty
		"",                        // Order: the date check fails, so the form starts again
		"", "", "3", "2026-10-20", // the form again: keep size and gift wrap
		"1", // Order
	}
	var out bytes.Buffer
	got, err := PromptTask(context.Background(), NewLineReader(strings.NewReader(strings.Join(lines, "\n")+"\n")), &out, Style{}, tk)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if want := `{"gift":true,"qty":3,"size":["M"],"when":"2026-10-20"}`; string(got) != want {
		t.Errorf("answer = %s, want %s", got, want)
	}
	for _, msg := range []string{
		"Order A-17", "Mug x2", "Shirt x1", "  2) Medium",
		"Pick a size.", `"XL" is not one of the choices`, "answer yes or no",
		"pick a number from 1 to 10", "type it as YYYY-MM-DD", "Pick a delivery date.",
		"Size (pick a number) [M]", "Gift wrap [Y/n]", "Quantity (1 to 10) [1]", "Deliver on (YYYY-MM-DD)",
	} {
		if !strings.Contains(out.String(), msg) {
			t.Errorf("prompt output has no %q:\n%s", msg, out.String())
		}
	}
}

func TestDescribeTaskLeavesFormsAlone(t *testing.T) {
	var buf bytes.Buffer
	DescribeTask(&buf, Style{}, "rcnv_1", "rreq_1", formTask(t, orderInput, orderSurface, ""), true)
	if strings.Contains(buf.String(), "Gift wrap") || !strings.Contains(buf.String(), "Its answer must match this JSON schema") {
		t.Errorf("want the task as sent, without its form:\n%s", buf.String())
	}
}

func TestRendererDrawsTheCompletedFormOnlyAtATerminal(t *testing.T) {
	completed := `{"components": [
	  {"id": "root", "component": "Row", "children": ["icon", "said"]},
	  {"id": "icon", "component": "Icon", "name": "check"},
	  {"id": "said", "component": "Text", "text": {"call": "formatString", "args": {"value": "Confirmed: ${/reason}"}}}
	]}`
	tk := formTask(t, `{"question":"Delete?"}`, confirmSurface, completed)
	data, _ := json.Marshal(tk.TaskData)
	for _, tt := range []struct {
		drawForms bool
		want      string
	}{
		{true, "? Delete?\n  Confirmed: Too old\n"},
		{false, "? Delete?\n  ↳ {\"confirmed\":true,\"reason\":\"Too old\"}\n"},
	} {
		var out bytes.Buffer
		r := &Renderer{Out: &out, DrawForms: tt.drawForms}
		for _, ev := range []convapi.Event{
			{ID: "rout_f", Type: "task", Data: data},
			{ID: "rout_g", Type: "task_response", Data: json.RawMessage(`{"confirmed":true,"reason":"Too old"}`), InReplyTo: "rout_f"},
		} {
			if err := r.Event(ev); err != nil {
				t.Fatal(err)
			}
		}
		if out.String() != tt.want {
			t.Errorf("DrawForms %v: got:\n%s\nwant:\n%s", tt.drawForms, out.String(), tt.want)
		}
	}
}

func TestOnlyBasicCatalogFormsAreDrawn(t *testing.T) {
	tk := formTask(t, `{}`, confirmSurface, "")
	tk.ClientTask.Presentations = json.RawMessage(strings.Replace(string(tk.ClientTask.Presentations), basicCatalogID, "https://example.com/catalog.json", 1))
	if _, _, ok := taskForm(tk); ok {
		t.Error("a form from another catalog should fall back to the schema prompts")
	}
}

func TestFormFunctions(t *testing.T) {
	f := newForm(&formSurface{}, json.RawMessage(`{"n": 1234.5, "when": "2026-10-16T09:05:00Z", "items": [1, 2]}`))
	call := func(name, args string) any {
		var a map[string]any
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			t.Fatal(err)
		}
		return f.call(name, a, "")
	}
	tests := []struct {
		name, args string
		want       any
	}{
		{"formatNumber", `{"value": {"path": "/n"}}`, "1,234.5"},
		{"formatNumber", `{"value": 1234.5, "decimals": 0, "grouping": false}`, "1234"},
		{"formatCurrency", `{"value": {"path": "/n"}, "currency": "USD"}`, "$1,234.50"},
		{"formatCurrency", `{"value": -3, "currency": "CHF"}`, "CHF -3.00"},
		{"formatDate", `{"value": {"path": "/when"}, "format": "EEE, MMM d 'at' HH:mm"}`, "Fri, Oct 16 at 09:05"},
		{"formatDate", `{"value": "not a date", "format": "yyyy"}`, "not a date"},
		{"pluralize", `{"value": 1, "one": "item", "other": "items"}`, "item"},
		{"pluralize", `{"value": 0, "one": "item", "other": "items"}`, "items"},
		{"length", `{"value": {"path": "/items"}, "min": 2}`, true},
		{"numeric", `{"value": "7", "max": 5}`, false},
		{"email", `{"value": "ada@example.com"}`, true},
		{"and", `{"values": [true, {"call": "not", "args": {"value": false}}]}`, true},
		{"or", `{"values": [false, false]}`, false},
		{"formatString", `{"value": "\\${kept} ${/n} ${now()}"}`, "${kept} 1234.5 ${now()}"},
	}
	for _, tt := range tests {
		if got := call(tt.name, tt.args); got != tt.want {
			t.Errorf("%s(%s) = %#v, want %#v", tt.name, tt.args, got, tt.want)
		}
	}
}

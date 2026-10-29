package a2ui

import (
	"encoding/json"
	"strings"
	"testing"
)

const greetingCatalog = `{
  "catalogId": "https://example.com/greeting.json",
  "components": {
    "Greeting": {
      "type": "object",
      "properties": {
        "id": {"type": "string"},
        "component": {"const": "Greeting"},
        "name": {"type": "string"},
        "action": {"type": "object"}
      },
      "required": ["id", "component", "name"],
      "additionalProperties": false
    }
  }
}`

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		name, form, wantErr string
	}{
		{
			name: "basic",
			form: `{"open": {"components": [
				{"id": "root", "component": "Button", "child": "label", "action": {"event": {"name": "ok"}}},
				{"id": "label", "component": "Text", "text": "OK"}]}}`,
		},
		{
			name: "custom catalog",
			form: `{"catalog": ` + greetingCatalog + `, "open": {"components": [
				{"id": "root", "component": "Greeting", "name": "Ann", "action": {"event": {"name": "ok"}}}]}}`,
		},
		{
			name: "basic component in a custom catalog",
			form: `{"catalog": ` + greetingCatalog + `, "open": {"components": [
				{"id": "root", "component": "Text", "text": "hi"}]}}`,
			wantErr: `the catalog has no component "Text"`,
		},
		{
			name: "property the catalog lacks",
			form: `{"catalog": ` + greetingCatalog + `, "open": {"components": [
				{"id": "root", "component": "Greeting", "name": "Ann", "colour": "red"}]}}`,
			wantErr: `Greeting has no property "colour"`,
		},
		{
			name:    "catalog with an outside ref",
			form:    `{"catalog": {"catalogId": "x", "components": {"A": {"$ref": "https://example.com/a.json"}}}, "open": {"components": []}}`,
			wantErr: "a catalog must stand alone",
		},
		{
			name: "wrong type",
			form: `{"open": {"components": [
				{"id": "root", "component": "Button", "child": "label", "action": {"event": {"name": "ok"}}},
				{"id": "label", "component": "Text", "text": 5}]}}`,
			wantErr: `component "label": at /text`,
		},
		{
			name:    "no root",
			form:    `{"open": {"components": [{"id": "top", "component": "Text", "text": "hi"}]}}`,
			wantErr: `no component with id "root"`,
		},
		{
			name:    "nothing completes the task",
			form:    `{"open": {"components": [{"id": "root", "component": "Text", "text": "hi"}]}}`,
			wantErr: "nothing can complete the task",
		},
		{
			name: "event action once completed",
			form: `{"open": {"components": [{"id": "root", "component": "Button", "child": "root", "action": {"event": {"name": "ok"}}}]},
				"completed": {"components": [{"id": "root", "component": "Button", "child": "root", "action": {"event": {"name": "again"}}}]}}`,
			wantErr: `has event action "again"`,
		},
		{
			name: "overlay of no component",
			form: `{"open": {"components": [{"id": "root", "component": "Button", "child": "root", "action": {"event": {"name": "ok"}}}],
				"overlays": {"nope": {"/text": "input.x"}}}}`,
			wantErr: `"nope" is not a component`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var f Form
			if err := json.Unmarshal([]byte(tc.form), &f); err != nil {
				t.Fatal(err)
			}
			err := f.Check()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Check() = %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("Check() = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// Every component of the basic catalog gets a description of its properties.
func TestBasicCatalogComponents(t *testing.T) {
	b, err := BasicCatalogComponents()
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Components) != 18 {
		t.Errorf("%d components", len(b.Components))
	}
	var common []string
	for _, p := range b.Common {
		common = append(common, p.Name)
	}
	if strings.Join(common, ",") != "accessibility,id,weight" {
		t.Errorf("common properties = %v", common)
	}
}

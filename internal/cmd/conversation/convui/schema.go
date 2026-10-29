package convui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Schema is the part of a JSON schema the CLI reads to describe inputs and
// build task forms.
type Schema struct {
	Type        any               `json:"type"`
	Description string            `json:"description"`
	Enum        []any             `json:"enum"`
	Items       *Schema           `json:"items"`
	Properties  map[string]Schema `json:"properties"`
	Required    []string          `json:"required"`
	Default     any               `json:"default"`
}

// Property is one property of an object schema.
type Property struct {
	Name     string
	Schema   Schema
	Required bool
}

func ParseSchema(raw json.RawMessage) (Schema, error) {
	var s Schema
	if len(raw) == 0 || string(raw) == "null" {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("read schema: %w", err)
	}
	return s, nil
}

// TypeName is the schema's type, ignoring null: "string", "integer",
// "array" and so on, or "" when it has none.
func (s Schema) TypeName() string {
	switch t := s.Type.(type) {
	case string:
		return t
	case []any:
		for _, e := range t {
			if name, ok := e.(string); ok && name != "null" {
				return name
			}
		}
	}
	if len(s.Properties) > 0 {
		return "object"
	}
	return ""
}

// Describe says what values the schema takes, such as "string" or
// "array of string, one or more of: a, b".
func (s Schema) Describe() string {
	t := s.TypeName()
	if t == "" {
		t = "any"
	}
	if t == "array" && s.Items != nil {
		t = "array of " + s.Items.Describe()
	}
	if len(s.Enum) > 0 {
		vals := make([]string, len(s.Enum))
		for i, v := range s.Enum {
			vals[i] = fmt.Sprint(v)
		}
		t += ", one of: " + strings.Join(vals, ", ")
	}
	return t
}

// OrderedProperties returns an object schema's properties in the order the schema
// lists them. raw is the schema s was parsed from.
func (s Schema) OrderedProperties(raw json.RawMessage) []Property {
	required := map[string]bool{}
	for _, r := range s.Required {
		required[r] = true
	}
	var out []Property
	for _, name := range propertyOrder(raw) {
		if p, ok := s.Properties[name]; ok {
			out = append(out, Property{Name: name, Schema: p, Required: required[name]})
		}
	}
	return out
}

// propertyOrder returns the keys of raw's "properties" object in order.
// encoding/json maps lose the order, and a form should follow the schema.
func propertyOrder(raw json.RawMessage) []string {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(top["properties"]))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return keys
		}
		key, _ := tok.(string)
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return keys
		}
	}
	return keys
}

// TextProperty returns the property that plain text fills: the input's only
// required property when it is a string, or its only property when nothing
// is required. The server's AG-UI endpoint uses the same rule.
func TextProperty(raw json.RawMessage) (string, bool) {
	s, err := ParseSchema(raw)
	if err != nil {
		return "", false
	}
	props := s.OrderedProperties(raw)
	var found *Property
	for i := range props {
		if props[i].Required {
			if found != nil {
				return "", false
			}
			found = &props[i]
		}
	}
	if found == nil && len(props) == 1 {
		found = &props[0]
	}
	if found == nil || found.Schema.TypeName() != "string" {
		return "", false
	}
	return found.Name, true
}

// RequiredNames lists the schema's required properties.
func RequiredNames(raw json.RawMessage) []string {
	s, err := ParseSchema(raw)
	if err != nil {
		return nil
	}
	return s.Required
}

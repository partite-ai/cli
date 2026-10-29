// Package a2ui checks A2UI form definitions the way mesh does when a task
// definition is saved, so a mistake shows up at plan time. It does not check
// what needs the task's schemas: overlay expressions and action contexts.
package a2ui

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

// basicCatalogJSON is a copy of mesh's A2UI v0.9 basic catalog, with the
// common types merged into its $defs.
//
//go:embed basic_catalog.json
var basicCatalogJSON []byte

// BasicCatalog returns the parsed basic catalog.
var BasicCatalog = sync.OnceValues(func() (*Catalog, error) {
	return ParseCatalog(basicCatalogJSON)
})

// Catalog is an A2UI catalog definition: a JSON Schema for each component a
// surface can have.
type Catalog struct {
	// document is the catalog definition as decoded JSON. Its refs point into
	// it.
	document map[string]any
	// components are the schemas of the catalog's components, by name.
	components map[string]*jsonschema.Resolved
}

// ParseCatalog reads a catalog definition. The catalog must stand alone: each
// of its refs points into it.
func ParseCatalog(raw []byte) (*Catalog, error) {
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("not an A2UI catalog: %w", err)
	}
	if id, _ := document["catalogId"].(string); id == "" {
		return nil, errors.New("the catalog has no catalogId")
	}
	if err := checkLocalRefs(document); err != nil {
		return nil, err
	}
	components, ok := document["components"].(map[string]any)
	if !ok {
		return nil, errors.New("the catalog has no components")
	}

	// jsonschema-go follows a ref only through schema keywords, so the
	// components and functions move into $defs, and the refs to them follow.
	defs := map[string]any{}
	if d, ok := document["$defs"].(map[string]any); ok {
		maps.Copy(defs, d)
	}
	for _, section := range []string{"components", "functions"} {
		entries, _ := document[section].(map[string]any)
		for name, s := range entries {
			key := section + ":" + name
			if _, ok := defs[key]; ok {
				return nil, fmt.Errorf("$defs has %q, which mesh uses for %s %q", key, section, name)
			}
			defs[key] = s
		}
	}
	defs, _ = relinkRefs(defs).(map[string]any)

	c := &Catalog{document: document, components: make(map[string]*jsonschema.Resolved, len(components))}
	for name := range components {
		root := map[string]any{"$defs": defs, "$ref": "#/$defs/" + escapePointerToken("components:"+name)}
		if s, ok := document["$schema"]; ok {
			root["$schema"] = s
		}
		b, err := json.Marshal(root)
		if err != nil {
			return nil, err
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(b, &schema); err != nil {
			return nil, fmt.Errorf("component %q: %w", name, err)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			return nil, fmt.Errorf("component %q: %w", name, err)
		}
		c.components[name] = resolved
	}
	return c, nil
}

// HasComponent reports whether the catalog has a component type.
func (c *Catalog) HasComponent(name string) bool {
	_, ok := c.components[name]
	return ok
}

// ValidateComponent checks a component against the catalog's schema for its
// type.
func (c *Catalog) ValidateComponent(component map[string]any) error {
	name, _ := component["component"].(string)
	schema, ok := c.components[name]
	if !ok {
		return fmt.Errorf("the catalog has no component %q", name)
	}
	// The validator does not name a property the schema does not allow.
	if properties, closed := c.componentProperties(name); closed {
		for _, key := range slices.Sorted(maps.Keys(component)) {
			if !properties[key] {
				return fmt.Errorf("%s has no property %q", name, key)
			}
		}
	}
	// The validator wants the JSON numbers as float64.
	b, err := json.Marshal(component)
	if err != nil {
		return err
	}
	var instance map[string]any
	if err := json.Unmarshal(b, &instance); err != nil {
		return err
	}
	if err := schema.Validate(instance); err != nil {
		return schemaError(err)
	}
	return nil
}

// componentProperties returns the properties the catalog gives a component,
// and whether the component can have no others.
func (c *Catalog) componentProperties(component string) (map[string]bool, bool) {
	components, _ := c.document["components"].(map[string]any)
	s, ok := components[component].(map[string]any)
	if !ok {
		return nil, false
	}
	schemas := c.expandSchema(s, 0)
	closed := schemas[0]["unevaluatedProperties"] == false || schemas[0]["additionalProperties"] == false
	properties := map[string]bool{}
	for _, s := range schemas {
		p, _ := s["properties"].(map[string]any)
		for key := range p {
			properties[key] = true
		}
	}
	return properties, closed
}

// maxSchemaDepth bounds how far expandSchema follows refs, for catalogs whose
// refs loop.
const maxSchemaDepth = 32

// expandSchema returns a schema and the schemas it is made of: the targets of
// its refs and its allOf.
func (c *Catalog) expandSchema(s map[string]any, depth int) []map[string]any {
	if depth > maxSchemaDepth {
		return nil
	}
	out := []map[string]any{s}
	if ref, ok := s["$ref"].(string); ok {
		if target, ok := c.resolveRef(ref); ok {
			out = append(out, c.expandSchema(target, depth+1)...)
		}
	}
	members, _ := s["allOf"].([]any)
	for _, m := range members {
		if m, ok := m.(map[string]any); ok {
			out = append(out, c.expandSchema(m, depth+1)...)
		}
	}
	return out
}

// resolveRef returns the schema a ref in the catalog points at.
func (c *Catalog) resolveRef(ref string) (map[string]any, bool) {
	var v any = c.document
	if ref != "#" {
		tokens, ok := pointerTokens(strings.TrimPrefix(ref, "#"))
		if !ok {
			return nil, false
		}
		for _, token := range tokens {
			if v, ok = pointerChild(v, token); !ok {
				return nil, false
			}
		}
	}
	s, ok := v.(map[string]any)
	return s, ok
}

// checkLocalRefs checks that each ref in a catalog points into the catalog.
func checkLocalRefs(value any) error {
	switch v := value.(type) {
	case map[string]any:
		for k, child := range v {
			if ref, ok := child.(string); ok && k == "$ref" && !strings.HasPrefix(ref, "#") {
				return fmt.Errorf("the catalog refers to %q; a catalog must stand alone", ref)
			}
			if err := checkLocalRefs(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := checkLocalRefs(child); err != nil {
				return err
			}
		}
	}
	return nil
}

// relinkRefs returns a copy of a schema whose refs to #/components/<name> and
// #/functions/<name> point at their places in $defs.
func relinkRefs(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			if ref, ok := child.(string); ok && k == "$ref" {
				for _, section := range []string{"components", "functions"} {
					if rest, ok := strings.CutPrefix(ref, "#/"+section+"/"); ok {
						name, path, _ := strings.Cut(rest, "/")
						ref = "#/$defs/" + escapePointerToken(section+":"+unescapePointerToken(name))
						if path != "" {
							ref += "/" + path
						}
					}
				}
				out[k] = ref
				continue
			}
			out[k] = relinkRefs(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = relinkRefs(child)
		}
		return out
	}
	return value
}

// schemaError rewrites a jsonschema-go validation error, which names the
// schema locations it went through, to name the place in the component and
// the reason: "at /text: is not a valid DynamicString".
func schemaError(err error) error {
	rest := strings.TrimPrefix(err.Error(), "validating root: ")
	var path []string
	var previous, definition string
	for {
		after, ok := strings.CutPrefix(rest, "validating ")
		if !ok {
			break
		}
		location, reason, ok := strings.Cut(after, ": ")
		if !ok {
			break
		}
		rest = reason
		step := location
		if strings.HasPrefix(location, previous+"/") {
			step = location[len(previous):]
		} else if name, ok := strings.CutPrefix(location, "/$defs/"); ok && !strings.Contains(name, "/") {
			definition = name
		}
		previous = location
		tokens, _ := pointerTokens(step)
		for i, token := range tokens {
			switch {
			case token == "items":
				path = append(path, "*")
			case token == "properties" && i+1 < len(tokens):
				path = append(path, tokens[i+1])
			}
		}
	}
	reason := rest
	if definition != "" && !strings.HasPrefix(definition, "components:") &&
		(strings.HasPrefix(reason, "oneOf:") || strings.HasPrefix(reason, "anyOf:")) {
		reason = "is not a valid " + definition
	}
	if len(path) == 0 {
		return errors.New(reason)
	}
	return fmt.Errorf("at /%s: %s", strings.Join(path, "/"), reason)
}

func pointerTokens(pointer string) ([]string, bool) {
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	tokens := strings.Split(pointer[1:], "/")
	for i, token := range tokens {
		tokens[i] = unescapePointerToken(token)
	}
	return tokens, true
}

func pointerChild(parent any, token string) (any, bool) {
	switch p := parent.(type) {
	case map[string]any:
		v, ok := p[token]
		return v, ok
	case []any:
		i, err := strconv.Atoi(token)
		if err != nil || i < 0 || i >= len(p) {
			return nil, false
		}
		return p[i], true
	}
	return nil, false
}

func escapePointerToken(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}

func unescapePointerToken(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
}

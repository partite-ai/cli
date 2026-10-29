package a2ui

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode"
)

// Kind is the shape a property of a basic catalog component has.
type Kind int

const (
	// KindString is a string, such as a component ID or an enum.
	KindString Kind = iota
	KindNumber
	KindBool
	KindStringList
	// KindDynamicString is a string, a binding to a path in the data model,
	// or a function call. So are the other dynamic kinds, for their types.
	KindDynamicString
	KindDynamicNumber
	KindDynamicBool
	KindDynamicStringList
	// KindChildList is a list of component IDs, or a template that makes a
	// child for each item of a list in the data model.
	KindChildList
	// KindIconName is the name of an icon, an SVG path, or a binding.
	KindIconName
	// KindAction is an event the task completes with, or a function call.
	KindAction
	// KindObject is an object with properties of its own.
	KindObject
	// KindObjectList is a list of objects with properties of their own.
	KindObjectList
)

// Property is a property of a basic catalog component, or of an object in
// one.
type Property struct {
	// JSONName is the property's name in the component.
	JSONName string
	// Name is JSONName in snake case.
	Name        string
	Kind        Kind
	Required    bool
	Enum        []string
	Description string
	// Properties are the properties of a KindObject or KindObjectList.
	Properties []Property
}

// Component is a component type of the basic catalog.
type Component struct {
	// JSONName is the component type, such as "TextField".
	JSONName string
	// Name is the type in snake case, such as "text_field".
	Name        string
	Description string
	// Properties are the component's own properties, without the common ones.
	Properties []Property
}

// BasicComponents describes the basic catalog's components. Common holds the
// properties every component has, such as id. Components holds each
// component's other properties, in the catalog's order.
type BasicComponents struct {
	Common     []Property
	Components []Component
}

// BasicCatalogComponents returns the basic catalog's components, as derived
// from the catalog itself.
var BasicCatalogComponents = sync.OnceValues(func() (*BasicComponents, error) {
	c, err := BasicCatalog()
	if err != nil {
		return nil, err
	}
	return c.describeComponents()
})

// dynamicKinds are the kinds of the catalog's $defs that hold a literal, a
// binding or a function call.
var dynamicKinds = map[string]Kind{
	"#/$defs/DynamicString":     KindDynamicString,
	"#/$defs/DynamicNumber":     KindDynamicNumber,
	"#/$defs/DynamicBoolean":    KindDynamicBool,
	"#/$defs/DynamicStringList": KindDynamicStringList,
	"#/$defs/ChildList":         KindChildList,
	"#/$defs/Action":            KindAction,
	"#/$defs/ComponentId":       KindString,
}

func (c *Catalog) describeComponents() (*BasicComponents, error) {
	anyComponent, ok := c.resolveRef("#/$defs/anyComponent")
	if !ok {
		return nil, fmt.Errorf("the catalog has no anyComponent")
	}
	refs, _ := anyComponent["oneOf"].([]any)

	var components []Component
	for _, r := range refs {
		ref, _ := r.(map[string]any)["$ref"].(string)
		name, ok := strings.CutPrefix(ref, "#/components/")
		if !ok {
			return nil, fmt.Errorf("anyComponent refers to %q", ref)
		}
		s, _ := c.resolveRef(ref)
		properties, required := c.objectProperties(s)
		delete(properties, "component")
		component := Component{JSONName: name, Name: snakeCase(name), Description: describe(s)}
		parts, _ := s["allOf"].([]any)
		for _, part := range parts {
			if part, ok := part.(map[string]any); ok && component.Description == "" {
				component.Description = describe(part)
			}
		}
		for _, key := range sortedKeys(properties) {
			p, err := c.describeProperty(key, properties[key], slices.Contains(required, key))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			component.Properties = append(component.Properties, p)
		}
		components = append(components, component)
	}

	// A property every component has the same way is common.
	out := &BasicComponents{}
	for _, p := range components[0].Properties {
		common := true
		for _, other := range components[1:] {
			if !slices.ContainsFunc(other.Properties, func(q Property) bool {
				return q.JSONName == p.JSONName && q.Kind == p.Kind && q.Required == p.Required
			}) {
				common = false
				break
			}
		}
		if common {
			out.Common = append(out.Common, p)
		}
	}
	for _, component := range components {
		component.Properties = slices.DeleteFunc(component.Properties, func(p Property) bool {
			return slices.ContainsFunc(out.Common, func(q Property) bool { return q.JSONName == p.JSONName })
		})
		out.Components = append(out.Components, component)
	}
	return out, nil
}

// objectProperties returns the properties of an object schema and its allOf
// and refs, and the names of the required ones.
func (c *Catalog) objectProperties(s map[string]any) (map[string]map[string]any, []string) {
	properties := map[string]map[string]any{}
	var required []string
	for _, part := range c.expandSchema(s, 0) {
		p, _ := part["properties"].(map[string]any)
		for key, v := range p {
			if v, ok := v.(map[string]any); ok {
				if existing, ok := properties[key]; ok && describe(existing) != "" && describe(v) == "" {
					continue
				}
				properties[key] = v
			}
		}
		r, _ := part["required"].([]any)
		for _, key := range r {
			if key, ok := key.(string); ok && !slices.Contains(required, key) {
				required = append(required, key)
			}
		}
	}
	return properties, required
}

func (c *Catalog) describeProperty(name string, s map[string]any, required bool) (Property, error) {
	p := Property{JSONName: name, Name: snakeCase(name), Required: required, Description: describe(s)}
	if err := c.describeKind(&p, s); err != nil {
		return p, fmt.Errorf("%s: %w", name, err)
	}
	return p, nil
}

func (c *Catalog) describeKind(p *Property, s map[string]any) error {
	if ref, ok := s["$ref"].(string); ok {
		if kind, ok := dynamicKinds[ref]; ok {
			p.Kind = kind
			if target, ok := c.resolveRef(ref); ok && p.Description == "" && kind == KindString {
				p.Description = describe(target)
			}
			return nil
		}
		target, ok := c.resolveRef(ref)
		if !ok {
			return fmt.Errorf("unresolved ref %q", ref)
		}
		if p.Description == "" {
			p.Description = describe(target)
		}
		return c.describeKind(p, target)
	}
	// A value that is one of the kinds above, narrowed, such as a date.
	if all, ok := s["allOf"].([]any); ok && len(all) > 0 {
		if first, ok := all[0].(map[string]any); ok {
			if _, ok := first["$ref"]; ok {
				return c.describeKind(p, first)
			}
		}
	}
	if one, ok := s["oneOf"].([]any); ok {
		for _, branch := range one {
			if b, ok := branch.(map[string]any); ok {
				if props, ok := b["properties"].(map[string]any); ok && props["svgPath"] != nil {
					p.Kind = KindIconName
					first, _ := one[0].(map[string]any)
					p.Enum = enumOf(first)
					return nil
				}
			}
		}
		return fmt.Errorf("a oneOf the provider does not know")
	}
	switch s["type"] {
	case "string":
		p.Kind = KindString
		p.Enum = enumOf(s)
	case "number", "integer":
		p.Kind = KindNumber
	case "boolean":
		p.Kind = KindBool
	case "array":
		items, _ := s["items"].(map[string]any)
		if ref, ok := items["$ref"].(string); ok {
			target, ok := c.resolveRef(ref)
			if !ok {
				return fmt.Errorf("unresolved ref %q", ref)
			}
			items = target
		}
		switch items["type"] {
		case "string":
			p.Kind = KindStringList
		case "object":
			p.Kind = KindObjectList
			return c.describeObject(p, items)
		default:
			return fmt.Errorf("a list of %v", items["type"])
		}
	case "object":
		p.Kind = KindObject
		return c.describeObject(p, s)
	default:
		return fmt.Errorf("type %v", s["type"])
	}
	return nil
}

func (c *Catalog) describeObject(p *Property, s map[string]any) error {
	properties, required := c.objectProperties(s)
	for _, key := range sortedKeys(properties) {
		child, err := c.describeProperty(key, properties[key], slices.Contains(required, key))
		if err != nil {
			return err
		}
		p.Properties = append(p.Properties, child)
	}
	return nil
}

// BlockName is the name of a KindObjectList property's blocks: its name in
// the singular, such as "tab" for "tabs".
func (p Property) BlockName() string {
	return strings.TrimSuffix(p.Name, "s")
}

func describe(s map[string]any) string {
	d, _ := s["description"].(string)
	return d
}

func enumOf(s map[string]any) []string {
	values, _ := s["enum"].([]any)
	var out []string
	for _, v := range values {
		if v, ok := v.(string); ok {
			out = append(out, v)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// snakeCase turns "validationRegexp" or "TextField" into "validation_regexp"
// or "text_field".
func snakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

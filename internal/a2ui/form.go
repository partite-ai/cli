package a2ui

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Form is the definition of an A2UI task form: a surface for each state of
// the task, and the catalog their components come from. Without a catalog,
// they come from the basic catalog.
type Form struct {
	Catalog   json.RawMessage `json:"catalog,omitempty"`
	Open      *Surface        `json:"open"`
	Completed *Surface        `json:"completed,omitempty"`
}

// Surface is the components of a form in one state of its task, and the
// overlays that set values of them from the task input. Overlays is keyed by
// component ID, then by a JSON pointer into the component, and holds CEL
// expressions.
type Surface struct {
	Components []map[string]any             `json:"components"`
	Overlays   map[string]map[string]string `json:"overlays,omitempty"`
}

// Check checks a form the way mesh does when it is saved, short of what needs
// the task's input and output schemas.
func (f *Form) Check() error {
	if f.Open == nil {
		return errors.New("no open surface")
	}
	catalog, err := BasicCatalog()
	if len(f.Catalog) > 0 {
		catalog, err = ParseCatalog(f.Catalog)
		if err != nil {
			return fmt.Errorf("catalog: %w", err)
		}
	}
	if err != nil {
		return err
	}

	if err := checkSurface(f.Open, catalog); err != nil {
		return fmt.Errorf("open surface: %w", err)
	}
	if !hasEventAction(f.Open) {
		return errors.New("open surface: no component has an event action, so nothing can complete the task")
	}
	if f.Completed != nil {
		if err := checkSurface(f.Completed, catalog); err != nil {
			return fmt.Errorf("completed surface: %w", err)
		}
		for _, c := range f.Completed.Components {
			if name, ok := eventActionName(c); ok {
				return fmt.Errorf("completed surface: component %q has event action %q, but the task is already completed", c["id"], name)
			}
		}
	}
	return nil
}

// checkSurface checks the components of a surface: their IDs, their children,
// each against the catalog, and that each overlay names one of them.
func checkSurface(s *Surface, catalog *Catalog) error {
	if len(s.Components) == 0 {
		return errors.New("no components")
	}
	ids := make(map[string]bool, len(s.Components))
	for i, c := range s.Components {
		id, _ := c["id"].(string)
		if id == "" {
			return fmt.Errorf("component %d has no id", i)
		}
		if ids[id] {
			return fmt.Errorf("component id %q is used more than once", id)
		}
		ids[id] = true
		if name, _ := c["component"].(string); name == "" {
			return fmt.Errorf("component %q has no component type", id)
		}
		if err := catalog.ValidateComponent(c); err != nil {
			return fmt.Errorf("component %q: %w", id, err)
		}
	}
	if !ids["root"] {
		return errors.New(`no component with id "root"`)
	}
	for _, c := range s.Components {
		for _, child := range childIDs(c) {
			if !ids[child] {
				return fmt.Errorf("component %q has child %q, which is not a component", c["id"], child)
			}
		}
	}
	for id, values := range s.Overlays {
		if !ids[id] {
			return fmt.Errorf("overlays: %q is not a component", id)
		}
		for pointer := range values {
			if _, ok := pointerTokens(pointer); !ok {
				return fmt.Errorf("overlay %q of component %q: a pointer starts with /", pointer, id)
			}
		}
	}
	return nil
}

func hasEventAction(s *Surface) bool {
	for _, c := range s.Components {
		if _, ok := eventActionName(c); ok {
			return true
		}
	}
	return false
}

// eventActionName returns the name of a component's event action.
func eventActionName(c map[string]any) (string, bool) {
	action, ok := c["action"].(map[string]any)
	if !ok {
		return "", false
	}
	event, ok := action["event"].(map[string]any)
	if !ok {
		return "", false
	}
	name, _ := event["name"].(string)
	return name, true
}

// childIDs returns the IDs of the components a component holds: its child,
// its children, and the component of a children template.
func childIDs(c map[string]any) []string {
	var out []string
	if child, ok := c["child"].(string); ok {
		out = append(out, child)
	}
	switch children := c["children"].(type) {
	case []any:
		for _, child := range children {
			if s, ok := child.(string); ok {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, children...)
	case map[string]any:
		if s, ok := children["componentId"].(string); ok {
			out = append(out, s)
		}
	}
	return out
}

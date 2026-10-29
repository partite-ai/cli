package cmdutil

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var idPattern = regexp.MustCompile(`^[a-z]{2,5}_[1-9A-HJ-NP-Za-km-z]+$`)

// IsID is true when ref looks like an id with the given prefix, such as
// cagt_... for an agent.
func IsID(ref, prefix string) bool {
	return strings.HasPrefix(ref, prefix+"_") && idPattern.MatchString(ref)
}

// Named is one candidate when resolving a name.
type Named struct {
	ID   string
	Name string
}

// Resolver looks up a resource by name and returns every candidate it finds.
// It may return near matches; Resolve keeps only exact ones.
type Resolver func(ctx context.Context, name string) ([]Named, error)

// Resolve turns a name or id into an id. kind is used in messages, such as
// "agent"; command is the CLI command group, such as "agent".
func Resolve(ctx context.Context, ref, prefix, kind, command string, find Resolver) (string, error) {
	if ref == "" {
		return "", UsageError("%s name or id is required", kind)
	}
	if IsID(ref, prefix) {
		return ref, nil
	}
	candidates, err := find(ctx, ref)
	if err != nil {
		return "", err
	}
	var exact []Named
	for _, c := range candidates {
		if c.Name == ref {
			exact = append(exact, c)
		}
	}
	if len(exact) == 0 {
		for _, c := range candidates {
			if strings.EqualFold(c.Name, ref) {
				exact = append(exact, c)
			}
		}
	}
	switch len(exact) {
	case 0:
		return "", NotFound(kind, ref, "partite "+command+" list")
	case 1:
		return exact[0].ID, nil
	}
	ids := make([]string, len(exact))
	for i, c := range exact {
		ids[i] = c.ID
	}
	return "", &Error{
		Code: ExitUsage,
		Msg:  fmt.Sprintf("%s are named %q", Plural(len(exact), kind), ref),
		Hint: "Use the id instead: " + strings.Join(ids, ", "),
	}
}

// Page is one page of a list query.
type Page[T any] struct {
	Items     []T
	EndCursor *string
	HasNext   bool
}

// ListAll fetches pages until it has limit items or there are no more. A limit
// of 0 or less fetches everything.
func ListAll[T any](ctx context.Context, limit int, fetch func(ctx context.Context, first int, after *string) (Page[T], error)) ([]T, error) {
	const pageSize = 100
	var all []T
	var after *string
	for {
		first := pageSize
		if limit > 0 {
			first = min(pageSize, limit-len(all))
		}
		page, err := fetch(ctx, first, after)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if !page.HasNext || page.EndCursor == nil || (limit > 0 && len(all) >= limit) || len(page.Items) == 0 {
			return all, nil
		}
		after = page.EndCursor
	}
}

// ReadSpec reads a YAML or JSON file into v, which should have json tags.
// A path of "-" reads standard input. Unknown fields are an error, so typos
// are caught.
func (a *App) ReadSpec(path string, v any) error {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(a.In)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return UsageError("read %s: %s", path, err)
	}
	return DecodeSpec(path, data, v)
}

// DecodeSpec decodes YAML or JSON data into v. name is used in messages.
func DecodeSpec(name string, data []byte, v any) error {
	var generic any
	if err := yaml.Unmarshal(data, &generic); err != nil {
		return UsageError("%s is not valid YAML or JSON: %s", name, err)
	}
	raw, err := json.Marshal(normalizeYAML(generic))
	if err != nil {
		return UsageError("%s: %s", name, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return UsageError("%s: %s", name, strings.TrimPrefix(err.Error(), "json: "))
	}
	return nil
}

// normalizeYAML turns map[any]any from YAML into map[string]any for JSON.
func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = normalizeYAML(e)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[fmt.Sprint(k)] = normalizeYAML(e)
		}
		return m
	case []any:
		for i, e := range t {
			t[i] = normalizeYAML(e)
		}
	}
	return v
}

// ReadValue returns s, or the contents of a file when s starts with @, or
// standard input when s is "@-". Used for flags that take long text or JSON.
func (a *App) ReadValue(s string) (string, error) {
	if !strings.HasPrefix(s, "@") {
		return s, nil
	}
	path := s[1:]
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(a.In)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", UsageError("read %s: %s", path, err)
	}
	return string(data), nil
}

// Confirm asks the user to confirm a destructive action. --yes skips the
// question. Without a terminal, it fails and asks for --yes.
func (a *App) Confirm(question string) error {
	if a.Yes {
		return nil
	}
	if !a.Interactive() {
		return &Error{Code: ExitUsage, Msg: question + " Not confirmed.", Hint: "Pass --yes to confirm without a prompt."}
	}
	fmt.Fprintf(a.Err, "%s [y/N] ", question)
	line, _ := bufio.NewReader(a.In).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return &Error{Code: ExitCancelled, Msg: "cancelled"}
}

// Deref returns the value of p, or the zero value when p is nil.
func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// OptString returns nil for an empty string, for optional GraphQL arguments.
func OptString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }

// Plural returns "1 agent" or "2 agents".
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %s", n, PluralOf(noun))
}

// PluralOf returns the plural of a noun: "agents", "meshes", "API keys".
func PluralOf(noun string) string {
	switch {
	case strings.HasSuffix(noun, "s"), strings.HasSuffix(noun, "sh"), strings.HasSuffix(noun, "ch"), strings.HasSuffix(noun, "x"):
		return noun + "es"
	case strings.HasSuffix(noun, "y") && !strings.ContainsRune("aeiou", rune(noun[len(noun)-2])):
		return noun[:len(noun)-1] + "ies"
	}
	return noun + "s"
}

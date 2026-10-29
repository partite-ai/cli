package cmdutil

import (
	"fmt"
	"sort"
	"strings"
	"time"

	gql "github.com/partite-ai/partite-cli/internal/graphql"
)

// FormatTime shortens a server timestamp for tables, in local time.
func FormatTime(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return t.Local().Format("2006-01-02 15:04")
}

// Tag is a name and value, as in the terraform provider's tags blocks.
type Tag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ParseTags reads --tag flags in name=value form.
func ParseTags(flags []string) ([]Tag, error) {
	tags := make([]Tag, 0, len(flags))
	for _, f := range flags {
		name, value, ok := strings.Cut(f, "=")
		if !ok || name == "" {
			return nil, UsageError("--tag must be name=value, got %q", f)
		}
		tags = append(tags, Tag{Name: name, Value: value})
	}
	return tags, nil
}

func TagsFromAPI(tags []*gql.Tag) []Tag {
	out := make([]Tag, 0, len(tags))
	for _, t := range tags {
		if t != nil {
			out = append(out, Tag{Name: t.Name, Value: t.Value})
		}
	}
	return out
}

func TagsToAPI(tags []Tag) []*gql.Tag {
	out := make([]*gql.Tag, len(tags))
	for i, t := range tags {
		out[i] = &gql.Tag{Name: t.Name, Value: t.Value}
	}
	return out
}

// FormatTags prints tags as name=value, sorted, for tables.
func FormatTags(tags []Tag) string {
	parts := make([]string, len(tags))
	for i, t := range tags {
		parts[i] = fmt.Sprintf("%s=%s", t.Name, t.Value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

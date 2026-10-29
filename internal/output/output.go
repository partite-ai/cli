// Package output prints command results as tables for people, or as JSON,
// YAML or bare ids for scripts and agents.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

type Format string

const (
	Table Format = "table"
	JSON  Format = "json"
	YAML  Format = "yaml"
	// ID prints only ids, one per line, for piping into other commands.
	ID Format = "id"
)

var Formats = []string{string(Table), string(JSON), string(YAML), string(ID)}

func ParseFormat(s string) (Format, error) {
	for _, f := range Formats {
		if s == f {
			return Format(s), nil
		}
	}
	return "", fmt.Errorf("unknown output format %q; use one of %s", s, strings.Join(Formats, ", "))
}

// IsTerminal is true when w is a terminal.
func IsTerminal(w any) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// Field is one labelled value in a single-object view.
type Field struct {
	Label string
	Value string
}

// Printer writes results in the chosen format.
type Printer struct {
	Out    io.Writer
	Format Format
}

// List prints rows. data is what JSON and YAML print. headers[0] must be the
// id column when the rows have ids.
func (p *Printer) List(data any, headers []string, rows [][]string) error {
	switch p.Format {
	case JSON, YAML:
		return p.Data(data)
	case ID:
		for _, r := range rows {
			if len(r) > 0 {
				fmt.Fprintln(p.Out, r[0])
			}
		}
		return nil
	}
	if len(rows) == 0 {
		fmt.Fprintln(p.Out, "No results.")
		return nil
	}
	tw := tabwriter.NewWriter(p.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(upper(headers), "\t"))
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = Truncate(oneLine(c), 60)
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

// Object prints one object. id is printed alone in the id format.
func (p *Printer) Object(data any, id string, fields []Field) error {
	switch p.Format {
	case JSON, YAML:
		return p.Data(data)
	case ID:
		fmt.Fprintln(p.Out, id)
		return nil
	}
	width := 0
	for _, f := range fields {
		width = max(width, utf8.RuneCountInString(f.Label))
	}
	for _, f := range fields {
		if f.Value == "" {
			continue
		}
		v := f.Value
		if strings.Contains(v, "\n") {
			fmt.Fprintf(p.Out, "%s:\n%s\n", f.Label, indent(strings.TrimRight(v, "\n"), "  "))
			continue
		}
		fmt.Fprintf(p.Out, "%-*s  %s\n", width+1, f.Label+":", v)
	}
	return nil
}

// Data prints data as JSON or YAML, or as indented JSON for the table format.
func (p *Printer) Data(data any) error {
	if p.Format == YAML {
		return writeYAML(p.Out, data)
	}
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(data)
}

// Done prints a short confirmation for people. Scripts get data instead, so
// the message is skipped for the other formats.
func (p *Printer) Done(format string, args ...any) {
	if p.Format == Table {
		fmt.Fprintf(p.Out, format+"\n", args...)
	}
}

// writeYAML goes through JSON so that json tags decide the field names and
// field order is kept.
func writeYAML(w io.Writer, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	blockStyle(&doc)
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	return enc.Close()
}

// blockStyle undoes the flow style that JSON input gives every node, and
// prints multi-line strings as literal blocks.
func blockStyle(n *yaml.Node) {
	n.Style = 0
	if n.Kind == yaml.ScalarNode && n.Tag == "!!str" && strings.Contains(n.Value, "\n") {
		n.Style = yaml.LiteralStyle
	}
	for _, c := range n.Content {
		blockStyle(c)
	}
}

func upper(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToUpper(s)
	}
	return out
}

func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

package convui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/partite-ai/partite-cli/internal/cmd/conversation/convapi"
	"github.com/partite-ai/partite-cli/internal/cmdutil"
)

// LineReader reads lines from a terminal. A read stops when its context
// ends, even though the line it waited for has not come, so Ctrl-C works
// while a question is asked.
type LineReader struct {
	in    io.Reader
	once  sync.Once
	lines chan lineOrErr
}

type lineOrErr struct {
	line string
	err  error
}

func NewLineReader(in io.Reader) *LineReader {
	return &LineReader{in: in, lines: make(chan lineOrErr)}
}

// ReadLine returns the next line without its newline, or io.EOF at the end
// of the input.
func (r *LineReader) ReadLine(ctx context.Context) (string, error) {
	r.once.Do(func() {
		go func() {
			br := bufio.NewReader(r.in)
			for {
				line, err := br.ReadString('\n')
				if line != "" || err == nil {
					r.lines <- lineOrErr{line: strings.TrimRight(line, "\r\n")}
				}
				if err != nil {
					r.lines <- lineOrErr{err: err}
					close(r.lines)
					return
				}
			}
		}()
	})
	select {
	case l, ok := <-r.lines:
		if !ok {
			return "", io.EOF
		}
		return l.line, l.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// taskOptions returns the options a task's input offers, as pick_one and
// multi_select give them.
func taskOptions(t convapi.Task) []string {
	var in struct {
		Options []any `json:"options"`
	}
	_ = json.Unmarshal(t.Input(), &in)
	out := make([]string, 0, len(in.Options))
	for _, o := range in.Options {
		out = append(out, fmt.Sprint(o))
	}
	return out
}

// choices returns the values a property can take: its enum, its items' enum,
// or the task's options when the property holds strings.
func choices(p Schema, options []string) []string {
	enum := p.Enum
	if p.TypeName() == "array" && p.Items != nil {
		enum = p.Items.Enum
	}
	if len(enum) > 0 {
		out := make([]string, len(enum))
		for i, v := range enum {
			out[i] = fmt.Sprint(v)
		}
		return out
	}
	t := p.TypeName()
	if t == "string" || (t == "array" && p.Items != nil && p.Items.TypeName() == "string") {
		return options
	}
	return nil
}

// DescribeTask prints a task as the server sent it, for someone or something
// that will answer it later: its input, the JSON schema its answer must
// match, and the commands that answer it. Forms are not drawn. question is
// false when the question was printed already.
func DescribeTask(w io.Writer, style Style, conversationID, requestID string, t convapi.Task, question bool) {
	if question {
		fmt.Fprintln(w, style.Question(TaskQuestion(t)))
	}
	if a := t.ToolCallApproval; a != nil {
		fmt.Fprintf(w, "\nTask %s asks to approve a call to %s with these arguments:\n%s\n", t.TaskID, ToolLabel(a.Tool), indentJSON(a.Arguments))
	} else {
		fmt.Fprintf(w, "\nTask %s, of type %s, with this input:\n%s\n", t.TaskID, t.Name(), indentJSON(t.Input()))
	}
	fmt.Fprintf(w, "\nIts answer must match this JSON schema:\n%s\n", indentJSON(t.ResponseSchema))
	schema, _ := ParseSchema(t.ResponseSchema)
	props := schema.OrderedProperties(t.ResponseSchema)
	base := fmt.Sprintf("partite conversation task complete %s %s", conversationID, requestID)
	fmt.Fprintln(w, "\nAnswer it with:")
	if _, ok := singleProperty(t); ok {
		example := "ANSWER"
		if options := taskOptions(t); len(options) > 0 {
			example = options[0]
		}
		fmt.Fprintf(w, "  %s --answer %s\n", base, shellQuote(example))
	}
	fmt.Fprintf(w, "  %s --data '%s'\n", base, exampleData(props))
	fmt.Fprintf(w, "Or decline it with:\n  partite conversation task cancel %s %s\n", conversationID, requestID)
}

func indentJSON(data json.RawMessage) string {
	var buf bytes.Buffer
	if json.Indent(&buf, data, "  ", "  ") != nil {
		return "  " + string(data)
	}
	return "  " + buf.String()
}

func exampleData(props []Property) string {
	parts := make([]string, 0, len(props))
	for _, p := range props {
		var v string
		switch p.Schema.TypeName() {
		case "array":
			v = `["..."]`
		case "boolean":
			v = "true"
		case "integer", "number":
			v = "0"
		case "object":
			v = "{}"
		default:
			v = `"..."`
		}
		parts = append(parts, fmt.Sprintf("%q: %s", p.Name, v))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:@", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// singleProperty returns the property of a task's answer when there is just
// one to fill: the only property, or the only required one.
func singleProperty(t convapi.Task) (Property, bool) {
	schema, err := ParseSchema(t.ResponseSchema)
	if err != nil {
		return Property{}, false
	}
	props := schema.OrderedProperties(t.ResponseSchema)
	if len(props) == 1 {
		return props[0], true
	}
	var found []Property
	for _, p := range props {
		if p.Required {
			found = append(found, p)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return Property{}, false
}

// AnswerFromText builds a task's answer from text, for tasks whose answer has
// one field: {"answer": text} for ask_question, {"selected": [..]} for
// multi_select, and so on. A list takes comma-separated values.
func AnswerFromText(t convapi.Task, text string) (json.RawMessage, error) {
	p, ok := singleProperty(t)
	if !ok {
		return nil, cmdutil.UsageError("this %s task's answer has more than one field, so --answer cannot fill it; use --data with JSON", t.Name())
	}
	v, err := parseValue(p.Schema, text, choices(p.Schema, taskOptions(t)))
	if err != nil {
		return nil, cmdutil.UsageError("--answer: %s", err)
	}
	return json.Marshal(map[string]any{p.Name: v})
}

// parseValue reads text as a value of the schema's type. A number picks one
// of choices, when there are choices.
func parseValue(s Schema, text string, choices []string) (any, error) {
	pick := func(item string) (string, error) {
		item = strings.TrimSpace(item)
		n, err := strconv.Atoi(item)
		switch {
		case err != nil || len(choices) == 0:
			return item, nil
		case n >= 1 && n <= len(choices):
			return choices[n-1], nil
		}
		return "", fmt.Errorf("%d is not a choice; pick a number from 1 to %d", n, len(choices))
	}
	switch s.TypeName() {
	case "boolean":
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "y", "yes", "true":
			return true, nil
		case "n", "no", "false":
			return false, nil
		}
		return nil, fmt.Errorf("answer yes or no")
	case "integer":
		n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", text)
		}
		return n, nil
	case "number":
		n, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", text)
		}
		return n, nil
	case "array":
		var out []any
		for _, item := range strings.Split(text, ",") {
			if strings.TrimSpace(item) == "" {
				continue
			}
			var itemSchema Schema
			if s.Items != nil {
				itemSchema = *s.Items
			}
			picked, err := pick(item)
			if err != nil {
				return nil, err
			}
			v, err := parseValue(itemSchema, picked, nil)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		if err := checkEnum(s.Items, out); err != nil {
			return nil, err
		}
		return out, nil
	case "string":
		v, err := pick(text)
		if err != nil {
			return nil, err
		}
		if err := checkEnum(&s, []any{v}); err != nil {
			return nil, err
		}
		return v, nil
	case "", "object":
		var v any
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			if s.TypeName() == "" {
				return pick(text)
			}
			return nil, fmt.Errorf("type the value as JSON")
		}
		return v, nil
	}
	return pick(text)
}

func checkEnum(s *Schema, values []any) error {
	if s == nil || len(s.Enum) == 0 {
		return nil
	}
	for _, v := range values {
		ok := false
		for _, e := range s.Enum {
			if fmt.Sprint(e) == fmt.Sprint(v) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("%v is not one of the choices", v)
		}
	}
	return nil
}

// PromptTask asks a person at a terminal for a task's answer, one field at a
// time. The task's question has been printed already.
func PromptTask(ctx context.Context, lr *LineReader, w io.Writer, style Style, t convapi.Task) (json.RawMessage, error) {
	if open, _, ok := taskForm(t); ok {
		return promptForm(ctx, lr, w, style, open, t.Input())
	}
	options := taskOptions(t)
	schema, err := ParseSchema(t.ResponseSchema)
	if err != nil {
		return nil, err
	}
	props := schema.OrderedProperties(t.ResponseSchema)
	if len(props) == 0 {
		fmt.Fprintf(w, "Type the answer as JSON matching this schema:\n  %s\n", t.ResponseSchema)
		for {
			line, err := ask(ctx, lr, w, style, "> ")
			if err != nil {
				return nil, err
			}
			if json.Valid([]byte(line)) {
				return json.RawMessage(line), nil
			}
			fmt.Fprintln(w, style.Red("That is not valid JSON."))
		}
	}
	answer := map[string]any{}
	for _, p := range props {
		cs := choices(p.Schema, options)
		if len(props) > 1 {
			label := style.Bold(p.Name)
			if p.Schema.Description != "" {
				label += " " + style.Dim(p.Schema.Description)
			}
			fmt.Fprintln(w, label)
		}
		for i, c := range cs {
			fmt.Fprintf(w, "  %d) %s\n", i+1, c)
		}
		prompt := "> "
		switch {
		case p.Schema.TypeName() == "boolean":
			prompt = "[y/n] > "
		case p.Schema.TypeName() == "array" && len(cs) > 0:
			prompt = "numbers or values, separated by commas > "
		case p.Schema.TypeName() == "array":
			prompt = "values, separated by commas > "
		case len(cs) > 0:
			prompt = "number or value > "
		case p.Schema.TypeName() == "object":
			prompt = "JSON > "
		}
		if !p.Required {
			prompt = "(optional) " + prompt
		}
		for {
			line, err := ask(ctx, lr, w, style, prompt)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(line) == "" {
				if !p.Required {
					break
				}
				fmt.Fprintln(w, style.Red("An answer is required."))
				continue
			}
			v, err := parseValue(p.Schema, line, cs)
			if err != nil {
				fmt.Fprintln(w, style.Red(err.Error()))
				continue
			}
			answer[p.Name] = v
			break
		}
	}
	return json.Marshal(answer)
}

func ask(ctx context.Context, lr *LineReader, w io.Writer, style Style, prompt string) (string, error) {
	fmt.Fprint(w, style.Cyan(prompt))
	line, err := lr.ReadLine(ctx)
	if errors.Is(err, io.EOF) {
		fmt.Fprintln(w)
		return "", cmdutil.Errorf(cmdutil.ExitError, "", "the input ended before the task was answered")
	}
	return line, err
}
